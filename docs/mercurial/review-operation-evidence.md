# Mercurial review operation evidence

Recorded 2026-10-26. Scope: private snapshots, comparisons and merge preparation only. Publication, live permission checks and transport-writer serialization are Task 2. No source checkout or published repository is used as a merge workspace.

## Native environment and limits

| Row | Evidence/status |
| --- | --- |
| Windows, Mercurial 7.2.4 | Native executable `D:/github/gogs/.venv-hg/Scripts/hg.exe`; `HGRCPATH='' hg version -T json` reports `ver: 7.2.4`, `extensions: []`. Native fixture results below. |
| Windows, Mercurial 5.0.2 | Unavailable; NOT verified. In particular configuration suppression, templates, internal merge tool and cross-branch ancestor behavior remain release validation debt. |
| macOS, both supported server releases | Unavailable; NOT verified. Filesystem normalization/case behavior is NOT established by this Windows run. |
| Linux, both supported server releases | Unavailable; NOT verified. |
| Native legacy client/server acceptance matrix | Outside this task and still release debt; no pass inferred from Go tests or this server executable. |

Resolution and bounded comparison use the existing 30-second `BrowserCommandTimeout`. Heavy snapshot and merge preparation use the configured Runner `SessionTimeout` as their overall ceiling (default 30 minutes), inheriting any shorter caller operation deadline/cancellation. Every subprocess also retains Runner queue, slot, idle, session and process-tree limits. Command output is at most 2 MiB. Diff rendering additionally caps 10,000 lines; comparison returns bounded content with `Truncated=true` and `ErrComparisonTooLarge`, never an unbounded success. Binary patches use native `--no-binary` summaries. No custom Python helper, wire parser, extension, external merge tool, repository script or debug-parent manipulation is used.

## Exact command shapes

Commands below run through `Runner`, with a managed `HGRCPATH`, stripped environment and `HGPLAIN=1`. Review commands use the repository-less Runner entry point plus explicit `-R <internal-path>`. This deliberately sets `HGRCSKIPREPO=1`; ordinary repository Runner commands retain managed transaction hooks. Repository paths are internal ID-resolved paths supplied by authorized application code, never request values or URLs.

Before opening any supplied store, a private native capability probe runs `init <probe>/repo`, writes benign marker settings to `.hg/hgrc` AND `.hg/hgrc-not-shared`, then runs `-R <probe>/repo showconfig -T json`. If either marker is read, or the probe cannot demonstrate suppression, review fails closed with `ErrUnsafeCheckout`. This is a behavioral feature gate, not a claimed older-version pass. The probe contains no executable configuration.

Resolution reads native `branches --closed -T <existing branch-head JSON template>` and `log -r 'sort((bookmark() or tag()) and not secret(), -rev)' -T <existing references template>`. Branch/bookmark/tag names are compared literally in Go. A named branch with multiple heads requires a matching explicit full head. Unknown, hidden, secret, deleted or stale selections fail rather than falling back to a tip. Ordinary browsing `Resolve` is unchanged.

Snapshot construction exclusively allocates `<output>/repo` under a new private directory:

1. `clone --pull -U -r <full-target-node> -- <target-path> <output>/repo`
2. Replace the generated clone hgrc with a private-artifact comment (no inherited configuration or default source path).
3. `-R <output>/repo pull --force -r <full-source-node> -- <source-path>`
4. Enumerate private bookmark references and delete them using `bookmark --delete -- <literal-name>`; preserve the selected destination bookmark identity only in application artifact metadata.
5. `log -r 'heads(ancestors(id(SOURCE)) and ancestors(id(TARGET))) and not secret()' -T '{node}\n'`

`--pull` avoids mutable hardlinked/shared stores; `-U` prevents checkout. `--force` only permits collecting unrelated histories inside the private artifact so they can be rejected by the base check, followed by private cleanup. It is never used to publish into a target. Zero common bases returns `ErrUnrelatedHistory`; multiple maximal common ancestors return `ErrAmbiguousHead`. The criss-cross fixture demonstrates that no arbitrary single ancestor is selected. Identical endpoints return `ErrNoChanges` before allocating output.

