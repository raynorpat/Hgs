# Push Activity Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Record pushes, show them in the dashboard/profile stream and a per-repository Atom feed, and close issues from changeset messages once they are on mainline.

**Architecture:** When a write session to a repository ends, `modules/hosting` queues the repository for a single background activity worker. The worker compares the repository with per-repository state stored on `Repository` (next unseen revision, last processed `default` heads) through new read helpers in `modules/hg`, then calls model functions that write `Action` rows and close issues. The repository's Atom feed is a web route that reads the `Action` rows and authorizes private repositories with a per-user feed token.

**Tech Stack:** Go, xorm, Macaron, Mercurial revsets/templates, `encoding/xml`.

**Spec:** `docs/superpowers/specs/2026-10-26-push-activity-design.md`

## Global Constraints

- Server Mercurial ≥ 5.0. Every revset and template must work on 5.0.2 and 7.2.4.
- `modules/hg` imports no models/web code. Models never import hosting/routers. `modules/hosting` composes.
- No new third-party dependency. Atom is built with `encoding/xml`.
- Copy goes in `conf/locale/locale_en-US.ini`, not in templates.
- Recording never fails, delays or rolls back a push. Worker errors are logged with `log.Error` and nothing else.
- Unknown repository, private repository with a missing/invalid/revoked token, and a token whose owner lacks read access all return the same 404.
- Commit messages: one line, `<subsystem>: <what it does>`, no trailers.
- Portable across Windows, macOS and Linux.

## Review Focus

1. **A repository whose `default` branch does not exist** (only feature branches pushed): the revsets must return empty, not abort. The test goes in Task 2 (`TestMainlineHeadsWithoutDefault`).
2. **Repeated worker runs with nothing new** (most writer requests are not pushes, e.g. `capabilities` or `listkeys`): nothing should be written, and only one cheap `hg log` should run. The test goes in Task 4 (`TestActivityIdleRunWritesNothing`).
3. **Feed token in logs**: `macaron.Logger` logs `RequestURI` including the query. The token must not appear. The test goes in Task 6 (`TestFeedTokenRedactedFromRequestURI`).
4. **Author email of a user with no access to a private repository**: the closure and stream entry must be credited to the pusher instead. The test goes in Task 3 (`TestChangesetActorRequiresRead`).
5. **The repository is deleted, or the server shuts down, while a run is queued or running**: no panic and no hg run against a deleted path; the run is skipped and caught up later. The test goes in Task 4 (`TestActivitySkippedWhileDraining`).

---

### Task 1: Activity state, action types, and push/close actions in models

**Files:**
- Create: `models/activity.go`
- Modify: `models/repo.go` (add fields to `Repository`), `models/action.go` (new `ActionType`s, push content accessor)
- Test: `models/activity_test.go`

**Interfaces:**
- Produces:
  - `Repository.ActivityTip int64`: the next revision to process. 0 means never synced.
  - `Repository.ActivityMainline string`: space-separated 40-hex node IDs of the `default` heads already processed. Columns are `xorm:"NOT NULL DEFAULT 0"` and `xorm:"TEXT"`.
  - `const PUSH_REPO ActionType = 5` (fills the `_` slot), `const CLOSE_ISSUE ActionType = 11`.
  - `type PushChangeset struct { Node, Author, Summary string; Date time.Time }`
  - `type PushContent struct { Branch string; Changesets []PushChangeset; Total int }`. At most 5 `Changesets`, newest first. `Total` is the full count.
  - `func (a Action) GetPushContent() PushContent`, which unmarshals `Content` and returns the zero value (empty `Changesets`) when it isn't valid JSON. Templates call it directly.
  - `func NewPushAction(pusher *User, repo *Repository, content PushContent) error`, which goes through `NotifyWatchers` with `IsPrivate: repo.IsPrivate` and `RepoUserName: repo.Owner.Name` (it loads the owner if nil).
  - `func SaveActivityState(repoID, tip int64, mainline []string) error`, which updates only those two columns. Models do not import `modules/hg`, so hosting converts `[]hg.NodeID` to and from `[]string`.
  - `func ClosingIssueIndexes(message string) []int64`.

- [ ] **Step 1: Write the failing tests**

