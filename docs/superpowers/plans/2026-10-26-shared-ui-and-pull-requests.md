# Shared UI and Mercurial Pull Requests Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deliver one Bitbucket-inspired interface across the application, including Issues and Admin, with accessible repository discovery, a working Mercurial fork/review/merge workflow, and the approved Hgs landing page and mascot.

**Architecture:** Keep Go/Macaron, server-rendered templates, xorm, and the managed Mercurial runner. Replace duplicated presentation with shared partials, semantic CSS tokens, and scoped JavaScript. Add ID-based fork/PR services and guarded, recoverable local Mercurial operations; do not implement a second transport or permission system.

**Tech Stack:** Existing Go module/toolchain (currently Go 1.27.3), Macaron, xorm and existing database drivers; checked-in CSS and local SVG icons; small JavaScript modules with no deployment build service; managed Mercurial server >=5.0, tested versions 5.0.2 and 7.2.4, native clients >=2.0 under the hosting plan's matrix.

**Spec:** [Approved shared UI/PR design](../specs/2026-10-26-shared-ui-and-pull-requests-design.md), [design system](../../DESIGN.md), and [hosting prerequisite plan](2026-10-25-mercurial-hosting.md). Read all three. Updated 2026-10-26 with approved landing-page/identity Task 16 and completion/draft-recovery Tasks 17–18. Task 0 was audited and remains blocked; no production tasks have run. Execution is approved with subagents in the existing worktree. Prerequisite work remains with the existing hosting chat.

## Global Constraints

- “Mercurial-only hosting, fresh database/no Git migration, ID-based repository storage and access, Go-managed Mercurial processes, no custom Python helper or production wire-protocol parser, no arbitrary hooks/extensions, and native Windows/macOS/Linux support.”
- “Product implementation of this project must not bypass the earlier transport feasibility gate.” Its evidence currently says **FAILED**; this plan does not choose or authorize a remedy.
- “Hosted repository stores remain without checkouts.” Temporary working directories are permitted only for safe, isolated merge preparation.
- “Unmerged source commits must not become cloneable from the target merely because someone opened or reviewed a PR.”
- “No background job may inherit a previous actor's reusable grant.” Recheck live permission and credential/account state.
- “Do not squash, rebase, rewrite source changesets, automatically delete branches, or change unrelated bookmarks.”
- “No legacy Bootstrap issue-page shell remains at completion.” All supported page families converge on shared components.
- Utility rail 56px + context navigation 208px; account avatar 28px centered in a 40px square control; proposed narrow-screen breakpoint around 800px, with a real menu opener.
- Recents retains 20 unique repository IDs per signed-in account and displays the latest 10 currently readable repositories. Search covers repository/owner names, not code.
- No Bitbucket branding, new SPA, external-server forks, CI/pipelines, merge queues, mandatory approval counts, squash/rebase, web conflict editor, or new PR email delivery.
- Keep product text localized, assets local, `AppSubUrl` respected, and all mutations CSRF/permission protected. Preserve existing account/issue notifications.
- No broad cleanup of unrelated code. Never stage another task's changes. No remote or push is authorized by this plan.

- Product identity: Hgs or Mercurial service; exact landing tagline: **A painless, self-hosted Mercurial service.** Preserve copyright/license notices and internal identifiers.
- Public landing Explore shows public repositories only and is omitted when [service] REQUIRE_SIGNIN_VIEW = true. Enforce the same policy at query and route boundaries. Signed-in workspace Explore retains authorized private results.

- Keep both Watch and Star. Include existing mail branding, local Help, language/footer navigation, and the approved mascot's beaker as favicon/navigation mark; preserve mail delivery rules.
- Draft recovery uses a cookie ID plus temporary server storage and requires the original still-open page and same authorized account. Navigation/reload/close discards recovery; no persistent draft history or automatic submission.

## Review Focus

1. A permission change between discovery, page render, and an action must not reveal a private name/count or permit a stale operation: Tasks 4, 8, 10, 12, 14, 16, 18.
2. A bookmarked source/destination moves while a reviewer reads the page: comparisons become stale and a merge cannot silently use new nodes: Tasks 10–12.
3. Native SSH/HTTPS pushes race with a web merge, or the process dies after publishing but before DB commit: no lost bookmark update, duplicate merge, or false success: Tasks 2, 12, 14.
4. Real narrow screens, long localized names, failed avatar images, and keyboard popouts expose layout regressions: Tasks 3, 5–7, 13, 15–18.
5. Fork history contains unsafe paths, symlinks, `.hgsub`, binary/huge diffs, or private ancestors: browsing/review must stay safe and unsupported merge checkouts must fail without touching live storage: Tasks 1, 8, 10, 14.

## Baseline and ordering

The earlier hosting plan remains the prerequisite owner for its transport gate, ID-based authorization/lifecycle, revision/file/history APIs, application wiring, Git removal, native integration, and packaging. This plan adds the approved UI/fork/PR scope; it does not quietly mark those prerequisites complete.

Complete and validate the earlier plan's Tasks 6–15 before this plan's production tasks. The original Task 13 removes the broken Git fork/PR stubs first; this plan then installs new Mercurial implementations. Its absence test must continue to reject Git/mirror/import/release/archive/hook endpoints, but stop asserting that the newly implemented Mercurial fork/PR endpoints are absent at this plan's cutover. Do not reintroduce Git handlers to satisfy route tests. Coordinate any overlapping original-plan work before editing the same files.

One coordinated plan is appropriate: discovery, fork access, PR visibility, Issues, and Admin all consume the same authorization/layout. Implementation sequence: **0 → 1 → 2 → 3 → 4 → 5 → 6 → 7 → 8 → 9 → 10 → 11 → 12 → 13 → 14 → 15 → 16 → 17 → 18**. Tasks 1–2 are feasibility gates; failure returns to design rather than continuing with attractive but nonfunctional PR pages.

At execution start, use an isolated worktree if needed, reconcile the baseline, and record its commit. Do not copy the current unrelated uncommitted transport files into a feature commit. Plans can be read now; passing a dependency is an evidence-backed checkpoint, not an unchecked assumption.

## File ownership map

| Unit | Files and responsibility |
| --- | --- |
| Mercurial review operations | New `modules/hg/review_types.go`, `review_snapshot.go`, `review_compare.go`, `review_merge.go`, `review_publish.go` and corresponding `_test.go` / `_integration_test.go` files: bounded immutable snapshots, comparisons, merge preparation/publication |
| Write coordination | New `modules/hosting/repository_operations.go`; extend prerequisite `modules/hosting/service.go`, `modules/hgbridge/capability.go`, `control.go`, `hooks.go`: reuse authorization and serialize against all transport writers |
| Landing and identity | Task 16: templates/home.tmpl, routers/home.go, public/img/hgs-mascot.png, shared presentation and locale/default branding; approved reference docs/prototypes/landing-page/ |
| Shared presentation | New `modules/ui/{model.go,render.go}`, `templates/shared/*`, `public/css/app.css`, `public/js/app/{shell.js,forms.js,markdown.js}`, `public/img/ui-icons.svg` |
| Discovery/recents | New `models/{repo_catalog.go,repo_recent.go}`, `routers/user/recent.go`; existing `routers/home.go`, `templates/explore/repos.tmpl`, `templates/user/dashboard/*` |
| Forks | New `models/repo_fork.go`, `modules/collaboration/{service.go,fork.go}`, `routers/repo/fork.go`; rewrite `templates/repo/fork.tmpl` |
| PR storage/domain | New `models/{pull_request.go,pull_review.go,pull_comment.go,pull_operation.go}`; new `modules/collaboration/{pull.go,comparison.go,review.go,merge.go,lifecycle.go}` |
| PR routes/views | New `routers/repo/{pull_request.go,pull_review.go,pull_merge.go}`, `templates/repo/pull/*`; adapt `routers/user/home.go`, `templates/user/dashboard/pulls.tmpl` |
| Existing page migration | Existing workspace, repository, issue, organization, account, Admin, authentication, installer and error templates; their handlers change only to supply shared view data or preserve secure form behavior |
| UI completion | Task 17: repository action handlers/shared header, local Help route/template, shared footer and language control |
| Draft recovery | Task 18: models/editor_draft.go, modules/editordraft/service.go, routers/user/drafts.go, public/js/app/drafts.js and focused tests |
| Validation/records | New `tests/integration/{ui_test.go,collaboration_test.go,merge_recovery_test.go}`, `docs/mercurial/{review-operation-evidence.md,shared-ui-evidence.md}`; extend native workflow |