A versioned JSON ownership/identity record stores the full source, target and unique base nodes plus target selector. Compare/prepare require this record and matching immutable pins. Existing output paths, overlapping published/snapshot paths, linked directory ancestors and altered pins are rejected. Failure cleanup is limited to the directory exclusively allocated by that call; pre-existing/unrelated output is never removed. Cleanup is best effort on OS I/O failure; callers must reconcile any leftover private allocation, never publish it as success.

Comparison uses:

- `log -r 'sort(ancestors(id(SOURCE)) - ancestors(id(TARGET)), rev) and not secret()' -T <existing changeset JSON template>`
- `diff --git --nodates --no-binary -r <full-base-node> -r <full-source-node>`

Only validated full hexadecimal node IDs enter these fixed revsets. Source pushes after snapshot creation cannot alter this private comparison. A source already included by the target has no proposed changesets (`ErrNoChanges` for comparison).

Preparation clones only the snapshot's required history into a separate private repository with `clone --pull -U -r TARGET`, then `pull -r SOURCE`. Only the two-parent mode checks out files:

- `update --clean -r TARGET`
- `merge --tool :merge -r SOURCE`
- On failure, `resolve --list -T '{mergestatus}\n'` distinguishes native unresolved files (`ErrMergeConflict`) from process/timeout/resource failures.
- On success, `commit -u <server-supplied-actor> -m <server-composed-PR-message>`, followed by native changeset inspection verifying destination branch and ordered parents exactly `(TARGET,SOURCE)`.

The actor/message API does not accept arbitrary command fragments or a user-supplied date. Empty/control-containing author identities and oversized metadata are rejected. Source changesets/authors are imported intact, not rewritten. No target bookmark is moved by this task; `PreparedMerge.Bookmark` identifies the one intended bookmark for Task 2's guarded publication.

## Demonstrated native merge semantics

| Graph | Mode | Result/parents | Result named branch |
| --- | --- | --- | --- |
| Target ancestor of source, same branch | `fast_forward` | Existing exact SOURCE node | Destination branch (same as source) |
| Target ancestor of source, different branches | `two_parent` | New native commit; ordered parents TARGET, SOURCE | Destination node's branch |
| Divergent, clean content | `two_parent` | New native commit; ordered parents TARGET, SOURCE | Destination node's branch |
| Source ancestor of target | `already_integrated` | Existing exact TARGET node; no new merge | Destination node's branch |
| Divergent conflict | typed failure | `ErrMergeConflict`; no accepted result | Public target unchanged |

Cross-branch ancestor success above is demonstrated on Windows/Hg 7.2.4 only. No merge command uses `--force` or `debugsetparents`; the ordinary native merge produces the required two-parent changeset. Fixtures assert public store bytes and public bookmarks remain unchanged during review/preparation.

## Checkout policy and evidence boundaries

For `two_parent` preparation, all source, target and base manifests are checked together BEFORE allocating its workspace or creating a checkout. This catches cross-tree as well as within-tree case collisions. Symlinks, subrepository declarations/state, metadata components, traversal, non-UTF-8 raw names and unsupported host representations fail closed. Executable regular files are data; their executable bit does not cause execution.

Windows checks additionally reject forbidden characters/controls, trailing dot/space components, DOS device names (including whitespace before extensions), any `~` short-name alias syntax, and case collisions in file/directory prefixes. Windows and macOS currently have a deliberate conservative **ASCII-only two-parent materialization policy**. This does NOT mean all Unicode names are inherently unrepresentable. Existing `CheckPath` checks argument encoding, not raw manifest byte decoding, NTFS Unicode comparison/short-name equivalence or APFS normalization. A fully audited native representation/collision layer is not yet present. macOS ASCII refusal is an intentional policy, NOT native macOS evidence. `fast_forward` and `already_integrated` skip materialization preflight: they preserve the exact existing result node using private checkout-free clone/pull only. Ownership, configuration isolation, pinned identities and process/payload bounds remain enforced for every mode. Non-ASCII/raw-name content remains browsable; unsupported two-parent integrations can be performed locally.

The native Unicode fixture uses `import --bypass` to put a UTF-8 `漢字` file directly into the store. `Manifest` returns the exact name; with an independently diverged target, two-parent preparation returns `ErrUnsafeCheckout` before allocating a checkout. Current Windows fixture success does not establish Unicode materialization safety or any macOS support. Removing this policy requires separately proven lossless decoding and host collision rules, not assuming that a representable command argument suffices.

