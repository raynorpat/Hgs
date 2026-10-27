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
