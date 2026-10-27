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

All source, target and base manifests are checked together BEFORE creating a preparation checkout. This catches cross-tree as well as within-tree case collisions. Symlinks, subrepository declarations/state, metadata components, traversal, non-UTF-8 raw names and unsupported host representations fail closed. Executable regular files are data; their executable bit does not cause execution.

Windows checks additionally reject forbidden characters/controls, trailing dot/space components, DOS device names (including whitespace before extensions), any `~` short-name alias syntax, and case collisions in file/directory prefixes. Windows and macOS currently have a deliberate conservative **ASCII-only preparation policy**. This does NOT mean all Unicode names are inherently unrepresentable. Existing `CheckPath` checks argument encoding, not raw manifest byte decoding, NTFS Unicode comparison/short-name equivalence or APFS normalization. A fully audited native representation/collision layer is not yet present. macOS ASCII refusal is an intentional policy, NOT native macOS evidence. This restriction also applies to fast-forward/already-integrated preparation because the same preflight is used for all modes. Non-ASCII/raw-name content remains browsable; authors can integrate locally.

The native Unicode fixture uses `import --bypass` to put a UTF-8 `漢字` file directly into the store. `Manifest` returns the exact name, while preparation returns `ErrUnsafeCheckout` before allocating a checkout. Current Windows fixture success does not establish Unicode materialization safety or any macOS support. Removing this policy requires separately proven lossless decoding and host collision rules, not assuming that a representable command argument suffices.

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