Hostile repository hgrc, hook and merge-tool sentinel commands do not execute; an ambient `HGMERGE` is stripped. Subrepository fixture URLs point at an instrumented local HTTP server and assert zero requests. Symlink/case fixtures are imported without checkout; unsupported materialization fails without target mutation. Existing browsing tests remain separate regression coverage.

## Test commands and recorded results

All integration commands set `$env:GOGS_TEST_HG='D:/github/gogs/.venv-hg/Scripts/hg.exe'` in the invoking PowerShell process. No unavailable row is treated as a skipped pass.

- Required RED: `go test -tags integration ./modules/hg -run 'TestReview' -count=1` -> FAIL, 54.358s. All five initial required tests/subcases fail with explicit `review resolution not implemented`, with fixture setup successful. The initial scratch scaffolding consisted only of type/signature declarations and explicit missing-feature errors; it is absent from the implementation.
- First native GREEN: same command -> PASS, 243.830s (five initial test families, including all merge modes).
- Additional native boundaries: `go test -tags integration ./modules/hg -run 'TestReview(Rejects|ComparisonBounds|PreparedBookmark|DoesNotRun|Artifacts)' -count=1` -> PASS, 174.449s.
- Cross-endpoint case-collision RED: `go test -tags integration ./modules/hg -run '^TestReviewCheckoutRejectsCaseCollisionAcrossEndpoints$' -count=1` -> FAIL, 24.475s, `cross-endpoint path collision accepted: <nil>`. Root cause: separate per-tree maps concealed collisions across source/target. After checking the union: same command -> PASS, 19.857s.
- Windows alias/device RED: `go test ./modules/hg -run '^TestReviewCheckoutPathPreflight$' -count=1` -> FAIL, 1.048s: `COM¹.txt`, `lpt²`, `HG~1/hgrc`, `CON .txt`, `café` accepted. After conservative policy/device checks: same command -> PASS, 0.956s. Unicode rejection here is policy, not proof every name is intrinsically unrepresentable.
- Native store-browsing/unsupported-checkout check: `go test -tags integration ./modules/hg -run '^TestReviewUnsupportedPathsRemainBrowsable$' -count=1` -> PASS, 23.904s.
- Full ordinary project suite: `go test ./...` -> PASS, all packages (including hg 28.711s, cmd 126.283s, hosting 84.160s); final source rerun recorded below.

Expanded review suite (after combined-path fix, before final Windows alias/deadline changes): same required command -> PASS, 551.120s. All added visibility, stale-selector, outbound-request and criss-cross cases passed. Later Windows alias, Unicode browsing and heavy-operation deadline changes are covered by focused final checks; this 551.120s run is not claimed as a run of those later additions.

Pre-existing native regression: `go test -tags integration ./modules/hg -run 'Test(Browse|References|RunnerRealMercurial|History|FileHistory|Diff)' -count=1` -> PASS, 228.053s. Final ordinary suite, targeted deadline RED/GREEN and post-deadline native snapshot/merge results are recorded in the Task 1 report after completion.


Final deadline correction evidence: `go test ./modules/hg -run '^TestReviewHeavyOperationDeadline$' -count=1` failed at 0.952s with `deadline in 30s`, then passed at 0.779s after heavy operations inherited configured Runner session ceiling/caller cancellation. Post-fix native `go test -tags integration ./modules/hg -run 'TestReviewSnapshotDoesNotChangeTarget|TestReviewMergeCases/cross_branch' -count=1` passed at 51.769s. Final `go test ./...` passed all ordinary packages (hg 28.947s, cmd 115.811s, hosting 73.495s). Final safety/deadline focused tests passed at 0.805s. The expanded 551.120s native suite preceded these final timeout/alias changes; focused checks cover them, without claiming a later full-matrix run.


## Review follow-up I1: store-only modes do not require materialization

Against base `3de7e8b`, `go test -tags integration ./modules/hg -run '^TestReviewStoreOnlyModesDoNotMaterialize$' -count=1` failed in 37.376s: both modes incorrectly rejected `.hgsub` as an unsafe checkout. The native fixture contains a case-only rename, a UTF-8 Unicode path, a symlink, and .hgsub/.hgsubstate pointing at an instrumented HTTP endpoint; it is imported directly into stores without checkout.

