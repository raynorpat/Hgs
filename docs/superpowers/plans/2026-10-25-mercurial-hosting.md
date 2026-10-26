# Mercurial-only Gogs Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Git hosting with portable Mercurial repository creation, SSH/HTTPS transport, and web browsing while preserving Gogs users, permissions, and issues.

**Architecture:** Keep the Go application and templates. Run isolated Mercurial processes for storage operations, a private HTTP worker per request, and an stdio worker per embedded SSH session. Enforce writes through application-managed transaction hooks and a parent-owned capability service.

**Tech Stack:** Go 1.27.3; Mercurial server ≥ 5.0 (tested at 5.0.2 and 7.2.4), native clients ≥ 2.0 (tested at 2.0.2 and 7.2.4); `net/http`, `os/exec`, `golang.org/x/crypto/ssh`, `golang.org/x/sys`; existing Macaron/templates and xorm database layer, with pinned compatibility updates. SQLite is the small-install baseline; retain MySQL/PostgreSQL drivers.

**Spec:** [Approved design](../specs/2026-10-25-mercurial-hosting-design.md). Read both documents before execution.

## Global Constraints

- "Keep Gogs' users, permissions, and issues. Remove Git hosting entirely."
- "The code must remain portable across Windows, macOS, and Linux."
- "Initial repositories are empty." No Git/database migration and no Git API compatibility promise.
- "No Git executable required at runtime, no Git repository fallback, no Git wire endpoints, and no Git compatibility layer disguised as Mercurial."
- "No shell-script-based deployment requirement." No custom Python helper or replacement wire protocol.
- Storage is `<repository-root>/<immutable repository ID>/`, implementing the approved proposal; no checked-out working files.
- "SSH always authenticates an account key, including public clones." HTTPS anonymous reads follow public/sign-in policy.
- "Loopback binding prevents remote access but is not authentication against hostile local processes." Trusted-host deployment only.
- "Never transparently replay a write after a worker failure."
- "No claim that successful cross-compilation proves platform support."
- All approved exclusions remain: forks, mirrors, imports, pull requests, releases, archives, web editing, editable hooks, push webhooks/feeds, automatic issue closing, subrepository traversal, and extension-specific protocols.
- Version policy (revised 2026-10-25 at the user's request): the server accepts Mercurial ≥ 5.0 and rejects older versions at startup; native clients ≥ 2.0 must clone/pull/push. The server matrix tests 5.0.2 (oldest supported, Python 2.7) and 7.2.4 (newest tested); the client matrix tests 2.0.2 (pre-bundle2, pre-phases) and 7.2.4. Versions in between are accepted but documented as untested. Before Task 1, confirm Go 1.27.3 and Mercurial 7.2.4 exist as released; they are planning choices, not verified facts. The installed 7.0.3 is a convenient intermediate check, not a release target. No tools are installed during plan authoring.
- Legacy exchange stays enabled for old clients (`server.bundle1`, legacy `pushkey`); obsolescence-marker exchange is explicitly disabled in managed configuration.

## Review Focus

1. Repository visibility/ownership/key changes while a transaction is open: revalidate writes and never transfer one actor's authority to another (Tasks 3, 7, 14).
2. Worker cancellation with blocked pipes and a running hook, especially Windows executable paths containing spaces: reap the whole process tree without deadlocking the hook callback (Tasks 2, 3, 6).
3. Two principals concurrently use the same repository: request workers/capabilities remain separate and a reader worker (static deny hooks, no capability) cannot inherit writer access (Tasks 3, 4, 6, 14).
6. Oldest supported versions: a 2.0-era client against a 5.0 server and against the newest server, over both transports, with legacy bundle1/pushkey paths denied for readers (Tasks 5, 6, 14).
4. Case-distinct, symlink, non-UTF-8, or platform-reserved filenames in repository history: never check out/follow paths; provide escaped display and lossless path identity, or a clear bounded unsupported-encoding result (Tasks 9, 12).
5. Empty or all-closed repositories and concurrent pushes during pagination: issues still work, landing selection is deterministic, and history pages stay pinned (Tasks 9, 10, 12).

## Execution structure and conventions

Use one staged plan: storage, identity, transports, and browsing share the same authorization and repository model. Splitting these into independent product projects would conceal integration dependencies. Each task below is independently testable, but intermediate commits are not deployable releases.

Sequence: **1 → 2 → 3 → 4 → 5 → 6 (transport gate) → 7 → 8 → 9 → 10 → 11 → 12 → 13 → 14 → 15**. Do not begin broad model/UI conversion until Task 6 passes natively on all three OS families. An unavailable runner means the gate is unverified, not passed. An architectural failure goes back to design discussion.

All file paths below are relative to `D:/github/gogs`. New package names are decisions; preserve existing names where files already exist. Test snippets specify required assertions rather than full copy/paste files. Any fixture helpers named in a snippet are local test helpers to implement in that task's listed test file; they are not hidden production dependencies. Use standard `testing` and `errors`/`reflect`, with `t.Cleanup` for owned resources.

`integration` is a Go build tag for real Mercurial/native-client tests. Add `tests/integration` in Task 6 and extend it later. Tests must fail, rather than skip, if an explicitly requested integration prerequisite is missing. Ordinary unit tests need no installed Mercurial. Repository fixtures use temporary directories and an isolated hgrc; never use the operator's repository/config.

For each test cycle, observe the expected failure before implementing. Infrastructure/build tasks use the failing build or diagnostic as their red check; do not invent a test that merely asserts a config file's contents. After each green task, run the affected package tests and `go build ./...` to keep intermediate code compilable. Run the complete suite at integration milestones, not after every tiny edit.

**Commit checkpoints:** The project is a Git repository (initialized at the user's request on 2026-10-25; no remote). Commit each task's files at its checkpoint with the listed message. Do not add a remote or push. Worktree setup belongs to execution, not plan authoring.

## Execution status and environment (updated 2026-10-25)

Done before plan execution began (commits `cb8e76e`..`a5bed02`):
- `go.mod`/`go.sum` replace `.gopmfile`; all 27 commit-pinned dependencies translated to their exact legacy commits; the one substitution is the SQLite driver (`mattn/go-sqlite3` → pure-Go `modernc.org/sqlite`, below; the old pin stays as an indirect requirement); `toolchain go1.27.3`. `go mod verify`, `go build ./...` and `go vet` on touched packages pass with `CGO_ENABLED=0`.
- SQLite uses pure-Go `modernc.org/sqlite` v1.60.1, compiled in by default (no `sqlite` build tag, no C toolchain); `TestSQLiteEngineRoundTrip` covers it.
- Modern-Go fixes: `ldap_test.go` printf vet errors; `templates/repo/issue/list.tmpl` `{{..` typo that panicked at startup; nil `MultipartForm` panic in `routers/repo/issue.go` `uploadFiles` (regression test added).
- Smoke-tested `gogs web` on Windows: SQLite install, login, admin pages, repository creation, issue creation/view.

Task 1 remaining: `docs/mercurial/build-baseline.md`, `newTestDatabase` helper and `TestFreshDatabasePreservesUsersAndIssues`/`TestPasswordVerification`, `.github/workflows/mercurial.yml`, retire `.travis.yml`. `modules/ldap` tests dial a dead public LDAP server and must move behind an explicit integration opt-in.

Local environment: Windows 11 only. Go 1.27.3 at `C:\Program Files\Go`. Mercurial 7.0.3 via scoop, whose ambient configuration loads a missing `hggit` extension (useful evidence for the isolation tests). Python 3.13; no Python 2.7. The required tested versions (server 5.0.2 and 7.2.4, clients 2.0.2 and 7.2.4) are not installed, and there are no Linux or macOS runners and no CI remote. Consequences:
- Integration steps against real Mercurial run locally only on Windows, and only with versions the user has approved installing; anything else is recorded as unavailable, never passed.
- The Task 6 gate requires native Windows, Linux and macOS results. Without those runners it cannot pass, so execution stops after Task 6 and reports the gate result rather than starting Task 7.
- Override (user, 2026-10-26): execution continues to Task 7 and later tasks while the Task 6 gate remains unverified on Linux/macOS and 5.0.2/2.0.2 (stream-bundle gap accepted as a documented limitation). Native Linux and macOS gate evidence is still required before any release.

Task completion record (commit ranges are `start..end`, end inclusive):
- Task 1 — complete 2026-10-25 (`2cb904d..b18dab5`).
- Task 2 — complete 2026-10-25 (`b18dab5..9152f1a`).
- Task 3 — complete 2026-10-25 (`9152f1a..5b9aabb`).
- Task 4 — complete 2026-10-25 (`5b9aabb..765b3d5`).
- Task 5 — complete 2026-10-26 (`765b3d5..4694a42`).
- Task 6 — complete 2026-10-26 (`4694a42..1cb1c12`). Recorded result: **Gate: FAILED → accepted limitation (user sign-off 2026-10-26)** — revocation at transaction close does not hold for stream-clone bundles unbundled into empty repositories, accepted and documented; every other row passed on Windows — and **still unverified** on Linux/macOS and Mercurial 5.0.2/2.0.2, which were unavailable. Evidence: `docs/mercurial/transport-evidence.md`.
- Task 7 — complete 2026-10-26 (`609b70e..d8a398c`), reviewed; shared authorization and portable SSH key storage.
- Task 8 — complete 2026-10-26 (`d8a398c..5a9bd8a`), reviewed; ID-based repository lifecycle and transfer/deletion fixes.
- Task 9 — complete 2026-10-26 (`5a9bd8a..4fae7f4`), reviewed; inherited implementation adopted with encoding/branch-ancestry fixes. Windows/Mercurial 7.2.4 checks pass; native Linux/macOS and Mercurial 5.0.2 remain unverified.
- Task 10 — complete 2026-10-26 (`ec877fd..126145e`), reviewed; snapshot history, file ancestry, and bounded diffs verified on Windows/Mercurial 7.2.4.
- Task 11 — complete 2026-10-26 (`126145e..07afdf7`), reviewed; runtime composition, bounded session cleanup, password-version revocation, database-format guard, and installation wiring verified locally.
- Task 12 — complete 2026-10-26 (`07afdf7..cc4fbf4`), reviewed; Mercurial UI, metadata-only issue workflows, path/cursor identity, and streaming cleanup verified locally. Native Unix UI/latency remains unverified.
- Tasks 13–15 — not started. User authorized continuation with subagents on 2026-10-26; execution ledger: `.superpowers/sdd/2026-10-25-mercurial-hosting/progress.md`.
- User decisions (resolved 2026-10-26): the stream-bundle revocation gap is accepted as a documented limitation (spec, Task 15 docs, local upstream proposal draft `docs/mercurial/upstream-stream-push-proposal.md`); writers drop the per-changeset close hooks (`pretxnclose-phase`, `pretxnclose-bookmark`), readers keep all deny hooks.

## Continuation implementation clarifications (2026-10-26)

- Task 10 cursors carry the immutable node, original local revision (`SnapshotRevision`), and exclusive before-revision bound. `BeforeRevision=-1` starts a first page at either the current visible tip or an explicit validated snapshot. HTTP encoding belongs to Task 12. Repository history includes all visible branches through the snapshot; file history follows only the snapshot's recorded file ancestry. A file absent at that snapshot returns `ErrNotFound`.
- Task 11 must inspect database format before schema synchronization. Existing HTTP workers intentionally detach from request cancellation, so transports need explicitly injected worker session registration for revocation/drain, including readers without capabilities. A service-owned current-authority monitor (default one second) supplies bounded-delay cleanup; write hooks remain synchronous authorization with the previously accepted stream-bundle exception. Repository deletion still blocks new admissions and waits for worker release.
- Task 11 preserves live installation through a cmd-owned service holder and injected initialization callback. It must not add model-to-hosting imports, cross-package global observers, or an installer-only restart requirement. Necessary bridge/SSH/schema/deletion integration files are included even where the earlier task file list omitted them.
- Task 12 drops the per-entry last-commit column (approved option c) to keep directory browsing to a small constant command count. File-history links preserve the viewed node/revision; the global history timeline starts at the latest visible snapshot across all heads.
- Task 14 must pin raw Mercurial testdata to LF and verify a Windows checkout with automatic CRLF conversion; production raw-path parsing must preserve bytes.

## File and interface map

| Area | Files and responsibility |
| --- | --- |
| Build | `go.mod`, `go.sum`, `.github/workflows/mercurial.yml`: reproducible dependencies and native matrix |
| Child processes | `modules/hg/runner.go`, `config.go`, `process_windows.go`, `process_unix.go`: isolated commands, streaming, process trees |
| Hook authority | `modules/hgbridge/capability.go`, `control.go`, `hooks.go`; `cmd/hg_hook.go`: bounded worker identities and mutation checks |
| Protocol workers | `modules/hgbridge/http.go`, `stdio.go`; `modules/ssh/command.go`, `ssh.go`: private Mercurial serving and embedded SSH |
| Domain | `models/repo_access.go`, `repo_lifecycle.go`; existing `repo.go`, `access.go`, `publickey.go`: identity, ID storage, provisioning/deletion |
| Mercurial reads | `modules/hg/types.go`, `refs.go`, `files.go`, `history.go`, `diff.go`: typed changesets/manifests/ref selection |
| Composition | `modules/hosting/service.go`, `cmd/web.go`, `routers/hg/http.go`: connect models, authorization, workers, application shutdown |
| Views | Existing middleware, repository handlers/templates: browse and issues without Git objects |
| Assurance | `tests/integration/*_test.go`, `docs/mercurial/*.md`: native round trips, denied writes, operational instructions |

Dependency rule: `modules/hg` imports no models or web code. `modules/hgbridge` depends on `hg` and injected authorization callbacks, never models. `modules/ssh` depends on bridge and an injected identity resolver. Models may call `hg` but never import hosting/routers. `modules/hosting` composes these pieces; routers receive the service. This prevents model/transport import cycles.

### Task 1: Establish a reproducible build and database test baseline

**Files:** Create `go.mod`, `go.sum`, `.github/workflows/mercurial.yml`, `docs/mercurial/build-baseline.md`, `models/testutil_test.go`, `models/baseline_test.go`. Modify `.gopmfile`, `.travis.yml`, `cmd/web.go` (dependency version checks), and exact imports/API call sites required by dependency resolution. Preserve application behavior here.

**Interfaces:** Produces `newTestDatabase(t *testing.T)` in model tests, which initializes package `x` with a temporary SQLite database, restores globals on cleanup, and forbids parallel tests sharing `x`. No production API change.

- [ ] **1. Record the failing baseline.** Run `go version`, `go env GOMOD`, and `go build ./...`; record failures and legacy build tags in the baseline document. Inventory existing tests requiring live LDAP/network services.
- [ ] **2. Establish pinned dependencies.** Use module path `github.com/gogits/gogs`, language/toolchain Go 1.27.3, and translate legacy commit pins to real module versions. Timebox this translation (record the budget in the baseline document); replace vanished 2014-era dependencies with the smallest maintained substitute rather than reconstructing them. Done 2026-10-25: SQLite uses the pure-Go `modernc.org/sqlite` driver (registered as `sqlite3` for xorm), built by default with no tag and no C toolchain. Resolve missing/renamed modules explicitly; pin `x/crypto`/`x/sys` to maintained versions compatible with Go 1.27.3. Update only necessary call sites. Retain MySQL/PostgreSQL. Write down each substitution and reason; do not guess nonexistent versions or fetch mutable dependencies on every build.
- [ ] **3. Add meaningful baseline tests.** `TestFreshDatabasePreservesUsersAndIssues` creates user, repository row, issue, label, and comment; asserts lookup and ownership links survive database reload. `TestPasswordVerification` verifies correct/wrong password behavior. Repair the existing test-engine helper's shadowed engine argument if reused. External-service tests require an explicit integration opt-in and documented prerequisites.
  ```go
  if reloadedIssue.RepoId != repo.Id || reloadedIssue.PosterId != user.Id || len(reloadedComments) != 1 {
      t.Fatal("fresh database lost issue ownership or discussion")
  }
  ```
- [ ] **4. Verify.** Run `go mod verify`, `go test ./...`, `go build ./...`; expect exit 0. Repeat native build/unit jobs on Windows amd64, Linux amd64, and macOS arm64. CI installs pinned Mercurial 7.2.4 and 5.0.2 (with a pinned Python 2.7) for later tagged server jobs, plus a pinned 2.0.2 client; record versions. Where a platform cannot run 5.0.2 or 2.0.2 (e.g. no Python 2.7 on macOS arm64), record that job as unavailable, not passed. Do not silently downgrade a dependency or bypass checksum verification.
- [ ] **5. Checkpoint:** `build: establish reproducible Go and database baseline`.

### Task 2: Isolated Mercurial runner and portable process supervision

**Files:** Create `modules/hg/{runner.go,config.go,process_windows.go,process_unix.go,runner_test.go,process_test.go}`. Modify `modules/setting/setting.go`, `conf/app.ini` to parse `[mercurial]` options without activating hosting yet.

**Interfaces:** `type Config struct { Executable, ConfigPath string; MaxProcesses int; SessionTimeout time.Duration }`; the other timeouts and byte/line bounds below are package constants, promoted to configuration only when an operator need is demonstrated; `NewRunner(Config) (*Runner,error)`; `(*Runner).Check(context.Context) error`; `(*Runner).Run(ctx context.Context, repoPath string, args []string, input io.Reader, output io.Writer) error`; `(*Runner).Start(ctx context.Context, repoPath string, args []string, env map[string]string) (*Child,error)`. `Child` exposes `Stdin io.WriteCloser`, `Stdout, Stderr io.ReadCloser`, `Wait() error`, `Stop(context.Context) error`. Internal callers alone construct args/env; these are not public user command APIs. `Run` drains stderr and applies the configured output bound; streaming transports and raw files use `Start` with caller-owned copy loops. Browser commands enforce their own duration context; session streams use idle and session bounds.

- [ ] **1. Add `TestRunnerIgnoresAmbientConfig`, `TestRunnerBoundsOutput`, `TestCancelReapsDescendants`, `TestProcessLimitQueueTimeout`.** Use a Go test subprocess as a controlled executable, including a grandchild and blocked stderr/stdout. Representative assertion:
  ```go
  if !errors.Is(err, context.DeadlineExceeded) || childAlive || grandchildAlive {
      t.Fatalf("cancellation leaked process or lost deadline: %v", err)
  }
  ```
  The fake process prints selected environment names; assert ambient `HGRCPATH`, `HGPLAINEXCEPT`, `HGRCSKIPREPO`, `HG*` overrides, `PYTHONPATH`, and `PYTHONHOME` cannot inject configuration. Preserve required OS/runtime variables through a documented allowlist; pass only app-defined `GOGS_HG_*` hook context.
- [ ] **2. Red:** `go test ./modules/hg -run 'TestRunner|TestCancel|TestProcessLimit' -count=1`; expected missing runner or failed isolation/reaping assertions.
- [ ] **3. Implement interfaces and config.** Defaults: 8 child slots, 10s startup, 30s browser command, 120s transfer idle, 30m session ceiling, 5s queue, 30s shutdown, 1 MiB rendered file, 2 MiB/10,000-line diff. Stream transport/raw output without the browser byte cap. Use a monotonic idle watchdog on streaming activity. Use Windows Job Objects and hidden process creation, Unix process groups, with ownership-safe cleanup. Hooks must not consume a worker slot or wait behind their own parent. `Check` parses the Mercurial version, rejects anything below 5.0 or unparseable with explicit unsupported/missing diagnostics, logs a warning for versions outside the tested set, and probes required features (JSON templates, the hooks listed in Task 3) rather than inferring them from the version string alone. Environment stripping covers both Python 2 and Python 3 Mercurial builds. Configure UTF-8, `HGPLAIN`, managed `HGRCPATH`, no prompts/pager/color, and no user extension loading.
- [ ] **4. Green:** repeat unit command; then a tagged `TestRunnerRealMercurial` performs `init` and structured version/help checks in a directory with spaces/Unicode against both 5.0.2 and 7.2.4, and asserts a 4.x executable is rejected. Expect no ambient-extension diagnostics on each native OS.
- [ ] **5. Checkpoint:** `feat: isolate and supervise Mercurial processes`.

### Task 3: Session capabilities and fail-closed Go hook callbacks

**Files:** Create `modules/hgbridge/{capability.go,control.go,hooks.go,capability_test.go,hooks_test.go}`, `cmd/hg_hook.go`, `cmd/hg_hook_test.go`. Modify `gogs.go` to register `internal-hg-hook` without starting web/database initialization in that command.

**Interfaces:** In bridge: `type Principal struct { UserID, KeyID, TokenID int64 }` (zero UserID = anonymous; zero KeyID/TokenID = that credential is not used); `type Grant struct { Principal Principal; RepoID int64; CanWrite bool }`; `type Authorize func(context.Context, Grant) error`; `NewController(Authorize, time.Duration) (*Controller,error)`; controller methods `Issue(Grant) (capability string, env map[string]string, revoke func(), err error)` (rejects grants with `CanWrite == false`; readers get no capability), `Check(ctx context.Context, capability string, repoID int64, hook string) error`, `Close(context.Context) error`; `HookConfig(executable string, canWrite bool) (map[string]string,error)`. In cmd: `RunHGHook(ctx context.Context, hook string, env []string) error` with fixed modes `deny` (unconditional, no network) and `check` (capability callback), and `CmdHGHook` CLI registration. The authorizer rechecks credential validity and repository rights on every `check`, which is what enforces in-flight revocation of writes; terminating live sessions on key/token/permission changes is handled by the hosting service (Task 11), not by a second revocation path in the controller.

- [ ] **1. Add `TestCapabilityBoundToRepositoryAndActor`, `TestReaderHooksDenyWithoutController`, `TestHookFailsClosed`, `TestRevocationBeforeTxnClose`.** Use a mutable fake authorizer and an injected test clock. Assert unknown/expired/wrong-repository tokens fail; reader hook config contains only `deny`-mode hooks and denies even when the controller is unreachable; `Issue` refuses a read-only grant; writer demotion or credential revocation before commit fails; controller unavailability fails writes. `TestHookExecutablePathQuoting` uses executable paths with spaces, quotes where legal, and Unicode, and is verified by real hook execution in step 4 because Mercurial runs hooks through `cmd.exe` / `/bin/sh`. Never log tokens or passwords.
  ```go
  if cfg, _ := HookConfig(exe, false); strings.Contains(strings.Join(values(cfg), " "), "check") {
      t.Fatal("read-only hook configuration can reach the capability callback")
  }
  ```
- [ ] **2. Red:** `go test ./modules/hgbridge ./cmd -run 'TestCapability|TestHook|TestRevocation' -count=1`; expected missing types/failed policy assertions.
- [ ] **3. Implement.** Generate 32 cryptographically random bytes per capability, keep authority only in parent memory, bind expiry to the session ceiling, and expose a separate loopback-only control listener. The request supplies a fixed allowlisted hook name and repo ID; actor/max rights come from stored capability, not request JSON. Limit control body to 4 KiB and callback to 5s. Install `pretxnopen`, `prepushkey`, `pretxnclose`, `pretxnclose-bookmark`, and `pretxnclose-phase` (all present in 5.0; confirm against 5.0.2 sources); wire each to the same executable with fixed arguments and OS-tested quoting, in `deny` mode for readers and `check` mode for writers; writers get no `pretxnclose-bookmark` or `pretxnclose-phase` hook, because `pretxnclose` runs right after them in the same validate step and rolls back what they guard (user decision 2026-10-26). Managed config also disables obsolescence-marker exchange and keeps `server.bundle1` enabled for 2.0-era clients. `txnclose` is advisory refresh only, never an authorization veto. Revalidate current rights via callback without opening Mercurial or taking a repository lock. Cancel expired writer workers.
- [ ] **4. Green:** repeat command; add tagged `TestRealHookRejectsTransaction` using the compiled application binary (installed under a path with spaces and Unicode) as hook, write-capable/read-only grants, and real Mercurial transaction rollback on both 5.0.2 and 7.2.4. Assert both repository contents and metadata unchanged on failure.
- [ ] **5. Checkpoint:** `feat: enforce Mercurial transaction authority with Go hooks`.

### Task 4: Authenticated private HTTP worker bridge

**Files:** Create `modules/hgbridge/{http.go,http_test.go,http_integration_test.go}`.

**Interfaces:** Consumes runner/controller. `type Target struct { RepoID int64; Path string }`; `NewHTTPBridge(*hg.Runner,*Controller) *HTTPBridge`; `(*HTTPBridge).Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, target Target, grant Grant) error`. Returns errors only before response commitment; logs/redacts late streaming failures and never writes a second response or retries.

- [ ] **1. Add `TestHTTPBridgeKeepsPrincipalsSeparate`, `TestHTTPBridgeDoesNotReplayPush`, `TestHTTPBridgeStripsCredentials`, `TestHTTPBridgeHidesBackendUI`.** A recording child/backend counts delivered mutation requests, captures forwarded headers, and simulates disconnect after committing. Assert exactly one delivery, no public Authorization/Cookie/forwarded identity headers, a distinct capability per writer request, no capability at all for reader requests, and no backend HTML access. Require a single `cmd` query on an otherwise transport-root path; ambiguous/duplicate selectors and metadata paths fail.
  ```go
  if deliveredWrites != 1 || forwarded.Get("Authorization") != "" || readerEnv["GOGS_HG_CAPABILITY"] != "" {
      t.Fatal("HTTP worker replayed a write or crossed an identity boundary")
  }
  ```
- [ ] **2. Red:** `go test ./modules/hgbridge -run TestHTTPBridge -count=1`.
- [ ] **3. Implement.** Start one repository-specific `hg serve` bound to `127.0.0.1` port 0, parse its announced port without opening a reserve-and-close port race, verify readiness within 10s, proxy streaming via Go HTTP transport with replay disabled, then revoke/reap. Preserve Mercurial wire headers and batching. Deny unknown top-level commands using a list derived from the pinned version's documented/core wire command registry; forward supported batches unchanged and rely on hooks for mutation control. Configure push permission per grant (readers: push disabled plus `deny` hooks) and loopback-only `push_ssl=false`. Keep the legacy HTTP commands 2.0-era clients use (bundle1 `unbundle`, `pushkey`, `x-hgarg` headers) on the allowlist. No worker pooling or public backend URLs. Only call `Serve` after app-level read/TLS authorization (Task 11).
- [ ] **4. Green:** unit command plus `go test -tags integration ./modules/hgbridge -run TestHTTPBridge -count=1`. Native 7.2.4 and 2.0.2 clients clone/push/pull through a TLS test frontend (TLS 1.2 minimum, single certificate so old clients without SNI work). Reader push fails, successful writer push is visible to a second clone, disconnect reaps worker.
- [ ] **5. Checkpoint:** `feat: proxy Mercurial HTTP through isolated workers`.

### Task 5: Embedded SSH server with strict Mercurial command parsing

**Files:** Replace `modules/ssh/ssh.go`; create `modules/ssh/{command.go,command_test.go,ssh_test.go}`, `modules/hgbridge/{stdio.go,stdio_test.go}`.

**Interfaces:** Bridge `ServeStdio(ctx context.Context, runner *hg.Runner, controller *Controller, target Target, grant Grant, input io.Reader, output, stderr io.Writer) (exitCode int, err error)`. SSH `type ResolveKey func(context.Context, ssh.PublicKey) (hgbridge.Principal,error)`; `type ResolveTarget func(context.Context,hgbridge.Principal,string,string) (hgbridge.Target,hgbridge.Grant,error)`; `ParseCommand(string) (owner, repository string, err error)`; `NewServer(signer ssh.Signer, keys ResolveKey, repos ResolveTarget, runner *hg.Runner, controller *hgbridge.Controller) *Server`; `(*Server).Serve(net.Listener) error`, `Shutdown(context.Context) error`.

- [ ] **1. Add `TestParseNativeHGCommand` and `TestSSHRejectsArbitraryExecution`.** Capture command shapes from Mercurial 2.0.2 and 7.2.4 clients on Unix and Windows with a test SSH recorder; table-test supported quoting/order, including single-quoted (Unix client) and double-quoted (Windows client) repository arguments, and reject `--config`, `--cwd`, `--cmdserver`, multiple repositories, traversal, NULs, CR/LF, shell operators, and trailing commands. Assert rejected input starts no child. Add `TestSSHRejectsShellPTYForwardingEnv`, `TestSSHReturnsChildStatus`, `TestSSHSeparatesConcurrentUsers`.
  ```go
  if _, _, err := ParseCommand("hg -R owner/repo serve --stdio; whoami"); err == nil {
      t.Fatal("SSH command parser accepted trailing execution")
  }
  ```
- [ ] **2. Red:** `go test ./modules/ssh ./modules/hgbridge -run 'TestParse|TestSSH|TestStdio' -count=1`.
- [ ] **3. Implement.** Parse protocol request payloads with SSH library unmarshalling, not trimming binary payload bytes. Support only observed native command grammar, reconstruct all local argv, and resolve paths through callbacks. Accept service username `hg`; keys identify users. Separate stderr and send SSH exit status. Bound handshake/idle/session duration and reject every channel/request type except allowed session execution. Store host signer outside this module (Task 11); no prototype paths/global environment mutation remain.
- [ ] **4. Green:** unit command plus tagged tests with native `hg` (7.2.4 on each OS; 2.0.2 where runnable) using OpenSSH as the test client. Successful clone/push/pull and reader denial; cancel blocked channel and verify worker/hooks disappear. The server must not require installed `ssh`, `sshd`, or `ssh-keygen`.
- [ ] **5. Checkpoint:** `feat: serve Mercurial over embedded SSH`.

### Task 6: Prove transport and authorization feasibility before conversion

**Files:** Create `tests/integration/{transport_test.go,mutation_test.go,supervision_test.go,testutil_test.go}`, `docs/mercurial/transport-evidence.md`. Extend `.github/workflows/mercurial.yml` to run these native tests.

**Interfaces:** Test-only fixtures assemble runner, controller, SSH server, TLS bridge, and an in-memory authorizer. `snapshotRepository` captures nodes, heads, bookmarks, phases, and tags using independent `hg` commands. It excludes benign lock/cache files from equality. No new production API.

- [ ] **1. Add `TestTransportMutationMatrix`.** Run both transports × server {5.0.2, 7.2.4} × client {2.0.2, 7.2.4}, reader/writer, normal/new-head changesets, bookmark create/move/delete, phase changes (current client only; 2.0 has no phases), legacy pushkey, bundle1 and bundle2 updates, attempted obsolescence-marker push, and batching. Use Go test wire drivers where the current native client cannot emit legacy variants; do not implement wire handling in production. Each reader case asserts:
  ```go
  if operationSucceeded || !reflect.DeepEqual(before, after) {
      t.Fatal("read-only operation changed repository state")
  }
  ```
  Positive writer controls must exercise the same supported mutation paths; merely rejecting everything is not a pass. Document core wire writes and hooks reached using Mercurial 5.0.2 and 7.2.4 source/help.
- [ ] **2. Red:** `go test -tags integration ./tests/integration -run 'TestTransport|TestSupervision' -count=1`; expect assertion failures for uncovered paths, never a fake red or skipped suite.
- [ ] **3. Close actual gaps within selected architecture.** Add `TestTransportRevocation`, `TestSupervisionHookCallbackUnderSaturation`, and `TestTransportAmbiguousCompletion`. Forbid unsupported extension operations and unproven writes rather than guessing hook coverage. Inspect whether normal publishing reads trigger transactions; public/read-only clone and pull must still work. Do not weaken read policy to make them pass.
- [ ] **4. Gate:** Run the command on native Windows, Linux, macOS with Go 1.27.3 and the full version matrix; the newest server/client pair must pass on all three, and the 5.0.2 server and 2.0.2 client must pass at least on Linux, with other platforms recorded as passed or unavailable. Record versions, commands, outcomes, and process cleanup evidence. If a core required operation cannot be implemented safely with these processes/hooks, stop and revise design. If all pass, mark the gate complete and proceed.
- [ ] **5. Checkpoint:** `test: prove cross-platform Mercurial transport isolation`.

### Task 7: Shared account/repository authorization and portable SSH keys

**Files:** Create `models/{repo_access.go,repo_access_test.go,publickey_test.go}`. Modify `models/{access.go,publickey.go,token.go,user.go,org.go}`; organization/team methods live in `models/org.go` in this checkout. No shell-based key management remains. Replace the name-keyed `Access` table (`UserName`/`RepoName` "owner/repo" strings, `models/access.go`) with `(UserId, RepoId, Mode)` in the fresh schema and update every caller.

**Interfaces:** `type RepositoryAccess struct { Read, Write, Admin bool }`; `RepositoryAccessFor(userID, repoID int64) (RepositoryAccess,error)`; `AuthenticateRepositoryHTTPS(username,password string) (userID,tokenID int64,err error)`; `AuthenticateRepositoryKey(key ssh.PublicKey) (userID,keyID int64,err error)`; `ValidateRepositoryCredential(userID,keyID,tokenID int64) error`. Existing key create/delete APIs retain UI contracts. `userID=0` means anonymous. Hosting later maps these to bridge types. Password authentication returns tokenID 0; token authentication returns the stored token's ID so in-flight revocation can be checked without retaining the secret.

- [ ] **1. Add `TestRepositoryAccessMatrix`, `TestHTTPSCredentialRevocation`, `TestKeyStoreNeedsNoSSHExecutables`.** Cover private/public/sign-in-required, disabled/unrelated users, owner, collaborator, organization owner/team member, and existing administrator semantics. Do not grant a new blanket admin bypass. Revoke access/token/key between checks and assert immediate denial. Key insertion/retrieval must work with an empty SSH-tool PATH and preserve a name plus unique canonical key/fingerprint.
  ```go
  if access.Read != true || access.Write != false || revokedCredentialErr == nil {
      t.Fatal("reader access or credential revocation is incorrect")
  }
  ```
- [ ] **2. Red:** `go test ./models -run 'TestRepositoryAccess|TestHTTPSCredential|TestKeyStore' -count=1`.
- [ ] **3. Implement.** Key access records by repository ID and user ID; delete the name-based lookup and lowercase name normalization. Preserve organization inheritance rules. Renames need no access-row changes; transfers update only rows whose ownership or team derivation changes. Use `ssh.ParseAuthorizedKey` and SHA-256 fingerprints in Go; reject key options/trailing multiple keys, remove authorized_keys and ssh-keygen side effects. Use existing password/login/token validation and disabled-account checks, not a new auth database. Keep callbacks fresh rather than cache a grant forever.
- [ ] **4. Green:** repeat command with isolated SQLite fixtures; assert database outages return denial/errors, not public/private fallback. Verify current account/issue baseline still passes.
- [ ] **5. Checkpoint:** `feat: share hosting authorization and portable key storage`.

### Task 8: ID-based repository lifecycle and fresh schema

**Files:** Create `models/{repo_lifecycle.go,repo_lifecycle_test.go}`. Modify `models/{repo.go,models.go,user.go}`, `modules/auth/repo_form.go`; adapt existing repo creation/settings callers only as needed to compile. Keep unconverted Git routes unreachable at cutover, not dual-backend product behavior.

**Interfaces:** `type RepositoryState string` values `provisioning`, `ready`, `deleting`; `type CreateHgRepositoryOptions struct { OwnerID int64; Name,Description string; Private bool }`; `CreateHgRepository(ctx context.Context, runner *hg.Runner, root string, opts CreateHgRepositoryOptions) (*Repository,error)`; `RepositoryStoragePath(root string, repoID int64) (string,error)`; `ReconcileHgRepositories(ctx context.Context, root string) error`; `type DrainRepository func(context.Context,int64) error`; `DeleteHgRepository(ctx context.Context, root string, repoID int64, drain DrainRepository) error`. Retain and update existing rename/transfer model functions rather than duplicate them.

- [ ] **1. Add `TestRepositoryProvisioningRecovery`, `TestRepositoryRenameKeepsStorageAndIssues`, `TestRepositoryDeleteDrainsFirst`, `TestRepositoryPathContainment`.** Inject failures after row allocation/init/move and before ready; restarting reconciliation must not publish half-created repos or delete unrelated directories. Rename/transfer preserves immutable ID, storage path, issue links, and correct collaborator/team access (rename touches no access rows). Deletion retries safely after each stage and storage symlinks/reparse points cannot lead outside root.
  ```go
  if beforePath != afterPath || beforeID != afterID || issue.RepoId != afterID {
      t.Fatal("rename/transfer moved storage or detached issues")
  }
  ```
- [ ] **2. Red:** `go test ./models -run 'TestRepositoryProvision|TestRepositoryRename|TestRepositoryDelete|TestRepositoryPath' -count=1`.
- [ ] **3. Implement.** Add explicit state and unique owner/name constraint, stage on the repository volume with an ownership marker, initialize no working copy, and persist managed repo configuration. Path accepts positive IDs only. Hide non-ready rows from list/lookup/transport. Deletion tombstones first, blocks new sessions, drains without holding a database transaction, removes only validated owned storage, then deletes related rows. Reconcile states/markers idempotently, including crash after move. Never apply a fresh schema to an existing Git installation; installer rejects nonempty legacy databases/repository roots with a clear fresh-install message.
- [ ] **4. Green:** repeat plus tagged real-init test; assert only `.hg` and managed metadata exist, no README checkout or Git execution. Retain SQLite/MySQL/PostgreSQL schema compatibility; backend integration coverage finishes in Task 14.
- [ ] **5. Checkpoint:** `feat: manage Mercurial repositories by immutable ID`.

### Task 9: Typed Mercurial references and file browsing

**Files:** Create `modules/hg/{types.go,refs.go,files.go,refs_test.go,files_test.go}`.

**Interfaces:** `type NodeID string` validated as full 40-hex nodes for this pinned version; `type RefKind string` values `branch`, `bookmark`, `tag`, `node`; `type RefSelection struct { Kind RefKind; Name string }`; `type Reference struct { Kind RefKind; Name string; Node NodeID; Revision int64; Closed bool }`; `type ManifestEntry struct { Path string; Executable,Symlink bool }`. Runner methods: `References(ctx context.Context,path string) ([]Reference,error)`, `Resolve(ctx context.Context,path string,ref RefSelection) (NodeID,error)`, `Landing(ctx context.Context,path string) (NodeID,error)`, `Manifest(ctx context.Context,path string,node NodeID) ([]ManifestEntry,error)`, `ReadFile(ctx context.Context,path string,node NodeID,file string,out io.Writer) error`. `path` is repository storage path. Empty repository yields `ErrEmptyRepository`; missing path/ref yields `ErrNotFound`.

- [ ] **1. Add `TestLandingSelectsOpenDefaultHead`, `TestReferencesRemainDistinct`, `TestReadFileNeverChecksOut`, `TestLiteralReferenceCannotInjectRevset`.** Fixture has two default heads, another named branch, closed heads, a tag/bookmark with equal names, and a hostile ref string. Assert full nodes, all heads selectable, highest revision open-default preference, all-closed tip fallback, explicit empty result. Read symlink bytes and case-distinct `A`/`a` on Windows without a checkout. Reject traversal and `.hg` metadata queries.
  ```go
  if selected != newerOpenDefaultNode || tagNode == bookmarkNode || checkoutCreated {
      t.Fatal("reference selection or checkout-free browsing is incorrect")
  }
  ```
- [ ] **2. Red:** `go test ./modules/hg -run 'TestLanding|TestReferences|TestReadFile|TestLiteral' -count=1` using recorded structured outputs; then tagged real fixtures.
- [ ] **3. Implement.** Use tested machine-readable templates/JSON, validate encoding without lossy replacement, and resolve names by typed lookup to nodes before content access. Never pass arbitrary revsets. Preserve raw repository path bytes in Go strings; at the later HTTP boundary, valid UTF-8 paths are ordinary percent-encoded URL segments and only non-UTF-8 paths use an opaque base64url token; escape display text. Python 2 (5.x) and Python 3 (6.x+) Mercurial builds may emit non-UTF-8 paths differently in JSON; fixtures cover both. If the pinned CLI cannot represent a path losslessly, return a typed unsupported-encoding error, never serve a different file. Filter secret/hidden revisions consistently; do not assume CLI default visibility equals the served view. Directory grouping comes from manifests, not host filesystem paths.
- [ ] **4. Green:** `go test -tags integration ./modules/hg -run 'TestLanding|TestReferences|TestReadFile|TestLiteral' -count=1` against 5.0.2 and 7.2.4; pass on all OS families (5.0.2 where runnable), including executable bits and symlink metadata stored without checkout. Raw readers stream, rendering callers apply byte limits.
- [ ] **5. Checkpoint:** `feat: browse Mercurial references and manifests`.

### Task 10: Stable history, changeset details, and bounded diffs

**Files:** Create `modules/hg/{history.go,diff.go,history_test.go,diff_test.go}`.

**Interfaces:** `type Changeset struct { Node NodeID; Parents []NodeID; User,Message,Branch string; Date time.Time; Revision int64 }`; `type HistoryQuery struct { Snapshot NodeID; SnapshotRevision, BeforeRevision int64; Path string; Limit int }` (`BeforeRevision=-1` starts; empty Snapshot pins visible tip); `type HistoryPage struct { Snapshot NodeID; SnapshotRevision int64; Entries []Changeset; NextBefore int64; HasMore bool }`; `type DiffResult struct { Text []byte; Truncated,Binary bool }`; `(*Runner).History(ctx context.Context,path string,q HistoryQuery) (HistoryPage,error)`, `Changeset(ctx context.Context,path string,node NodeID) (Changeset,error)`, `Diff(ctx context.Context,path string,node NodeID,parentIndex int) (DiffResult,error)`.

- [ ] **1. Add `TestHistoryDoesNotShiftOnPush`, `TestFileHistoryFollowsRename`, `TestDiffSelectsMergeParent`, `TestDiffBoundsBinaryAndLongLines`.** Fetch page one, push another changeset, fetch page two with cursor; assert same snapshot, no duplicates/skips and no new revision leakage. Cover named heads not ancestral to the default head. Second-parent diff differs appropriately; root uses null parent, nonmerge parent index 1 fails. Assert a single huge line cannot evade byte bounds.
  ```go
  if first.Snapshot != second.Snapshot || !reflect.DeepEqual(combinedNodes, expectedPinnedNodes) {
      t.Fatal("concurrent push shifted history pagination")
  }
  if len(diff.Text) > 2*1024*1024 || !diff.Truncated { t.Fatal("oversized diff was not bounded") }
  ```
- [ ] **2. Red:** `go test ./modules/hg -run 'TestHistory|TestFileHistory|TestDiff' -count=1`.
- [ ] **3. Implement.** Repository history uses all visible changesets up to the pinned tip's local revision, not only ancestors of one branch. On this publishing server with strip and obsolescence disabled, local revision numbers only grow, so `rev < before and rev <= snapshotrev` is already pinned; validated integers and full hex nodes are the only values placed in revsets. Encode snapshot node and before-revision in the cursor, validate both on each request (snapshot node must still map to the same revision), default page size 30/max 100. File history follows Mercurial recorded copy/rename information. Read parent and author metadata as Mercurial data; no invented committer. Bound diff bytes/lines per runner config and distinguish binary/truncation from an empty diff. Reject hidden nodes even when explicitly addressed.
- [ ] **4. Green:** repeat and tagged real fixtures on each platform; verify simultaneous append cannot alter pinned pages. Return an expired/invalid cursor response if its snapshot is no longer visible, never silently restart midway.
- [ ] **5. Checkpoint:** `feat: add stable Mercurial history and changeset diffs`.

### Task 11: Compose hosting, lifecycle authorization, and public routes

**Files:** Create `modules/hosting/{service.go,service_test.go}`, `routers/hg/{http.go,http_test.go}`. Modify `cmd/web.go`, `routers/install.go`, `modules/setting/setting.go`, `conf/app.ini`, `models/repo.go`, `gogs.go`.

**Interfaces:** `type hosting.Config struct { Mercurial hg.Config; RepositoryRoot,HostKeyPath,PublicURL,AppSubURL,SSHAddress,SSHAdvertisedHost string; SSHAdvertisedPort int; TrustedProxyCIDRs []string }`; `hosting.New(ctx context.Context,cfg Config) (*Service,error)`; Service methods `HTTP() http.Handler`, `ListenSSH(net.Listener) error`, `DrainRepository(context.Context,int64) error`, `Shutdown(context.Context) error`. Service owns runner/controller/SSH/HTTP lifetime and adapts model functions from Tasks 7–8. `routers/hg.Handler(service *hosting.Service) http.Handler` mounts transport with URL-prefix normalization. Configuration is passed explicitly, not through newly introduced cross-package global state.

- [ ] **1. Add `TestHostingRejectsPlaintextCredentials`, `TestHostingTrustsOnlyConfiguredProxies`, `TestHostingPrivateRepositoryDoesNotLeak`, `TestHostingStartsWithoutGit`.** Valid direct TLS/trusted proxy traffic succeeds; forged forwarding headers and public plaintext credentials/writes fail before any child starts. Browser cookies do not authorize a transport write. Unknown/private-inaccessible paths share 404 behavior after applicable authentication challenge. Test `/hg/owner/repo`, percent-encoding, sub-URL deployments, duplicate path separators, and malformed paths.
  ```go
  if forgedProxyResponse.Code < 400 || workersStarted != 0 {
      t.Fatal("untrusted forwarding headers bypassed the TLS boundary")
  }
  ```
- [ ] **2. Red:** `go test ./modules/hosting ./routers/hg -run TestHosting -count=1`.
- [ ] **3. Wire the composition root.** Startup validates Mercurial, creates/loads a persistent Ed25519 host key with private permissions/ACLs, reconciles lifecycle states, starts control listener, then public services. Add configurable SSH address/advertised port (default 2222), hostname and trusted proxy CIDRs; minimum public TLS 1.2. Reserve `hg` in account/org name validation. Authentication runs before bridge/worker creation; transport uses Basic credentials/tokens only, not browser session auth, while ordinary forms keep CSRF protection. The hook authorizer calls both `ValidateRepositoryCredential` and `RepositoryAccessFor` on every veto-capable writer hook, checks ready state, and cannot elevate the original grant. The service tracks live sessions by user/key/token/repository and terminates them on revocation, visibility, or ownership changes; this is session cleanup, not write authorization. Register `/hg/:owner/:repo` before broad repo routes and strip only the known app sub-URL. Remove old Git HTTP catchall, startup Git checks, and forced-command entrypoints from live routing now. Keep old unused functions only until Task 13 removes them.
- [ ] **4. Green:** repeat and tagged full-host transport smoke tests. Verify restart preserves SSH host fingerprint; missing/unsupported hg, invalid config, or host-key failure does not leave partial listeners/workers. Shutdown drains then cancels; repository deletion uses Service.DrainRepository.
- [ ] **5. Checkpoint:** `feat: connect portable Mercurial hosting to Gogs`.

### Task 12: Repository UI and issues work with empty Mercurial repositories

**Files:** Modify `modules/middleware/{context.go,repo.go}`, `routers/repo/{repo.go,view.go,branch.go,commit.go,download.go,setting.go}`, `modules/auth/repo_form.go`, `modules/base/template.go`, `cmd/web.go`; templates `repo/{create,bare,home,view_list,view_file,commits,commits_table,diff,branch,header,nav,toolbar,sidebar,sidebar_mini}.tmpl`, `repo/settings/{options,nav}.tmpl`; `conf/locale/locale_en-US.ini`. Create `routers/repo/hg_views_test.go`.

**Interfaces:** Replace `ctx.Repo.GitRepo/Commit/Tag` with explicit Hg changeset/selection values from Tasks 9–10. Keep `RepoAssignment` for ready repository lookup/read access independent of changesets. `RepoRef` resolves nodes only on browsing routes. New view URLs: `/src/<node>/<path...>`, `/raw/<node>/<path...>`, `/commits/<path...>?cursor=<encoded>`, `/commit/<node>?parent=0|1`, with paths as percent-encoded segments; a non-UTF-8 path uses `?path=<base64url>` instead of segments; named selections use `?ref_kind=branch|bookmark|tag&ref_name=...` and redirect to canonical node URLs. All URLs include the existing owner/repo and app sub-URL prefixes.

- [ ] **0. Decide the tree view's "last commit" column.** `routers/repo/view.go` currently calls `GetCommitOfRelPath` once per directory entry; ported literally that is one `hg` process per file. Pick one, record the choice in this plan: (a) a single history query over the directory computing each entry's latest changeset, (b) one `hg serve --cmdserver pipe` process per page request serving all of its commands, or (c) drop the column. Add `TestTreeViewBoundedInvocations` asserting the number of Mercurial invocations for a 200-entry directory is a small constant.
- [ ] **1. Add `TestEmptyRepositoryStillHasIssues`, `TestMercurialViewSelection`, `TestFileRenderingCannotExecuteMarkup`, `TestRawPathIdentity`, `TestIssueWorkflowSurvivesMercurialCutover`, `TestPrivateIssueAttachments`.** The last two move here from Task 13 so issue behavior is proven at UI cutover; they cover create/list/comment/label/assign/milestone/close/reopen, rename/transfer keeping links, private issue/attachment denial for unrelated users, and notification enqueueing with fake mail delivery. Assert empty repository renders clone commands, issue create/comment works without invoking hg history, branch/bookmark/tag collisions remain distinct, slash-containing refs resolve, malformed nodes/cursors return bounded errors. Render script-like source/README safely, show binary/oversized notices, and stream raw file with `nosniff` and safe disposition. Case/non-UTF-8 path tokens must round-trip or show the declared encoding error.
  ```go
  if issueResponse.Code != 200 || historyCalls != 0 || !strings.Contains(emptyPage, "hg clone") {
      t.Fatal("empty repository broke issues or lacked Mercurial instructions")
  }
  ```
- [ ] **2. Red:** `go test ./routers/repo -run 'TestEmptyRepository|TestMercurialView|TestFileRendering|TestRawPath|TestTreeView|TestIssueWorkflow|TestPrivateIssue' -count=1`.
- [ ] **3. Adapt handlers and templates.** Creation uses Task 8, no README/license/gitignore commit controls. Build HTTPS and SSH clone links from configured public addresses. Show all heads, branch closed state, parent links, and history/diff bounds. Resolve private access before content and template rendering. Remove assumptions about `master`, Git committers, refs, and tree object IDs. File reads use only Task 9. Keep issue/attachment authorization and existing sanitizers; every raw/history/diff route uses shared authorization.
- [ ] **4. Green:** repeat tests and inspect rendered empty/populated/merge/binary/private pages in the browser with a local test database. Record page latency for the repository home and a 200-entry directory on Windows and one Unix platform. Verify keyboard navigation and links, including app sub-URL. Existing account/issue styling stays intact; no new UI framework.
- [ ] **5. Checkpoint:** `feat: browse Mercurial repositories in the Gogs UI`.

### Task 13: Remove remaining Git features and preserve account/issue behavior

**Files:** Remove `modules/git/`, `models/git_diff.go`, `cmd/{serve,update}.go`, `routers/repo/http.go`, `conf/content/git-bare.zip`, and Git-only templates after all consumers are converted. Modify `models/{repo,models,action,update,release,webhook}.go`, `routers/repo/{repo,setting,release,pull,download}.go`, `routers/api/v1/{repo,repo_file,repo_hooks}.go`, `routers/{org/setting,admin/admin}.go`, `modules/cron/manager.go`, `modules/setting/setting.go`, `modules/base/{base,template}.go`, `cmd/web.go`, `gogs.go`, relevant navigation/settings templates, `conf/app.ini` and all `conf/locale/locale_*.ini`. Delete whole files only when no retained functions need them. Add `tests/integration/absence_test.go` and `docs/mercurial/removed-features.md`. This task is deletion only; issue regression tests already live in Task 12 and must keep passing here.

**Interfaces:** Remove unsupported route/job registrations and fresh-schema tables (`Mirror`, `Release`, `UpdateTask`, push webhook tasks) after references are gone. Retained repository JSON contains Mercurial clone URLs and no false Git semantics. No Git-compatible API alias. Preserve existing account/organization/issue APIs.

- [ ] **1. Add `TestGitAndDeferredRoutesAbsent`.** Assert Git upload/receive endpoints, archive/fork/mirror/migration/release/pull/hook endpoints return 404/405; even authorized users cannot reach them.
  ```go
  if response.Code != 404 && response.Code != 405 { t.Fatal("removed endpoint is still reachable") }
  ```
- [ ] **2. Red:** `go test -tags integration ./tests/integration -run 'TestGitAndDeferred' -count=1`; also rerun the Task 12 issue tests, which must stay green throughout removal.
- [ ] **3. Remove unreachable Git code and dependent features.** Replace Git version parsing with a small neutral version utility if still needed; remove `IsBare` in favor of empty-repository results, stale mirror/release context, Git maintenance/admin actions, hook editing, global Git config writes, and push auto-close/webhook calls. Audit org/user deletion and rename flows, search/API responses, mail/feed formatting, all locales, and deployment scripts. Keep generic issue notifications and account functions. For untranslated new copy use accurate English fallback; do not leave localized Git clone instructions visible. Document feature removals.
- [ ] **4. Green:** repeat integration command; run `go test ./...`, `go build ./...`, then search executable Git invocations/imports/settings/routes with `rg`. Review matches individually: historical/license/module-host strings and the fact that `hg diff --git` names a patch format are not Git executable support. A runtime smoke test uses a PATH without Git or SSH server utilities; startup, create, push, browse, and issues must work.
- [ ] **5. Checkpoint:** `refactor: remove Git hosting and deferred repository features`.

### Task 14: Full application fault, permission, and database acceptance matrix

**Files:** Create/extend `tests/integration/{hosting_test.go,lifecycle_test.go,permissions_test.go,database_test.go,shutdown_test.go,testutil_test.go}`; extend `.github/workflows/mercurial.yml`; fix only failures in owning components.

**Interfaces:** Test-only host fixture starts the real configured application with temp database/root/assets, TLS certificate, and SSH listener; provisions users/teams/keys via application/model setup, drives native clients and actual HTTP routes, and always reaps its process tree. Do not substitute fake authorization for acceptance tests.

- [ ] **1. Add `TestHostingAcceptance`, `TestLifecycleCrashRecovery`, `TestPermissionChangesDuringPush`, `TestShutdownUnderLoad`.** Exercise both transports for owner/writer/reader/unrelated/anonymous where applicable, across the server {5.0.2, 7.2.4} × client {2.0.2, 7.2.4} matrix; concurrent different principals; visibility/ownership/team/key/token changes; blocked callback and worker saturation; crash before/after repository init/move/ready and during deletion; stale capabilities after restart; disconnect after a real committed push. Verify denial leaves node/bookmark/phase snapshots unchanged, accepted pushes survive restart, and old credentials cannot open new sessions.
  ```go
  if revokedPushSucceeded || !reflect.DeepEqual(before, afterDenied) || liveWorkersAfterShutdown != 0 {
      t.Fatal("revocation, transaction rollback, or worker cleanup failed")
  }
  ```
- [ ] **2. Red:** `go test -tags integration ./tests/integration -run 'TestHostingAcceptance|TestLifecycleCrash|TestPermissionChanges|TestShutdown' -count=1`; isolate and record real failing cases.
- [ ] **3. Repair gaps and add database coverage.** Run schema/create/rename/transfer/issue/delete tests against temporary SQLite plus isolated MySQL and PostgreSQL databases in Linux CI. Avoid production connection strings. Hooks querying permissions must not deadlock on locks held by their parent transaction. Restore any retained account/login behavior exposed by these tests rather than narrowing requirements silently.
- [ ] **4. Green acceptance:** `go test -tags integration ./... -count=1`; `go test -race ./modules/hg ./modules/hgbridge ./modules/ssh ./modules/hosting ./models`; `go vet ./...`; `go build ./...`. Run native integration jobs on all three OS families, with race tests on supported native toolchains. Inspect UI pages from Task 12 after integration fixes. No unexpected skips; report unavailable runners/databases explicitly. Record basic latency/process-count/representative clone and push measurements, plus repository page latency (home, large directory, history, diff), without claiming a scalability target.
- [ ] **5. Checkpoint:** `test: validate Mercurial hosting and preserved Gogs workflows`.

### Task 15: Portable packages, operator documentation, and release handoff

**Files:** Create `docs/mercurial/{installation.md,operations.md,compatibility.md}`, `scripts/package.go`. Modify `README.md`, `README_ZH.md`, `conf/app.ini`, `.github/workflows/mercurial.yml`, `docker/{README.md,blocks/docker_gogs/Dockerfile,blocks/docker_gogs_dev/Dockerfile,templates/init_gogs.sh.tpl}`, and retained `scripts/`/`packager/` entrypoints that advertise Git/forced-command dependencies. Retire unsupported legacy packaging explicitly rather than pretending it is verified.

**Interfaces:** Package script is a build-only Go command, `go run scripts/package.go -binary <native binary> -out <archive path>`; place a build-ignore constraint on it so application package builds exclude it. It bundles assets/config/license with the binary; it does not download/install Mercurial or run shell scripts. No new runtime API.

- [ ] **1. Define package smoke acceptance.** Extract archive into a path with spaces/Unicode on each native OS, configure tested Mercurial, start directly, create repo, clone/push/pull over both transports, browse and use issues, stop and restart. The run must succeed without Git, sshd, or ssh-keygen installed on server PATH; external native client SSH is a test-client dependency only.
- [ ] **2. Write packaging and documentation.** Document the version policy (server ≥ 5.0, clients ≥ 2.0), which combinations were tested on which platforms, the Python 2.7 end-of-life caveat for 5.x servers with a recommendation to run a current release, exact validated versions/architectures, Mercurial installation path/runtime, host-key persistence, TLS and trusted-proxy configuration, limits, trusted-host loopback assumption, unsupported features, errors, and process cleanup. Operator docs must state the accepted stream-bundle limitation (user decision 2026-10-26): a stream-clone bundle unbundled into an empty repository by a writer whose access is revoked between transaction open and close is not undone; see `docs/mercurial/transport-evidence.md`. Backups stop writes/drain and capture DB + repo root + host keys + config together; give a tested restore procedure. State plainly that `.hg` is storage and only `/hg/<owner>/<repo>` is public transport. Keep source license/attribution.
- [ ] **3. Verify package and recovery.** Run package smoke checks on all native targets, then back up/restore to a new test directory and confirm repository nodes, issues, permissions, and SSH host fingerprint match. Test missing hg and incompatible version diagnostics. Startup must not mutate operator-wide Git/Mercurial settings.
- [ ] **4. Produce release evidence.** Complete `compatibility.md` with actual commands, exact versions, matrix outcomes, accepted limits, and any unverified combinations. Re-run the Task 14 acceptance commands only if packaging/code changes affect their behavior. Run a final source/runtime Git-removal audit. Whole-change review checks this plan and approved design before release; no publishing/deployment is part of this planning request.
- [ ] **5. Checkpoint:** `docs: package and document portable Mercurial hosting`.

## Coverage and handoff

| Approved design requirement | Owning tasks |
| --- | --- |
| Reproducible build, native portability, tested versions | 1, 2, 6, 15 |
| Fresh empty repositories, stable ID paths, lifecycle/recovery | 8, 11, 14 |
| Users, teams, keys/tokens, read/write rules | 3, 7, 11, 14 |
| HTTPS/SSH wire serving, TLS, revocation, strict execution | 3–6, 11, 14 |
| Files, refs, heads, history, diffs, empty-repo UI | 9, 10, 12 |
| Issues, attachments, notifications, account regressions | 1, 7, 12–14 |
| Git/deferred feature removal, accurate copy/API behavior | 11, 13, 15 |
| Bounds, process shutdown, failures, backups | 2–6, 8, 14, 15 |

Self-review completed at plan handoff: checked existing modification targets (including organization/team methods in `models/org.go`), corrected streaming versus bounded runner use, added credential IDs for in-flight token revocation, passed hosting configuration explicitly, and mapped every review-focus condition to named tests. No production code, dependencies, or transport experiments were created by writing this plan. Implementation validation remains outstanding.

Revision 2026-10-25 (after review, at the user's request): server ≥ 5.0 / client ≥ 2.0 version policy and matrix; ID-keyed access table; static deny hooks for readers with capabilities only for writers; session termination moved to the hosting service; reduced runner configuration; readable path URLs; bounded Mercurial invocations per page with a tree-view decision step; simplified history cursor; obsolescence-marker exchange disabled; Windows SSH quoting and shell-executed hook quoting; Task 1 timebox and pure-Go SQLite spike; issue regression tests moved from Task 13 to Task 12.

Recommended execution: **subagent-driven, one task at a time**, with independent review of the authorization/transport tasks. This is a larger, tightly integrated change where accepting a false read-only guarantee could expose or alter private repositories. Native execution by the current agent remains an option; choose the method after reviewing the plan.

## Sources used for planning choices

- [Go release history](https://go.dev/doc/devel/release) and [downloads](https://go.dev/dl/) for the Go 1.27.3 toolchain selection.
- [Mercurial 7.2 release notes](https://mercurial-scm.org/relnotes/7.2) and [7.2.4 distribution files](https://pypi.org/project/mercurial/7.2.4/) for the newest tested Mercurial target and available platform distributions.
- [Mercurial 5.0 distribution files](https://pypi.org/project/mercurial/5.0.2/) and [2.0.2 distribution files](https://pypi.org/project/mercurial/2.0.2/) for the oldest supported server and client; hook and template availability in 5.0 must be confirmed from its sources in Task 3/6.
- Mercurial protocol/hook references in the approved design; implementation must verify them against the pinned version, not assume the installed 7.0.3 proves them.

