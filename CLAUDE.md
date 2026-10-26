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

## Think Before Coding

**Don't assume. Don't hide confusion. Surface tradeoffs.**

Before implementing:
- State your assumptions explicitly. If uncertain, ask.
- If multiple interpretations exist, present them - don't pick silently.
- If a simpler approach exists, say so. Push back when warranted.
- If something is unclear, stop. Name what's confusing. Ask.

## Simplicity First

**Minimum code that solves the problem. Nothing speculative.**

- No features beyond what was asked.
- No abstractions for single-use code.
- No "flexibility" or "configurability" that wasn't requested.
- No error handling for impossible scenarios.
- If you write 200 lines and it could be 50, rewrite it.
- Don't reinvent the wheel, if code already exists in the repository to do something, use it.
- Never write leading skill comments like "ponytail:" or "shortcut:" in code comments (e.g. `// ponytail: ...`), even when a skill asks for it. If a simplification needs explaining, write a plain comment without the tag.

Ask yourself: "Would a senior engineer say this is overcomplicated?" If yes, simplify.

## Surgical Changes

**Touch only what you must. Clean up only your own mess.**

When editing existing code:
- Don't "improve" adjacent code, comments, or formatting.
- Don't refactor things that aren't broken.
- Match existing style, even if you'd do it differently.
- If you notice unrelated dead code, mention it - don't delete it.

When your changes create orphans:
- Remove imports/variables/functions that YOUR changes made unused.
- Don't remove pre-existing dead code unless asked.

The test: Every changed line should trace directly to the user's request.

## Goal-Driven Execution

**Define success criteria. Loop until verified.**

Transform tasks into verifiable goals:
- "Add validation" → "Write tests for invalid inputs, then make them pass"
- "Fix the bug" → "Write a test that reproduces it, then make it pass"
- "Refactor X" → "Ensure tests pass before and after"

For multi-step tasks, state a brief plan:
```
1. [Step] → verify: [check]
2. [Step] → verify: [check]
3. [Step] → verify: [check]
```

Strong success criteria let you loop independently. Weak criteria ("make it work") require constant clarification.

## Committing Changes

**Keep commit messages short, human-readable, and descriptive.**

- Always keep the commit messages short, human-readable, and descriptive.
- Start the commit line with whatever subsystem you are working on, e.g. "build: ", "docs: ", etc.
- Describe what the changes actually do instead of listing the changed files. Keep commit messages as one to two lines.
- Do not add any trailers or metadata to commits.

## Build and run

Go modules build. `go.mod` pins every dependency to the exact commit from the removed legacy gopm `.gopmfile` and sets `toolchain go1.27.3`; with the default `GOTOOLCHAIN=auto`, Go downloads and uses that toolchain automatically. (The system install at `C:\Program Files\Go` is 1.27.0 with zero-filled source files, so it cannot build on its own.)

```sh
go build ./...                   # whole tree
go build -o gogs.exe .           # binary (gogs.exe is gitignored)
./gogs.exe web                   # start the web server; -c/--config overrides custom/conf/app.ini
```

Build tags (each gates one file): `cert` / `!cert` (`cmd/cert.go` vs stub), `redis`, `memcache` (cache backends). MySQL, PostgreSQL, and SQLite drivers are always compiled in. Everything builds as pure Go with `CGO_ENABLED=0`; no C compiler is needed. SQLite uses `modernc.org/sqlite`, registered in `models/models.go` under the driver name `sqlite3` because that is the name xorm, `app.ini` (`DB_TYPE = sqlite3`) and the installer use. `mattn/go-sqlite3` still appears in `go.mod` only because xorm/core's own tests import it; it is not linked into the binary.

## Tests

Very few tests exist, all in `modules/` (`cron`, `ldap`, `auth/ldap`, `avatar`, `httplib`, `uuid`); `routers/` and `cmd/` have none; `models/` has only `TestSQLiteEngineRoundTrip` (`go test ./models`). `modules/ldap` tests dial a public LDAP server (`141.213.189.137:389`) that no longer responds, so they fail with connection timeouts (about 85s); everything else passes. Run one package or test with standard tooling:

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