```go
func TestClosingIssueIndexes(t *testing.T) {
	cases := map[string][]int64{
		"Fixes #12":                         {12},
		"close #1, resolves #2 and FIXED #3": {1, 2, 3},
		"fix #4\n\nAlso closes #4":          {4}, // deduplicated, first-seen order
		"see #5":                            nil,
		"prefix#6 fixes#7":                  nil, // keyword needs whitespace before #, word boundary before keyword
		"refixes #8":                        nil,
		"Resolve  #9":                       {9},
	}
	for msg, want := range cases {
		if got := ClosingIssueIndexes(msg); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", msg, got, want)
		}
	}
}

func TestPushActionRoundTrip(t *testing.T) {
	newTestDatabase(t)
	// create owner + public repo + watcher; NewPushAction(...)
	// assert: one Action row per receiver (actor + watcher), OpType PUSH_REPO,
	// GetPushContent() equals what was passed, GetFeeds(watcher) returns it.
}

func TestSaveActivityState(t *testing.T) {
	newTestDatabase(t)
	// SaveActivityState(repo.Id, 7, []string{nodeA, nodeB}); reload repo:
	// ActivityTip == 7, ActivityMainline == nodeA+" "+nodeB; other columns unchanged.
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./models -run 'TestClosingIssueIndexes|TestPushActionRoundTrip|TestSaveActivityState' -count=1`
Expected: FAIL (undefined: `ClosingIssueIndexes`, `NewPushAction`, `SaveActivityState`)

- [ ] **Step 3: Implement the code in `models/activity.go` and the fields/types above**

The keyword regexp is `(?i)(?:^|\s)(?:close|closes|closed|fix|fixes|fixed|resolve|resolves|resolved)\s+#([0-9]+)\b`. Extend `ActionIcon` in `modules/base/template.go`: 5 → `"repo-push"`, 11 → `"issue-closed"`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./models -count=1`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add models/activity.go models/activity_test.go models/repo.go models/action.go modules/base/template.go
git commit -m "models: record push actions and repository activity state"
```

### Task 2: Mercurial activity reads

**Files:**
- Create: `modules/hg/activity.go`
- Test: `modules/hg/activity_test.go` (parsing), `modules/hg/activity_integration_test.go` (`//go:build integration`, real hg via `GOGS_TEST_HG`, same pattern as `history_integration_test.go`)

**Interfaces:**
- Consumes: `Runner.Run`, `parseChangesets`, `changesetTemplate`, `ParseNodeID`, and `NodeID` from `history.go`/`refs.go`.
- Produces:
  - `func (r *Runner) TipRevision(ctx context.Context, path string) (int64, error)` returns -1 for an empty repository.
  - `func (r *Runner) ChangesetRange(ctx context.Context, path string, from, to int64) ([]Changeset, error)` returns revisions `from..to` inclusive in revision order, excluding secret.
  - `func (r *Runner) MainlineHeads(ctx context.Context, path string, upTo int64) ([]NodeID, error)` returns public heads of the `default` branch among revisions `0..upTo`, sorted by node.
  - `func (r *Runner) NewlyMainline(ctx context.Context, path string, heads, processed []NodeID, from int64, limit int) ([]Changeset, error)` returns public ancestors of `heads` that are not ancestors of `processed`, with revision ≥ `from`, in revision order, at most `limit`.

Each call applies `BrowserCommandTimeout`. Revset text contains only integers formatted with `%d` and nodes that passed `ParseNodeID`, never user strings. Select the branch with `branch("re:^default$")`, so a missing branch yields an empty set instead of aborting. When `processed` is empty, omit the `- ancestors(...)` term. When `heads` is empty, return nil without running hg.

- [ ] **Step 1: Write the failing tests**

The integration tests build a repo with `hg init` plus commits:
- `TestTipRevisionEmpty`: a fresh repo returns -1.
- `TestChangesetRange`: with 3 commits, `(1,2)` returns revs 1 and 2 with Branch/Message/User filled.
- `TestMainlineHeadsWithoutDefault`: a repo whose only commits are on branch `feature` returns an empty slice and no error.
- `TestNewlyMainlineAfterMerge`: `default` has A. `feature` has B and C. With processed=[A], heads=MainlineHeads before the merge returns nothing new. After merging feature into default (M), heads=[M] returns B, C and M; A is excluded. The `from` and `limit` arguments page correctly (limit 2 returns B and C, then from=rev(C)+1 returns M).

- [ ] **Step 2: Run them to verify they fail**

