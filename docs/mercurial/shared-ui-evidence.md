# Shared UI integration prerequisite evidence

## Current development handoff — 2026-10-26

**Task 0: development baseline ready under the explicit user direction to branch from mercurial-hosting and start. Release/native matrix acceptance is not complete.**

- Isolated worktree: `C:/Users/raynorpat/.codex/worktrees/shared-ui-implementation/gogs`; branch `codex/shared-ui-implementation`; baseline `096df57` (`packager: remove dead packager dir`). Initial status clean.
- The source checkout is concurrently running hosting Task 16 benchmark work. Its uncommitted compatibility, home-template and integration-test changes were not copied. The older UI design/prototype worktree is preserved.
- Fresh exact-worktree verification: `go test ./models ./modules/hg ./modules/hgbridge ./modules/hosting ./routers/repo -count=1` passed (26.802s, 22.678s, 17.702s, 62.407s, 1.611s respectively); `go build ./...` passed, exit 0.
- The hosting plan records Tasks 7–15 implemented/reviewed. Its recorded extracted-package acceptance exercised real HTTPS/SSH clone/push/pull, account/issue workflow, restart and SQLite restore. Those are historical receipts, not tests rerun in this audit.
- `docs/mercurial/compatibility.md` retains two unresolved Windows filesystem-lock failures in the full native run; isolated rechecks passed without an identified root-cause fix. Linux/macOS, Hg 5.0.2/2.0.2, race and network-database acceptance remain unverified. Configured CI is not execution evidence. No native release gate is marked passed here.
- The stream-bundle close-veto issue was resolved by an explicitly approved policy change: writers are authorized at `pretxnopen`/`prepushkey`; already-open transactions may complete, and cancellation guarantees neither completion nor rollback. No writer close-time authorization is reintroduced by this handoff.

### Available application contracts

- `models.RepositoryAccessFor(userID, repoID int64) (RepositoryAccess,error)` and `ValidateRepositoryCredential(userID,keyID,tokenID int64) error` provide current ID-based authorization.
- `models.RepositoryStoragePath(root string,repoID int64) (string,error)` resolves ID storage; lifecycle states are `provisioning`, `ready`, `deleting`. Hosted stores have no checkout. Repository deletion drains registered operations.
- `hosting.New(context.Context,Config) (*Service,error)` composes runner, controller, HTTPS and embedded SSH. `hosting.Provider.Service() *Service` supports the cmd-owned install/startup holder. Use this actual provider when wiring new handlers, rather than an obsolete assumed singleton injection.
- `Service.Runner()`, `RepositoryRoot()`, `RegisterBrowserRead(...)`, `DrainRepository(...)` and the existing registry/authority monitor are available. Task 2 must extend actual native writer coordination, not invent a web-only mutex.
- `hg.NodeID`, `RefSelection`, `Reference` and runner `References`, `Resolve`, `Landing`, `Manifest`, `ReadFile`, `Changeset`, `History`, `Diff` exist. `ReadFile(ctx,path,node,file,out)` streams to an `io.Writer`; `History` accepts `HistoryQuery` and returns `HistoryPage`; existing `Diff` compares one changeset to a parent. Review comparisons need their own bounded between-node operation.
- `Resolve` chooses a preferred branch head for browsing; Task 1 must reject ambiguity for review instead. `Controller.Issue` requires its existing `touch func()` argument.

### Scope and ordering

The approved UI plan supersedes only the earlier broad-UI/fork/PR exclusions. Keep other deferred/Git routes absent and update fork/PR absence assertions only when real Mercurial routes are implemented. Preserve the selected Go/managed-Mercurial architecture and permission policy. Tasks 1–2 must prove isolated snapshots, safe merge preparation and guarded publication before broad UI work. Cross-platform validation remains required before release.

---

## Historical initial audit (superseded for development readiness)

Audit date: 2026-10-26. **Task 0: BLOCKED / incomplete.** Neither the transport gate nor the required Mercurial application baseline passes. This note authorizes no product implementation or architectural remedy.

## Baseline and provenance

- Worktree: `C:/Users/raynorpat/.codex/worktrees/shared-ui-pull-requests/gogs`.
- Branch: `codex/shared-ui-pull-requests`; starting HEAD: `3b27479 docs: approve shared UI design and plan implementation`.
- Elevated `git status --short` returned no changes before this audit. No concurrent edits were present in this worktree. No files were copied from the original checkout.
- An initial sandboxed status call reported `fatal: this operation must be run in a work tree`; the authorized elevated call succeeded. This was an access-context issue, not evidence of a dirty baseline.
- `go version`: `go1.27.3 windows/amd64`.
- `git remote -v` shows existing origin `git@github.com:raynorpat/Hgs.git`. Historical statements that there is no remote are stale; this audit did not fetch, push, or infer that CI has run.
- Reviewed: approved hosting design/plan, approved shared UI/PR design, `docs/DESIGN.md` (approved shared design system), Task 0 brief, `CLAUDE.md`, transport evidence, source APIs, integration tests, and native CI configuration. The full shared UI implementation plan was not read.

## Transport gate: FAILED and native matrix unverified

`docs/mercurial/transport-evidence.md` still explicitly records FAILED. Its last change is `1cb1c12`; preceding evidence changes include `13a3c95` and `d21fb76`. The original hosting plan's completion record labels Task 6's audit complete while explicitly retaining a failed gate; it records Tasks 7–15 as not started. Completion of the experiment is not passage of the gate.

The recorded Windows 7.2.4 test demonstrates stream-clone bundle store files surviving a rejected transaction close in an empty repository. `tests/integration/transport_test.go` still contains `TestKnownGapStreamBundleRevocation`, whose purpose is to assert that unsafe behavior. A green known-gap test is not acceptance. No approved resolution or passing replacement evidence exists in the reviewed baseline.

Required current-version native Windows/Linux/macOS passes and Linux legacy-server/client coverage (5.0.2/2.0.2) are not established. Recorded Windows results include the gap; Linux/macOS and legacy rows are unavailable in the evidence. `.github/workflows/mercurial.yml` defines native current-version and Linux legacy jobs, but configuration is not execution evidence. Existing origin does not establish job results. No native matrix was rerun here: the documented unresolved failure and absent application layer already block progression.

Owner/handoff: the original hosting Task 6 and its design owner must resolve the architectural failure through approved design and supply actual required native/version results. This UI plan does not choose a remedy or waive that gate.

## Application baseline: FAILED prerequisite

Executed in this worktree:

| Command | Actual result |
| --- | --- |
| `git status --short` (elevated) | exit 0, empty output |
| `git log -1 --oneline` | `3b27479 docs: approve shared UI design and plan implementation` |
| `go test ./models ./modules/hg ./modules/hgbridge ./modules/hosting ./routers/repo -count=1` | exit 1: `modules/hosting: directory not found`; models passed (4.696s), hg passed (14.472s), hgbridge passed (14.970s), routers/repo passed (1.852s) |
| `go build ./...` | exit 0 |

Build success proves the existing tree compiles; it cannot prove the missing composition package or proposed application behavior. These ordinary tests do not execute the tagged transport matrix.

Concrete missing prerequisites:

- `modules/hosting` does not exist. Original hosting Tasks 7–15 are unimplemented according to their completion record.
- `models/access.go` uses `Access.UserName` and `Access.RepoName` string keys. `RepositoryAccessFor` and `ValidateRepositoryCredential` are absent from the searched model sources.
- `models/repo.go:684` still resolves `RepoPath(userName, repoName)` to owner/name plus `.git`, rather than immutable repository ID storage.
- `routers/repo/view.go` imports `modules/git`; `modules/hg` contains runner/config/process code, without the planned typed reference, file, history or diff browsing layer.
- The integration directory contains transport fixtures using injected authorization, not the real configured application acceptance fixture required by hosting Task 14. Searches found no `TestEmptyRepositoryStillHasIssues` or `TestGitAndDeferredRoutesAbsent`. Historical Windows Git-era account/issue smoke evidence and bridge tests do not satisfy Mercurial application acceptance.

Owner/handoff: after Task 6 passes, the original hosting work must deliver ID-based authorization/storage/lifecycle, composed public hosting routes and Mercurial browsing, with actual end-to-end acceptance evidence. UI/PR production tasks cannot consume these APIs yet.

## Exact available transport API inventory

These are existing implementation signatures, not a claim of complete application integration:

