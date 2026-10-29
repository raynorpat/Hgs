# Contribution indexing and query evidence

Task 21 implementation is `bb5f7c3`, with fixes in `f619847`. Independent review and scoped re-review accepted all four reported Important fixes with no new blocking findings. This records executed development checks separately from unverified release requirements; profile calendars and authorized activity queries belong to Tasks 22–23.

The index uses full-node changeset identities and unique event/repository memberships. Native history scans use fixed snapshot/node/revision continuation in pages of at most 100, stage replacement memberships, and publish them atomically after successful completion. Captured durable job sequences preserve publication arriving during a scan. Author claims are attributed through current uniquely verified email ownership; changeset actor IDs remain zero. Proof is distinct from automatically active account/email flags, and lookup addresses remain internal.

Canonical issue, PR, first-review and repository events use transactional jobs. First formal-review time is preserved independently of editable review state and repaired only toward an earlier canonical audit record. Repository creation records the human actor when known; historical unknown actors stay unknown. Jobs use actorless read-only hosting admission, current storage identity and lifecycle cancellation. Native push notifications are advisory and enqueue-only; confirmed normal/recovered review publication notifies outside registry locks.

Executed on Windows with Go 1.27.3, Mercurial 7.2.4 and SQLite:

| Check | Result and scope |
| --- | --- |
| Proof and ownership | Cross-account token reassignment, replay/purpose isolation, ambiguity, ownership removal/re-add, continuous promotion/profile ownership, expiry and resend boundaries pass focused tests. Explicit confirmation uses existing protections; valid legacy activation remains eligible without granting ambiguous attribution. |
| Native history | 106 visible nodes cover named heads, merge changesets, secret exclusion, 100-entry paging, a publication racing a fixed snapshot, and fork deduplication. Final measured indexing sequence 11.279s; this is fixture timing, not a production throughput guarantee. |
| Native push | Denied transaction-open emits no publication signal. A committed push signals; a failing observer leaves committed history successful. |
| Native review | Normal, already-complete and recovered-complete publication notify; partial/repair-required outcomes do not. Maintenance re-entry establishes callbacks run outside registry locks. Final selected package run 145.238s. |
| Mail and routes | Owned fake SMTP performs an actual local exchange. Owner selection, CSRF, disabled mail, code-only URLs, shared 180-second limit and proof consumption pass. No external mail or real addresses were used. |
| Enqueue deadline | Actual pinned-driver stalled-handshake fixtures exercise independent 200/600ms budgets, deadline clamping, expired retry and socket cleanup. The per-call absolute bound is at most 5s and respects earlier deadlines. Cancellation without a deadline is bounded, rather than instantaneous. |
| Lifecycle and scheduling | Focused tests cover worker cancellation/join and startup failure. Injected ticks demonstrate reconciliation during a blocked scan; cancellation waits for both owned workers. |

The first independent review found four Important defects. Meaningful failing regressions reproduced each before correction:

- Stale application discovery restored pull/review membership after actual source deletion. Publication now locks lifecycle rows before its canonical reread, and retained merged history requires actual source-row absence. The deterministic interleaving uses SQLite; it is not a PostgreSQL isolation runtime test.
- Throttled secondary creation left an inactive address without activation mail. Delivery reservation and creation now share one transaction, with rollback and shared-limit coverage.
- Failed job insertion followed by a zero-row update returned success. Recovery now requires one affected durable row; real competing SQLite insertion and rejected-insert fixtures cover both outcomes.
- A long indexing backlog delayed reconciliation. A persistent owned reconciliation loop now runs alongside the serial index worker and is joined on exit.

Final affected checks after these corrections passed: models 24.242s, contributions 1.131s, user routers 4.551s, cmd 6.228s; tagged contribution/account integration 18.683s. Build and vet exited 0. The unchanged native review suite was not replayed for these model/scheduler corrections.

The original `go test ./... -count=1` invocation remains **FAIL**: an unchanged Mercurial descendant-process test failed at its one-second startup deadline. Its isolated unchanged rerun passed 1.793s; affected packages passed individually. This does not turn the full invocation green. Expected fixture diagnostics, a nonfatal module stat-cache permission warning and CRLF notices remain recorded.

Real MySQL/PostgreSQL SQL/TLS interoperability, other native platforms/versions, race/C-compiler, browser and inherited full release requirements remain unverified. Database/container tools are unavailable locally. Loopback driver-boundary tests do not establish live database compatibility or cancellation of legacy xorm operations elsewhere. Changeset email metadata remains an untrusted authorship claim. Task 22 must include visible application/proof/index changes in cursor validity, including earlier-date repairs to existing events.

## Task 22 — authorized query verification

Task 22 implementation `f8c2ca7` and correction `de61241` passed independent task review and scoped re-review after both reported Important findings were fixed. This establishes the development gate; supported database/platform release validation remains open.

Contribution reads use indexed SQL and current canonical authorization before years, counts, deduplication and grouping. No contribution query scans Mercurial history. UTC date projections, bounded authorization batches, whole-group pagination and opaque viewer/scope/filter-bound cursors are covered by focused fixtures. Each group retains its full count and at most 25 representative links; Task 23 provides the cap label and real repository navigation.

The initial broad affected run passed models/contributions/collaboration/user/cmd selectors (72.624/36.392/3.754/3.887/25.388s), and tagged contribution integration passed 20.819s. Later completeness and source-visibility corrections received separate affected runs: 10.193/25.663s and 4.956/6.291s. The strengthened exact proof-swap regression passed 2.669s. Final build and vet exited 0; build retained the nonfatal stat-cache permission warning.

A synthetic organization history contains 5,000 events with 5,000 distinct authors. The measured read returned the independently expected full count with one group and 25 links using 16 queries, 38 transferred rows and a maximum returned batch of 25. These numbers measure query count and transferred rows, not internal database scanning. The additional user-scope foreign-repository completeness probe is outside this measurement.

Actual SQLite tests cover the one-connection callback boundary and a committed edit interleaved with the pinned read snapshot. Earlier deadlock and shared-cache inconsistency failures were retained before correction. The production network database isolation paths have not been exercised against live MySQL/PostgreSQL services. Supported-platform, race and inherited full-suite release gaps remain open.

The review found two additional correctness defects, each reproduced before correction: repairing an unprojected shared changeset through an inaccessible fork changed readable totals without staling an existing cursor, and selecting year 1 admitted Go's zero timestamp. Shared repairs now atomically stamp all affected published memberships in batches of 100; an unchanged private restage does not invalidate the readable snapshot. Selected-year narrowing preserves the timestamp validity bound. The final affected regression union passed models 12.283s and contributions 23.330s. A separate 205-membership test verifies rollback, stored author identity and bounded batches. These repair measurements are distinct from the 5,000-event query measurement above.