Paths refer to the baseline after prerequisite integration. If a prerequisite's public signature differs from its older plan, use the actual tested implementation and update all dependent signatures here before starting; do not build adapters to obsolete speculative APIs.

## Cross-task contracts

### Mercurial layer (Task 1)

Consume prerequisite `hg.NodeID`, `hg.RefSelection { Kind hg.RefKind; Name string }`, `hg.Reference`, `(*hg.Runner).References`, `.Resolve`, `.Manifest`, `.ReadFile`, `.Changeset` and `.History`. New types live in `modules/hg/review_types.go`:

```go
type ReviewSelection struct { Ref RefSelection; Head NodeID }
type ReviewEndpoint struct { Path string; Selection ReviewSelection; Node NodeID }
type ReviewSnapshot struct { Path string; Source, Target, Base NodeID }
type ReviewComparison struct {
    Base, Source, Target NodeID
    Changesets []Changeset
    Diff DiffResult
}
type PreparedMerge struct {
    Path string
    Source, Target, Result NodeID
    DestinationBranch, Bookmark string
    Mode string // fast_forward, two_parent, already_integrated
}
```

`Path` is an internal absolute storage path, never a request value. `Head` disambiguates a named branch. Resolve a missing/ambiguous/deleted/hidden ref to a typed error, not an arbitrary head. Internal artifact roots are separate from public repository paths.

### Application composition (Task 8, using Task 2 coordinator)

New package `modules/collaboration` imports models, hg, and hosting; models must not import it. `type Config struct { ArtifactRoot string; OperationTimeout time.Duration }`; default operation timeout is 30 minutes, concurrent heavy operations share the runner's bounded pool. `New(runner *hg.Runner, host *hosting.Service, cfg Config) (*Service,error)`. Dependencies are explicit; no new global singleton. The service manages authorized artifacts and operation records, while models use the established xorm engine.

Inject one `*collaboration.Service` into Macaron at application composition in `cmd/web.go`. New handlers receive it as a parameter, e.g. `func PullList(ctx *middleware.Context, service *collaboration.Service)`. Handlers deriving an actor use `ctx.User.Id`; no client-supplied actor ID or privilege boolean.

### Query and paging conventions

New catalog and PR pages use page size 30, maximum 100, a deterministic tie-breaker by immutable ID, and count after authorization/filtering. Normalize invalid page sizes to the default; reject malformed page/filter values with a user-readable error. Existing issue pagination semantics stay intact. Strings in URLs are encoded; use `AppSubUrl` at the presentation boundary.

---

### Task 0: Verify integration prerequisites and scope handoff

**Files:** Read the three design/plan references and `docs/mercurial/transport-evidence.md`; create `docs/mercurial/shared-ui-evidence.md` with baseline and dependency status. No runtime changes in this task.

**Interfaces:** Consumes evidence and tested APIs from the hosting plan. Produces an execution baseline and exact supported API inventory for later tasks.

- [ ] **1. Record the baseline.** Run `git status --short`, `git log -1 --oneline`, and inspect the transport evidence plus the original task completion records. Identify concurrent edits. Record missing prerequisites honestly.
- [ ] **2. Check the transport gate.** Require native Windows/Linux/macOS current-version passes and Linux legacy-version coverage as specified by the hosting plan. A reproduced known gap, skipped suite, or unavailable required runner is not a pass. If unresolved, stop production work; request resolution through its owning task/design.
- [ ] **3. Check the application baseline.** Run `go test ./models ./modules/hg ./modules/hgbridge ./modules/hosting ./routers/repo -count=1` and `go build ./...`; inspect the original end-to-end acceptance evidence. Require ID-based authorization/storage and Mercurial browsing to be present, not just runner tests.
- [ ] **4. Reconcile scope.** Record that this project supersedes the earlier fork/PR/UI exclusions only. The approved landing-page/logo phase follows the core UI in Task 16. Update old-plan handoff notes so executors do not remove the newly implemented routes later.
- [ ] **5. Commit the evidence note** as `docs: establish shared UI integration baseline`. Do not mark this task complete until Steps 2–3 pass.

### Task 1: Prove safe local snapshots, comparisons, and merge preparation

**Files:** Create the Task 1 hg files in the ownership map, their tests, and `docs/mercurial/review-operation-evidence.md`. Reuse `modules/hg/config.go` limits and existing native fixture conventions.

**Interfaces:** Produce Runner methods `ResolveReview(ctx context.Context, path string, selection ReviewSelection) (ReviewEndpoint,error)`, `SnapshotReview(ctx context.Context, source,target ReviewEndpoint, outputPath string) (ReviewSnapshot,error)`, `CompareReview(ctx context.Context, snapshot ReviewSnapshot) (ReviewComparison,error)`, `PrepareReviewMerge(ctx context.Context, snapshot ReviewSnapshot, outputPath,author,message string) (PreparedMerge,error)`. Return sentinel errors `ErrAmbiguousHead`, `ErrNoChanges`, `ErrUnrelatedHistory`, `ErrMergeConflict`, `ErrUnsafeCheckout`, `ErrComparisonTooLarge`; existing not-found/empty errors remain distinguishable.

- [ ] **1. Write meaningful failing tests.** Add `TestReviewSnapshotDoesNotChangeTarget`, `TestReviewSelectionRejectsAmbiguousHead`, `TestReviewComparisonPinsNodes`, `TestReviewMergeCases`, `TestReviewCheckoutNeverExecutesContent`. Native fixtures assert target store snapshots remain equal before publication; only selected visible history is copied; source changes after snapshot cannot alter the result. For the merge table, assert `(mode, result parents, result branch)` for same-branch descendants, cross-branch descendants, divergence, and already-integrated source. Reject unrelated/empty endpoints. Assert no outbound request or sentinel executable invocation for malicious configuration, merge tools, symlinks/metadata paths, and `.hgsub`.
- [ ] **2. Run RED:** `go test -tags integration ./modules/hg -run 'TestReview' -count=1`. Record actual missing behavior, not setup errors.
- [ ] **3. Implement the methods.** Use only internal ID-resolved paths, bounded runner calls and controlled configuration. Snapshots/preparation have private application-owned paths and never serve as public targets. Use explicit built-in noninteractive tools; do not silently select ours/theirs. Limit diff bytes to the prerequisite 2 MiB limit, indicate binary/truncated content, reject unsupported tree materialization before checkout. Keep full changeset identity; do not use debug-parent commands. Record and test exact commands/version differences in the evidence file.
- [ ] **4. Verify GREEN** with the same command on supported native versions/platforms. Test Windows case-colliding/unrepresentable paths with an honest unsupported result, while manifest browsing remains available. A safe failure is distinct from merge success.
- [ ] **5. Commit** as `feat: prepare bounded Mercurial review snapshots`. Stop and revise the design if its required operations cannot be demonstrated within the selected architecture.

### Task 2: Prove publication guards across web and native writers

**Files:** Create `modules/hg/review_publish.go`, `modules/hosting/repository_operations.go` and tests; extend `modules/hgbridge/{capability,control,hooks}.go` and their tests only as needed; extend review-operation evidence.