- `hg.NewRunner(cfg Config) (*Runner, error)`; runner methods `Check(context.Context) error`, `Run(context.Context, string, []string, io.Reader, io.Writer) error`, `Start(context.Context, string, []string, map[string]string) (*Child, error)`, and `StartWithConfig(context.Context, string, []string, map[string]string, string) (*Child, error)`.
- `hg.Child`: `Touch()`, `Wait() error`, `Stop(context.Context) error`.
- `hgbridge.Principal { UserID, KeyID, TokenID int64 }`; `Grant { Principal Principal; RepoID int64; CanWrite bool }`; `Authorize func(context.Context, Grant) error`; `Target { RepoID int64; Path string }`.
- `hgbridge.NewController(Authorize, time.Duration) (*Controller, error)`; `Issue(g Grant, touch func()) (capability string, env map[string]string, revoke func(), err error)`; `Check(context.Context, string, int64, string) error`; `Close(context.Context) error`; `ServeHTTP(http.ResponseWriter, *http.Request)`.
- `hgbridge.HookConfig(string, bool) (map[string]string, error)`.
- `hgbridge.NewHTTPBridge(*hg.Runner, *Controller, string) (*HTTPBridge, error)`; `Serve(context.Context, http.ResponseWriter, *http.Request, Target, Grant) error`; `Close()`.
- `hgbridge.NewStdioBridge(*hg.Runner, *Controller, string, string) (*StdioBridge, error)`; `ServeStdio(context.Context, Target, Grant, io.Reader, io.Writer, io.Writer) (int, error)`; `Close()`.
- `ssh.NewServer(ssh.Signer, ResolveKey, ResolveTarget, *hgbridge.StdioBridge) *Server`; `Serve(net.Listener) error`; `Shutdown(context.Context) error` (package is `modules/ssh`, signer type is from `golang.org/x/crypto/ssh`).

In particular, implemented controller `Issue` requires the `touch func()` argument; the earlier hosting plan's interface sketch omits it. No application authorization, lifecycle or browsing API is represented as available here.

## Scope reconciliation and downstream handoff

The approved shared UI/PR design supersedes earlier broad UI redesign, fork and PR exclusions only. Hosted stores remain without checkouts; isolated temporary merge preparation is permitted. Mercurial-only hosting, fresh database, ID-based access/storage, Go-managed processes, no custom Python helper/production wire parser, and native Windows/macOS/Linux support remain required. Landing-page/logo work remains deferred for separate design review.

For original hosting Task 13, the old `TestGitAndDeferredRoutesAbsent` instruction to remove fork/pull endpoints must be reconciled with the newer approved design: remove obsolete Git implementations, preserve newly implemented authorized Mercurial fork/PR routes when those exist. No such new routes were implemented by this audit. This is the handoff note for later executors; the original plan was not rewritten as if its blocked prerequisites had completed.

## Task 0 checklist

- [x] Record isolated baseline and dependency evidence.
- [ ] Transport gate passes with required native/version evidence.
- [ ] Mercurial application baseline and end-to-end acceptance pass.
- [x] Record scope reconciliation and original-plan removal handoff.
- [x] Save useful evidence; commit this note independently of product work.

Task 0 must remain incomplete until both unchecked prerequisites pass. No runtime code, transport remedy, original-checkout edit, remote operation, or invented passing evidence is part of this audit.

## Task 3 — shared application shell (2026-10-27)

Shared infrastructure is implemented on `codex/shared-ui-implementation` after the local Tasks 1–2 development review gates. Actual page-family migration remains Tasks 5–7; these are disposable, visibly labeled synthetic fixtures, not production workflow acceptance. Existing release/native validation debt above is unchanged.

The real Macaron renderer, `base.TemplateFuncs`, i18n middleware, request context and `SetURLPrefix` are exercised by `modules/ui/render_test.go`. `BuildShell` consumes existing repository authority and performs no database queries. The reduced anonymous shell has no app navigation. Future Search/Recents/PR/Help/sign-out links require explicit server composition after their handlers exist; there are no placeholder production routes.

### Visual and keyboard receipts

Isolated loopback fixture: `UI_FIXTURE_ADDR=127.0.0.1:43127 go test ./modules/ui -run TestSharedShellBrowserFixture -v -timeout 30m`. It serves only synthetic fixture data and invokes the actual Markdown API handler. Its owned server was stopped and viewport override reset after QA. No user server, original prototype, installer or admin mutation was touched.

[Raw DOM geometry](ui-task3/geometry.json) records actual measurements. Desktop rail = 56px, context navigation = 208px. Real, broken and initials avatars = 28×28 inside 40×40 controls; horizontal and vertical center offsets = 0px, including enlarged text. Broken images are hidden while centered initials remain. No measured page has `scrollWidth` larger than `clientWidth`; vertical scrollbars account for client width below the nominal viewport.

| Nominal viewport | State | Actual measurements | Screenshot pixels |
| --- | --- | --- | --- |
| 1440×1000 | Real local avatar, workspace | rail 56; nav 208; control 40×40; avatar 28×28; offsets 0,0; page/client 1440/1440 | 1440×1000 |
| 1024×900 | Fallback, keyboard account focus | offsets 0,0; page/client 1009/1009; inverse outline 3px | 1009×887 |
| 800×900 | Broken avatar, repository | image width 0, hidden; Menu shown; context hidden; page/client 785/785 | 785×883 |
| 390×844 | Expanded repository drawer | drawer x56, width280; page/client 390/390; Close keyboard outline 3px, rgb(255,171,0) | 390×844 |
| 390×844 | Search popout | x64,y56,width310,height171,right374; search input focused; one open popout | 375×812 |
| 1440×1000 | 200% text enlargement | body 28px; title 56px; offsets 0,0; page/client 1425/1425 | 1425×990 |
| 390×844 | 200% text enlargement | body 28px; title 48px; offsets 0,0; page/client 375/375 | 375×812 |

IAB did not apply browser chrome zoom shortcuts (viewport/DPR/visual scale stayed unchanged). The explicitly labeled `text=200` fixture doubles body, heading, caption, table and helper font sizes and line heights; this is text enlargement stress evidence, not a claim of native browser zoom. The controller accepted this method. It has no production effect.

- [Desktop shared shell](ui-task3/1440-real.jpg), [fallback account focus](ui-task3/1024-fallback-focus.jpg), [800px broken avatar](ui-task3/800-broken.jpg).
- [390px shell](ui-task3/390-broken.jpg), [keyboard drawer](ui-task3/390-drawer-focus.jpg), [narrow search](ui-task3/390-search-popout.jpg).
- [Desktop enlarged text](ui-task3/1440-text200.jpg), [narrow enlarged text](ui-task3/390-text200.jpg), [Admin shell](ui-task3/1024-admin.jpg), [reduced anonymous shell](ui-task3/390-reduced.jpg).
- [Sanitized Markdown preview](ui-task3/390-markdown-preview.jpg).

Keyboard checks passed: Search focuses its field; Escape closes and returns focus; opening Recents closes Search; outside click closes; ordinary popouts do not trap Tab; drawer Shift+Tab wraps Close→Settings and Tab wraps Settings→Close; Escape restores Menu. All other body children become inert while the modal drawer is open and their prior inert state is restored. Keyboard utility tooltips use `aria-describedby`; account focus and drawer focus have visible 3px inverse rings. Reduced-motion rules remove motion and smooth scrolling.

Malicious Markdown containing script, image onerror and javascript links was submitted through the shared client to the actual API. The client explicitly uses `mode=gfm`; DOM checks found 0 script elements, 0 event-handler attributes and 0 executable javascript links. Write restores the unchanged original input. The API's default raw mode is not a sanitization boundary and must not be used by migrated editors.

A real `/code` language-link click produced `/code/?lang=en-US&page=2&q=hg`, retaining safe filters once and dropping the synthetic token parameter. The shared destination uses Macaron's stripped URL.Path rather than the legacy RequestURI-based Link.

### Verification

Required shell RED: missing ContextKind/Shell/BuildShell; GREEN: `go test ./modules/ui -run 'TestSharedShell' -count=1` passed, 3.596s. Later mount and alert regression RED/GREEN receipts are in the Task 3 implementation report.

Whole ordinary `go test ./...` passed: cmd 114.871s, models 31.052s, hg 33.173s, hosting 99.483s, ui 3.437s; all other packages passed/cached or had no tests. `go vet ./...` and `go build ./...` exited 0. After the final language/alert refinements, `go test ./modules/ui -count=1` passed (3.200s), `go vet ./modules/ui ./modules/base` passed, and all three shared scripts passed `node --check`. Tagged native acceptance and unresolved cross-platform release rows were not rerun or promoted.

## Task 4: authorized discovery and account Recents

Verified in the real IAB browser at `/code`, against an opt-in loopback-only server on `127.0.0.1:43128` with a fresh temporary SQLite database and visibly synthetic `fixturealice`/`fixturebob` accounts. No user server, repository storage, configuration, installation or production account was used. The browser tab was closed and the owned listener stopped after QA.

- [Anonymous Explore](ui-task4/anonymous-explore.png) contains only `fixturealice / public-lab` and no private count or rail.
- [Alice search](ui-task4/alice-search.png) returns only Alice's private match; [Bob search](ui-task4/bob-search.png) returns only Bob's private match after switching accounts in the same browser session.
- A successful authorized foreground fixture repository visit appears in [Alice Recents](ui-task4/alice-recents.png). [Bob Recents](ui-task4/bob-recents.png) stays empty. [Cleared Recents](ui-task4/cleared-recents.png) shows the empty state after a real CSRF form submission from the popout and its 303 redirect.
- [Create form](ui-task4/create-form.png) is the existing authorized repository creation handler at `/code/repo/create`; it was opened without submitting. Its legacy layout is retained for the downstream Task 5 migration.

