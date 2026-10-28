# Task 17 implementation report

## Scope and interfaces

Implemented against clean `9a971a01ea61b95171e6fcd342dcf0c07b935747` (Task 16 and email-fix milestone), after reading the exact brief, controller context/final rulings, resume preparation, constraints, CLAUDE.md, relevant DESIGN sections, hosting design/plan, and implementer/TDD/verification contracts.

- Added `routers.Help(*middleware.Context)` and local `help.tmpl` in the shared shell: reduced Anonymous for guests, Workspace for signed-in users. Production GET `/help` is before catchalls and uses existing RequireSignInView/install guards.
- Publish the existing Help capability centrally from BuildShell only when installed. Existing rail/footer links consequently work on shared pages; no installer bypass or auth shortcut.
- Help describes Source/Changesets/Branches, typed Mercurial references, clone/pull/update/commit/push, access, Issues/Pull requests, and independent Watch/Star meanings. Clone instructions use the repository's configured Clone control. SSH instructions require both `!DisableSSH` and nonempty `SSHAddress`, matching hosting composition. No credentials/config dump, fabricated repository host, Git workflow, or mutation control.
- Product strings are localized in English with existing fallback layering; German Help label provides real selected-translation coverage. Official Mercurial guide destination was opened and verified: https://www.mercurial-scm.org/guide.
- Production Localization now uses the approved existing `Redirect:false` option. Native locale selection/cookies and the Task 15 adapter remain intact. Reuse central safe query allowlist and UILanguageURL; selected language can remain in the URL.
- Preserved already-working independent labeled Watch/Star POST forms, CSRF/auth, duplicate-prevention JS, counts, guest sign-in links, subscription meaning, and notification behavior. Header/footer markup needed no rewrite. Existing Hgs/operator identity, version and GoGits source/license legal destination remain intact.
- Actual forced late SQL failures exposed non-atomic Watch/Star membership/counters. Reported concrete reproduction/root cause to controller before changing models; controller explicitly approved minimal transaction ownership. Exported WatchRepo/StarRepo now own sessions/rollback/commit; relevant reads/writes use the session. Internal watchRepo(Engine) remains available for existing caller-owned transactions, with no nested transaction. No schema, authorization/lifecycle change, new notification, or concurrent duplicate guarantee.

## Files

- `cmd/web.go`, `cmd/ui_help_test.go`, `cmd/ui_repository_test.go`: actual route/locale guards, root/subURL translation/cookie/filter continuation, real BasicAuth private-reader POST proof.
- `routers/help.go`, `routers/help_test.go`, `templates/help.tmpl`: handler, presentation/configuration/legal regression matrix, local Help content.
- `modules/ui/render.go`: installed-only central Help capability.
- `conf/locale/locale_en-US.ini`, `conf/locale/locale_de-DE.ini`: Help copy/selected language label.
- `models/repo.go`, `models/repo_actions_test.go`: atomic preference updates and all four add/remove late-failure rollback tests.
- `tests/integration/ui_test.go`: requested focused names, reader state/count rendering, independent actions, sequential duplicates, GET/CSRF denial, guest/private visibility, forced error flash consumed after redirect, rollback, explicit retry, and no action-feed side effects.
- This report. No other task's files staged.

## Commands and TDD evidence

Windows amd64; `go version go1.27.3 windows/amd64`. Tests use fresh owned TEMP/TMP outside checkout:

```powershell
$taskTemp='C:/Users/raynorpat/.codex/visualizations/2026/10/26/01a177c6-a836-7dd1-a55e-6a2aca024544/task17-temp'
$env:TEMP=$taskTemp
$env:TMP=$taskTemp
$env:GOGS_TEST_HG='D:/github/gogs/.venv-hg/Scripts/hg.exe'
```

Logs are retained under that owned directory. No live config/data, SMTP, alternative checkout, prototype ports, global configuration, system installation, remote, merge or push used.

### Setup failures and test corrections (not feature RED)