**Interfaces:** In hg: `type PublishReviewRequest struct { Prepared PreparedMerge; TargetPath string; Expected ReviewEndpoint; OperationID string }`; `(*Runner).PublishReview(ctx context.Context, request PublishReviewRequest, hookEnv map[string]string) error`. In hosting: `type MutationExpectation struct { RepoID int64; Selection hg.ReviewSelection; Node hg.NodeID; OperationID string }`; `(*Service).WithRepositoryMutation(ctx context.Context, actorID int64, expected MutationExpectation, apply func(context.Context,map[string]string) error) error`. It supplies a bounded operation-specific hook environment; it is not a general bypass capability. Produce `ErrStaleRepository` and `ErrMutationBusy` in hosting.

- [ ] **1. Add `TestReviewPublishRacesNativePush`, `TestReviewPublishRevocation`, `TestReviewPublishUpdatesOnlySelectedBookmark`, `TestReviewPublishLostResponse`.** Race real SSH and HTTPS pushes against publication; assert either a valid serialized result or explicit stale refusal. Assert no successful stale overwrite, no extra named-branch head caused by racing a changed destination, unrelated bookmarks unchanged, denied state identical, and prepared result detectable after a dropped response.
- [ ] **2. Run RED:** `go test -tags integration ./modules/hg ./modules/hosting ./tests/integration -run 'TestReviewPublish' -count=1`.
- [ ] **3. Implement guarded publication.** Use Mercurial's transaction locking and existing hook controller to check expected endpoints/permission at the guarded write boundary. Include native writers and lifecycle drains in coordination; a Go mutex around web handlers alone is insufficient. Never take an external Mercurial lock and then wait for a child needing it. Prove history plus intended bookmark publication and recovery as a unit; do not assume successive CLI calls are atomic. No client controls hook env, target path, or operation identity. Add a bounded persisted operation identity hook to support Task 12 reconciliation without network fetches.
- [ ] **4. Verify GREEN** and record command sequences, lock/hook points, crash points and unsupported cases. Run current-version matrix on all three platforms and retained legacy-server checks. If any required contract needs a Python extension, protocol parser, or weaker authority, stop and propose a design amendment before continuing.
- [ ] **5. Commit** as `feat: guard Mercurial review publication`.

### Task 3: Shared application layout, tokens, and utility controls

**Files:** Create `modules/ui/{model,render,render_test}.go`; `templates/shared/{head,start,end,rail,workspace_nav,repo_nav,admin_nav,alerts,form_field,pagination}.tmpl`; `public/css/app.css`; `public/js/app/{shell,forms,markdown}.js`; `public/img/ui-icons.svg`; update `modules/base/template.go` for required shared helpers and `conf/locale/locale_en-US.ini`.

**Interfaces:** `ui.ContextKind` values `workspace`, `repository`, `admin`, `anonymous`; `type NavItem struct { Key,Label,URL,Icon string; Active bool; Count *int64 }`; `type Shell struct { Kind ContextKind; Title,SubURL,AvatarURL,Initials string; SignedIn,CanAdmin bool; Navigation []NavItem }`; `ui.BuildShell(ctx *middleware.Context, kind ContextKind, active string) Shell` sets view data key `Shell`. Template entry/exit are `shared/start` and `shared/end`; each page supplies its content between them. `shell.js` initializes once using `data-popout`, `data-popout-target`, `data-drawer` and ARIA relationships, without global legacy selectors.

- [ ] **1. Add `TestSharedShellPermissionsAndSubURL` and `TestSharedShellEscapesLabels`.** Render signed-in, anonymous, admin, repository-reader/writer contexts at `/` and `/code`; assert correct links/order, no unauthorized Admin/settings actions, escaped names, and a real narrow-screen navigation trigger. Use existing template function registrations rather than a toy string-only renderer.
- [ ] **2. Run RED:** `go test ./modules/ui -run 'TestSharedShell' -count=1`.
- [ ] **3. Implement common components** with exact `docs/DESIGN.md` tokens and semantic controls. Utility order is Search, Explore, Recents, Create; account image 28px is centered in a 40px square. Popouts close on Escape/outside click, restore keyboard focus, and only one opens; modal drawers trap focus. Use scoped event delegation and existing sanitized Markdown endpoint. Add real reduced-motion and focus behavior. Pure visual CSS changes need visual inspection, not tests asserting CSS text.
- [ ] **4. Verify GREEN** and inspect rendered fixtures at 1440px, 1024px, 800px and 390px, zoomed text, long labels, real/broken/fallback avatars. Check center offsets are zero, keyboard focus stays visible, and no horizontal page overflow. Record screenshots and actual dimensions in UI evidence.
- [ ] **5. Commit** as `ui: add shared application layout and components`.

### Task 4: Authorized discovery, search, and account Recents

**Files:** Create `models/{repo_catalog,repo_recent,repo_catalog_test,repo_recent_test}.go`, `routers/user/{recent,recent_test}.go`; modify `models/models.go`, `routers/home.go`, `cmd/web.go`; create `templates/shared/{search,recent,create_menu}.tmpl`; update `templates/explore/repos.tmpl` and `public/js/app/shell.js`.

**Interfaces:** `models.CatalogQuery { Text,Visibility,Scope string; OwnerID int64; Page,PageSize int }` (`Visibility=all/public/private`, `Scope=accessible/mine`); `CatalogPage { Repositories []*Repository; Total,Page,PageSize int }`; `ListAccessibleRepositories(ctx context.Context, actorID int64,q CatalogQuery) (CatalogPage,error)`; `RecordRepositoryVisit(ctx context.Context,actorID,repoID int64,at time.Time) error`; `RecentRepositories(ctx context.Context,actorID int64) ([]*Repository,error)`; `ClearRepositoryVisits(ctx context.Context,actorID int64) error`. New `GET /user/recent`, `POST /user/recent/clear`, `GET /explore/search?q=...` return only authorized partials/data; `/explore` remains the full page.

- [ ] **1. Add `TestCatalogFiltersBeforeCountAndPage`, `TestRecentsLimitsAndRenames`, `TestRecentsRevocationAndAccountIsolation`.** Fixtures with 25 visits assert 20 retained/10 displayed, no duplicate IDs, current names after rename, and revoked/deleted entries absent from both rows/counts. Assert anonymous catalog/search queries return only public rows/counts regardless of visibility filters and return no repository data when RequireSignInView is enabled; signed-in discovery keeps its authorized scope. Simulate responses arriving out of order and verify the UI only displays the newest query. Private-fork ancestry fixtures belong to Task 8, when fork relations exist.
- [ ] **2. Run RED:** `go test ./models ./routers/user -run 'TestCatalog|TestRecents' -count=1`.
- [ ] **3. Implement catalog filtering with the shared authorization policy** (Task 8 extends it for actual private forks), pagination and input limits (query max 200 Unicode characters). Record visits only after successful foreground repository-page authorization/render, not assets/background polling. Search uses 200ms debounce and cancels/ignores stale requests. Create popout links to the authorized existing form. Add clear-Recents CSRF check and 303 redirect/updated partial.
- [ ] **4. Verify GREEN** plus browser Search/Explore/Recents/Create with multiple accounts and sub-URL deployment. Refresh the authorization on every read; do not store repository names in a client-wide persistent cache.
- [ ] **5. Commit** as `feat: add authorized discovery and recent repositories`.

### Task 5: Migrate workspace and repository browsing/settings

**Files:** Modify `templates/user/dashboard/{dashboard,nav,feeds}.tmpl`, `templates/repo/{home,bare,header,sidebar,sidebar_mini,view_file,view_list,commits,commits_table,diff,branch,list,create}.tmpl`, retained `templates/repo/settings/{options,nav,collaboration}.tmpl`, `routers/user/home.go`, `routers/repo/{view,commit,branch,repo,setting}.go`; create `tests/integration/ui_test.go` and `templates/shared/revision_selector.tmpl`.

