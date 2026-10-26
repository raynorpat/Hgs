# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state: Git → Mercurial conversion in progress

This is the Gogs 0.5.13 Beta source (Feb 2015), being converted into a **Mercurial-only** hosting service. Before changing anything structural, read:

- `docs/superpowers/specs/2026-10-25-mercurial-hosting-design.md` — approved design (scope, exclusions, security model)
- `docs/superpowers/plans/2026-10-25-mercurial-hosting.md` — 15-task implementation plan

Key decisions those documents fix, which are easy to violate by accident:

- Git is removed entirely: no Git executable at runtime, no Git wire endpoints, no dual-backend mode, no Git compatibility layer. Fresh database, no migration.
- Mercurial version policy: server ≥ 5.0 (tested 5.0.2 and 7.2.4), native clients ≥ 2.0.
- Go stays the application language; Mercurial runs as managed child processes (`hg serve` per HTTP request, `hg serve --stdio` per SSH session). No custom Python helper and no reimplementation of the wire protocol — architectural changes go back to the user for approval.
- Write authorization is enforced inside Mercurial transactions by hooks calling an internal subcommand of the same Go binary; read-only sessions get static deny hooks.
- Must stay portable across Windows, macOS, and Linux (no shell-script deployment requirement).
- Task order matters: the transport feasibility gate (Task 6) must pass before broad model/UI conversion.

The documents say the directory has no version control; that is now stale — a Git repository was initialized and an initial commit made. Use plan checkpoints as real commits.

## Build and run

There is **no `go.mod`** yet. Dependencies are pinned by commit in `.gopmfile` (gopm, GOPATH era), and `.travis.yml` targets Go 1.2–1.4. A modern Go toolchain (the machine has Go 1.27) will not build this tree as-is; establishing a module build is plan Task 1. Until then, the legacy commands are:

```sh
go build -tags "sqlite cert"     # as used by .bra.toml (live-reload dev config)
./gogs web                        # start the web server; -c/--config overrides custom/conf/app.ini
```

Build tags (each gates one file): `sqlite` (`models/models_sqlite.go`, SQLite3 driver — needs CGO), `cert` / `!cert` (`cmd/cert.go` vs stub), `redis`, `memcache` (cache backends). MySQL and PostgreSQL drivers are always compiled in.

## Tests

Very few tests exist, all in `modules/` (`cron`, `ldap`, `auth/ldap`, `avatar`, `httplib`, `uuid`); `models/`, `routers/`, and `cmd/` have none. Some (LDAP, httplib) hit live network services. Run one package or test with standard tooling once the build works:

```sh
go test ./modules/cron
go test ./modules/cron -run TestConstantDelayNext -count=1
```

The plan introduces an `integration` build tag for tests needing real Mercurial/native clients, and `newTestDatabase(t)` for model tests on temporary SQLite.

## Architecture

**Entry point and commands.** `gogs.go` registers CLI subcommands from `cmd/` (codegangsta/cli): `web` (the server), `serv` (SSH forced-command handler invoked from `authorized_keys`; parses `SSH_ORIGINAL_COMMAND`, checks access, execs git), `update` (called from the Git update hook, records `models.UpdateTask` rows), `dump` (backup), `cert` (self-signed TLS).

**Configuration.** `modules/setting` loads `conf/app.ini` (defaults, shipped) then overlays `custom/conf/app.ini`; the custom directory is `$GOGS_CUSTOM` or `<workdir>/custom`. Settings are package-level globals read everywhere. `conf/` also holds locales (`conf/locale/locale_*.ini`), gitignore/license templates, and `content/git-bare.zip`.

**Web layer.** `cmd/web.go` builds the Macaron app (`newMacaron`) and registers *all* routes in `runWeb` via `m.Group` blocks — that is the route table. Middleware in `modules/middleware`: `Contexter` wraps Macaron's context into `middleware.Context` (`ctx.Handle(status, title, err)` renders `templates/status/<code>`; `ctx.HTML` renders a `base.TplName`), `Toggle` enforces sign-in/admin, `RepoAssignment` loads owner/repo/permissions and opens the Git repository into `ctx.Repo`, `RepoRef` resolves branch/tag/commit. Handlers live in `routers/` (`repo`, `user`, `org`, `admin`, `api/v1`, `install.go`); forms with binding/validation are in `modules/auth`. Templates are in `templates/` (with a parallel `templates/ng` set), static assets in `public/`.

**Data layer.** `models/` uses xorm with a package-global engine `x` (`SetEngine`/`NewEngine` in `models/models.go`, which `Sync2`s all tables). Notable shapes: repository access is stored in `Access` keyed by lowercase `UserName` and `"owner/repo"` strings (the plan replaces this with repository/user IDs); `Team.RepoIds` is a `|`-delimited text field; `models/repo.go` both manages rows and shells out to git directly.

**Git coupling (what the conversion removes).** `modules/git` wraps the git CLI (commits, trees, refs, tags); used by `models/repo.go`, `models/git_diff.go`, `middleware/repo.go`, `routers/repo/*`, and the API. Git HTTP transport is `routers/repo/http.go`; SSH is `cmd/serve.go` + `cmd/update.go`; `modules/ssh/ssh.go` is an unfinished embedded-SSH prototype with hard-coded paths, not production code. `routers/repo/view.go` computes a last commit per tree entry, one git call each — the plan flags this as a per-process cost hazard for Mercurial.

**Planned new packages** (dependency rule from the plan: `modules/hg` imports no models/web code; `modules/hgbridge` depends only on `hg` plus injected callbacks; models never import hosting/routers; `modules/hosting` composes everything): `modules/hg` (runner, refs, files, history, diff), `modules/hgbridge` (capabilities, hooks, HTTP/stdio workers), `modules/hosting`, `routers/hg`, `cmd/hg_hook.go`, `tests/integration`.

## Conventions

- Keep import paths `github.com/gogits/gogs/...`; historical attribution/branding need not be renamed, but product copy, clone instructions, and errors must describe Mercurial accurately.
- Copy changes go in `conf/locale/locale_en-US.ini` (and other locales when removing Git-specific text), not hard-coded in templates.
