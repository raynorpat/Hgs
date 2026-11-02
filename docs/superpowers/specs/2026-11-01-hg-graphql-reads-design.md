# Mercurial reads through hgqlserve

## Goal

Every read that `modules/hg` makes outside a write flow goes to the Mononoke `hgqlserve` service instead of starting an `hg` process. `routers/`, `models/` and other callers do not change.

This is sub-project 3 of the hg removal roadmap in the Mononoke repository, `docs/superpowers/specs/2026-10-31-graphql-read-api-design.md`. That spec defines the API this one consumes.

This spec changes a recorded decision. `CLAUDE.md` fixes "Mercurial runs as managed child processes"; after this work that holds only for writes and the wire protocol, and `CLAUDE.md` must say so.

## Constraints

- `Runner` method signatures stay as they are.
- No `hg` fallback for reads: a read either comes from the service or fails.
- Repository and review storage do not move on an install that passes the root check.
- `hg serve`, writes and write-flow reads stay on `hg`.
- Windows, macOS and Linux all stay supported. `hgqlserve` runs only on loopback.

## Root

`hgqlserve` takes one `--root`. Hgs computes the deepest common parent of the repository root and `REVIEW_ARTIFACT_ROOT` and passes it.

Hosting startup fails with a message telling the operator to move one store if:

- the common parent is a filesystem or volume root, or
- the two stores are on different drives, so no common parent exists.

One token reaches both stores. Only Hgs holds it, and the service listens only on loopback. `validateComparisonRoot` keeps its existing rules, because it compares the two stores with each other and the shared parent does not change that.

## Components

### `modules/hg/readserver.go`

Starts `hgqlserve --root DIR -l 127.0.0.1:0 --token-file FILE`, reads the `Listening on` line for the address, restarts it with backoff if it exits, and stops it with the hosting service. It reuses the existing per-platform process-tree code. Hosting startup waits for a successful `/gql` query before serving. This wait is bounded by `StartupTimeout`.

The token is random, generated at each start, and written with mode `0600` beside `ConfigPath`.

### `modules/hg/readclient.go`

A small HTTP client for `/gql`, `/raw`, `/diff` and `/close`, using bearer auth. It maps `extensions.code`:

| Code | Go error |
|---|---|
| `NOT_FOUND` | `ErrNotFound` |
| `EMPTY_REPOSITORY` | `ErrEmptyRepository` |
| `INVALID_CURSOR` | `ErrInvalidCursor` |
| Anything else, or no response | `ErrReadService` (new) |

Each `Runner` method converts its absolute path to a path relative to the root. `CheckPath` and `reviewPath` stay in Go and run first.

### Configuration

One new key, `[mercurial] HGQLSERVE`: the absolute path of the binary.

### Wiring

`hosting.Service` owns one read server and gives its client to both `runner` and `inspector`.

## Read mapping

| `Runner` method | Service call |
|---|---|
| `Changeset`, `reviewChangeset` | `changeset(id)` |
| `History` | `history`, same cursor shape as `HistoryPage` |
| `References`, `Landing`, `reviewReferences` | `refs`, then the existing `LandingOf` |
| `Resolve` | `refs`, with the existing Go matching |
| `ListDirectory`, `ListPath` | `tree` |
| `ReadFile` | `/raw`, streamed to the writer |
| `Diff` | `/diff?changeset=&parent=` through the existing `diffWriter` |
| `CompareReview` | `changesetsBetween` and `/diff?from=&to=` |
| `ResolveReview` | Existing matching over `refs` |

`reviewCapability` and `publicationCapability` stay for the `hg` operations that need them. `reviewCapability` is no longer required before `ResolveReview` or `CompareReview`.

### Staying on `hg`

Reads inside write flows run on stores those flows are building, so they move with sub-project 5:

- the manifest read in `PrepareReviewMerge`
- the `log` checks in `SnapshotReview` and `SnapshotMergeReview`
- all reads in `CloneVisible`
- `ReviewPublicationState`, `InspectReviewEndpoint` and `ValidateReviewPublication`, which publication hooks and their preflight call
- `debuglocks` in `RecoverReviewPublication`
- startup checks in `Runner.Check`

## Repository handles

`hgqlserve` reads revlogs without mapping them, on the Mononoke branch `unmapped-reads`, which this work requires. On Windows `hg` can therefore roll back after a read, and an open repository can be deleted or renamed, so Hgs does not call `POST /close`.

## Failure

- While the service is down, reads return `ErrReadService`, and web routes show their normal error page.
- `BrowserCommandTimeout` still bounds each read through its context.
- `ErrQueueTimeout` no longer applies to reads, because they take no `MaxProcesses` slot.

## Behaviour changes

- Diff hunks can differ from `hg`'s. They apply to the same result, and file headers and copy lines are the same.
- `/raw` reads a whole file into server memory, where `hg cat` streams. Requests for large files cost more memory.
- `/diff` has a 5-second per-file limit and a 64 MiB cap. Hgs's `MaxDiffBytes` of 2 MiB still applies first, and the client stops reading there.

## Behaviour that stays

- A non-UTF-8 ref name still returns `ErrUnsupportedEncoding`, detected from `nameBase64`.
- `tree` and `history` cannot take an exact non-UTF-8 path, so such a request returns `ErrUnsupportedEncoding`. Names returned in a listing are exact, decoded from `nameBase64`.
- An empty history path is sent as no path.
- `Long` values are not used by these reads.

## Testing

- `modules/hg` integration tests run against a real binary found through `GOGS_TEST_HGQLSERVE`. They skip when it is unset, as hg-dependent tests skip without `hg`.
- Each read gets a test comparing it with the `hg` command it replaced, on repositories holding secret and pruned changesets. The old templates stay in test code as the oracle.
- A test whose fake `hg` fails on any read command proves no read starts a process. `cmd/hg_fake_test.go` keeps its fake `hg` for write paths.
- Tests asserting exact diff hunks change to comparing headers and the applied tree.
- `readserver` tests cover restart after exit, startup wait, the root refusals and stop on shutdown.
- A test proves a failed push rolls back after a read, and a repository can be deleted after a read.

## Done means

1. All Hgs tests pass on Windows and Linux.
2. No read method starts an `hg` process.
3. Pages show what they show today, apart from diff hunks.
4. `CLAUDE.md` and `docs/mercurial/` describe the new architecture, including `HGQLSERVE`, the root rule and the operator steps when the root check fails.