**Interfaces:** Consume `ui.BuildShell`, catalog APIs, and prerequisite typed Mercurial view models; preserve real clone URLs, immutable source URLs and existing form names. Expose `data-revision-kind` and encoded node/path attributes rather than conflating branches/bookmarks/tags.

- [ ] **1. Add `TestSharedUIRepositoryRoutes` and `TestSharedUIWorkspace`.** Request real empty/populated/private/merge/file pages; assert selected sidebar item, escaped source/README, valid app-sub-URL links, no Git wording, working typed reference selection and unavailable action omission. Assert no fabricated activity entry when no supported event exists.
- [ ] **2. Run RED:** `go test -tags integration ./tests/integration -run 'TestSharedUIRepository|TestSharedUIWorkspace' -count=1`.
- [ ] **3. Migrate markup to shared components** and adapt page-specific scripts without duplicate legacy initialization. Preserve source highlighting, copy clone command, diff navigation and form validation. Keep public landing markup and its current logo until Task 16. Fork/PR navigation becomes active only with Tasks 8/13, with no dead route exposed at a production checkpoint.
- [ ] **4. Verify GREEN** and browser-inspect long repository names, multiple heads, binary/large files, empty README, readers versus owners, and narrow tables. Check bounded command counts from the hosting baseline remain intact.
- [ ] **5. Commit** as `ui: migrate workspace and repository pages`.

### Task 6: Migrate the complete Issues experience

**Files:** Modify `templates/user/issues.tmpl`; `templates/repo/issue/{list,create,view,milestone,milestone_new,milestone_edit}.tmpl`; create `templates/repo/issue/labels.tmpl` and `templates/shared/{markdown_editor,discussion,issue_metadata}.tmpl`; adapt `routers/repo/issue.go`, `routers/user/home.go`, retained issue-label/preview behavior into `public/js/app/issues.js`; extend `routers/repo/issue_test.go` and integration UI tests.

**Interfaces:** Existing handlers/forms/routes and issue IDs remain authoritative. Shared editor attributes: `data-markdown-editor`, `data-preview-url`, associated `textarea`/preview panel IDs; discussion anchors retain `issue-comment-<id>`. Labels/milestones use the actual retained endpoints, not `/issues2` or sample template routes. Issue mutations retain current authentication/CSRF/authorization.

- [ ] **1. Add `TestSharedUIIssuesWorkflow`, `TestSharedUIIssuePrivateAttachments`, `TestSharedUIIssueGlobalFilters`.** Via real forms, create/comment/edit where permitted, assign, label, set milestone, close/reopen; assert canonical URLs/anchors, status history, preserved submitted input on error, account-filtered global counts, and private attachment denial. Include issue creation in an empty Mercurial repository without a history command.
- [ ] **2. Run RED:** `go test -tags integration ./tests/integration -run 'TestSharedUIIssue' -count=1`; keep prerequisite issue regression tests green.
- [ ] **3. Replace all issue-page layout** with the shared shell, Open/Closed tabs, existing filters and pagination, New issue action, Markdown editor, readable timeline and responsive metadata. Keep attachments/notifications and labels/milestones intact. Do not add priorities, boards, workflow states, or PR/issue identity merging. Remove legacy Bootstrap dependencies for these migrated pages after their scripts are replaced.
- [ ] **4. Verify GREEN** plus keyboard Write/Preview, assignment controls, label colors, close/reopen, validation failures, translated labels, long comments, empty/filter-no-match states, desktop and 390px views.
- [ ] **5. Commit** as `ui: migrate issues and discussion to shared components`.

### Task 7: Migrate Admin, organization, account, and standalone pages

**Files:** Modify every retained template in `templates/admin/`, `templates/org/`, `templates/user/settings/`, `templates/user/auth/`, plus `templates/user/profile.tmpl`, `templates/install.tmpl`, `templates/status/{401,403,404,500}.tmpl`. Adapt handlers in `routers/admin/{admin,users,orgs,repos,auths,notice}.go`, `routers/org/{org,members,teams,setting}.go`, `routers/user/{setting,auth,social}.go`, `routers/install.go` only as needed. Extend `cmd/web.go` for secure mutation methods and integration UI tests.

**Interfaces:** Consume shared shell kinds `admin`, `workspace`, `anonymous`; preserve existing form field/action contracts except converting legacy GET mutations to CSRF-protected POST. Use the same shared confirmation/dialog behavior for destructive actions. Admin visibility must not imply new repository read authority.

- [ ] **1. Add `TestSharedUIAdminAuthorization`, `TestSharedUIAdminMutationMethods`, `TestSharedUIAccountsAndOrganizations`.** Assert nonadmins cannot access Admin via direct URLs, GET requests cannot delete notices/run maintenance, CSRF-denied POSTs have no effect, config stays read-only, and actual supported actions work. Exercise membership/settings/SSH keys, sign-in/reset/activation and validation redirects with a fake mail sink. No test invokes a destructive action against real server data.
- [ ] **2. Run RED:** `go test -tags integration ./tests/integration -run 'TestSharedUIAdmin|TestSharedUIAccounts' -count=1`.
- [ ] **3. Migrate the pages** to shared tables/forms/tokens and matching contextual navigation. Preserve existing policy and invite/authentication behavior. Remove Git-only maintenance displays; show only supported real statistics. Standalone installer/auth/errors use reduced layout; do not replace the landing mascot here.
- [ ] **4. Verify GREEN** and inspect Admin overview/users/configuration, team membership, profile/settings, login/error pages with keyboard and narrow-screen drawers. Validate two locales, image failure, and long usernames.
- [ ] **5. Commit** as `ui: unify administration and account pages`.

### Task 8: Mercurial fork provisioning and private ancestry access

**Files:** Create `models/{repo_fork,repo_fork_test}.go`, `modules/collaboration/{service,fork,fork_test}.go`, `routers/repo/{fork,fork_test}.go`; extend prerequisite `models/{repo_access,repo_lifecycle,models}.go`, `modules/hosting/service.go`, catalog tests; rewrite `templates/repo/fork.tmpl`, add Fork action to repository header, register routes in `cmd/web.go`.

**Interfaces:** `models.ForkRelation { RepositoryID,SourceID,FamilyID int64 }`; `GetForkRelation(repoID int64) (ForkRelation,error)`; `ForkAncestors(repoID int64) ([]int64,error)`; `collaboration.ForkOptions { SourceID,OwnerID int64; Name,Description string }`; `(*Service).Fork(ctx context.Context,actorID int64,opts ForkOptions) (*models.Repository,error)`; `ForkStatus(ctx context.Context,actorID,repoID int64) (models.RepositoryState,error)`. Produce internal `.Fork` lifecycle without relying on removed Git functions; use existing provisioning/ready/deleting records and ID storage.

