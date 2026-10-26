# Build and database baseline

Established 2026-10-25 on Windows 11 (amd64).

## Toolchain
- Go 1.27.3 (`go 1.27.0`, `toolchain go1.27.3` in `go.mod`), module path `github.com/gogits/gogs`.
- `CGO_ENABLED=0`; no C toolchain is needed.
- Before this work `go build ./...` failed: there was no `go.mod` (only `.gopmfile`, GOPATH-era) and the tree did not compile with a modern toolchain.

## Baseline record
Before the module work (commit cb8e76e, the initial import, checked out in a temporary clone): no `go.mod` existed (`go env GOMOD` printed `NUL`, i.e. no module) and `go build ./...` failed immediately:

```
pattern ./...: directory prefix . does not contain main module or its selected dependencies
```

Now, in this repository:

```
$ go version
go version go1.27.3 windows/amd64
$ go env GOMOD
D:\github\gogs\go.mod
```

## Release existence (checked 2026-10-25)
- Go 1.27.3: the installed toolchain reports `go1.27.3` (`$GOROOT/VERSION`).
- Mercurial 7.2.4 and 5.0.2: listed by `pip index versions mercurial` against PyPI (7.2.4 is also the installed version in `.venv-hg`).
- Mercurial 2.0.2: **not on PyPI** (`pip download mercurial==2.0.2` found no distribution; the oldest PyPI release is 2.6.2). It exists as the upstream tarball `https://www.mercurial-scm.org/release/mercurial-2.0.2.tar.gz` (HTTP 200, sha256 `ca8b8fb93f2e3e8c3cdf2f81d87e92592f5f20c5bfcaaeb6a75550d4a69572c9`); CI installs it from there with the checksum verified.

## Dependency pins
Timebox: the legacy-pin translation was budgeted at one working session and met. The 1:1 translation needed no substitutions; the one substitution made (SQLite driver) was a deliberate choice, not a failed translation.

The `.gopmfile` entries with a commit were translated 1:1 to pseudo-versions of the same legacy commits (27 direct dependencies, all resolved). Module resolution is pinned by `go.sum`; CI runs `go mod verify`.

Substitutions and additions, each with its reason:

| Change | Version | Reason |
|---|---|---|
| `github.com/mattn/go-sqlite3` replaced by `modernc.org/sqlite` as the SQLite driver | v1.60.1 (published 2026-09-29; v1.61.0 skipped as under 14 days old) | The 2015 go-sqlite3 pin needs cgo and a C toolchain. The pure-Go driver builds identically on Windows, Linux and macOS. Registered as driver name `sqlite3` because xorm, `app.ini` and the installer use that name. |
| New transitive modules from modernc.org/sqlite | `github.com/dustin/go-humanize` v1.1.0, `github.com/google/uuid` v1.6.0, `github.com/mattn/go-isatty` v0.0.24, `github.com/ncruces/go-strftime` v1.1.0, `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec (plus the `modernc.org/*` runtime modules in `go.sum`) | Required by the driver. |
| `golang.org/x/net`, `golang.org/x/text` | v0.61.0, v0.43.0 | `.gopmfile` listed these without a commit (unpinned); pinned to maintained releases compatible with Go 1.27.3. |
| `golang.org/x/crypto` | v0.58.0 | Imported by `modules/ssh` but not pinned in `.gopmfile`; pinned to a maintained release. |
| `golang.org/x/sys` | v0.49.0 (indirect) | Required by the above; maintained version for Go 1.27.3. |
| `github.com/mattn/go-sqlite3` | legacy pin kept as `// indirect` | Kept as `// indirect` because `go mod why -m` shows only `go-xorm/core`'s test package imports it; the application does not (not compiled in). |

No other dependency was replaced, upgraded or removed.

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