Run: `GOGS_TEST_HG=<hg> go test -tags integration ./modules/hg -run 'Tip|ChangesetRange|Mainline' -count=1`
Expected: FAIL (undefined methods)

- [ ] **Step 3: Implement `modules/hg/activity.go`**

- [ ] **Step 4: Run the tests to verify they pass**

Run the same command as Step 2 against both 5.0.2 and 7.2.4 if both are installed, plus `go test ./modules/hg -count=1`.
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add modules/hg/activity.go modules/hg/activity_test.go modules/hg/activity_integration_test.go
git commit -m "hg: read changeset ranges and mainline ancestry for activity"
```

### Task 3: Shared issue close path and changeset closing

**Files:**
- Modify: `models/issue.go`, `routers/repo/issue.go:758-786` (manual close uses the new function), `templates/repo/issue/view.tmpl:101-107` (close comment shows the changeset)
- Modify: `models/activity.go`
- Test: `models/activity_test.go`

**Interfaces:**
- Consumes: `CLOSE_ISSUE` from Task 1, `RepositoryAccessFor`, `GetUserByEmail`.
- Produces:
  - `func CloseIssue(issue *Issue, repo *Repository, doer *User, node string) error`. It sets `IsClosed`, calls `UpdateIssue`, `UpdateIssueUserPairsByStatus` and `ChangeMilestoneIssueStats` (when the issue has a milestone), then `CreateComment(doer.Id, repo.Id, issue.Id, COMMENT_TYPE_CLOSE, node, nil)`, then `NotifyWatchers` with a `CLOSE_ISSUE` action whose `Content` is `fmt.Sprintf("%d|%s", issue.Index, node)`. If the issue is already closed it returns nil and does nothing. `node` is "" for a manual close.
  - `func ChangesetActor(repo *Repository, author string, pusher *User) *User` parses `author` with `net/mail.ParseAddress`, falling back to the whole string when it contains `@`. It returns the user from `GetUserByEmail` only if that user `IsActive` and `RepositoryAccessFor(u.Id, repo.Id).Read`; otherwise it returns `pusher`. (My call: `GetUserByEmail` also matches activated secondary emails, which are verified, so this is consistent with the spec's intent.)
  - `func CloseIssuesFromChangeset(repo *Repository, pusher *User, node, author, message string) error` closes, through `CloseIssue`, each index from `ClosingIssueIndexes` that exists in `repo` and is open. Missing issues are skipped.

The manual reopen path in `routers/repo/issue.go` stays as it is. The manual close branch calls `CloseIssue(issue, ctx.Repo.Repository, ctx.User, "")`, so manual closes now also produce a `CLOSE_ISSUE` action, which the feed needs. In `view.tmpl`, a type-2 comment with non-empty `.Content` renders "Closed this issue via `<short node>`" linking to `{{$.RepoLink}}/commit/{{.Content}}`. The copy goes in the locale under `[repo]` as `issues.closed_via_changeset`.

- [ ] **Step 1: Write the failing tests**

```go
func TestCloseIssueOnce(t *testing.T)                 // open issue → closed, repo.NumClosedIssues+1, one close comment with Content=node, one CLOSE_ISSUE action; second call: no new rows
func TestCloseIssuesFromChangesetSkipsMissing(t *testing.T) // "fixes #1 fixes #99": #1 closed, no error
func TestChangesetActorRequiresRead(t *testing.T)     // private repo; author email of active user without access → pusher; with read access → that user; inactive → pusher; "Name <x@y>" and bare "x@y" both parse; "nobody" → pusher
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./models -run 'TestCloseIssue|TestChangesetActor' -count=1`
Expected: FAIL (undefined)

- [ ] **Step 3: Implement the functions and the router/template changes**

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./models ./routers/... -count=1 && go build ./...`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add models/issue.go models/activity.go models/activity_test.go routers/repo/issue.go templates/repo/issue/view.tmpl conf/locale/locale_en-US.ini
git commit -m "issues: close issues from changeset messages through the shared close path"
```

### Task 4: Activity worker in hosting

**Files:**
- Create: `modules/hosting/activity.go`
- Modify: `modules/hosting/service.go` (`Service` fields; start in `New`, stop in `Shutdown`; hook into `register`)
- Test: `modules/hosting/activity_test.go` (queue unit tests), `modules/hosting/activity_integration_test.go` (`//go:build integration`, uses `fixtureConfig`, `nativeHookBinary` and `nativeRun` like `permissions_integration_test.go`)