Preparation now selects mode before preflight and applies representation checks only for `two_parent`, before allocating that workspace. The same command passed in 44.215s after the fix. Both store-only modes assert exact existing node identity/parents/branch, a null working parent and no materialized files, byte-for-byte unchanged public source/target stores, and zero HTTP requests. Existing unsafe-checkout refusal fixtures now explicitly diverge from their destination and assert a base different from both endpoints. Conservative two-parent restrictions and unavailable-platform debt remain unchanged. Affected native regression `go test -tags integration ./modules/hg -run 'TestReview(MergeCases|Checkout|NoOutboundSubrepositoryRequest|UnsupportedPathsRemainBrowsable|DoesNotRunExternalMergeTool|ArtifactsCannotReusePublishedPaths|HeavyOperationDeadline)' -count=1` passed in 283.490s. This includes the unchanged direct path-preflight tests and all revised divergent refusal graphs, merge modes/conflicts, malicious tools/configuration and output-ownership checks. Ordinary `go test ./...` passed all packages (hg 23.453s, cmd 101.557s). No broad native suite or unavailable platform/version row is claimed.

## Task 2: guarded publication and local recovery

The internal hosting operation now reserves both source and target IDs. The minimal `MutationExpectation` extension carries the source selector/node and immutable `PreparedMerge`. `ReviewPublication` persists operation ID, actor ID, bounded immutable payload, started/aborted/complete/terminal-failed/repair-required state; `ReviewReservation` has one primary-key reservation per repository. The payload contains no credential or reusable grant. `ReviewPublicationStatus` and pending reservation rows provide local reconciliation identity for Task 12 without fetching from a remote repository.

Reservation checks and native/browser session registration share one coordinator critical section. An already-registered native process (including a process still exiting) makes publication return `ErrMutationBusy`; publication does not kill it or assume rollback. Both endpoints are reserved together, same-repository endpoints are deduplicated, and database row acquisition is ordered by ID. No endpoint is held while waiting for another in-memory reservation. Reserved stores reject new managed sessions, including readers. Normal native hook authorization also refuses reserved stores. This proof assumes one hosting service owns the managed repositories; an additional independently running hosting process or arbitrary filesystem writer is outside it.

The operation/reservation transaction commits before dispatching any publication command. Before dispatch, validation failures atomically abort the operation and remove its reservations. Once `Started` commits, command/callback failures are ambiguous. Valid-actor interrupted operations retain reservations and resume the same identity. Revoked authority or an explicitly lost/invalid artifact instead triggers the observational terminalization described below. The actor and payload must match on every retry. Completed identity is retained, and an original-operation retry cannot prepare or create another merge.

Deletion takes the same database row lock before checking reservations and setting the deleting state. It refuses a pending publication without tombstoning the repository, so the original operation can still recover. Startup loads reservations before lifecycle reconciliation or transport construction. Deletion reconciliation independently refuses any reserved repository. Shutdown closes admissions and tracks active publication callbacks in the existing session registry; it waits for them and cancels their context when the graceful deadline expires before closing the hook controller.

### Native command boundary

The private prepared artifact is validated before `Started`, including selected target identity, source/target nodes, result mode, destination branch and ordered two-parent result. Both live endpoints and source-read/target-write/account authority are checked again under the reservation. `pretxnopen` repeats live authority and exact endpoint checks while Mercurial holds its store lock. A separately bounded one-process inspection runner avoids waiting for the publication command's occupied process slot. This adds at most one inspection process to the configured normal runner capacity. Hook callback deadlines still fail closed.

Publication uses these native commands:

1. In the private prepared store: `bundle --type v1 --base TARGET --rev RESULT BUNDLE`. Bundle1 carries only the changegroup, not bookmarks or bundle2 remote-changegroup/configuration parts.
2. In the target store, without checkout: `unbundle BUNDLE`.
3. For a bookmark destination only: `bookmark --inactive --force --rev RESULT -- NAME`.

These are separate transactions, not an atomic CLI sequence. The durable fence spans both. Publication checks the exact result and selected destination afterward, then commits completion/removes database reservations before reopening in-memory admission. Result presence alone does not mean bookmark publication completed. The source bookmark and every unrelated bookmark remain untouched. A same-store source named-branch head disappearing because this operation's exact two-parent result subsumed it is accepted only after `Started`, while its fence remains held.

