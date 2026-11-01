# Mercurial Reads Through hgqlserve Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Move every `modules/hg` read outside a write flow from `hg` processes to a managed `hgqlserve` child, with no caller changes.

**Architecture:** `ReadServer` owns the child process and `ReadClient` speaks its HTTP API. `Runner` keeps its method signatures, and each read method calls `ReadClient` instead of `hg`. `hosting.Service` starts the server before anything reads.

**Tech Stack:** Go standard library `net/http`, `hgqlserve` from Mononoke, Mercurial as the test oracle.

**Spec:** `docs/superpowers/specs/2026-11-01-hg-graphql-reads-design.md`. API reference: `docs/graphql.md` in the Mononoke repository.

## Global Constraints

- `Runner` method signatures do not change. `routers/` does not change.
- No `hg` fallback for reads. A read comes from the service or fails with `ErrReadService`.
- `hgqlserve` binds `127.0.0.1:0` only. The token is random per start, in a `0600` file beside `ConfigPath`.
- Hosting startup refuses a common parent that is a filesystem or volume root, or stores on different drives.
- `CheckPath` and `reviewPath` stay in Go and run before any request.
- Reads inside write flows stay on `hg`: `PrepareReviewMerge`'s manifest, `SnapshotReview` and `SnapshotMergeReview`'s `log` checks, `CloneVisible`'s tags and `verify`, `RecoverReviewPublication`'s `debuglocks`, and `Runner.Check`.
- Windows, macOS and Linux all stay supported.
- Tests needing the service use `GOGS_TEST_HGQLSERVE`, an absolute binary path, and call `t.Skip` when it is unset. Build it from Mononoke `server-runtime-stalls` with its pending lingering-close fix committed.
- Commit messages are short, start with the subsystem, and have no trailers.
- Work on branch `hg-graphql-reads`.

## Review Focus

1. `hgqlserve` crashes and restarts on a new port with a new token: the next read works without any Hgs restart. Tested in Task 2.
2. `HGQLSERVE` is missing or not executable: startup fails with an error naming that key, not a read failure on the first page. Tested in Task 2.
3. The service dies partway through `/raw`: `ReadFile` returns an error, not a silently short file. Tested in Task 5.
4. A file path holds spaces, `%`, `&`, `+` or non-ASCII: `/raw` still reads that file. Tested in Task 5.
5. The stores' paths differ only by case on Windows, or by a symlink: the relative path still resolves inside the root. Tested in Task 2.

---

### Task 1: `ReadClient`

**Files:**
- Create: `modules/hg/readclient.go`, `modules/hg/readclient_test.go`
- Modify: `modules/hg/types.go`

**Interfaces:**
- Produces:
  - `var ErrReadService = errors.New("hg: read service unavailable")`
  - `type ReadClient struct` with `NewReadClient(root string) *ReadClient`, `SetEndpoint(addr, token string)`, safe for concurrent use with reads.
  - `func (c *ReadClient) relative(path string) (string, error)`: an absolute path to a slash path under `root`.
  - `func (c *ReadClient) Query(ctx context.Context, operation, query string, variables map[string]interface{}, out interface{}) error`
  - `func (c *ReadClient) Stream(ctx context.Context, endpoint string, params url.Values, w io.Writer) (truncated bool, err error)`
  - `func (c *ReadClient) Close(ctx context.Context, path string) error`

- [ ] **Step 1: Write failing tests** against `httptest.Server`. `TestQueryDecodesData`. `TestQuerySendsBearerAndOperationName`. `TestErrorCodes`: `NOT_FOUND`, `EMPTY_REPOSITORY` and `INVALID_CURSOR` in `errors[0].extensions.code` give `ErrNotFound`, `ErrEmptyRepository` and `ErrInvalidCursor`, and `INTERNAL`, an unknown code, a 500 and a refused connection give `ErrReadService`. `TestStreamErrorBody`: a 404 JSON body gives `ErrNotFound` and writes nothing. `TestStreamTruncated`: `X-Truncated: true` returns `truncated == true`. `TestRelative`: inside root gives a slash path; outside gives an error. `TestCloseRefusedConnectionIsReleased`: a refused connection returns nil.
- [ ] **Step 2: Run.** `go test ./modules/hg -run 'Query|ErrorCodes|Stream|Relative|Close'`. Expected: FAIL, undefined.
- [ ] **Step 3: Implement.** `http.Transport` with `DisableKeepAlives: true`, because the server closes each connection. Keep address and token behind a `sync.RWMutex`. The context's deadline bounds every request. A context error is returned as itself, not `ErrReadService`.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** as `hg: add the read service client`.