The first exact brief invocation used a fresh sandbox-local TEMP directory. Native Hg exited 255 during `hg init` with `Access is denied` on `.hg/.requires-…~`. A second shell could not write `red-production.log` into that sandbox-owned TEMP. Switched to the owned writable visualization directory above, then reran the same commands. Those prerequisite failures are not claimed as behavioral RED.

The first functional integration run also contained an incorrect stale `f.private.NumWatches` expectation (the actual owner watch is persisted after repository creation), and inspected only the first Set-Cookie header for `_flash`. Corrected to reread persisted owner baseline and consume all real response cookies through the redirect to assert visible translated failure feedback. The corrected RED below isolates the actual production failure. Initial wrapper commands ended with Get-Content and therefore returned shell status 0 despite Go FAIL; subsequent wrappers preserve `$LASTEXITCODE` explicitly. Original outputs/invocations were retained in tool history and logs rather than relabeled as passing.

### Actual RED before relevant production changes

1. `go test ./cmd -run '^TestSharedHelpAndFooter$' -count=1`
   - `red-production.log`: actual production `/help` and `/code/help` both return 404 from the user catchall; FAIL cmd. Expected missing-route failure.
2. After Help implementation, same command before changing production locale option:
   - `red-locale.log`: Help 200, then German footer destination `/help?lang=de-DE&page=2&q=mercurial` (and `/code/help?...`) returns **302 `/help` / `/code/help`**, losing filters; FAIL cmd. Expected existing Redirect=true failure.
3. Exact brief command:
   `go test -tags integration ./routers ./routers/repo ./tests/integration -run 'TestRepositoryWatchStarUI|TestSharedHelpAndFooter' -count=1`
   - `red-focused-owned.log`: missing Help 404; late watch counter trigger leaves membership true, and late user-star trigger leaves membership true and repository stars incremented despite handler error. Includes initial expectation/cookie corrections noted above.
   - `red-failure-corrected.log` (corrected assertions, before model fix): routers pass; reader error flash follow passes; Watch state `watching=true … counts=1/0; want false/false 1/0`; Star state `watching=true starred=true counts=1/1; want false/false 1/0`; FAIL integration 108.270s. Ordinary independent action/duplicate assertions passed. No manufactured RED for already-working controls.
4. `go test ./models -run '^TestRepositoryWatchStarFailureRollback$' -count=1`
   - `red-model.log`: all four subtests fail as expected: failed watch/unwatch changed membership; failed star has repo/user counts `1/0 want 0`; failed unstar has `0/1 want 1`; FAIL models 2.718s. SQLite BEFORE UPDATE triggers fail *after* membership mutation (and after repository star counter for star/unstar).

### GREEN and affected validation

- `go test ./models -run '^TestRepositoryWatchStarFailureRollback$' -count=1`
  - `green-model.log`: `ok github.com/gogits/gogs/models 2.629s`.
- `go test ./routers ./modules/ui ./cmd -run 'TestSharedHelpAndFooter|TestSharedShell|TestSharedUIRegisteredRepositoryActions' -count=1`
  - `green-help.log`: routers 3.634s, modules/ui 7.985s, cmd 7.726s, all ok.
- `go test ./routers ./routers/repo ./modules/ui ./modules/middleware ./modules/mailer -count=1`
  - `green-affected.log`: all ok; routers 8.102s, routers/repo 1.643s, ui 20.637s, middleware 2.860s, mailer 6.141s. Includes unchanged explicit Redirect=true adapter coverage and both existing mail branding tests, including issue watcher regression.
- `go test ./models -run 'TestRepositoryWatchStarFailureRollback|TestRepositoryAccessMatrix|TestTransferOwnershipUpdatesOwnerOnly|TestForkOrganization' -count=1`
  - `green-affected-models.log`: all ok, models 11.707s. Covers changed helper's organization/caller-transaction seams without repeating unchanged PR/native suites.
- `go test ./cmd -run 'TestSharedHelpAndFooter|TestRepositoryWatchStarUI|TestSharedUIRegisteredRepositoryActions' -count=1`
  - `green-production.log`: all ok, cmd 13.266s. Actual production router, native auth/CSRF, private READABLE account, root/subURL Help/policy/install, selected translation, scoped language cookie and subsequent cookie selection, safe filters preserved, token/password omitted.
