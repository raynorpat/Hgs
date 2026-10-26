# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Current state: Mercurial-only hosting branch

This is the Gogs 0.5.13 Beta source (Feb 2015), converted into a **Mercurial-only** hosting service. Before changing anything structural, read:

- `docs/superpowers/specs/2026-10-25-mercurial-hosting-design.md` — approved design (scope, exclusions, security model)
- `docs/superpowers/plans/2026-10-25-mercurial-hosting.md` — implementation plan (Task16 benchmark appended; not yet executed)

Key decisions those documents fix, which are easy to violate by accident:

- Git is removed entirely: no Git executable at runtime, no Git wire endpoints, no dual-backend mode, no Git compatibility layer. Fresh database, no migration.
- Mercurial version policy: server ≥ 5.0, native clients ≥ 2.0. Native Windows evidence is 7.2.4 server/client; 5.0.2/2.0.2 are unexecuted matrix targets here. See `docs/mercurial/compatibility.md` for actual outcomes and gaps.
- Go stays the application language; Mercurial runs as managed child processes (`hg serve` per HTTP request, `hg serve --stdio` per SSH session). No custom Python helper and no reimplementation of the wire protocol — architectural changes go back to the user for approval.
- Writers are checked at pretxnopen/prepushkey by an internal subcommand of the same binary. Already-open transactions may complete; live cancellation is best-effort, without a completion/rollback guarantee. Readers get static deny hooks on all veto-capable hooks.
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

Go modules build. `go.mod` pins the retained legacy dependencies and the selected modern runtime replacements and sets `toolchain go1.27.3`; with the default `GOTOOLCHAIN=auto`, Go downloads and uses that toolchain automatically.

```sh
go build ./...                   # whole tree
go build -o gogs.exe .           # binary (gogs.exe is gitignored)
./gogs.exe web                   # start the web server; -c/--config overrides custom/conf/app.ini
```

Build tags (each gates one file): `cert` / `!cert` (`cmd/cert.go` vs stub), `redis`, `memcache` (cache backends). MySQL, PostgreSQL, and SQLite drivers are always compiled in. Everything builds as pure Go with `CGO_ENABLED=0`; no C compiler is needed. SQLite uses `modernc.org/sqlite`, registered in `models/models.go` under the driver name `sqlite3` because that is the name xorm, `app.ini` (`DB_TYPE = sqlite3`) and the installer use. `mattn/go-sqlite3` still appears in `go.mod` only because xorm/core's own tests import it; it is not linked into the binary.

## Tests

Ordinary tests cover commands, models, Mercurial runners/bridges, authorization, process ownership, SSH, routes and UI boundaries. External LDAP/httpbin checks and native transport suites use the `integration` build tag. Run ordinary checks with `go test ./...`, `go vet ./...` and `go build ./...`. Native acceptance requires `GOGS_TEST_HG` and an installed complete Mercurial runtime plus a native OpenSSH client; see `docs/mercurial/compatibility.md` for exact commands and real failures/unverified rows. `newTestDatabase(t)` provisions temporary SQLite for model tests. Supplied package acceptance requires explicit binary/assets and direct mode; no production test API exists.

## Architecture

**Entry point and commands.** `gogs.go` registers `web`, optional build-tagged `cert`, and the internal `internal-hg-hook` authorization/deny command. Removed `serv`, `update` and `dump` commands are not operational interfaces. Embedded SSH is application-owned; no forced-command/authorized_keys server setup is required.

**Configuration.** `modules/setting` loads shipped executable-root `conf/app.ini`, then custom configuration (`GOGS_CUSTOM` or the executable-root custom directory, or an explicit `web --config`). Runtime assets remain external. Database/TLS/custom/session path caveats and backup locations are documented in `docs/mercurial/installation.md` and `operations.md`; do not assume all paths are CWD-independent. `conf/locale` retains translator attribution.

**Web layer.** `cmd/web.go` builds Macaron (`newMacaron`) and registers routes in `newWebHandler`; `runWeb` owns listeners, hosting initialization and bounded shutdown. Middleware resolves database permissions and Mercurial repository/revision context. Handlers remain in `routers/`, forms in `modules/auth`, templates in `templates/` and static files in `public/`. Only `/hg/<owner>/<repo>` exposes Mercurial HTTPS transport; `.hg` remains private storage.

**Data layer.** `models/` uses xorm with package-global engine `x`, a fresh hosting-format database and immutable repository/user-ID access rows. Repository storage is `<repository-root>/<immutable ID>/`; lifecycle operations and reconciliation coordinate with hosting drains. No Git database/repository migration is supplied.

**Mercurial composition.** `modules/hg` owns native execution and repository reads and imports no models/web code. `modules/hgbridge` owns capabilities/hooks/HTTP/stdio workers with injected authorization; `modules/hosting` composes model authorization, bounded workers and persistent embedded SSH identity. Models do not import hosting/routers. Native clients drive `tests/integration`; fake process tests are not native portability evidence. The final Task14 all-target native gate failed on two Windows lock errors despite isolated C: rechecks passing; do not silently promote configured CI or cross-builds into native support.

## Conventions

- Keep import paths `github.com/gogits/gogs/...`; historical attribution/branding need not be renamed, but product copy, clone instructions, and errors must describe Mercurial accurately.
- Copy changes go in `conf/locale/locale_en-US.ini` (and other locales when removing Git-specific text), not hard-coded in templates.
