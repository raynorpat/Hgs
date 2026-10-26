# Shared UI integration prerequisite evidence

Audit date: 2026-10-26. **Task 0: BLOCKED / incomplete.** Neither the transport gate nor the required Mercurial application baseline passes. This note authorizes no product implementation or architectural remedy.

## Baseline and provenance

- Worktree: `C:/Users/raynorpat/.codex/worktrees/shared-ui-pull-requests/gogs`.
- Branch: `codex/shared-ui-pull-requests`; starting HEAD: `3b27479 docs: approve shared UI design and plan implementation`.
- Elevated `git status --short` returned no changes before this audit. No concurrent edits were present in this worktree. No files were copied from the original checkout.
- An initial sandboxed status call reported `fatal: this operation must be run in a work tree`; the authorized elevated call succeeded. This was an access-context issue, not evidence of a dirty baseline.
- `go version`: `go1.27.3 windows/amd64`.
- `git remote -v` shows existing origin `git@github.com:raynorpat/Hgs.git`. Historical statements that there is no remote are stale; this audit did not fetch, push, or infer that CI has run.
- Reviewed: approved hosting design/plan, approved shared UI/PR design, Task 0 brief, `CLAUDE.md`, transport evidence, source APIs, integration tests, and native CI configuration. The full shared UI implementation plan was not read.

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