- [ ] **1. Add `TestForkSelectedSourceAndAccess`, `TestForkProvisioningRecovery`, `TestForkNeverCopiesSecrets`, `TestForkLifecycleRestrictions`, `TestCatalogPrivateAncestry`.** Fork a fork and assert its own selected visible history is copied, empty sources remain empty, no issues/PRs/hooks/secrets copied, no shared mutable store, and personal/organization owner permissions checked. Assert private descendants require all private-ancestor read rights; catalog/Recents rows and counts plus native transport deny after revocation. Reject parent visibility/delete with dependents and unsafe transfer. Fault after row/init/copy/move must never publish half a fork or delete unrelated storage.
- [ ] **2. Run RED:** `go test ./models ./modules/collaboration ./routers/repo -run 'TestFork' -count=1`, then native tagged fork tests.
- [ ] **3. Implement fork service and form** using Task 1 snapshot-safe local operations (fork provisioning copies the selected source's full visible history and supported refs, without a target comparison). Add `(*hg.Runner).CloneVisible(ctx context.Context,sourcePath,outputPath string) error` in `review_snapshot.go` with tests in this task. Reset generated config, preserve no checkout, and apply inherited visibility. Persist operation ownership and reconcile provisioning at startup; lifecycle operations drain coordinated work.
- [ ] **4. Verify GREEN** with native clones/pushes to ready forks, permission-filtered status pages, sub-URL and browser form errors. Run `go test ./models -run 'TestCatalog|TestRecents' -count=1` for the extended discovery regression cases and the native transport suite.
- [ ] **5. Commit** as `feat: provision local Mercurial forks`.

### Task 9: PR records, state transitions, and list authorization

**Files:** Create `models/{pull_request,pull_request_test}.go`, `modules/collaboration/{pull,pull_test}.go`; extend `models/models.go` and `modules/collaboration/service.go`.

**Interfaces:** `models.PullState` values `open`, `merged`, `declined`; `PullRequest` contains ID, TargetRepoID, SourceRepoID, Number, AuthorID, Title, Body, SourceSelection, TargetSelection, CurrentComparisonID, State, timestamps (selections serialized from `hg.ReviewSelection`, stored as data); `collaboration.NewPull { SourceRepoID,TargetRepoID int64; Source,Target hg.ReviewSelection; Title,Body string; ReviewerIDs []int64 }`; `PullQuery { RepoID int64; State,Filter string; Page,PageSize int }`; `PullPage { Requests []models.PullRequest; Total,Page,PageSize int }`. Service methods `CreatePull(ctx context.Context,actorID int64,input NewPull) (models.PullRequest,error)`, `GetPull(ctx context.Context,actorID,targetRepoID,number int64) (models.PullRequest,error)`, `ListPulls(ctx context.Context,actorID int64,q PullQuery) (PullPage,error)`, `EditPull(ctx context.Context,actorID,pullID int64,title,body string) error`, `SetPullState(ctx context.Context,actorID,pullID int64,state models.PullState) error` (publicly permits only decline/reopen; merge state is internal to Task 12).

- [ ] **1. Add `TestPullCreatePermissionsAndFamily`, `TestPullNumberAllocation`, `TestPullVisibilityAndTransitions`.** Assert source write plus both reads, same family or same repo, matching privacy, distinct valid endpoints, title/body limits (255 characters / 64 KiB), unique target-local sequence under concurrent creates, and no collision with issue routes. Anonymous/unrelated users see no titles/counts. Authors decline/reopen their own eligible PR; target writers may decline/reopen; merged cannot reopen or be set through the generic state API. Reject destination edits.
- [ ] **2. Run RED:** `go test ./models ./modules/collaboration -run 'TestPullCreate|TestPullNumber|TestPullVisibility' -count=1`.
- [ ] **3. Implement storage and services** with explicit source/target authorization and transactional sequence allocation. Same-repository requests are supported. Initial creation stores an honest comparison-pending state; it cannot be merged before Task 10 produces a valid snapshot. Use target repository IDs for routes and ownership, not mutable names.
- [ ] **4. Verify GREEN** on temporary SQLite and supported MySQL/PostgreSQL fixture modes; assert predictable conflict/error behavior rather than driver-specific silent duplicate numbers.
- [ ] **5. Commit** as `feat: add authorized pull request lifecycle`.

### Task 10: Immutable comparisons and artifact visibility

**Files:** Create `modules/collaboration/{comparison,comparison_test}.go`, `models/{pull_comparison,pull_comparison_test}.go`; extend snapshot/diff tests and `models/models.go`.

**Interfaces:** `models.PullComparison { ID,PullID,Revision int64; SourceNode,TargetNode,BaseNode hg.NodeID; ArtifactID,State string; Created time.Time }` (states `pending`, `ready`, `failed`, `unavailable`); `(*Service).RefreshComparison(ctx context.Context,actorID,pullID int64) (models.PullComparison,error)`; `Comparison(ctx context.Context,actorID,pullID,comparisonID int64) (models.PullComparison,hg.ReviewComparison,error)`; `ReadComparisonFile(ctx context.Context,actorID,pullID,comparisonID int64,side,path string,out io.Writer) error`. Artifact IDs are opaque application allocations, not URL paths supplied by clients.

- [ ] **1. Add `TestPullComparisonIsolatedFromTarget`, `TestPullComparisonMovesAndAmbiguity`, `TestPullArtifactsRecheckVisibility`.** Assert target clone content unchanged by PR open/refresh; source/destination movement produces a new comparison version; ambiguous heads/deleted refs disable merge; old comparison nodes stay immutable; hidden/secret/private-ancestor data never appears. Test traversal, oversized/binary diffs, authorization revocation, interrupted writes and orphan artifacts.
- [ ] **2. Run RED:** `go test -tags integration ./modules/collaboration -run 'TestPullComparison|TestPullArtifacts' -count=1`.
- [ ] **3. Implement authorized bounded snapshots** through Task 1. Common-ancestor ambiguity is explicit failure, not arbitrary selection. Store artifacts under configured private root, publish only complete comparisons, discard owned partial allocations on failure, and reconcile on startup. Refresh selected mutable refs on demand; mark stale before merge even when async refresh lags. Never expose a direct static-file mount for artifacts.
- [ ] **4. Verify GREEN** with real native source updates, truncated lines and merge histories; prove an unauthorized user cannot retrieve an old artifact by guessed ID.
- [ ] **5. Commit** as `feat: retain immutable pull request comparisons`.

### Task 11: Review authority, comments, and outdated anchors

**Files:** Create `models/{pull_review,pull_comment,pull_event,pull_review_test,pull_comment_test}.go`, `modules/collaboration/{review,review_test}.go`; extend model registration.

**Interfaces:** `models.ReviewState` values `approved`, `changes_requested`, `inactive`; `InlineAnchor { ComparisonID int64; Path,Side string; Line int }`; `(*Service).AssignReviewers(ctx context.Context,actorID,pullID int64,reviewerIDs []int64) error`; `ReviewPull(ctx context.Context,actorID,pullID,comparisonID int64,state models.ReviewState) error`; `DismissReview(ctx context.Context,actorID,pullID,reviewerID int64,reason string) error`; `CommentPull(ctx context.Context,actorID,pullID int64,body string,anchor *InlineAnchor) (commentID int64,err error)`; `ResolveThread(ctx context.Context,actorID,pullID,threadID int64,resolved bool) error`. Thread author and target writers may resolve/reopen a thread; resolution does not itself clear a formal Request changes.

- [ ] **1. Add `TestPullReviewAuthority`, `TestPullReviewBlocksCannotBeBypassed`, `TestPullReviewRevisionAndAccessLoss`, `TestPullCommentAnchors`.** Assert arbitrary readers can comment but cannot submit formal blocking reviews; only assigned nonauthor target writers with source read may formally review; author/target writers assign eligible reviewers. Removing an assignment cannot bypass a live block; admin dismissal records reason. Node updates invalidate approvals but preserve change requests; permission loss inactivates formal reviews. Inline anchors remain on immutable old comparisons and are marked outdated after refresh; reject nonexistent paths/lines/sides, unsafe markup, empty/over-64-KiB comments.
- [ ] **2. Run RED:** `go test ./models ./modules/collaboration -run 'TestPullReview|TestPullComment' -count=1`.
- [ ] **3. Implement service authorization and audit events.** Check fresh effective access when reading review eligibility and merging, not only when review rows were created. Apply no mandatory approval count. Reuse existing sanitization for rendering; persist user Markdown as data. Notify through in-app lists/events only; existing issue mail is unchanged.
- [ ] **4. Verify GREEN** including an upstream reader attempting self-assignment, author self-approval, disabled reviewer account, assignment races and stale comparison review submission.
- [ ] **5. Commit** as `feat: add pull request discussion and review`.

### Task 12: Durable merge orchestration and crash reconciliation

**Files:** Create `models/{pull_operation,pull_operation_test}.go`, `modules/collaboration/{merge,merge_test}.go`; extend service startup/shutdown and `tests/integration/merge_recovery_test.go`.

**Interfaces:** `models.MergeOperation` holds ID (random opaque string), PullID, ActorID, ComparisonID, expected source/target nodes, prepared result node, bookmark, artifact ID, timestamps, state (`unstarted`, `prepared`, `published`, `finalized`, `failed`) and bounded error code; unique active operation per PR. `collaboration.MergeRequest { PullID,ComparisonID int64; Source,Target hg.NodeID; IdempotencyKey string }`; `MergeResult { OperationID string; State string; Node hg.NodeID }`; Service methods `MergePull(ctx context.Context,actorID int64,input MergeRequest) (MergeResult,error)`, `MergeStatus(ctx context.Context,actorID,pullID int64,operationID string) (MergeResult,error)`, `ReconcileOperations(ctx context.Context) error`.

- [ ] **1. Add `TestPullMergeRejectsStaleReview`, `TestPullMergeKeepsHistoryAndBranch`, `TestPullMergeIdempotencyAndRecovery`, `TestPullMergeConcurrentWriters`.** Gate on current source read/target write, active account, readiness, exact nodes/review state. Test same-branch fast-forward, cross-branch descendant two-parent merge, divergence, already integrated, conflict, native push race, and unrelated bookmark invariance. Inject crash at every durable-state boundary; assert one resulting merge node, no false Merged state, and deterministic recovered operation for repeated same-key requests. Different payload with same key is rejected.
- [ ] **2. Run RED:** `go test -tags integration ./modules/collaboration ./tests/integration -run 'TestPullMerge' -count=1`.
- [ ] **3. Implement orchestration using Tasks 1–2.** Persist the intent before publishing; keep the prepared result identity durable. Revalidate permissions and nodes at the guarded write boundary and mark stale instead of replaying new endpoints. Publish only expected history/bookmark; finalize PR/event after confirmed publication. If outcome is ambiguous, query stored operation/result before retrying; do not regenerate a commit timestamp/node. On restart, reconcile prepared/published records before accepting conflicting operations. Never resume an uncommitted write under a stale grant. Bound and cancel workers; retain evidence needed to reconcile a published mutation even if the actor later loses access.
- [ ] **4. Verify GREEN** with real SSH/HTTPS racing writers, revocation between checks, restart after publication, full disk/output limits, and conflicting bookmark movement. The web UI's status endpoint must report recovering rather than claim failure/success prematurely.
- [ ] **5. Commit** as `feat: merge pull requests with durable recovery`.

### Task 13: Real PR routes, forms, lists, review detail, and merge UI

**Files:** Create `routers/repo/{pull_request,pull_review,pull_merge,pull_request_test}.go`, `templates/repo/pull/{list,new,view,conversation,changesets,files,reviewers}.tmpl`, `public/js/app/pulls.js`; update `routers/user/home.go`, `templates/user/dashboard/pulls.tmpl`, shared navigation, locale resources and `cmd/web.go`.

**Interfaces:** All handlers use `(ctx *middleware.Context, service *collaboration.Service)` plus typed bound forms for POSTs. Routes: `GET /pulls`; `GET /:owner/:repo/pulls`; `GET|POST /:owner/:repo/pulls/new`; `GET /:owner/:repo/pulls/:number`; POST suffixes `edit`, `state`, `refresh`, `reviewers`, `review`, `review/dismiss`, `comments`, `threads/:thread/resolve`, `merge`; GET `operations/:operation` and permission-checked `comparisons/:comparison/files/*`. Dispatch by literal suffix before wildcard routes. All PR numbers resolve through destination repository ID. Prefix every route through the existing sub-URL mechanism.

- [ ] **1. Add `TestPullHTTPWorkflow`, `TestPullHTTPDenialAndCSRF`, `TestPullUIStaleConflictAndBinary`.** Drive authenticated real forms from new PR through review/update/merge; assert typed source/target selection, source fork identity, correct global/repo counts and links, canonical comments, state tabs, and a permission-checked artifact download. Reject forged actor/target IDs, missing CSRF, old comparison merge submissions, and unsafe filenames. Confirm UI distinguishes pending, stale, conflicted, unavailable, failed, recovering and merged.
- [ ] **2. Run RED:** `go test -tags integration ./routers/repo ./tests/integration -run 'TestPullHTTP|TestPullUI' -count=1`.
- [ ] **3. Implement view models/forms** over the service APIs. Repository Pull requests precedes Issues; workspace Pull requests follows Organizations. Detail has Conversation/Changesets/Files changed, safe diff links, outdated comment markers and reviewer actions appropriate to authority. Create a pre-merge confirmation showing exact target and strategy; conflicts link to repository-specific native Mercurial instructions. Disable duplicate submissions and poll operation state with bounded backoff. No arbitrary GET mutation or sample PR content remains.
- [ ] **4. Verify GREEN** plus browser walkthrough on desktop/mobile, keyboards, source updates mid-review, empty lists, permission loss and sub-URL deployment. Do not run actual external email or maintenance effects during visual checks.
- [ ] **5. Commit** as `ui: connect fork pull request creation review and merge`.

### Task 14: Lifecycle and private-data integration

**Files:** Create `modules/collaboration/{lifecycle,lifecycle_test}.go`; extend `models/repo_lifecycle.go`, `modules/hosting/service.go`, catalog/recent logic, `tests/integration/collaboration_test.go` and merge recovery tests.

**Interfaces:** `(*Service).BeforeRepositoryChange(ctx context.Context,actorID,repoID int64,change string) error` validates `rename`, `transfer`, `visibility`, `delete`; `AfterRepositoryDeleted(ctx context.Context,repoID int64) error` performs idempotent PR/artifact cleanup; integrate through existing lifecycle callbacks, not parallel competing delete APIs. Mutation and cleanup state is durable; source and target IDs remain stable across rename/transfer.

- [ ] **1. Add `TestCollaborationDeleteAndTransfer`, `TestCollaborationRevocationEverySurface`, `TestCollaborationMergedHistoryRetention`.** Delete a leaf source: open PRs decline, unmerged payload/comments are inaccessible and purged, only permitted audit tombstone remains. Merged history stays under target read policy. Target deletion removes PR artifacts; parent deletion/privacy change with dependent forks is blocked. Transfer cannot widen private ancestry access. Recents/catalog/PR counts/inline file routes lose revoked data immediately. Drain concurrent merges before lifecycle writes; crash/retry must not recreate deleted data.
- [ ] **2. Run RED:** `go test -tags integration ./modules/collaboration ./tests/integration -run 'TestCollaboration' -count=1`.
- [ ] **3. Implement lifecycle coordination** with hosting drain and the guarded operation ledger. Ongoing jobs cannot publish after deletion/access revocation. Orphan cleanup uses recorded ownership and containment-checked native filesystem APIs only. Denied responses do not echo private names or internal paths.
- [ ] **4. Verify GREEN** against personal/organization/private fork chains and both transports; include cycles/corrupt ancestry failing closed and database outage denial.
- [ ] **5. Commit** as `fix: preserve fork and review privacy through lifecycle changes`.

### Task 15: Complete cutover and acceptance evidence

**Files:** Extend `tests/integration/{ui_test,collaboration_test,absence_test}.go`, `.github/workflows/mercurial.yml`, `docs/mercurial/{shared-ui-evidence,review-operation-evidence,removed-features}.md`; remove only obsolete unused templates/assets after route/selector audit; update locale resources and packaging paths.

**Interfaces:** No new runtime API. Every supported route uses the common presentation system; fork/PR routes use Mercurial services. Retain only specialized legacy libraries still used by supported features and document each retained dependency.

- [ ] **1. Add `TestSharedUICutoverAndRouteMatrix`, `TestCollaborationAcceptanceNoGit`, `TestSharedUISubURLAndLocales`.** Audit every retained page family including global/repository Issues, label/milestone, all Admin sections, auth/install/errors. Assert old demo routes are gone, new fork/PR endpoints present, other deferred/Git endpoints still absent, assets packaged and linked once, and startup/fork/push/review/merge works on a PATH without Git. Update the original deferred-route absence test only for explicitly implemented features.
- [ ] **2. Run the focused tests**, then fix only failures in their owning components. Search obsolete template references and global JS selectors before removal; an unused filename alone is not proof an asset is safe to delete.
- [ ] **3. Run final validation:** `go test ./... -count=1`, `go test -tags integration ./... -count=1`, `go test -race ./models ./modules/hg ./modules/hgbridge ./modules/hosting ./modules/collaboration`, `go vet ./...`, `go build ./...`. Record pre-existing external LDAP/network failures separately; do not suppress them and call the suite green. Use native platform jobs and the retained version matrix. Actual successful results are required; a written CI file is not evidence it ran.
- [ ] **4. Finish browser QA** at the listed widths, keyboard-only, text zoom, two locales, and reader/writer/admin/guest roles. Record screenshots plus commands/results/version matrix in UI evidence. Verify the 28px avatar's centers coincide with its 40px control, no desktop top-nav duplication, and mobile drawers/popouts remain reachable.
- [ ] **5. Commit** as `test: validate shared UI and Mercurial collaboration`. Continue to Task 16 before final whole-branch review or integration handoff.

### Task 16: Approved Hgs landing page, mascot, and public discovery policy

**Files:** Modify templates/home.tmpl, routers/home.go, templates/explore/repos.tmpl, shared identity/footer partials from Task 3, public/css/app.css, conf/app.ini default display branding, modules/setting/setting.go display-name fallback if present, and conf/locale/locale_*.ini product labels. Add public/img/hgs-mascot.png from docs/prototypes/landing-page/gopher-lab.png and public/img/hgs-mascot.LICENSE.md with source, license and adaptation credit. Also modify templates/mail/auth/*.tmpl, templates/mail/notify/*.tmpl and their renderer tests, shared head/rail/footer icon references, and packaged favicon files. Add public/img/hgs-beaker.svg as the shared master plus favicon PNG/ICO derivatives, using the beaker from the approved mascot. Extend routers/home_test.go and tests/integration/ui_test.go. Retire public/img/gogs-lg.png only after all supported references are replaced. Use the approved prototype as a visual reference, not runtime code.

**Interfaces:** Reuse setting.Service.RequireSignInView and Task 4's ListAccessibleRepositories(ctx context.Context, actorID int64, q models.CatalogQuery) (models.CatalogPage,error). Produce Home view-data boolean ShowPublicExplore = !setting.Service.RequireSignInView. Landing catalog links use AppSubUrl + "/explore?visibility=public"; any landing repository data uses actorID 0 and Visibility "public". Task 4's anonymous catalog/search policy remains authoritative even if query parameters are changed. Preserve signed-in Home dashboard routing, existing registration/provider view data, and configured instance names; change default product identity, not operator-selected names.

- [ ] **1. Add focused failing tests.** TestLandingPublicExplorePolicy uses public and private repository fixtures: with REQUIRE_SIGNIN_VIEW false, landing links target the public filter and no private names/counts appear; with true, no landing Explore links/previews/counts are rendered and direct anonymous Explore/search requests use the existing sign-in response with no repository payload. Include attempts to request visibility=all/private and page through results. TestLandingIdentityAndAuth asserts the exact tagline, Hgs default identity, retained custom instance name, local mascot/attribution, AppSubUrl links, conditional registration, configured social providers, and signed-in dashboard routing. TestLandingAssetPackaging checks the PNG and attribution are included in packaged assets. Keep signed-in authorized-private catalog regression coverage from Task 4. Add TestHgsIdentityAssets and TestExistingMailBranding: assert local beaker references/packaging, Hgs or configured instance identity, intact action URLs and escaping for all existing mail families, and readable link text without images; use a fake mail sink and assert delivery triggers/recipients are unchanged.
- [ ] **2. Run RED:** go test ./routers ./models -run 'TestLanding|TestCatalog' -count=1 and go test -tags integration ./tests/integration -run 'TestLanding' -count=1. Confirm failures reflect missing policy/presentation, not fixture setup.
- [ ] **3. Implement the approved layout and branding.** Build the public page with shared tokens, compact Hgs header, exact tagline, approved local Gopher, actual sign-in routes and policy-controlled public Explore. Omit the prototype notice, placeholder repositories, preview dialogs and example host from production. Use real repository URLs for any clone example or omit it when no public repository is available. Reuse server-side catalog policy; never hide fetched private data in JavaScript. Phase out Gogs product labels/default descriptions in retained templates/locales while preserving legal notices, historical attribution, import paths and operator configuration. Add the mascot credit and license alongside the asset. Use one beaker master matching the approved mascot for favicon/navigation and email identity, with small-size/inverse variants. Refresh existing email HTML and branding with inline compatible styles and absolute AppUrl links; preserve legal notices, token/link generation, recipients and triggers. Do not add PR mail.
- [ ] **4. Verify GREEN** with the same focused commands, go build ./..., and the full UI route regression suite from Task 15 after the branding changes. Browser-check both configuration values at 390px and 1440px, keyboard navigation, public-empty state, registration on/off, configured sign-in providers, and sub-URL deployment. Confirm no private repository data in HTML, search results or counts; verify image transparency/legibility and attribution, 16/32px favicons, 24/32px rail marks on blue/white, and representative existing emails with images enabled/blocked. Record results in docs/mercurial/shared-ui-evidence.md.
- [ ] **5. Commit** as ui: apply approved Hgs landing page and identity. Continue to Tasks 17–18 before whole-branch review and integration handoff.

### Task 17: Watch/Star, local Help, and shared footer navigation

**Files:** Modify routers/repo/repo.go retained Action handler, cmd/web.go routes, templates/shared/repo_nav.tmpl and the shared repository header established in Task 5, templates/shared/end.tmpl/footer partial, and conf/locale/locale_*.ini. Create routers/help.go, templates/help.tmpl and routers/help_test.go; extend tests/integration/ui_test.go.

**Interfaces:** Preserve existing watch/star models and selected-state/count view data; separate the two actions. Replace GET mutation routes with POST forms to the retained repository action handler under current authorization/CSRF middleware. Add routers.Help(ctx *middleware.Context) for GET /help using the shared shell and RequireSignInView policy. Footer locale selection uses existing locale routing, safe current path/query and AppSubUrl.

- [ ] **1. Add TestRepositoryWatchStarUI, TestSharedHelpAndFooter.** Exercise watch/unwatch independently of star/unstar, correct counts and state, reader permission, duplicate requests, failure feedback, anonymous sign-in links, direct GET nonmutation and missing-CSRF denial. Assert Help is reachable on desktop/mobile, uses configured clone methods without secrets or unsupported actions, and obeys sign-in policy. Locale switching retains safe context; footer preserves operator identity and legal notices without hard-coded Gogs marketing links.
- [ ] **2. Run RED:** go test -tags integration ./routers ./routers/repo ./tests/integration -run 'TestRepositoryWatchStarUI|TestSharedHelpAndFooter' -count=1.
- [ ] **3. Implement existing-action controls and shared navigation.** Put labeled Watch/Star alongside Clone/Fork with independent active state; preserve notification semantics and avoid optimistic count changes on failures. Provide local Hgs/Mercurial help for the actual supported workflows and a shared language/footer control. Keep new product strings localized.
- [ ] **4. Verify GREEN** with the focused command, then inspect header/footer/Help at 390px and 1440px using keyboard, a guest, a reader and an owner. Confirm neither Watch nor Star adds an unapproved PR notification path.
- [ ] **5. Commit** as ui: complete repository actions and help navigation.

### Task 18: Same-page editor draft recovery across session expiry

**Files:** Create models/editor_draft.go, models/editor_draft_test.go, modules/editordraft/service.go, modules/editordraft/service_test.go, routers/user/drafts.go, routers/user/drafts_test.go and public/js/app/drafts.js. Extend models/models.go registration, cmd/web.go, existing login/session-check handlers, shared Markdown editor, issue/PR forms and locale strings. Add tests/integration/editor_draft_test.go and acceptance evidence.

**Interfaces:** Define editordraft.Target { RepositoryID int64; Kind string; ItemID int64 } with Kind issue_new, issue_edit, issue_comment, pull_new, pull_edit, pull_comment, pull_review. Define Payload { Title, Body string; Metadata map[string]string }, allowing only each form's established non-secret fields. Page identity is a cryptographically random page ID and secret retained only in document memory; store its verifier, never the raw secret, with the record. Cookie hgs_draft identifies a random browser draft collection, not draft text. Authenticated endpoints under AppSubUrl: POST /user/drafts/open allocates target/page identity, POST /user/drafts/:page/save stores a versioned payload, POST /user/drafts/:page/restore returns this page's payload after reauthentication, POST /user/drafts/:page/discard deletes it, GET /user/drafts/session returns current session/account and fresh CSRF state without repository data. Restore never lists other drafts. A separate disposal token can only discard its own record, including after expiry; no read/write capability is granted by it.

**Service contract:** In modules/editordraft define Grant { CollectionID, PageID, PageSecret, DisposalToken string; ExpiresAt time.Time } and Snapshot { Version int64; Payload Payload; ExpiresAt time.Time }. Define Authorize func(context.Context, int64, Target) error, and NewService(store Store, authorize Authorize, now func() time.Time) *Service. Methods: Open(ctx context.Context, actorID int64, collectionID string, target Target) (Grant,error); Save(ctx context.Context, actorID int64, collectionID,pageID,pageSecret string, version int64, payload Payload) (Snapshot,error); Restore(ctx context.Context, actorID int64, collectionID,pageID,pageSecret string) (Snapshot,error); Discard(ctx context.Context, pageID,disposalToken string) error; PurgeExpired(ctx context.Context) error. Sentinel errors: ErrUnauthenticated, ErrForbidden, ErrExpired, ErrDiscarded, ErrStaleVersion, ErrLimitExceeded, ErrInvalidPayload.

**Persistence contract:** models.EditorDraft stores PageID, CollectionID, ActorID, RepositoryID, Kind, ItemID, PageSecretHash, DisposalTokenHash, Version, PayloadJSON, ExpiresAt and Discarded. Target/Payload stay in editordraft; serialize their validated fields into the model without a reverse package import. Store methods: Create(ctx context.Context, row *models.EditorDraft, maxLive int, now time.Time) error; Find(ctx context.Context, pageID string) (*models.EditorDraft,error); SaveIfLive(ctx context.Context, pageID string, expectedVersion,nextVersion int64, payloadJSON string, expiresAt,now time.Time) error; DiscardIfToken(ctx context.Context, pageID string, disposalTokenHash []byte) error; PurgeExpired(ctx context.Context, now time.Time) error. Provide models.NewEditorDraftStore() using the established engine. Create enforces the per-account cap atomically; SaveIfLive rejects expired/discarded rows and version races; DiscardIfToken clears payload and leaves a short-lived terminal record until expiry so in-flight saves cannot recreate it. Use int64 IDs/version, string identifiers/serialized payload, []byte token hashes, bool Discarded and time.Time timestamps. Hash unpredictable 32-byte tokens and compare in constant time; cookie and page credentials never substitute for current account/repository authorization. Lifecycle deletion clears all account/repository rows through existing callbacks.

- [ ] **1. Add TestEditorDraftOwnerAndPermission, TestEditorDraftLimitsAndExpiry, TestEditorDraftVersionAndDiscard, TestEditorDraftHTTPPolicy and TestEditorDraftBrowserLifecycle.** Assert same-account/target authorization, cookie-alone denial, fresh CSRF, permission loss/account switch clearing, no secrets/attachments, 255-character title and 64 KiB body, 10-live-draft cap without silent eviction, expiry after 30 minutes inactivity, and account/repository cleanup. Older autosaves must not replace newer versions; deletion racing an in-flight save cannot recreate a draft. Browser cases: one-second debounce/five-second maximum autosave delay, session expiry with editor retained, login in another tab, restore without automatic submission, newer in-page text winning, Save failed feedback, two tabs isolated, navigation/reload/close and Back/Forward resurrection refusal, same-document Write/Preview retention, success/cancel disposal, and unreachable unload deletion followed by TTL cleanup.
- [ ] **2. Run RED:** go test ./models ./modules/editordraft ./routers/user -run 'TestEditorDraft' -count=1 and go test -tags integration ./tests/integration -run 'TestEditorDraft' -count=1. Browser lifecycle assertions require a real browser harness; supplement the Go suite with recorded browser checks, not a test that only inspects JavaScript strings.
- [ ] **3. Implement bounded temporary storage and page-bound recovery** exactly as DESIGN.md specifies. Use host-only HttpOnly SameSite=Lax cookie scoped to AppSubUrl + /user/drafts, Secure on HTTPS, max-age 1800 seconds refreshed on successful saves. Check live authorization and CSRF for read/write; no raw cookie content, URLs, storage APIs or logs may contain draft text/page secrets. Keep the page capability only in document memory. Rotate/clear it and stale fields on pagehide/bfcache re-entry; deletion is best effort and TTL cleanup is mandatory. Preserve local text through expired-session failures and offer existing login in a separate tab plus Check sign-in. Reauthenticate the same account, refresh CSRF, validate target metadata and restore only when not overwriting newer in-memory text. Never auto-submit. Clear recovery on success/cancel/navigation, without clearing other tabs. Reject oversized payloads visibly, without truncation or storing credentials.
- [ ] **4. Verify GREEN** with focused service/handler/integration tests and the browser lifecycle cases, including sub-URL deployment, Unicode at limits, blocked cookies, simulated save outage and expiry during a save. Record exact commands and results. Re-run the UI route regression suite plus go build ./... after integration. Inspect that session-expiry errors are machine-distinguishable without returning draft data to an unauthenticated request.
- [ ] **5. Commit** as feat: recover editor drafts after session expiry. Complete whole-branch review for Tasks 0–18 and present integration choices without implicitly merging or pushing.

## Self-review and execution handoff

Coverage: shared layout/tokens (3); discovery/popouts/recents (4); workspace/source/history/settings (5); complete Issues (6); Admin/accounts/org/auth/installer/errors (7); forks/privacy (8,14); PR lifecycle/comparisons/reviews/merge (9–13); native/runtime/visual cutover (15); prerequisite safety (0–2); approved landing page, Hgs branding, Gopher/beaker assets, existing email presentation and public discovery policy (16); Watch/Star, Help/footer/language (17); same-page draft recovery (18).

Cross-task types/methods are defined above or attributed to the hosting prerequisite. `collaboration.Service` is introduced in Task 8 after guarded hosting primitives exist; earlier UI tasks do not depend on that package. Routes expose real features only once their service task is complete. Feasibility contracts are gates, not unverified promises or authority to weaken the approved design.

The user approved subagent-driven execution in the existing worktree, and subsequently approved the landing page/Gopher with the public discovery correction now captured in Task 16. Task 0 remains blocked on the hosting prerequisites assigned to the existing chat. Resume with the preserved execution method after those prerequisites pass; no additional design or execution-method approval is required for this scope.