Search and Recents are mutually exclusive popouts. Real browser checks confirmed Escape dismissal and outside-click dismissal after the pointer-focus fix. Browser QA discovered that pointer activation could dismiss a popout before its form received the click; the shell now preserves focus transitions during pointer interaction inside the panel. The executable Node regression covers null and transient outside focus, genuine outside-click and Escape dismissal, and trigger focus restoration.

`node --test public/js/app/shell_test.cjs`: 3/3 passed. It executes the production shell script with controlled input events, timers and delayed responses: 200ms debounce, cancellation on new input, stale responses arriving both during debounce and after newer results, stale failures, redirected HTML refusal, native submit and subURL query encoding.

Model/route tests cover current read authorization before totals and pagination, ready lifecycle, anonymous public-only filters, RequireSignInView at query and route boundaries, no global-admin private bypass, enabled accounts, owner search, 200 Unicode characters, page-size default 30/max 100, account isolation, unique ID retention 20/display 10, renames, revocation, deletion, CSRF clear/303 and foreground success-only visits. Assets, polling, errors, failed templates and redirects do not create visits. Actual production registration retains its installation guard.

Whole ordinary `go test ./...` passed (cmd 115.961s, models 41.350s, hosting 103.488s, ui 4.803s, routers/user 11.018s; other packages passed/cached or had no tests). Final amended-code checks passed: actual install-route regression, models/routers/user/modules/ui focused tests, three Node tests, focused `go vet` and `node --check`. No tagged native acceptance or cross-platform release status was promoted.


## Task 5 — workspace, repository browsing and settings

Disposable loopback fixture: `UI_REPOSITORY_FIXTURE_ADDR=127.0.0.1:43129 go test -tags integration ./tests/integration -run TestSharedUIBrowserFixture -v -timeout 30m`. Uses real Macaron handlers, current repository policy, SQLite, session/CSRF, and native Mercurial 7.2.4. Fixture accounts, store contents and passwords are synthetic. No user server, application configuration, live repository or mail delivery was touched.

Desktop inspection at 1440×1000 confirmed the 56px utility rail and 208px context navigation, current Source/Changesets/Settings context, long repository names, owner controls, local avatar fallbacks, clone protocol switching and visible “Copied OK” feedback. The selector shows two different full nodes for the same named branch, independently typed bookmark and tag choices, and immutable file URLs with encoded spaces and ampersands. Source highlighting retains escaped `<script>` text, with three source line anchors and zero embedded script elements. Merge detail exposes both parents, with seven/eight patch lines for the selected parent and immutable line anchors.

390×844 inspection confirmed a real Menu/Close drawer, wrapping long titles, binary and oversized notices with Raw/History actions, a zero-byte README, private reader source without repository Settings, and horizontally contained history/diff tables. Document client/scroll widths were both 375px (15px viewport scrollbar); the history region was 287px wide with 440px scroll width. Screenshots and exact receipts are in [ui-task5](ui-task5/geometry.json): [workspace](ui-task5/workspace-desktop.png), [source and Clone](ui-task5/source-clone-desktop.png), [typed references](ui-task5/revisions-desktop.png), [source file](ui-task5/source-file-desktop.png), [narrow source](ui-task5/source-file-narrow.png), [drawer](ui-task5/source-drawer-narrow.png), [history](ui-task5/history-narrow.png), [merge](ui-task5/merge-diff-desktop.png), [narrow merge](ui-task5/merge-diff-narrow.png), [binary](ui-task5/binary-narrow.png), [large file](ui-task5/large-file-narrow.png), [empty README](ui-task5/empty-readme-narrow.png), [private reader](ui-task5/private-reader-desktop.png), [narrow reader](ui-task5/private-reader-narrow.png), [empty repository](ui-task5/empty-repository-desktop.png), and [owner settings](ui-task5/settings-owner-desktop.png).

The history screenshot precedes the final small no-wrap hash-cell polish. Workspace was recaptured with all four local fallback images successfully loaded. The other captures retain the same native fixture graph from the previous owned fixture process; generated nodes and timestamps naturally differ after a restart. An accidental early history capture was replaced by the actual narrow merge view.

The real integration form checks pass create, settings update, rename, transfer and delete with stable repository IDs, plus independent Watch/Star and collaborator add/remove. GET never mutates; missing CSRF receives the existing middleware's HTTP 400 and leaves state unchanged. Reader settings requests remain HTTP 404. Invalid settings preserve non-secret input and destructive-action password failures never echo the password. Production registration and existing issue/rename/transfer workflows are tested separately in `cmd`.

Browser limitation: a final native `window.confirm` rename-cancel probe timed out in IAB's CDP focus handling before a cancel result could be observed. Subsequent close calls on that tab also timed out. No rename was confirmed or submitted; all successful mutations were confined to disposable automated fixtures. The initial ordinary suite found the missing old-source transfer caption; the escaped caption was restored and the affected workflow passed again. The whole ordinary run is not claimed as having passed; unaffected packages passed and the final covering checks passed. Existing platform/native-acceptance release requirements are unchanged.


## Task 6 - canonical Issues and discussions

Actual canonical global/repository Issues, new issue, detail/timeline, labels, and milestone list/new/edit pages now use the shared shell and local scoped scripts. The unregistered issue2/milestone2 prototypes were not activated. The audited, unused legacy repo/nav and repo/toolbar compatibility partials were removed; the shared header retains independent CSRF-protected Watch and Star forms.

Disposable browser fixture: `UI_ISSUE_FIXTURE_ADDR=127.0.0.1:43136 GOGS_TEST_HG=D:/github/gogs/.venv-hg/Scripts/hg.exe go test -tags integration ./tests/integration -run '^TestSharedUIIssueBrowserFixture$' -count=1 -v -timeout 30m`. It uses synthetic accounts, a temporary SQLite database, isolated attachments and ID-based stores, actual handlers/forms/CSRF/session, and native Mercurial 7.2.4. No live service/configuration/user repository, prototype, or real mail delivery was used. All owned fixture servers stopped normally. The last fixture passed (604.925s including manual QA), the temporary viewport was reset, and the owned IAB tab closed; its final tab inventory was empty. This does not resolve or claim cleanup of Task 5's older browser session.

[Geometry and capture receipts](ui-task6/geometry.json) record nominal 1440x1000 and 390x844 viewports. Desktop document widths were 1440/1440; narrow document/client widths were 375/375 with a 15px vertical scrollbar, and the drawer also measured 390/390. No page-level horizontal overflow was observed. IAB's tall fullPage capture produced fixed-position and scrollbar artifacts; those composites were omitted. Evidence uses viewport captures, including explicitly scrolled metadata views; screenshot pixels can be smaller than the nominal viewport, as recorded. Restarted disposable fixtures naturally have different contents/timestamps.

- Lists: [global desktop](ui-task6/global-1440.jpg), [global 390px](ui-task6/global-390.jpg), [empty assigned filter](ui-task6/global-empty-1440.jpg), [repository desktop](ui-task6/repository-list-1440.jpg), [repository 390px](ui-task6/repository-list-390.jpg).
- Editors: [Write selected](ui-task6/create-write-1440.jpg), [Preview selected](ui-task6/create-preview-1440.jpg), [German desktop](ui-task6/create-de-1440.jpg), [German 390px](ui-task6/create-de-390.jpg).
- Discussion: [desktop](ui-task6/discussion-1440.jpg), [390px](ui-task6/discussion-390.jpg), [scrolled responsive metadata](ui-task6/discussion-metadata-390.jpg).
- Labels and milestones: [desktop labels](ui-task6/labels-1440.jpg), [scrolled 390px labels](ui-task6/labels-390.jpg), [desktop milestones](ui-task6/milestones-1440.jpg), [390px milestones](ui-task6/milestones-390.jpg), [native date/shared editor](ui-task6/milestone-new-390.jpg), [translated milestone edit](ui-task6/milestone-edit-1440.jpg).

Browser interactions exercised actual create/edit, assignee and milestone selection/clearing, label creation/application/removal, close-with-comment and reopen, canonical comment anchors, native required-title failure preserving the body, long comments/URLs, native milestone date creation, and keyboard Write/Preview and Menu/Escape focus restoration. Sanitized preview removed script and image event attributes while Write retained the submitted source. Write/Preview selection is visibly underlined and uses the existing aria-pressed state. Sampled label rendering used black on #f0d2ab and white on #123456. The browser reproduced stale 100% milestone progress after reopening; the covered zero-value persistence fix was then confirmed by real close/reopen showing progress 100 -> 0.

The pinned locale library has no missing-key fallback. App-owned Localization now loads built-in English, then the requested locale, then its custom overlay before delegating unchanged selection/cookie/formatting behavior to the pinned middleware. German Issues strings are translated; missing shared strings inherit readable English defaults. Tests preserve explicit translations equal to key suffixes, custom values, formatting arguments, configured order/descriptions, cookie path, and repeated composition.