**Interfaces:**
- Consumes: Task 1 (`SaveActivityState`, `NewPushAction`, `PushContent`), Task 2 (all four runner methods), Task 3 (`CloseIssuesFromChangeset`, `ChangesetActor`), and the existing `RegisterBrowserRead`, `models.GetRepositoryById`, `models.GetUserById` and `models.RepositoryStoragePath`.
- Produces:
  - `type activityQueue struct`, a deduplicating map from repoID to the last pusher's userID plus a 1-buffered wake channel. It has `func (q *activityQueue) add(repoID, userID int64)` and `func (q *activityQueue) take() map[int64]int64`.
  - `func (s *Service) processActivity(ctx context.Context, repoID, pusherID int64) error`.

**Wiring:** in `register`, when `g.CanWrite && g.RepoID > 0 && !browserRead`, wrap the returned release func so that after the registry release it calls `s.activity.add(g.RepoID, g.Principal.UserID)`. One worker goroutine is started in `New` and stopped in `Shutdown` after sessions drain (before `s.cancel()`). Shutdown does not wait for queued work beyond the cleanup deadline; unprocessed repositories catch up on their next write session. For each taken repository, the worker calls `processActivity` with a context bounded by `hg.BrowserCommandTimeout × 4`, and logs errors.

**`processActivity` algorithm** (this order is the contract):

```
release, err := s.RegisterBrowserRead(0, repoID, cancel)   // drain/shutdown/delete safety; err → return nil (skip)
defer release()
repo, pusher := load; path := RepositoryStoragePath(...)
tip := TipRevision
if tip < 0 || tip+1 == repo.ActivityTip { return nil }     // empty or nothing new: one hg call
heads := MainlineHeads(path, tip)
if repo.ActivityTip == 0 { return SaveActivityState(repo.Id, tip+1, heads) }   // initial sync: silent baseline
for batch of 200 over ChangesetRange(repo.ActivityTip, tip):
    accumulate per-branch PushContent (Total += n; keep newest 5)
for each branch (sorted by name): NewPushAction(pusher, repo, content)
processed := parse repo.ActivityMainline
for from := 0; ; page of 200 over NewlyMainline(heads, processed, from, 200):
    CloseIssuesFromChangeset(repo, ChangesetActor(repo, cs.User, pusher), cs.Node, cs.User, cs.Message)
SaveActivityState(repo.Id, tip+1, heads)
```

My call: actions are written before the state is saved, so a crash in between can duplicate one push entry. It can never double-close, because `CloseIssue` skips closed issues.

- [ ] **Step 1: Write the failing tests**

Unit tests:
- `TestActivityQueueDedup`: add(1,10), add(1,11), add(2,10) makes take() return {1:11, 2:10}, and a second take() is empty.

Integration tests (real hg, HTTP push via the service like `TestPermissionChangesDuringPush`; wait with a poll on `Repository.ActivityTip`):
- `TestActivityInitialPushIsBaseline`: a first push of 3 commits including "fixes #1" leaves issue #1 open and no PUSH_REPO actions, and `ActivityTip` is 3.
- `TestActivityPushToDefault`: a second push of 7 commits gives one PUSH_REPO action with Total 7, 5 changesets newest first, Branch "default".
- `TestActivityFeatureBranchThenMerge`: pushing "fixes #2" on `feature` gives a PUSH_REPO action for feature and leaves #2 open. Pushing the merge into default closes #2, with a close comment whose Content is the feature changeset's node.
- `TestActivityRepushIdempotent`: rerunning `processActivity` after a closing push writes no new actions or comments.
- `TestActivityIdleRunWritesNothing`: a `processActivity` call with nothing new leaves the Action count unchanged.
- `TestActivitySkippedWhileDraining`: with `DrainRepository` in progress, `processActivity` returns nil without running hg (the `RegisterBrowserRead` error path).
- `TestActivitySSHPush`: an SSH push (same harness as `ssh_permissions_integration_test.go`) records a PUSH_REPO action.

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./modules/hosting -run TestActivityQueue -count=1` and `GOGS_TEST_HG=<hg> go test -tags integration ./modules/hosting -run TestActivity -count=1`
Expected: FAIL (undefined)

- [ ] **Step 3: Implement `modules/hosting/activity.go` and the service wiring**

- [ ] **Step 4: Run the tests to verify they pass**

Run both commands from Step 2, plus `go test ./modules/hosting -count=1`, to confirm the existing shutdown/registry tests still pass.
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add modules/hosting/activity.go modules/hosting/activity_test.go modules/hosting/activity_integration_test.go modules/hosting/service.go
git commit -m "hosting: process push activity after write sessions end"
```

