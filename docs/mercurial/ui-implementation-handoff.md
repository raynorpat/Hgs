# Shared UI implementation handoff

The approved Tasks 0–25 are implemented on `codex/shared-ui-implementation`. The work includes the shared Bitbucket-inspired navigation, repository/issue/fork pull-request workflows, account/installer/Admin pages, session-expiry draft recovery, Hgs branding, profile READMEs and avatars, contribution tracking, and illustrated maintenance/404/500 pages. The approved scope remains in [DESIGN.md](../DESIGN.md) and the [implementation plan](../superpowers/plans/2026-10-26-shared-ui-and-pull-requests.md).

The final whole-branch review covered 677 changed paths. Its nine Important and nine Minor findings were addressed in one fix wave and independently re-reviewed. No Critical or Important findings remain at this development gate. One additional Minor is deferred: some pull-request History reason labels display canonical values such as `changes_requested`, `declined` and `open`. Actor/time information, escaped freeform reasons and authorization are verified; those canonical labels are not fully localized.

## Final verification and source boundary

Production runtime code is frozen at `6b79e735e14e70d34c6ca23dc587d4930da4d20d`. Subsequent source commit `162baf6d0520fdcede4ad8bae0a0e62e67181e31` changes only `modules/cron/cron_test.go`, correcting the test's observation of its first scheduled instant. Both complete Cron package checks passed for that one-file delta. The full compound commands below ran at the runtime freeze, not at the later commit.

| Command | Completed result | Receipt |
| --- | --- | --- |
| `go test ./... -v -count=1 -timeout=120m` | Exit 0; 8 explicit skips | [Ordinary](ui-final/validation/ordinary.json), [index](ui-final/validation/ordinary-index.txt) |
| `go test -tags integration ./... -v -count=1 -timeout=120m` | Exit 1: four external LDAP connection timeouts to port 389; `tests/integration` passed. 26 explicit skips | [Tagged](ui-final/validation/integration-tagged.json), [index](ui-final/validation/integration-tagged-index.txt) |
| `go build ./...` | Exit 0 | [Build](ui-final/validation/build.json) |
| `go vet ./...` | Exit 0 | [Vet](ui-final/validation/vet.json) |
| Required race command | Exit 2: requires cgo; explicit cgo diagnostic also cannot build without gcc | [Race](ui-final/validation/race.json) |
| Complete ordinary/tagged Cron packages after the test-only amendment | Both exit 0 | [Ordinary](ui-final/validation/cron-ordinary.json), [tagged](ui-final/validation/cron-tagged.json), corresponding `.log` files |

These are Windows/amd64, Go 1.27.3 and native Mercurial 7.2.4 results. Skips are not passes. Original failed invocations and full raw logs remain in the retained controller evidence; later passing receipts do not overwrite them.

[Final browser evidence](ui-final/README.md) includes 15 saved, directly inspected captures with source identities and hashes. It covers affected Admin layouts and keyboard scrolling, pull-request history/selectors/literal-code rendering, profile geometry, activation resend and its normal Sign In recovery link. Earlier task evidence retains the broader profile, draft, landing and standalone-status checks. Browser fixtures and command processes stopped and joined; [cleanup](ui-final/validation/all-command-cleanup.json) records the final absence of owned processes. User test tabs were preserved.

## Remaining release validation

This is development acceptance, not an all-green release certification. Open checks include race instrumentation, Linux/macOS and legacy Mercurial versions, live MySQL/PostgreSQL isolation/TLS, remaining transport/fault/recovery and representative large-history performance scenarios, and broader accessibility coverage such as true text zoom, screen readers and other browser engines. Native rename-confirmation cancellation remains unexecuted. External LDAP connectivity also remains unresolved. The plan retains unchecked matrix requirements rather than marking unperformed checks complete.

The branch and worktree are preserved. No runtime merge, push or release was performed. [Detailed chronological evidence](shared-ui-evidence.md) and [all 70 controller rulings with their costs if wrong](shared-ui-decisions.md) accompany this handoff; the original ledger and raw receipts remain retained for audit and follow-up.
