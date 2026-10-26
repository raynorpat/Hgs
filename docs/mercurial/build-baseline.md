# Build and database baseline

Established 2026-10-25 on Windows 11 (amd64).

## Toolchain
- Go 1.27.3 (`go 1.27.0`, `toolchain go1.27.3` in `go.mod`), module path `github.com/gogits/gogs`.
- `CGO_ENABLED=0`; no C toolchain is needed.
- Before this work `go build ./...` failed: there was no `go.mod` (only `.gopmfile`, GOPATH-era) and the tree did not compile with a modern toolchain.

## Dependency pins
The 27 direct dependencies in `.gopmfile` were translated 1:1 to pseudo-versions of the same legacy commits. No substitutions were needed; nothing was replaced or upgraded. Module resolution is pinned by `go.sum`; CI runs `go mod verify`.

Modern, maintained modules added or bumped (needed by Go 1.27.3 and the SQLite driver):

| Module | Version |
|---|---|
| golang.org/x/crypto | v0.58.0 |
| golang.org/x/net | v0.61.0 |
| golang.org/x/text | v0.43.0 |
| golang.org/x/sys | v0.49.0 |
| modernc.org/sqlite | v1.60.1 |

## SQLite
SQLite uses the pure-Go `modernc.org/sqlite` driver, registered as `sqlite3` in `models/models.go` because xorm opens SQLite by that name. It is compiled in by default (no `sqlite` build tag, no cgo), which keeps Windows, Linux and macOS builds identical and toolchain-free. MySQL and PostgreSQL drivers are retained.

## Build tags that remain
`cert`, `redis`, `memcache` (optional features, unchanged). New: `integration`, which enables tests needing external services (below).

## Legacy build failures fixed
1. `modules/ldap` tests failed `go vet` (compile-time vet errors in `ldap_test.go`); repaired.
2. A template typo in the issue list template was rejected by modern `html/template`; fixed.
3. Non-multipart issue and comment posts panicked on a nil `MultipartForm`; now handled.

Also: `models/update.go` used unkeyed struct literals that fail `go vet ./models`; they are now keyed. `modules/log` was excluded from git by the `log/` ignore pattern and is now tracked.

A Go installation with zero-filled files was found on this machine while bringing up the toolchain; the working 1.27.3 install is the one on PATH.

## Tests
- `models`: `newTestDatabase` (`models/testutil_test.go`) gives each test a temporary SQLite database and restores globals on cleanup; it panics if used from a parallel test. `TestFreshDatabasePreservesUsersAndIssues` and `TestPasswordVerification` (`models/baseline_test.go`) run against it.
- Needing live network, now behind `-tags integration`:
  - `modules/ldap` dials `ldap.itd.umich.edu:389` (dead; previously ~85 s of timeouts).
  - `modules/httplib` calls `httpbin.org`.
  Prerequisite: outbound access to those hosts. `go test ./...` passes offline; `go test -tags integration ./modules/ldap ./modules/httplib` is expected to fail unless they are reachable.
- `cmd/web.go` `checkVersion` was confirmed against the pinned deps (macaron 0.5.2, binding 0.0.4, cache 0.0.7, csrf 0.0.3, i18n 0.0.6, session 0.1.6, ini 1.2.0 all meet the expected minimums); no change.
- `.travis.yml` (Go 1.2-1.4) was deleted; CI is `.github/workflows/mercurial.yml`.

## CI status
`.github/workflows/mercurial.yml` runs `go mod verify`, `go build ./...`, `go test ./...` on windows-latest, ubuntu-latest and macos-latest (arm64), and installs Mercurial 7.2.4 via pip. A `legacy-hg` job installs Mercurial 5.0.2 and a 2.0.2 client in a `python:2.7` container (Linux only). **The workflow has never been executed**: there are no runners here. Only the Windows amd64 build and tests were run locally. Linux and macOS jobs are unverified. Mercurial 5.0.2 and 2.0.2 rows on Windows and macOS arm64 are unavailable (no Python 2.7), not passed.