### Task 5: Activity stream rendering

**Files:**
- Modify: `templates/user/dashboard/feeds.tmpl`, `conf/locale/locale_en-US.ini` (`[action]`)
- Test: `models/activity_test.go` (content accessor edge case only)

**Interfaces:**
- Consumes: `PUSH_REPO`, `CLOSE_ISSUE`, `GetPushContent`, `GetIssueInfos`.

Locale keys under `[action]`:
- `push_repo = pushed to <a href="%[1]s/src/%[2]s">%[3]s</a> at <a href="%[1]s">%[4]s</a>`, taking repo link, branch head node, branch name and repo path.
- `close_issue = closed issue <a href="%s/issues/%s">%[1]s#%[2]s</a>`
- `close_issue_via = closed issue <a href="%s/issues/%s">%[1]s#%[2]s</a> via <a href="%[1]s/commit/%[3]s"><code>%[4]s</code></a>`
- `push_more = and %d more changesets`
- `compare_changesets = View comparison for these %d changesets »`, which links to `/commits/<newest node>`. With a single changeset it links to `/commit/<node>`. First confirm that the `/commits/*` route (`routers/repo/commit.go`) treats its argument as a starting node. If it doesn't, always link to `/commit/<newest node>`.

For each changeset in a PUSH_REPO entry, the template prints its short node (via the existing `ShortSha`) linked to `/commit/<node>`, then its summary run through the existing issue-link renderer (the `RenderCommitMessage`-style template func at `modules/base/template.go:90`). Branch names and summaries are escaped by the template, never through `Str2html`. Only the locale format strings are trusted HTML, and the branch argument must be HTML-escaped before it is passed to `Tr ... | Str2html`.

- [ ] **Step 1: Write the failing test**