- `go build ./...`
  - Exit 0. `build.log` contains an environment warning: Go could not write its module stat-cache temporary file under the read-only module cache (`…github.com/gogits/gogs/@v/v0.0.0-20261028173922-9a971a01ea61.info….tmp: Access is denied`). Build succeeded; no permission/config workaround or global cache mutation performed.
- `go vet ./cmd ./routers ./routers/repo ./models ./modules/ui ./modules/middleware ./modules/mailer`
  - Exit 0, empty `vet.log`.

- Final exact brief command:
  `go test -tags integration ./routers ./routers/repo ./tests/integration -run 'TestRepositoryWatchStarUI|TestSharedHelpAndFooter' -count=1`
  - `green-focused.log`: Exit 0; routers 6.639s, routers/repo 1.624s [no matching tests], integration 121.682s, all ok. Includes actual native Hg 7.2.4 fixture, reader state/count rendering, duplicate/GET/CSRF/guest boundaries, late-error visible feedback, unchanged state/counts, and explicit retry with independent Watch retained during Star failure.
- `git diff --check`: Exit 0; only expected local LF/CRLF conversion notices from Git, no whitespace errors.

## Self-review, provenance and limits

- Reviewed entire production/test diff against task scope. Reused existing POST controls, locale component, safe query builder, shell/footer and model APIs. No new framework or optimistic JS counts; no PR mail path introduced. The model fix is the only explicitly approved extra production file scope.
- Locale caveat retained: pinned prepareOptions falls back to Macaron convention config when false. Current app never calls macaron.SetConfig and receives empty convention config; a future explicit convention REDIRECT=true can override false and must preserve this product contract. No claim that false forces all future configuration. Task 15 explicit true test remains unchanged.
- English fallback supplies the new Help body in untranslated locales; German Help heading is translated. Native Windows Hg 7.2.4 fixtures provide only their stated focused evidence, not a new portability/full-native acceptance claim. No full 40-minute native suite, unchanged broad PR suite, benchmarks, Linux/macOS release matrix, or external mail client matrix repeated.
- Root owns all Cua. No browser tools used here. Automated HTTP/template/CSS evidence is not 390px/1440px keyboard, focus, overflow, guest/reader/owner usability proof. Current browser handoff is blocked by root's recorded stuck IAB tab/user help; visual review remains explicitly pending with root. Existing production-router browser fixture may be reused by root with native Hg and normal authentication; no auth-bypass fixture was created.

## Frozen browser handoff

After every final check passed, controller requested reuse of **existing** TestLandingBrowserFixture public variant. No harness/endpoint/auth shortcut was added. Launched from the frozen Task 17 source represented by this commit (base `9a971a01` plus only the listed diff):

```powershell
$env:TEMP=$taskTemp; $env:TMP=$taskTemp
$env:GOGS_TEST_HG='D:/github/gogs/.venv-hg/Scripts/hg.exe'
$env:UI_LANDING_FIXTURE='1'; $env:UI_LANDING_VARIANT='public'
go test ./cmd -run '^TestLandingBrowserFixture$' -count=1 -timeout 60m -v
```

`browser-fixture.log` reports native Mercurial 7.2.4 and owned disposable root `task17-temp/TestLandingBrowserFixture4158368102/002`. Bound only to loopback. Root handoff: `http://127.0.0.1:65259/code/help` and `http://127.0.0.1:65259/code/alice/project`; existing normal stop URL `http://127.0.0.1:65259/fixture/stop`. Normal sign-in is `/code/user/login` with synthetic alice/password or bob/password; root requested read-only guest checks. No SMTP invocation; no real config/data. The existing fixture's unused mail preview/identity utilities remain untouched. Fixture is left running for root's visual pass/normal stop, not claimed as a completed test until stopped.

Default sandbox `git add` could not create `D:/github/gogs/.git/worktrees/gogs1/index.lock`; only the authorized local stage/commit requires normal sandbox escalation. This was not a code/test failure.