Capabilities are bound in controller memory and in the hook envelope to one operation ID, repository and live actor check. A generic transport capability cannot be presented as an operation capability; an operation capability cannot be used without its matching identity. Publication callbacks accept only `pretxnopen`. Production writer hooks remain transaction-open/prepushkey authorization; no transaction-close veto was restored. The test-only close barrier used for crash injection is not installed by production.

### Recovery and crash boundaries

Before retry, the service checks native locks and journals. An already-started target operation may invoke bounded native `debuglocks` **without force options**, followed when necessary by `recover --verify`. Mercurial acquires/releases its own store/working-state locks and reaps only lock owners it identifies as stale. Go never removes a native lock/journal, holds an external Mercurial lock around a child, or runs a custom Python helper. Current target-write/source-read authority and both durable reservations are required. A live/foreign owner, failed recovery/verification, or unexpected selectors cannot be reported as recovery success. Valid-actor interrupted operations stay pending. A revoked actor or explicitly lost/invalid artifact is handled without further repository mutations: a complete observed result becomes historical completion; an intact, quiescent incomplete result becomes terminal failed; uncertain target state becomes terminal repair-required. Terminalization releases a distinct untouched source, and retains only the target reservation when repair is required. Same-store source/target uses that one target quarantine. Terminal IDs cannot resume or be upgraded by a late completion.

Current-version source inspected in Mercurial 7.2.4: `localrepo.recover` acquires `self.lock()` before transaction rollback; `debugcommands.debuglocks` with no options nonblockingly acquires/releases both locks; `lock._testlock` uses a break lock, rereads the owner and tests same-host PID liveness before removing a stale lock. The production command never uses `--force-free-lock` or `--force-free-wlock`.

The local crash test kills the real managed Mercurial child while its real unbundle journal exists, restarts the service, and retries the original operation through native recovery. This proves worker/process interruption on Windows, not power-loss durability, filesystem corruption repair, or Unix parent-death behavior. A lost response after the native result and bookmark are committed likewise retains the fence until same-ID reconciliation; retry adds no changeset. Revocation between history and bookmark transactions leaves authorized history present and the bookmark unchanged. After quiescent observation the operation becomes terminal failed and both stores are released; it never resumes under that ID, even if the original actor later regains access. A revoked interrupted transaction keeps only the uncertain target quarantined while restoring distinct source availability. No recovery command is run for a revoked actor, and no read-only source is ever repaired by this operation. No rollback promise is made for an already-authorized native transaction.

### Task 2 validation scope

Windows with Mercurial 7.2.4 is the locally exercised row. Native Linux/macOS, Mercurial 5.0.2/retained legacy-server checks, full native acceptance, race-detector runs, and real PostgreSQL/MySQL execution remain release requirements, not passes. SQL locking uses a current `SELECT ... FOR UPDATE` on PostgreSQL/MySQL and a transaction-held row update on SQLite; MySQL no-op changed-row counts are not assumed to indicate a matched row.

Earlier iteration results below capture RED/GREEN progression, not all final source. In particular the terminal-policy follow-up supersedes the earlier revoked-actor retry behavior. Final covering results are recorded afterward:

- Required initial RED command: `go test -tags integration ./modules/hg ./modules/hosting ./tests/integration -run TestReviewPublish -count=1` failed on the absent publication APIs. No native test was counted as passing at that stage.
- Native selected-bookmark/lost-response/initial busy-source/stale-preflight suite passed in 262.614s. Subsequent tests replace the initial registration-only race name with real native transports.
- Real Mercurial HTTPS and Mercurial/OpenSSH push races, native live/stale lock ownership, and reservation-aware deletion passed in 91.951s. Both transports reached a real held `pretxnopen`, refused concurrent publication as busy, then forced stale refusal after the writer committed, with one named-branch head and no prepared review history imported.
- Killed-native-journal RED failed with `publication store is not quiescent`; native recovery GREEN passed in 75.683s.
- Reserved-deletion RED exposed tombstoning of a pending repository; after row-serialized tombstoning, the focused deletion test passed.
- Invalid prepared-mode RED exposed history imported before rejection; artifact prevalidation GREEN plus bookmark-transaction revocation passed in 105.211s.
- Same-repository explicit named-branch heads and source/target bookmark cases passed in 153.137s; exact ordered merge parents, one resulting head, unrelated bookmarks and duplicate-free retry were asserted.
- Explicit operation capability identity RED failed on missing bound APIs; GREEN passed in 1.405s.
- `go test ./...` passed all ordinary packages (cmd 117.112s, models 32.092s, hg 25.481s, hgbridge 13.377s, hosting 82.408s). Final expanded native suite and any final focused self-review corrections are recorded below.