Real integration tests cover signed-in create/comment/edit, author-versus-writer authority, assignment/labels/milestones, status history/counters, CRUD of labels/milestones, safe validation input, CSRF, actual multipart and attachment-only comments, zero-byte files, private/anonymous/revoked/foreign attachment denial, cross-repository mutation denial without changes, and current-access filtering before totals/pages. An empty store is temporarily made unavailable while its Issues workflow runs, proving no history/open-storage dependency. A mixed set of 25 denied and 21 permitted candidates verifies the second permitted page and counts; unavailable lifecycle rows contribute no totals. Notifications are captured in a build-tagged in-process mail queue, without starting SMTP.

Global All retains the existing owned-repository scope. Assigned/Created include issues from currently readable repositories. Repository IDs cannot widen access. The model scans bounded metadata batches across the full candidate scope before counts and paging; it does not truncate to a catalog page. No priorities, boards, new workflow states, PR activation, or issue/PR identity merging was added. Existing priority-sort query compatibility remains backend-only.

Verification provenance: required initial RED failed shared shell/input preservation and disclosed a revoked private issue; initial GREEN passed (66.376s). The browser-discovered milestone RED failed with closed=1/progress=100 after reopen; combined GREEN passed (89.627s). The ordinary `go test ./...` passed once before the later locale-loading change (cmd 132.738s, models 49.409s, hosting 108.800s, ui 4.945s, repo 1.236s, user 25.221s; other packages passed/cached or had no tests). The final complete required `go test -tags integration ./tests/integration -run 'TestSharedUIIssue' -count=1` passed 84.667s after positive label/milestone CRUD coverage. After locale composition, affected middleware/ui/repo/user package tests and `go build ./...` passed; production canonical issue regression tests passed 14.917s; the workflow including German fallback passed 27.859s. Final zero-byte/private/global/lifecycle checks passed 44.271s. Focused vet and issues.js syntax checks passed. The ordinary suite was not repeated or represented as running after the locale change; unchanged heavy publication/cross-platform suites were not rerun or promoted.


## Task 6 review corrections - I1 through I6

Correction base: 4e33778. Known persisted issue/comment/status outcomes now have truthful failure notices, canonical GET recovery and preserved400/500 statuses. Saved text is not offered as a new issue/comment; only genuinely unsaved text remains. Rejected attachment count/type/size requests give specific configured reasons. Real multipart/storage, synchronous notification preparation, and post-status text-insertion faults verify safe recovery without duplicate content. Label counter updates explicitly persist zeros without turning partial name/color edits into AllCols updates; close/reopen and final open/closed detach/reattach are covered.

Discussion author AvatarLink images are restored. Repository/global rows now show actual assignee identities and the latest issue/comment/status activity. A real uploaded avatar and local fallback are verified through the HTTP fixture. Open/Closed classes use the approved info/neutral tokens. Genuine empty and filtered no-match lists have different explanatory text and permitted New issue/Sign in/Choose repository/Clear filters actions; explicit denied/missing targets return non-disclosing 404 responses.

Focused RED reproduced the save/status/counter/identity/empty defects (57.843s). The same three-regression union passed 59.688s after correction. The explicit counter model check passed 2.154s and shared empty-action renderer check passed 2.241s. Final amended actual-upload identity and status-saved/text-unsaved recovery checks passed 45.804s. No unchanged whole suite or native-publication acceptance was rerun. The final grouped shutdown/vet/diff tool call was interrupted without a receipt; that vet result is unconfirmed, not a pass.

Final visual limitation: IAB was unavailable and CUA listed no browser providers. The supported native Chrome control request timed out awaiting app approval before binding/input. No amended desktop/narrow captures were obtained; prior images above remain historical and do not verify the changed colors, restored avatars, list metadata or new empty-state presentation. Focused HTTP/model/render evidence supports behavior; final visual acceptance remains outstanding. No unsupported browser workaround, user-tab action or shared browser-process termination occurred. The owned updated fixture at 127.0.0.1:43137 shut down normally (fixture PASS 630.262s), which is setup/cleanup evidence only and is not a browser QA pass.


## Task 7 - administration, organizations, accounts and installer

Retained Admin, organization/team, settings, authentication/signup, profile compatibility and actual installer pages now use the shared shell/tokens. Early 401/403/404/500 pages use a reduced standalone boundary with no prepared Shell or working database requirement. Ordinary management mutations and real logout use CSRF-protected POST; one-time activation and OAuth completion GET protocols remain. Account-owned token/social/email actions reject foreign IDs. Profile/org/team repository rows and team counts consume central current read/readiness policy, without a new administrator read bypass.

Disposable real HTTP/render/model fixtures cover admin denial and mutations, organization/team workflows, profile/email/SSH settings, provider validation, registration/CAPTCHA/activation/reset with an in-process fake mail sink, English/German and configured APP_NAME, secret nonreflection, DB-specific controls and invalid setup. Successful SQLite setup runs the real installer in a copied isolated child executable/config directory, verifies initial admin, callback and installed-lock denial. No live setup, real SMTP or user data was touched. Network database success and other-platform acceptance remain untested.

Required initial RED exposed legacy shells, GET mutations, secrets and early-status coupling. Final Task7 integration union passed 24.922s. Production route mutation check passed 5.016s; current-policy/locales passed 6.932s; provider/settings passed 8.048s. Controlled production-script tests plus existing shell tests passed 6/6, covering database controls, actual repository-search DTO, confirmation cancel/retry/duplicate prevention and popouts. These are not browser tests. The earlier file named policy-red failed fixture setup (missing conf/app.ini), not a policy assertion; final actual unauthorized and unready assertions pass.

One ordinary `go test ./...` passed (cmd 131.821s, models 46.305s, hosting 109.336s, ui 5.383s). It preceded the final dormant compatibility-header logout forms and whitespace cleanup. After those markup edits, the production route/template check passed 4.699s, build and focused vet exited 0, and diff whitespace checks passed. Unchanged heavy native publication/platform acceptance was not rerun or promoted.

Visual acceptance remains outstanding: no supported browser was available, and the previously blocked native attachment was not retried or bypassed. No Task7 390px/1440px, long-name, keyboard or image-failure browser pass or screenshots are claimed. Existing captures above are historical. API list/search policy remains Task8 scope; final branding, profile extensions, avatar-provider work and draft storage remain their later tasks.


## Task 7 review corrections - I1 and I2

Submitted-form state is now explicit in RenderWithErr when a form is supplied; account shell preparation no longer infers it from a flash. Message-only nil-form errors remain distinct. Actual invalid-avatar-upload and protected user/source deletion redirects retain saved fields and active/admin/TLS/auto-registration flags, while current invalid form responses preserve submitted safe values. These redirect regressions already passed before the fix: the pinned session library separates incoming display flashes from its injected outgoing flash object, so the review's precise incoming-flash failure was not reproduced. The clearer explicit contract is implemented without claiming a fabricated RED.

Team permission retention did reproduce: invalid new Write/Admin requests returned Read and the retry saved Read; an Admin-to-Read edit with another invalid field returned Admin and the retry kept Admin. Radio selection now uses submitted permission on validation errors and existing/default authority on initial GET. Actual corrected-input retries persist the requested Write/Admin/Read authority; invalid edits do not mutate stored authority.

Focused complete RED failed 8.028s, and the correction union including existing admin validation passed 10.286s. Affected UI and middleware tests passed 4.899s/1.078s. A separate real-renderer helper test passed 1.830s and verifies both non-nil form state and nil-form message-only behavior. Outside Task7, repository creation uses assigned values directly and repository settings passes nil; that rendered behavior remains unchanged. No broad suite, native acceptance or browser retry was run. Existing visual/network-database/platform acceptance debt and Minor M1–M4 remain separately tracked.

## Task 8 - local Mercurial forks and private ancestry

Local forks now have durable direct-source/family relations and invisible provisioning identities. The shared hosting registry coordinates typed fork claims with existing review claims, refuses a busy source without cancelling its writer, bounds asynchronous admission, and cancels/waits accepted work at shutdown. Startup rolls back owned incomplete destinations; malformed, colliding or uncertain ownership fails closed. The existing review recovery payload remains unchanged. No operation resumes from a persisted authorization grant.

Native CloneVisible transfers full visible history into an independent, verified, no-checkout store, including empty repositories and fork-of-fork sources. It excludes secret revisions, source configuration and arbitrary sidecars. Global tags remain history-owned; native local-tag provenance and validated tombstones preserve supported visible local overrides without recreating global tags introduced only in secret history. Tests cover multiple heads, closed branches, bookmarks/tags, source configuration secrets and independent file identity.

The central policy checks private ancestors before owner, administrator and anonymous access returns. Catalog, Recents, retained profile/org/team lists, native transport and API list/search use current policy; serialized API permissions and the accounts.js full_name DTO remain consistent. Lifecycle boundaries reject dependent-parent deletion/transfer and unsafe visibility changes. Safe private leaf transfers retain lineage/privacy and require prospective personal recipients, or the actual destination owner-team recipients plus implicit organization owners, to read every private ancestor. This conservative organization predicate may refuse transfer until membership/access is corrected.