### Task 2: `ReadServer`, root and hosting

**Files:**
- Create: `modules/hg/readserver.go`, `modules/hg/readserver_test.go`
- Modify: `modules/hg/config.go`, `modules/hg/runner.go`, `modules/hg/process_*.go` only if `processTree` needs export within the package, `modules/setting/setting.go:355`, `conf/app.ini:271-286`, `modules/hosting/service.go:115-150,251-285`

**Interfaces:**
- Consumes: `ReadClient`.
- Produces:
  - `Config.Hgqlserve string`, the absolute binary path, from `[mercurial] HGQLSERVE`, resolved the same way as `EXECUTABLE`.
  - `func CommonRoot(a, b string) (string, error)`, with `ErrNoCommonRoot`.
  - `type ReadServer` with `StartReadServer(ctx context.Context, binary, root, tokenDir string, client *ReadClient) (*ReadServer, error)` and `Stop(ctx context.Context) error`.
  - `func (r *Runner) SetReads(c *ReadClient)`, and field `reads *ReadClient`.
  - `func (r *Runner) Release(ctx context.Context, path string) error`, calling `Close`.

- [ ] **Step 1: Write failing tests.** `TestCommonRoot`: `/srv/hg/repos` and `/srv/hg/review` give `/srv/hg`; `/srv/hg` and `/opt/x` give `ErrNoCommonRoot`; on Windows `C:\a\b` and `D:\a\b` give `ErrNoCommonRoot`, and `C:\Hg\A` and `c:\hg\B` give the same parent. `TestCommonRootFollowsSymlinks`: a symlinked store resolves through `filepath.EvalSymlinks` first, skipped where links are refused. `TestStartMissingBinary`: the error contains `HGQLSERVE`. `TestReadsWaitForReady`: `StartReadServer` returns only after a query succeeds, bounded by `StartupTimeout`. `TestRestartAfterExit`: kill the child, then a query through the client succeeds within 10 seconds on the new address. `TestStopEndsChild`: no restart follows `Stop`. `TestTokenFileMode`: mode is `0600` on Unix.
- [ ] **Step 2: Run.** `go test ./modules/hg -run 'CommonRoot|Start|Reads|Restart|Stop|Token'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Start through `startTree` with `exec.Command(binary, "--root", root, "-l", "127.0.0.1:0", "--token-file", file)`. Read stdout until `Listening on `, then `client.SetEndpoint`, and keep draining stdout. Readiness runs `{ __typename }` through `client.Query`. Restart backoff doubles from 100 ms to 5 s, and resets after 30 seconds of uptime. Each restart writes a new token.
- [ ] **Step 4: Wire `hosting.New`.** After `s.runner.Check`, before `reconcileRevokedPublicationsAtStartup`: `CommonRoot(cfg.RepositoryRoot, cfg.ReviewArtifactRoot)`, `NewReadClient`, `StartReadServer`, then `SetReads` on `runner` and `inspector`. `Shutdown` stops it after sessions end. Add `HGQLSERVE = hgqlserve` with a comment to `conf/app.ini`.
- [ ] **Step 5: Update hosting tests** that build a `Service` to set `Hgqlserve` from `GOGS_TEST_HGQLSERVE`, skipping when unset.
- [ ] **Step 6: Run.** `go test ./modules/hg ./modules/hosting ./modules/setting`. Expected: PASS.
- [ ] **Step 7: Commit** as `hg: run hgqlserve as a managed child`.

### Task 3: Changesets and references

**Files:**
- Create: `modules/hg/reads_test.go`, `cmd/hg_readfake_test.go`
- Modify: `modules/hg/history.go:59-110`, `modules/hg/refs.go`, `modules/hg/review_snapshot.go:85-94,157-180`, tests that used `fakeViewHg` for these commands

**Interfaces:**
- Consumes: `Runner.reads`, `Query`.
- Produces:
  - `const changesetSelection`, the GraphQL fields of a changeset, and `func (g gqlChangeset) changeset() (Changeset, error)`, its decoder.
  - `func (r *Runner) readChangeset(ctx context.Context, path string, node NodeID) (Changeset, error)`, used by `Changeset` and `reviewChangeset`.
  - `func (r *Runner) readRefs(ctx, path) ([]Reference, error)`, used by `References`, `reviewReferences` and `Resolve`.
  - Test helper in `reads_test.go`: `newReadRunner(t) *Runner`, a real `hg` runner plus a real `ReadServer` over `t.TempDir()`.
  - Test helper `fakeReadAPI(t, handlers map[string]string) *hg.ReadClient` in `cmd/hg_readfake_test.go`: an `httptest.Server` answering canned JSON by `operationName`, which replaces read commands in `fakeViewHg`.

- [ ] **Step 1: Write failing tests** comparing with the old commands, kept in test code as constants: `TestReferencesMatchHg`, using `branchHeadsTemplate` and `referencesTemplate` on a repo with closed, secret and bookmarked heads and a pruned changeset. `TestChangesetMatchesHg`, comparing node, ordered parents, user, message, branch, date and revision. `TestSecretNodeNotFound`. `TestNonUTF8RefUnsupported`: a raw byte bookmark gives `ErrUnsupportedEncoding`. `TestPushVisibleOnNextRead`: `hg commit` then `References` shows it.
- [ ] **Step 2: Run.** `go test ./modules/hg -run 'References|Changeset|Secret|NonUTF8|Push'`. Expected: FAIL.
- [ ] **Step 3: Implement.** Query `refs { kind name nameBase64 node revision closed }`, in newest-first order. Invalid UTF-8 in decoded `nameBase64` returns `ErrUnsupportedEncoding`, matching `parseReferences`. `served` becomes a `changeset(id) { id }` query. `Date` is `time.Unix(time, 0).In(time.FixedZone("", -tz))`, the sign checked against `TestChangesetMatchesHg`. Remove templates and parsers this leaves unused outside tests, moving `branchHeadsTemplate` and `referencesTemplate` to test code only if the write flows do not use them.
- [ ] **Step 4: Convert `cmd` view tests** for these reads to `fakeReadAPI`.
- [ ] **Step 5: Run.** `go test ./modules/hg ./cmd`. Expected: PASS.
- [ ] **Step 6: Commit** as `hg: read changesets and references from the service`.

### Task 4: History

**Files:**
- Modify: `modules/hg/history.go:124-289`, `modules/hg/reads_test.go`, `modules/hg/history_integration_test.go`

**Interfaces:**
- Consumes: `changesetSelection`, `gqlChangeset`.
- Produces: `History` over `history(snapshot, snapshotRevision, before, path, limit)`.

- [ ] **Step 1: Write failing tests.** `TestHistoryPagesMatchHg`: walk 250 changesets at limit 30, comparing with `sort((0:N) and not secret(), -rev)`. `TestFileHistoryFollowsRenames`, compared with `log --follow`. `TestHistoryDeletedFileWithAnchor`. `TestHistoryCursorErrors`: each case in the existing `History` validation returns `ErrInvalidCursor`. `TestHistoryEmptyPathOmitted`: `Path: ""` sends no `path` variable. `TestHistoryNonUTF8PathUnsupported`: gives `ErrUnsupportedEncoding`. `TestHistoryEmpty`: gives `ErrEmptyRepository`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Keep the Go validation that runs before the query. Replace the pattern files and `historyWriter` with the query, and remove them.
- [ ] **Step 4: Run.** `go test ./modules/hg`. Expected: PASS.
- [ ] **Step 5: Commit** as `hg: read history from the service`.

### Task 5: Trees and file bytes

**Files:**
- Modify: `modules/hg/files.go`, `modules/hg/reads_test.go`, `modules/hg/files_test.go`, `modules/hg/browse_integration_test.go`

**Interfaces:**
- Consumes: `Query`, `Stream`.
- Produces: `ListDirectory`, `ListPath` over `tree`, and `ReadFile` over `/raw`.

- [ ] **Step 1: Write failing tests.** `TestListDirectoryMatchesHg` and `TestListDirectoryTreeManifest`, against `manifest -v`. `TestListPathFile`, `TestListPathUnknownNotFound`. `TestTreeNonUTF8NameExact`: a listed name equals the stored bytes. `TestReadFileBinaryMatchesCat`. `TestReadFilePathsNeedingEscape`: names `a b.txt`, `100%.txt`, `a&b+c.txt` and `naïve.txt` read back. `TestReadFileServiceDiesMidBody`: an `httptest` server declaring more bytes than it sends gives an error wrapping `ErrReadService`. `TestReadFileSecretNotFound`.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Query `tree(changeset, path) { directory entries { name nameBase64 directory executable symlink } }`, using `nameBase64` for `TreeEntry.Name`. Build `/raw` parameters with `url.Values`. Remove `flatScan`, the manifest walkers and `debugdata` code unless `PrepareReviewMerge` still uses them; keep `usesTreeManifest` only if used.
- [ ] **Step 4: Run.** `go test ./modules/hg ./cmd`. Expected: PASS.
- [ ] **Step 5: Commit** as `hg: read trees and file bytes from the service`.

### Task 6: Diffs

**Files:**
- Modify: `modules/hg/diff.go`, `modules/hg/diff_test.go`, `modules/hg/browse_integration_test.go`, `modules/hg/reads_test.go`

**Interfaces:**
- Consumes: `Stream`, `newDiffWriter`.
- Produces: `Diff` over `/diff?changeset=&parent=`.

- [ ] **Step 1: Write failing tests.** `TestDiffHeadersMatchHg`: `diff --git`, rename, copy and mode lines equal `hg diff --git --nodates --no-binary`. `TestDiffAppliesToSameTree`: apply with `hg import --bypass`, compare trees. `TestDiffOverLimitTruncated`: over `MaxDiffBytes` gives `Truncated: true`. `TestDiffServerTruncated`: `X-Truncated: true` gives `Truncated: true`. `TestDiffRootAgainstNull`. Change existing tests that assert exact hunks to these checks.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Keep the parent index validation. `Stream` into `diffWriter`, treating `ErrOutputLimit` as today.
- [ ] **Step 4: Run.** `go test ./modules/hg ./cmd`. Expected: PASS.
- [ ] **Step 5: Commit** as `hg: read diffs from the service`.

### Task 7: Review reads

**Files:**
- Modify: `modules/hg/review_compare.go`, `modules/hg/review_publish.go:27-90,234-369`, `modules/hg/review_snapshot.go:96-155,262-276`, `modules/hg/review_integration_test.go`, `modules/hg/review_publish_test.go`, `modules/hg/review_snapshot_test.go`

**Interfaces:**
- Consumes: `readChangeset`, `readRefs`, `Stream`.
- Produces: `reviewBases` over `commonHeads`, `CompareReview` over `changesetsBetween` and `/diff?from=&to=`.

- [ ] **Step 1: Write failing tests.** `TestCompareReviewMatchesHg`: changesets equal the old revset, and the diff passes the header and apply checks of Task 6. `TestCompareTooLarge`: gives `ErrComparisonTooLarge`. `TestCompareNoChanges`: gives `ErrNoChanges`. `TestReviewBasesCrissCross`: two bases give `ErrAmbiguousHead`. `TestResolveReviewAmbiguous`. `TestInspectEndpointSeesPublish`: a read straight after `PublishReview` sees the imported node. `TestReadsNeedNoCapabilityProbe`: `ResolveReview` runs no `hg` process. Keep `loadReviewMetadata`, `checkReviewJournal` and `reviewPath` checks as they are.
- [ ] **Step 2: Run.** Expected: FAIL.
- [ ] **Step 3: Implement.** Replace `reviewRun` read calls with the service, and remove `reviewCapability` and `publicationCapability` calls from these read methods only. `reviewCapability` remains for snapshot and merge. `reviewBases` is shared with snapshot creation, so its snapshot-path caller must still work: if it runs on a staging store outside the served state, keep a separate `hg` read there and note it in the commit.
- [ ] **Step 4: Run.** `go test ./modules/hg ./modules/collaboration ./modules/hosting`. Expected: PASS.
- [ ] **Step 5: Commit** as `hg: read review state from the service`.

### Task 8: Releasing repositories

**Files:**
- Modify: `modules/hosting/service.go:248-250`, `modules/collaboration/comparison_artifacts.go:237`, `modules/collaboration/merge_artifacts.go:103`, their callers and tests

**Interfaces:**
- Consumes: `Runner.Release`.
- Produces: `/close` before each removal. `models/` does not change, because `DeleteHgRepository` already calls the `DrainRepository` hook before storage is touched.

The fork and provisioning renames in `models` move staging stores the service never opened, so they need no `/close`.

- [ ] **Step 1: Write failing tests.** `TestDeleteAfterRead`: read a repo through the service, then `DeleteHgRepository` succeeds, on Windows too. `TestDrainFailsWhenCloseFails`: a `/close` 500 stops the delete, with the row kept. `TestComparisonArtifactRemovedAfterRead`: `CompareReview`, then remove the artifact. `TestPreparedMergeRemovedAfterRead`.
- [ ] **Step 2: Run.** `go test ./modules/hosting ./modules/collaboration`. Expected: FAIL on Windows, and on the close assertion elsewhere.
- [ ] **Step 3: Implement.** `Service.DrainRepository` calls `s.runner.Release` with the repository's storage path after sessions drain. Pass a `func(context.Context, string) error` release into the two artifact removal functions from their callers, which hold the host.
- [ ] **Step 4: Run.** Expected: PASS.
- [ ] **Step 5: Commit** as `hosting: release repositories before removing their storage`.

### Task 9: Proof and documentation

**Files:**
- Create: `modules/hg/noreadprocess_test.go`
- Modify: `CLAUDE.md`, `docs/mercurial/operations.md`, `docs/mercurial/installation.md`, `docs/mercurial/operator-notes.md`, `README.md` if it lists runtime dependencies

**Interfaces:**
- Consumes: every read method.

- [ ] **Step 1: Write `TestReadsStartNoProcess`.** A `Runner` whose `Executable` is a test binary that fails and records any invocation calls `Changeset`, `History`, `ReadFile`, `ListDirectory`, `ListPath`, `References`, `Landing`, `Resolve`, `Diff`, `CompareReview`, `ResolveReview`, `InspectReviewEndpoint`, `ReviewPublicationState` and `ValidateReviewPublication`, and asserts no invocation.
- [ ] **Step 2: Run.** `go test ./modules/hg -run TestReadsStartNoProcess`. Expected: PASS. A failure is a read Tasks 3-7 missed: fix it in the owning file.
- [ ] **Step 3: Update docs.** `CLAUDE.md`: Mercurial child processes now cover writes and the wire protocol, and reads go to `hgqlserve`. `docs/mercurial/`: `HGQLSERVE`, how to install the binary, the root rule and what to do when startup refuses it, `ErrReadService` behaviour, and the `/raw` memory note.
- [ ] **Step 4: Run all tests on Windows and Linux.** Run: `GOGS_TEST_HGQLSERVE=<path> go test ./...`. Expected: PASS. Linux needs a host with Go, `hg` and a Linux `hgqlserve`; if none exists, report it, and do not install anything.
- [ ] **Step 5: Commit** as `docs: describe reads through hgqlserve`.