`TestGetPushContentInvalid`: an `Action` whose `Content` is `"not json"` returns a `PushContent` with no changesets and `Total` 0. The template renders only the headline for such an entry.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./models -run TestGetPushContentInvalid -count=1`

- [ ] **Step 3: Implement the template and locale entries**

- [ ] **Step 4: Verify**

Run: `go test ./... -count=1 && go build -o gogs.exe .`. Then start `./gogs.exe web`, push twice to a repository with a branch name containing `<b>`, and check that the dashboard shows the style-A entry with the branch name escaped.
Expected: tests PASS, and the entry renders as in the spec's mockup.

- [ ] **Step 5: Commit**

```bash
git add templates/user/dashboard/feeds.tmpl conf/locale/locale_en-US.ini models/activity.go models/activity_test.go
git commit -m "ui: show pushes and changeset closures in the activity stream"
```

### Task 6: Repository Atom feed and feed tokens

**Files:**
- Create: `routers/repo/feed.go`, `models/feed.go`
- Modify: `models/user.go` (`User.FeedToken string`, `xorm:"VARCHAR(40) INDEX"`), `cmd/web.go` (route plus redaction middleware before `macaron.Logger()`), `routers/user/setting.go` and `templates/user/settings/applications.tmpl` (show and regenerate), the repository header template and `templates/ng/base/head.tmpl` (feed link and `<link rel="alternate">` when `.FeedURL` is set), `routers/repo/view.go` or `middleware.RepoAssignment` (set `ctx.Data["FeedURL"]`), `conf/locale/locale_en-US.ini`
- Test: `models/feed_test.go`, `routers/repo/feed_test.go`, `cmd/web_test.go` or an existing `cmd` route test file

**Interfaces:**
- Consumes: Task 1 action types and `GetPushContent`, `RepositoryAccessFor`.
- Produces:
  - `func GetRepositoryFeed(repoID int64, limit int) ([]*Action, error)` returns `repo_id = ? AND user_id = act_user_id AND op_type IN (PUSH_REPO, CREATE_ISSUE, COMMENT_ISSUE, CLOSE_ISSUE)`, newest first. The actor's own copy deduplicates the watcher fan-out.
  - `func FeedTokenFor(u *User) (string, error)` lazily creates a 40-hex `crypto/rand` token. `func RegenerateFeedToken(uid int64) (string, error)`.
  - `func FeedReader(repo *Repository, token string) bool`. A public repository returns true. A private one returns true only when `token` is non-empty, maps to an active user, and `RepositoryAccessFor(u.Id, repo.Id).Read` holds.
  - `func renderAtom(w io.Writer, repo *models.Repository, repoURL string, actions []*models.Action, tr func(string, ...interface{}) string) error` in `routers/repo/feed.go`.
  - Route `GET /:username/:reponame/activity.atom` → `repo.ActivityFeed`, registered before the `/:username/:reponame` groups and outside `RepoAssignment`, because the handler does its own lookup and 404s. The token comes from the query parameter `token`. The feed URL is `setting.AppUrl + owner/repo/activity.atom`, plus `?token=<FeedTokenFor(signed-in user)>` for a private repository.
  - Redaction: a handler registered before `macaron.Logger()` that sets `ctx.Req.RequestURI = ctx.Req.URL.Path` when the path ends in `/activity.atom`.

Atom details (my calls): a feed `id` and `link` that are the absolute repository URL; feed `updated` set to the newest item's time, or the repository's `Updated` time when empty; and 50 items. Each entry has `id` set to `tag:<host>,2026:action-<Id>`, `title` as plain text (for example "alice pushed 3 changesets to default"), `content type="html"` with the same body as the stream entry (absolute links), `author` set to the actor name, and `updated` set to `Created`. The response is `Content-Type: application/atom+xml; charset=utf-8` and, for token feeds, `Cache-Control: private, no-store`.

- [ ] **Step 1: Write the failing tests**

```go
func TestFeedReader(t *testing.T)            // public: "" ok; private: "" no, bad token no, valid token of reader yes, after access removed no, inactive user no, after RegenerateFeedToken old token no
func TestGetRepositoryFeedDedup(t *testing.T) // push action fanned out to 3 watchers appears once; CREATE_REPO excluded; limit honored
func TestRenderAtom(t *testing.T)            // output parses with encoding/xml into a struct; entry count, ids unique, html content escaped (summary "<script>" appears escaped), absolute links start with repoURL
func TestActivityFeed404Uniform(t *testing.T) // via httptest against newWebHandler or the handler directly: unknown repo, private no token, private bad token → identical 404 status and body
func TestFeedTokenRedactedFromRequestURI(t *testing.T) // request /u/r/activity.atom?token=SECRET through the redaction handler: RequestURI has no "SECRET"; handler still reads the token
```

- [ ] **Step 2: Run them to verify they fail**

Run: `go test ./models ./routers/repo ./cmd -run 'Feed|Atom' -count=1`
Expected: FAIL (undefined)

- [ ] **Step 3: Implement the models, handler, route, redaction, settings regenerate control (`POST /user/settings/applications/feed_token`), and feed links**

- [ ] **Step 4: Run the tests to verify they pass, then do a manual check**

Run: `go test ./... -count=1 && go vet ./... && go build -o gogs.exe .`. Then subscribe to a public feed and a private token feed with any Atom validator or reader, regenerate the token, and confirm the old URL 404s.
Expected: PASS, the feed validates, and the old token 404s.

- [ ] **Step 5: Commit**

```bash
git add models/feed.go models/feed_test.go models/user.go routers/repo/feed.go routers/repo/feed_test.go routers/user/setting.go cmd/web.go templates conf/locale/locale_en-US.ini
git commit -m "feeds: serve per-repository Atom feeds with private feed tokens"
```

### Task 7: Docs and spec bookkeeping

**Files:**
- Modify: `docs/superpowers/specs/2026-10-25-mercurial-hosting-design.md:56` (mark issue auto-closing and push activity feeds as delivered by this project), `docs/mercurial/operator-notes.md` (one paragraph: feed tokens act as credentials, how to revoke them, and that activity starts after the initial push)

- [ ] **Step 1: Edit both documents**
- [ ] **Step 2: Commit**

```bash
git add docs/superpowers/specs/2026-10-25-mercurial-hosting-design.md docs/mercurial/operator-notes.md
git commit -m "docs: record push activity and feed token operations"
```