The actual shared Fork form uses the existing cmd-owned provider and the same managed operation as synchronous collaboration.Fork. Accepted forms redirect to an authorized provisioning page with no-store, same-origin, bounded status polling and manual GET retry. Every status/ready response rechecks current authority. The final ready transaction rechecks enabled actor, source/private ancestors, destination owner and lifecycle identity, plus completed owned storage. Failed identities cannot publish after an ownership marker is restored. No PR handlers, external-host forks or mail were introduced.

Verification provenance: initial missing APIs were compile REDs, not semantic coverage. Executed regressions exposed local-tag omission/global-local conversion, synchronous progress behavior, unbounded admission, unprepared ready publication and failed-identity reuse. The secret-tag fixture's first failure was a native setup command, not a demonstrated secret leak; passing final assertions prove exclusion. The initial full ordinary suite failed a retained error contract and two obsolete lifecycle fixtures. Those were corrected; the whole invocation is not claimed as passing. Complete affected ordinary packages subsequently passed (models58.651s, hg32.013s, hosting105.810s, collaboration1.216s, repo1.325s).

The pre-ready-hardening native fork/catalog/shared-review union passed models55.617s, hg69.785s, hosting162.042s and HTTP43.502s; actual HTTP/SSH permission-change suites passed130.747s. After prepared-storage and lifecycle-message hardening, the native fork union passed models50.441s, hg61.794s and hosting109.416s, and actual form/API/status/lifecycle HTTP passed50.190s. The final one-condition failed-identity guard received targeted native failed-identity, read-only source-journal and lifecycle coverage45.789s. Final brief tests passed models8.024s, collaboration1.122s and repo1.146s; catalog/Recents passed7.709s. Final build exited0. Focused vet passed before the final one-condition guard. Controlled production-script tests passed9/9; production route/header checks passed12.792s. Historical receipts are not represented as reruns on later source.

All native evidence uses disposable Windows/SQLite fixtures and Mercurial7.2.4. No live configuration, source checkout, SMTP or user storage was changed. No browser was available and no blocked attachment was retried or bypassed: actual HTML/subURL/CSRF/permissions and controlled JavaScript are verified, but visual/keyboard acceptance remains outstanding. Linux/macOS, Mercurial5.0.2 and network-database lock/phantom behavior remain acceptance debt. Allocation and post-preparation revocation are synchronized; no separate mid-exchange revocation or positive native organization-fork completion fixture is claimed. The ignored Task8 report retains exact commands, correction provenance, lock/authority contracts and the Task9 constructor/provider/family handoff.

## Task 8 review correction - I1

The real AddTeamMember/organization-transfer interaction now uses a current transactional team snapshot. Membership reads team repository IDs inside its transaction and updates only membership count; transfers reload current owner-team metadata/members for both ordinary repositories and forks and update only repository IDs/count. Transfer retains repository→team ordering; membership adds no inverse repository locks. The separately committed AddOrgUser enrollment precondition is retained and explicitly asserted.

An executed deterministic RED reproduced stale membership overwriting a completed private-leaf transfer's repository list/count (2.418s); this was metadata loss, not evidence of a private-content leak. The focused correction passed2.355s. Final actual model boundary cases passed12.796s: membership/revocation winning before the transaction causes transfer denial with rollback; competing AddTeamMember/DeleteAccess calls while transfer holds authority remained pending and completed after release. Assertions cover complete team metadata, grants, enrollment/member counts, stable lineage/privacy and current effective access. Real organization workflow/permission-retry integration passed9.298s, and the final build exited0. These are Windows/SQLite receipts; existing network-database/platform/browser/native-proof limits remain. The equal-node local/global tag provenance minor is deferred to final acceptance.

Affected actual shared repository mutation and fork lifecycle-message HTTP checks also passed41.323s on the same final source. No full native release suite was repeated.

## Task 13 — pull request forms, review and merge

Real workspace/repository PR lists, exact owner/repository source forms, immutable comparison details, review/comment/thread actions and merge confirmation now use the shared shell, editor and local assets. Exact source and target pins and native merge strategy are shown before one protected merge POST. Bounded same-operation GET polling/manual refresh preserves unknown/recovering semantics. Merged comparisons are frozen; historical artifacts remain subject to current source and target read access. Read-only GET freshness does not allocate comparison work. The source input resolves current authorized family membership without a truncated catalog dropdown.

Windows/Hg7.2.4/SQLite native HTTP coverage passed in307.238s: real create/review/update/async merge, CSRF/forged identity/stale submissions, binary/empty/missing downloads, and actual blocked raw writes canceled by source/target permission revocation. Later narrow source-name/merged-refresh regressions passed38.043s; reviewer-error text retention passed29.376s after behavioral RED. Current model/router seam tests, production root/sub-URL routes, build and vet passed. The ordinary whole-tree invocation originally failed three cmd fixture/obsolete-assertion cases; corrected entire cmd passed100.481s, rather than claiming that original invocation was green. Unchanged Task11/12 native suites were not rerun.

[Task13 browser evidence and capture provenance](ui-task13/README.md) records real desktop/mobile walkthroughs, exact source validation, truthful unprepared state, stale/refresh/outdated comments, required keyboard confirmation, same-operation pending-to-merged polling and merged source-read revocation. Final merged pages have no Refresh action. Final captures precede only the later reviewer-assignment error-field correction and removal of unreliable optional fixture controls; that correction has real HTTP coverage. Optional fixture control POSTs never established a successful browser result and were removed; native source/privacy setup used only owned disposable fixtures. No production CSRF weakening or user repository/configuration/mail activity occurred. Other platforms, older Mercurial, network databases, OS-kill/full-disk and remaining broad acceptance matrix are not covered by these receipts.


## Task 15 cutover asset audit (2026-10-28)

The supported workspace/repository/issues/accounts/organization/admin pages use the shared server-rendered shell and local app assets. Guest landing still calls ng/base/head, header and footer; Task16 owns replacement. Reduced status is separate; Task25 owns final status art/layout. This is an intermediate cutover, not completion of every final page.