### Terminal partial results and Task 12 composition

The terminalizer rereads durable Started/state after native execution ends and uses conditional state transitions. It only inspects local target state; it does not finish a bookmark, recover a journal, reassign an actor, or synthesize authority. Startup applies the same observational classification for revoked pending operations or a known lost artifact before admission. Source-only read endpoints are never passed to native recovery. Valid-actor process interruptions and lost responses retain the original prepared result and operation ID and still use the proven same-ID native recovery path.

Task 12 must preserve original result/provenance when an operation is terminal failed. A fresh web attempt must not automatically create a second merge changeset. Require refresh/local resolution, or reuse that exact result only after fresh authority and current-state validation. Quiescent intact stores can be released for authorized native repair while the operation remains failed; uncertain/corrupt target state remains quarantined. No broad admin reassignment or repair UI is introduced by Task 2. Merge routes also must not keep their own browser-read registration while trying to reserve the same repository.

The first expanded covering run failed in 839.865s: source staleness, shutdown, both actual native transports and killed-journal recovery passed, but bookmark-revocation retry and both same-store cases hit the real five-second hook callback ceiling. Inspection originally launched roughly seven native processes per callback, including repeated isolation probes. The corrected implementation primes a success-only isolation capability check for the fixed configured runner during preflight and uses the same config-suppressed, fixed native templates for only the required reference family. Ordinary branch/bookmark callbacks now need three native reads (result, destination, source), without lengthening the five-second deadline or loading repository configuration. Repeated focused tests and the final covering run follow below.

Repeated native stabilization command `go test -tags integration ./modules/hosting -run 'TestReviewPublish(RevocationAtBookmarkTransaction|RevokedAfterCommitFinalizesFact|RevokedInterruptedTargetReleasesSource|SameRepositoryEndpoints)' -count=2 -timeout=15m -v` passed in 527.347s. Both cycles passed all selected cases under the unchanged five-second callback ceiling. Native revocation regressions prove source availability, terminal same-ID refusal, target-only quarantine for the killed journal, and historical completion after already-authorized commits. The first terminal implementation failed because it used a pre-dispatch Started snapshot; the corrected terminalizer rereads durable state and uses conditional transitions, with a unit test preventing terminal state resurrection.

Final-source ordinary `go test ./...` passed all packages (cmd 117.891s, models 30.710s, hg 26.037s, hgbridge 13.207s, hosting 88.920s). The final covering native command runs sequentially afterward; its result is recorded next. Errors after publication require rereading durable operation status: an observed complete result may be finalized even when the original response was lost or the actor was subsequently revoked.

Final-source required covering command `go test -tags integration ./modules/hg ./modules/hosting ./tests/integration -run TestReviewPublish -count=1 -timeout=25m -v` passed with native Mercurial 7.2.4 on Windows: hosting 872.378s, 23 top-level test families. modules/hg and tests/integration had no matching tests in this invocation and are not publication coverage. This sequential run followed the final ordinary suite without production/test changes. Cross-branch prepared-forgery rejection passed (43.98s), superseding its focused RED; lost-artifact startup finalization passed (50.46s), lost-response same-ID recovery (57.29s), real HTTPS/OpenSSH races (82.00s), killed-native-journal recovery (65.73s), revoked interrupted-target source release (43.46s), second-bookmark-transaction revocation (48.32s), native lock ownership (3.90s), already-committed historical completion (50.40s), and same-repository heads/bookmarks (136.12s). Reservation/lifecycle/startup/authority/terminal-replay/preflight/shutdown checks also passed. The unavailable platform, version, database, race-detector and full-acceptance rows listed above remain unpassed release requirements.