| Candidate/dependency | Concrete caller and selector closure | Disposition |
| --- | --- | --- |
| DEV /template/* and routers/dev/template.go | Only registration was DEV block in cmd/web.go. Real production-router regression returned200 for issue2, milestone2 and base/head before removal, now404/405. No other TemplatePreview caller. | Remove route/handler. |
| repo/issue2 list/labels and repo/milestone2 list | Only named callers were unregistered Issues2/Labels2/Milestones2. Removed DEV wildcard was remaining dynamic path. Whole cmd/routers/templates search has no retained caller. | Remove three prototypes and handlers. |
| templates/base head/footer/navbar/alert | Exact template base/ search has no caller. ng/base is distinct and active. Removal of DEV wildcard closes dynamic access. | Remove obsolete Bootstrap shell. |
| root public/css bootstrap-colorpicker.min, bootstrap.min, datepicker3, gogs, markdown, todc-bootstrap.min CSS and Bootstrap/TODC maps | Only template links were base/head. Shared pages load app.css; guest loads ng CSS plus font-awesome/github CSS. No retained lazy style link. | Remove Bootstrap-only styles/maps. |
| root public/js app, bootstrap-colorpicker.min, bootstrap-datepicker, bootstrap.min, jquery-1.10.1.min, jquery.validate.min, lib JS and jquery1.10 map | Only runtime script entry links were base/head. No retained template/shared script loads them dynamically. Old global IDs can share names with current forms, but those pages load scoped accounts/issues/repository scripts. Removing sole app.js entry closes its selectors without deleting controls. Guest uses ng jquery1.11 and ng lib.js. | Remove closed old script graph. |
| public/js/app/prettify.js | repo/header links once; repository.js invokes prettyPrint for data-source-code. | Retain source highlighting and Apache attribution. |
| ng/base, public/ng, root font-awesome/github CSS and fonts | Guest home actively loads ng/base/head; ng/gogs.js owns promo-form/username/register-button/social selectors. Bundled plugins are still loaded there. | Retain until Task16 proves replacement graph. |
| public/js/ZeroClipboard.swf | Active ng/gogs.js constructs AppSubUrl + /js/ZeroClipboard.swf. | Retain with ng dependency; shared pages do not load it. |
| root datepicker locale files | No final selector-by-file audit completed here. | Retain inventoried dormant candidates; not runtime support evidence. |

No operator custom-template directory was inspected. Custom overrides referring to removed internal Bootstrap components must adopt shared components; external overrides are not an internal source caller. Shared assets use the existing safe tree walker; public/js/app/*_test.cjs is excluded as source-only test material without widening accepted file types. Both public UI/review evidence documents accompany the archive. Archive checks inspect actual source entries and preserve translator/library attribution.


### Task 15 acceptance receipts and remaining matrix

Actual environment is Windows/amd64, Go1.27.3, Hg7.2.4 and disposable SQLite. New cutover tests cover shared account/Admin/auth/install/early-status and global/repository issue/milestone rendering, linked-once local dependencies, and root/sub-URL en-US/de-DE forms plus actual static script retrieval. Existing account/org/issue workflows continue to cover detail/edit/label/team routes; real cmd newWebHandler route checks separately establish production registration/method/CSRF/order and continued Git/deferred absence. Fixture aliases alone are not production route proof.

Focused final receipts: production routes cmd12.424s/router presentation0.473s; shared family matrix17.83s; root/subcode/two-locale/static retrieval34.28s; carried installer/SMTP/autocomplete/catalog/historical-review/POST discovery/unprepared pins regressions passed in their owning seams. Exact independently known binary source bytes (NUL changed SOH), empty zero bytes, and text line-one/line-two bytes passed in native HTTP stale/conflict/download coverage132.86s. Native equal-node tag source/fork/fork-of-fork resolution and Type parity/local provenance plus existing secrets test passed60.908s. Native Mercurial can prefer newer superseding global history; no stronger precedence is invented.

The actual configured NoGit process acceptance passed173.509s (test167.22s): fresh owned executable/assets/database/repositories, Git-containing directories excluded from server PATH, native HTTPS target and source pushes, protected production fork form/status, exact owner/repository PR, native comparison, owner assigns a separate writer, that writer signs in and formally reviews, owner merges, same-ID JSON polling reports merged and target contains the source node. The app stopped naturally and cleanly. Archive construction/entry checks are separate from this copied-tree runtime receipt; it does not promote older extracted-package results to a new supplied-archive run. Earlier fixture self-review/unassigned review403s and bounded full-HTML-poll recovering result remain failed invocations; the final flow mirrors production pulls.js JSON polling. No permission/hook/CSRF weakening or claim about arbitrary frequent HTML polling followed.

Actual rendered label-form extraction/submission passed24.253s on unchanged runtime. Browser label failure was a malformed synthetic LabelIds seed, repaired only in owned fixture data; a fresh real browser attach passed. Approved post-freeze copy-only en/de visibility guidance render proof passed32.023s with both public unchecked and private checked states preserved. Browser captures before that copy amendment retain their original provenance.

Ordinary whole-tree command go test ./... -count=1 -timeout 40m (native env explicitly unset) passed exit0: cmd124.240s, models71.924s, collaboration10.562s, hg39.361s, hgbridge17.923s, hosting88.463s, user15.570s; remaining packages passed/no tests. Vet/build exited0. Subsequent changes were fixture seed/import, actual rendered-form assertion and approved guidance copy; broad native behavior did not change. Final package suite passed9.848s after asset removal, including real current shared assets/evidence entries once and test sources excluded. Three unsafe-boundary symlink cases skipped for missing Windows symlink privilege; no privilege was changed. Controlled local script tests passed9/9,0skip.

| Acceptance row | Actual status |
| --- | --- |
| Required full tagged go test -tags integration ./... -count=1 -timeout 40m, Hg7.2.4 | FAILED exit1 on the pre-browser-polish snapshot: collaboration2407.123s and integration2408.803s exceeded the40m package budget; LDAP85.070s had four external connection timeouts. No full tagged pass. |
| Required race packages, CGO_ENABLED=1 | FAILED before tests: gcc missing from PATH in runtime/cgo. No compiler installed; no race pass. |
| Native Linux/macOS, Hg5.0.2/2.0.2 | Not executed/installed. CI native/legacy collaboration list additions are configuration only. |
| Real MySQL/PostgreSQL concurrency | Not executed; SQLite/current dialect tests are not networkDB proof. |
| Task14 Step4 full organization/private/transport/lifecycle matrix | OPEN; its actual Windows HTTPS/SSH/controlled-gap receipts are separately transferred to review-operation-evidence.md. |
| Forced OS kill, power failure, full disk, every filesystem boundary | Not executed; controlled durable interruptions and clean restart do not establish them. |
| Large Mozilla/live repository acceptance | Not opted in for Task15; no other-chat workloads used. Existing failed/unverified full-scale gates remain. |
| Guest landing and final status completeness | Task16 and Task25 respectively; active ng dependencies retained. Final whole-branch review is after all follow-on tasks. |
| Browser widths/keyboard/text enlargement/true zoom/locales/roles and Task5-7 visual debt | Actual staged [Task15 capture manifest](ui-task15/README.md) separates first-source real authenticated interactions from final guest en/de CAPTCHA/layout proof and blocked post-fix authenticated checks. True200% browser zoom, complete roles/popouts/keyboard and rename-cancel remain open. |

Historical capture/geometry links in this document refer to the source evidence tree. Portable archives include these evidence texts and runtime assets; they do not bundle all historical screenshot trees. Build stat-cache/line-ending notices from Task14 remain disclosed historical facts. Current required checks used an appropriate owned TEMP and per-command optional-lock setting without changing global Git/build settings.


### Final Task 15 browser amendments and validation provenance

[Actual Task15 captures and limitations](ui-task15/README.md) record initial true alice login, issue assignment/close/comment/reopen/label actions, desktop one-shell geometry, exact28px avatar centering in40px control, narrow issue375/375 and the real pre-fix Admin table375/525 failure. The Admin wrappers existed but had no overflow rule. They now constrain horizontal scrolling locally, keep headings readable, and expose labelled keyboard-focusable regions. Final rendered-container coverage passed47.592s with root/subURL/locales/static retrieval; browser post-fix authenticated Admin geometry remains unverified because a native-confirm interaction left one owned tab stuck. No successful rename cancel, complete all-role/popout/keyboard or true200% zoom is claimed. Root reset the supported Cua runtime once, closed completed capture tabs and reset viewport/visibility; old tab4 cleanup still needs user assistance.

Browser-driven source corrections after the original broad snapshot are narrow and independently covered: Admin Organizations/Configuration now use their existing localized title keys; existing en/de visibility helper describes private access instead of asserting every repository is private. Pinned i18n and Captchaer each run under a scoped route-relative RequestURI adapter and restore original RequestURI unconditionally. The locale adapter also accounts for Macaron trimming Path but retaining prefixed RawPath, preserving encoded slash/space identity. Cookies, fallback/overlays, native CAPTCHA generator/cache/validation and protected route policies remain with their original components. No global URI rewrite, new runtime/test API or authentication shortcut was added.

Actual emitted captcha PNG decoding/reload first passed at root and failed404 at subURL; the separate adapter fixes that seam. Final covering locale/headings/captcha/route command passed cmd12.193s/middleware1.456s. Final real browser signup captures at390x844 show local complete240x80 captcha images in en/de, document375/375; German1440x1000 is1440/1440 and language redirects retain one /code. No captcha was transcribed, solved, submitted or bypassed. Earlier broken-image captures remain labelled failure evidence.

Full tagged details are retained: native hg848.487s, bridge241.286s, hosting1192.704s, cmd243.449s and models118.498s passed; httpbin/httplib1.710s passed. Four modules/ldap tests (Connect, Search, SearchWithPaging, MultiGoroutineSearch) failed against141.213.189.137:389. The package budgets expired with TestPullStrategyInspection active1m and TestSharedUIRepositoryRoutes active57s/subcode11s, not individual tests running40m; no deadlock/root-cause claim or full rerun follows.

That invocation also exposed an inherited stale TestSharedUIAccountsCurrentReadPolicy expectation after Task14 rejects inactive credentials. Focused diagnosis7.317s confirmed persisted inactive=false, named actor model Read=false, HTTP anonymous200 with account controls and owner email absent. Root approved a test-only correction: previously valid session and Basic credentials may receive public anonymous content, while a new explicit private profile row/email/account controls remain hidden and named inactive model reads stay denied. Existing org/team live-state assertions remain. Final owning matrix/inactive-signin tests passed models2.379s and HTTP6.914s. No runtime auth/permission behavior was changed; the original full failure stays failed.

Final complete affected middleware/UI packages passed2.137s/5.753s. Original ordinary whole-tree pass and full native attempt predate these browser-driven web-only amendments; focused production-route tests plus final vet/build cover them. Native hosting/collaboration code was unchanged and the expensive native gate was not repeated. Race remains unexecuted after its missing-gcc build failure. After the timed-out tests, final build initially failed because their generated Go files remained under dist and were discovered as nested Hg/Git sources. No-active-owned-process and no-reparse/containment checks preceded removal of exactly three owned timeout directories; a fresh owned temp outside the checkout restored final vet/build exit0 with VCS stamping enabled. Final archive inspection passed7.970s; three Windows symlink-privilege skips remain. Logs and original failures were preserved; no compiler install or global Git/build workaround occurred.

All three owned browser servers stopped normally after root authorization. Task14 Step4, native platform/version/database/race/fault matrices, interrupted browser checks and Tasks16-25 remain open; this is a reviewed-development handoff candidate, not final release or whole-branch acceptance.

## Task 16 — approved Hgs landing and identity (2026-10-28)

The actual public Home now uses the approved Hgs composition and exact tagline, A painless, self-hosted Mercurial service. It uses the shared shell and local assets, real sign-in/conditional registration/provider routes, a real public-only catalog preview, and a native hg clone command only when a public repository exists. No prototype catalog, host, notice, dialog or simulated authentication is used. The command copies through the existing scoped repository controls; without script it remains selectable.

The production root route now uses the existing neutral Toggle, under the explicit Task16 controller ruling to render the approved identity landing when REQUIRE_SIGNIN_VIEW=true. This change is limited to root Home. ShowPublicExplore is the inverse of that setting. Disabled public browsing causes no landing catalog call, link, preview, count, clone or related empty state. Enabled browsing uses ListAccessibleRepositories(actorID0, Visibility public, PageSize3). Direct anonymous Explore/search preserve canonical404 when blocked, and query visibility all/private or pagination cannot widen the anonymous public policy. Signed-in dashboard routing and authorized private catalog remain. Actual newWebHandler tests cover both settings at root and /code, public-only counts, public-empty no-clone, signed private access and signed Home.

Configured APP_NAME values remain present and escaped in page/mail identity. Default APP_NAME/fallback and user-facing locale product labels now say Hgs; internal configuration/import/repository identifiers and provider names remain. Existing shared legal attribution/source/license/language/help behavior remains. The one identical duplicate hg-table-scroll/th CSS rule carried from Task15 review was removed without changing its behavior.

Approved mascot source/destination SHA-256 is D82630AF7EF9F05089FA6DC1D6F28B76B14B4478E6F6E1E4CD851C1B6945B810: docs/prototypes/landing-page/gopher-lab.png was byte-copied to public/img/hgs-mascot.png (1218×1292 RGBA;1,002,639bytes). No artwork regeneration/edit occurred. Visible credit and hgs-mascot.LICENSE.md preserve original Renee French/Go Gopher CC BY4.0 source/license links, accurate AI-adaptation date2026-10-26 and lab coat/beaker/pose changes, with no invented adaptation author/licensor or endorsement. Root independently verified the matching hash and image.

hgs-beaker.svg is the standalone vector master derived from the approved held beaker's pouring lip, rim, pale glass, graduations and blue liquid. The inverse is a palette derivative; the former sprite beaker is an alias to that same master. Header/favicon use master, blue rail inverse, and mail the32px export. Bundled Sharp rendered PNG16/32/180; installed Pillow produced ICO16/32 from the master180 export. Automated decoding verifies dimensions, transparent corners and ICO frame directory. Supported callers no longer reference gogs-lg.png/favicon.bak.png, so exactly those two obsolete images were removed. Dormant ng assets/templates and active prettify/license remain conservatively retained.

All six existing mail-template families now share compact inline-compatible presentation and local raster beaker identity, with absolute AppUrl assets/actions, escaped data, readable action labels and visible instance text. The separate direct issue-watcher HTML path is also branded and escaped. Existing helper signatures, delivery subjects/purpose, recipient rules, triggers, tokens, action paths and lifetimes remain. Reset display deliberately retains ActiveCodeLives to match the unchanged existing token generator. Fake-sink tests exercise all seven delivery helpers and actual SQLite watcher rows: actor exclusion, sender/recipient/subject, no empty-recipient delivery, action links and escaping. Existing signup/issue UI integration exercises their real triggers. No PR email was added and no SMTP worker or real mail delivery was used.

Packaging permits only the exact new public/img/hgs-mascot.LICENSE.md and public/img/favicon.ico names in addition to the prior extension policy. Real ZIP inspection requires mascot/license/master/inverse/favicon180/16/32/ICO each once; arbitrary unapproved.md/unapproved.ico and private/runtime files remain rejected. The final full package suite passed9.910s; the three pre-existing Windows symlink-privilege skips remain explicit.

Final focused command receipts on frozen runtime:

| Command | Result |
| --- | --- |
| go test ./routers ./models -run 'TestLanding\|TestCatalog' -count=1 | PASS routers2.231s/models2.083s |
| go test -tags integration ./tests/integration -run 'TestLanding' -count=1 | PASS6.879s |
| go test ./cmd ./modules/mailer -run 'TestLanding\|TestExistingMailBranding' -count=1 | PASS cmd8.476s/mailer3.038s |
| go test -tags integration ./tests/integration -run 'TestSharedUISignup\|TestSharedUIInstaller\|TestSharedUICutoverAndRouteMatrix\|TestSharedUISubURLAndLocales' -count=1 -timeout10m | PASS66.993s |
| go test scripts/package.go scripts/package_test.go -count=1 -v | PASS9.910s;3Windows symlink privilege skips |
| go build ./...; go vet ./... | Both exit0, VCS stamping enabled |

Complete relevant Task15 UI route regression go test -tags integration ./tests/integration -run 'TestLanding|TestSharedUI' -count=1 -timeout25m passed355.792s. Ordinary go test ./... -count=1 -timeout15m with native env unset passed all packages (cmd122.267s,models69.505s,hosting83.976s,ui5.813s). These began before the final reset-email display-preservation correction and additional public-empty/count test assertions/fixture variants; the final owning checks above cover those changes. Unchanged broad native publication/platform gates were not repeated or promoted.

Original failed invocations remain available in worktree-root task16-*.log files and the detailed local Task16 implementation report. Initial isolated Home renderer lacked legacy footer timing data (HTTP500), the first mail renderer lacked base.TemplateFuncs (compile panic), and sandbox TEMP denied native Go work-directory creation; these are setup failures rather than presentation RED. Corrected HTTP200 landing, real account rendering, real ZIP entries and six mail-family tests failed meaningfully before implementation. Direct watcher RED against the original helper exposed missing identity and raw script output, plus fixture SQLite cleanup ordering that was corrected. Later raw landing locale keys were corrected from [pulls] to [ui]; a transient unsupported DisableRegistration template function caused explicit failed whole ordinary/UI invocations before correction. An echoed search-input false positive and uninitialized SHOW_REGISTRATION_BUTTON fixture were corrected. The old failed results are not relabeled as passes.

All Task16 test TEMP/TMP directories are owned and outside the source checkout: C:/Users/raynorpat/AppData/Local/Temp/hgs-task16-2dd92e2e51774fb6aa86f7d6a2e1f71f. No Go fixtures were generated under dist. Windows/amd64 Go1.27.3, native UI fixtures Hg7.2.4 and disposable SQLite are the actual environment. No compiler installation, global Git setting, live service/data, merge or push was used.

Root-owned visual review uses six independent actual-production-router fixtures with disposable accounts/data, real sign-in/session behavior and no production fixture/authentication bypass. Initial reported visual checks pass at1440/390 for public/restricted landing, with document widths1425/375 and no private labels. Restricted has0Explorelinks/0repositorylinks/no clone; empty390has public-empty/no clone; closed390has0signup/0providerlinks; custom390preserves the configured operator name without overflow. The identity proofsheet loads all26actual images and shows favicon16/32 plus master/inverse16/24/32 on white/blue, with the approved mascot150px transparency. Images-removed mail previews are a clearly labeled simulation, not proof of real mail-client image blocking or cross-client compatibility. Root final signup/install/mail/capture findings and owned shutdown are recorded in the adjacent ui-task16 README once visual review completes.

Task15 full tagged aggregate/race/platform/old-Hg/fault/database/browser caveats remain as previously recorded. Tasks17–25 and final whole-branch/release acceptance remain outstanding.

Root final Task16 visual review reported no product fix findings. Actual signup390 and installer390/1440 display final beaker identity without submission; mail collaborator images-on, simulated image-removed and custom-name variants390 are readable without overflow. Exact twelve-JPEG sources, dimensions and interaction limits are recorded in [ui-task16/README.md](ui-task16/README.md). Root authorized the capture inventory and normal shutdown of all six owned servers; each stop returned200 and each test exited naturally with PASS. No process kill was used. Root reset the viewport and closed capture tab7; old stuck Task15 tab4 remains unconfirmed. Full keyboard traversal, clipboard Copy activation, OAuth/login/signup/install submissions, signed-in final rail geometry and true zoom remain browser acceptance debts; no CAPTCHA solve or real mail-client image-blocking pass is claimed.

The separately named PR UI integration supplement (TestPullSourceNameForm, TestPullReviewerErrorRetainsText, TestPullInlineCommentRetryRetainsAnchor, TestPullHTTPWorkflow, TestPullHTTPDenialAndCSRF, TestPullUIStaleConflictAndBinary) passed381.306s on frozen runtime, completing those route families beyond the TestSharedUI selector. The exact carried Task15 production route/presentation union passed cmd13.385s/routers-repo1.129s. After all tests/servers exited, positive owned-temp process audit found0active processes; exact target/containment and zero-reparse checks preceded removal of only the Task16-owned external temp tree (12remaining owned entries). Final temp existence=false; initial owned sandbox temp was already absent. All original test/browser failure and success logs remain local. No process kill, compiler/global setting change or unrelated cleanup occurred.

## Task16 review fix round1 — watcher content URLs

The Important review finding was verified: direct watcher email content inherited route-relative mention/issue/changeset hrefs from the unchanged web renderer. The email-only amendment now parses generated anchor attributes and resolves them with the standard URL resolver against AppUrl. Root-relative mentions retain the configured sub-URL exactly once, repository-relative references resolve beneath AppUrl, and already absolute external references retain their origin/fragment. Escaped issue text remains escaped; malformed user-authored reference hrefs are omitted while their rendered text remains, so URL parsing cannot cancel that existing mail delivery. No shared web renderer, auth/token/action URL, subject, recipient, trigger or PR-mail behavior changed; no remote fetch occurs.

Covering TestExistingMailBrandingIssueWatchers asserts exact root and /code mention/issue/40-node hrefs, absence of relative/duplicated-prefix links, unchanged main CTA/sender/recipient/subject, preserved script/ampersand escaping, existing external link/fragment and malformed-reference text/delivery. The six other existing mail helper checks also pass. Root/sub-URL reference RED failed2.617s; the malformed-content guard's initial RED failed2.530s before correction. Final focused mail receipts and final full build/affected vet exits are recorded in the Task16 implementation report and original task16-fix1-*.log files. Broad SharedUI/PR/native/ordinary suites were not repeated for this email-only amendment.

The twelve earlier ui-task16 captures precede this direct-watcher-only URL amendment; none is new visual evidence for its generated references. The shared templates/assets/landing geometry and documented browser/release debts are unchanged.

## Task 17 — local Help, footer navigation, and repository preferences (2026-10-28)

Committed source `3fbd16a` adds local `/help` before the production catchalls under existing sign-in/install guards, with the reduced guest shell or signed-in workspace shell. Shared Help links are published only after installation. Localized guidance covers supported Mercurial browsing, configured Clone methods, account access, issues, pull requests and independent Watch/Star meanings; SSH guidance follows the effective enabled/address condition. Existing operator identity and legal source/license footer destination remain.

Production locale selection uses the approved existing Redirect=false option and central safe-query links. Actual production root/sub-URL tests cover selected German translation, scoped language cookie, subsequent cookie selection and safe filters without copying token/password parameters. The native locale component and explicit Redirect=true adapter coverage remain. A future explicit Macaron convention REDIRECT=true can override false; current application configuration never installs that convention config.

Existing labeled Watch/Star POST controls, guest sign-in affordance, CSRF/permission policy, duplicate prevention and notification meaning were retained. Forced late SQL failure tests exposed partial membership/counter updates. The approved narrow correction makes exported model operations atomic while retaining the internal helper for caller-owned transactions. Add/remove rollback and repository/user counts, real authenticated private reader actions, sequential duplicate idempotence, visible error flash, explicit retry and action independence are covered without adding PR email or optimistic JavaScript counts.

Final exact focused integration command passed routers 6.639s, routers/repo 1.624s and native Hg 7.2.4 integration 121.682s. Full affected routers/repo/UI/middleware/mailer packages passed; selected model rollback/access/organization/transfer checks passed 11.707s, and production Help/locale/reader/owner checks passed 13.266s. Build and affected vet exited 0; build emitted a disclosed module stat-cache permission warning. Exact commands, meaningful RED, setup corrections and logs are in the Task 17 implementation report. Unchanged broad native/PR/platform suites were not repeated.

The four actual guest captures and precise interaction limits are recorded in [ui-task17/README.md](ui-task17/README.md). Root reported no new product fixes at 1440px and 390px. Help and repository document widths do not overflow; repository quickstart scrolls locally. German Help retains safe context through footer links and later cookie selection. Actual 32px rail beaker centers in its 40px control. This pass did not establish signed-in owner/reader keyboard interaction, preference clicks, menu activation, clipboard, or true zoom. The synthetic fixture's configured example.test quickstart is not native clone evidence.

The first sandboxed fixture was alive but unreachable and required stopping only its positively identified owned process; its forced-stop FAIL 318.299s remains a setup receipt. Restarting the same unchanged loopback-only fixture with scoped execution permission restored actual browser access. The replacement stop returned 200 and its test exited naturally with PASS 857.378s; listener/process absence was verified. All original logs remain in the owned outside-checkout Task 17 temp directory through independent review. No global network, authentication, SMTP or production source change fixed the setup issue. Working tab 9 closed and viewport reset; failed tab 8 and earlier stuck tab 4 cleanup/input recovery remain unconfirmed, with browser visibility restored for the pending user handoff. Tasks 18–25 and earlier explicit release/browser debts remain open.

## Task18 — same-page editor draft recovery (2026-10-28)

Source2171aa8 adds page-bound temporary drafts, scoped collection cookies, current permission/metadata validation, atomic cap/version/discard/lifecycle cleanup and an owned expiry purger. Issue/PR editors use a narrow single canonical async submission path to retain the original document on session expiry; no automatic submission/retry or new review-body feature is added. Native CSRF factory renewal and scoped SQL logging privacy boundaries retain the existing authentication/domain policies. Exact rulings, original failures, corrected test expectations and final focused/owning receipts are in the Task18 report.

Focused model/service/handler/production-router checks and integration passed; the integration browser-lifecycle case is explicitly skipped. Owning routers/UI/middleware, selected model/collaboration and issue UI integration passed. The broader cmd selector originally failed on an owned SMTP sandbox denial and an inherited locale302 assertion after Task17's approved Redirect:false change. Affected-only SMTP and corrected locale reruns passed, without claiming the original invocation passed. Build/vet exited0; the build's module stat-cache permission warning remains disclosed. No unchanged40-minute transport suite was replayed.

[Browser entry evidence](ui-task18/README.md) records the actual normal-login attempt and1440/390 captures. No login POST occurred, so authenticated draft lifecycle remains unverified; these images do not demonstrate recovery. The disposable fixture stopped and joined cleanly, and tab10 closure was confirmed. Earlier tabs4/8 and viewport cleanup remain unconfirmed after a browser timeout; visibility was restored for the pending manual handoff. No auth shortcut or alternate automation bypass was introduced. Inherited native/race/platform/database and remaining browser acceptance gates stay open.

### Task18 reviewed code fixes (11d2143)

A fresh scoped review verified six fixes: operational authorization errors no longer erase local drafts; trusted disabled/deleted session identities are separated from expiry/outage; known pre-mutation PR denials use JSON; unknown/partial submissions allow read-only account/access checks while remaining mutation-locked; inactive deletion uses exactly the locked/rechecked IDs; and candidate-source validation preserves fields without losing established-source checks. The status-only check allocates no draft and returns no payload/capability. Missing/unreadable candidate names remain non-disclosing input errors. No automatic submission/retry or native permission change was added.

Covering focused Go checks passed (models18.764s, service1.779s, user7.322s, cmd44.634s), owning middleware/repo checks passed3.720s/2.114s, focused integration passed10.933s, and the actual-controller Node suite passed9/9. These controller tests use DOM/fetch doubles and do not constitute browser acceptance. Build and affected vet explicitly exited0; the cache warning remains. Deterministic SQLite phase triggers cover the inactive-deletion set defect, while actual two-connection PostgreSQL/MySQL execution remains unavailable. The original absent empty Tee vet-file reference was corrected; the new amended-code vet receipt records its command and exit.

The original review's required authenticated browser lifecycle finding remains open after this fix review. Task18 is not accepted, and its next-task gate has not passed. The browser entry captures above predate11d2143 and demonstrate none of the corrected lifecycle behavior. The source tree is committed and all owned servers are stopped while browser recovery is pending.

### Task18 final scoped acceptance (3745fe7)

This supersedes the earlier blocked-browser/next-gate status while retaining those attempts as history. All reported Task18 code findings and actual authenticated browser lifecycle evidence are independently accepted through3745fe7. [Detailed browser evidence and limits](ui-task18/README.md) include expiry/normal reauthentication/newer-local text, Version0 edits, independent tabs/editors, navigation/Cancel, cookie refusal, Save503 and unknown outcomes, native attachment reselection/multipart, and actual failed unload followed by the real minute-purger removal of the retained owned row. Row expiry was explicitly advanced only in the disposable test; no30-minute wall-clock claim or production TTL/clock change is made.

The final real-purger attempt exposed SQLite local/UTC query mismatches. Equivalent-instant RED cases reproduced wrong caps, refused live saves and incorrect purge rows; the SQLite-only owning-query correction passes all24 offset/date-boundary leaves and all13 selected draft model tests. All5 focused fixture tests pass, including61.88s real-minute cleanup and early-stop joining. Build/affected vet exit0, with the original build-cache warning retained. Prior failed/setup receipts remain preserved; none is relabeled green. Other database timezone behavior, actual network-DB concurrency, native/platform/race and full release claims remain unverified or failed as previously documented.

The last actual browser fixture stopped normally: session47393 exit0/package269.339s, owned PID27236 absent/listener51345 count0. All managed fixtures are stopped; root-owned test tabs closed and viewport reset, user preview tabs preserved. Task18 development gate is clear and Tasks19–25 continue. No runtime merge/push or broad release acceptance is claimed.
