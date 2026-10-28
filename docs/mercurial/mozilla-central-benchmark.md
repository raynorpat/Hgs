# mozilla-central hosting measurements

**Task16 remains IN PROGRESS: full-scale acceptance is FAILED/UNVERIFIED.** The complete native acquisition succeeded, but source verification hit its two-hour bound, HTTPS never finished native client bundle preparation, and SSH hit the shipped 30-minute server-session bound during native receive staging. Neither hosted store acquired history. This report and harness are ready for review; they do not establish a verified full-scale host or a release. A user-approved 98-changeset UI smoke run passed ingestion and correctness; see "Reduced-scope UI smoke run".

The approved measurement-order exception allowed conditional hosting after the time-bound source failure, with full frozen node/ref/requirements identity checks. It never waived integrity verification. Undetected source damage could invalidate these observations. No failed full seed or source verification was retried, and no production optimization or limit increase was made.

## Dataset and environment

- Source: `https://hg-edge.mozilla.org/mozilla-central/`; native Hg7.2.4, no Firefox checkout/build, Git mirror or upstream writes. Native protocol access worked despite the HTML challenge.
- Acquired at 2026-10-27T01:22:28.4003393Z; local identity frozen at 03:41:17.1677499Z: **886,697 visible changesets**, three heads, all branch/closed-state/tag/bookmark metadata and requirements retained. Bookmarks were empty. Source `.hg` apparent size was 6,858,285,878B after acquisition and 6,858,391,996B after metadata/cache work; the difference is not evidence of changed history.
- Dataset tip: `374006e562b213c42f8c6e9ef28521447782fc32`. Complete revision-order node inventory SHA256: `08f971b72d19b56fd2caf392d92fb523ab12ebd3a1fe8c02d7c4d0f880f18788`. Full identity is in `frozen-source.json` and `source-verification-checkpoint.json`, explicitly `TIME-BOUND-UNVERIFIED`.
- The global tip is on tags-unified and contains only `.hgtags`. Representative code node on open default is **`6394817c482c7ed8baa4021802f4f231315ef0be`**, with 485,689 manifest paths. This does not reduce ingested history. Corrected browse/file-history sampling uses this separate node and the actual UI-generated history cursor.
- Windows11 IoT Enterprise LTSC10.0.26100, Ryzen9 3900X12 cores/24 threads,50,253,724KiB visible RAM, Go1.27.3 amd64/CGO_ENABLED=0. C: is NTFS on a Samsung MZVLB1T0HALR-000L2 NVMe SSD; `C:/Temp` is not a junction. Hosting used loopback HTTPS and SSH, separate from upstream CDN acquisition.

Owned data root: **`C:/Temp/gogs-mozilla-20261027`**, sentinel `gogs-task16-20261027-22bc36be`. This was a shared workstation, with warm filesystem state, concurrent unrelated work and explicitly recorded diagnostic/harness overlap. No disk-cold or exclusive-host claim, global cache flush, unrelated deletion or process kill.

The measured product is pinned to commit **`c45903230d3b6e83397ba57b8c92036c4225e5b5`**, exported into an isolated source snapshot with only Task16 test/homepage/compatibility overlays. The freshly extracted binary SHA256 is `acea006a3ff76564cf47f39c271bcdb743f08404d8ca74169cf64dfe5fc513c9`; archive SHA256 `cfbdfffd85b0de2cf1115ce023fa7f9e80f1f4011aee5186b49eb10315c89cdf`. Later concurrent commits were not silently mixed into measurements. Final report-only documentation updates are separate from that frozen archive.

## Completed attempts and failed gates

Each row is one attempt; failures/interruption are not successful latency samples.

| Operation | Wall seconds | Native CPU seconds | Native peak RSS bytes | Outcome |
| --- | ---: | ---: | ---: | --- |
| Initial compressed clone |3131.101|2779.516|3,527,782,400|Deliberately interrupted after diagnosis; incomplete source retained |
| Corrected stream clone |1528.541|846.578|1,057,259,520|Exit0, acquisition complete; integrity not established |
| Source ordinary `hg verify` |7200.184|2261.156|2,468,294,656|Deadline failure; integrity UNVERIFIED |
| Full HTTPS push |7200.091|1430.297|1,122,119,680|Client deadline during bundle generation; target empty |
| Full SSH push |1801.257|455.969|1,118,212,096|Client exit255 after server30-minute bound; target empty |

The initial52.185-minute attempt is part of the elapsed-work explanation, not omitted or presented as a successful clone. These five sequential major operations consumed about 347.69 minutes; orchestration, checks and diagnostics add time. Some checks overlapped long operations, so their durations must not simply be added again.

The first clone selected the advertised `19272f1f7a2c1a4bc07452f957d48d0b9a980197.zstd-max.hg` (2,845,608,025B) and reconstructed filelogs; compiled C modules were present. It was not an ordinary-wire or pure-Python fallback. The corrected `--stream --noupdate` clone selected the advertised stream-v2 bundle (6,774,723,329B). Native output reports 1,015,572 store files /6.23GB /1497.6s, followed by 158 catch-up changesets. These quoted native units differ from decimal byte counts. Mozilla documents the [streaming clone tradeoff](https://mozilla-version-control-tools.readthedocs.io/en/latest/hgmo/bundleclone.html). Separate 16MiB CDN/S3 range probes overlapped acquisition and are diagnostic observations, not isolated bandwidth or ETA measurements.

Source verification was ordinary full-repository `hg verify`, **not** experimental `--full`. It produced no complete stdout/stderr before timeout; silence is not integrity proof. The original receipt's erroneous ExitCode0 is preserved as a corrected harness defect; independent native evidence records exit1. No successful verification was fabricated or repeated. Native lock acquisition via bounded `debuglocks` reaped the proven dead verifier's stale lock; no force-unlock or history recovery was used.

HTTPS generated a **15,772,237,824B incomplete uncompressed HG20 temporary file** before transmission. Installed `httppeer.py:587–601` stages the complete payload first. Thus the two-hour delay measured native preparation, not Gogs applying history. The harness records context deadline/ExitCode-1; a separate held-process observer records actual native exit1.

SSH frames payload directly, but native `wireprotov1server.py:721–728` spools it before applying history. Receive staging ended at **5,868,797,952B**, also HG20 with no bundle-level compression parameters. The server log explicitly says `Mercurial session exceeded its time limit`; the backend exited after 1800.122s, and Gogs subsequently exited0 cleanly. The final 06:11:48.7961227Z snapshot, after process exits, showed only the private store's 83-byte `requires` file. No committed-history, rollback or corruption claim follows from this failed receive.

On2026-10-27, the two closed failed staging files were removed after owner, literal-path, size, header and exclusive-open checks, reclaiming21,641,035,776B. Their logs and metadata remain in `raw/failed-staging-reclaim-20261027.json`. The incomplete first source and complete frozen source remain retained. Neither failed staging file was a valid reusable bundle.

## Resource limits and observations

Shipping limits were unchanged: **8 workers,30-minute session,120-second idle,5-second queue,30-second browser command**. Client operations had 2-hour ceilings and `http.timeout=120`; the app supervisor was 4 hours and suite supervisor 8 hours. Writer hooks remained transaction-open/prepushkey only, with no writer-close veto; readers retained all-deny hooks.

Initial allocation reserved 139GiB for four 20GiB stores,8GiB staging,20GiB transaction space,1GiB initial raw output and 30GiB free-disk reserve. After acquisition, remaining allocation was 119GiB. Observed HTTP staging justified an explicit 8→16GiB staging estimate update; hard guards did not change. The failed HTTP file remained in used space, and SSH receive staging consumed the existing 20GiB transaction allowance. Only one sequential full reader clone was budgeted; none was created. Stream acquisition alone used an explicitly tighter 2GiB owned-RSS cap with 6GiB free-RAM startup; hosting retained8GiB startup,4GiB free-RAM reserve,8GiB owned-RSS cap and 30GiB free disk.

Across 4261 conditional-suite resource samples, maximum observed owned-subtree RSS was**1,349,304,320B**, minimum shared free RAM**8,834,482,176B**, and minimum C: free space**117,510,746,112B**. No resource-supervisor guard fired. Drive free-space changes include other workstation activity and are not owned-store growth or transfer bytes; summed per-process peaks are not a simultaneous memory peak.

A separately human-approved Defender trace overlapped native HTTP preparation from 04:39:10.7484701 to 04:39:27.0514975Z. Its sanitized summary records 1643 Hg-triggered scans,12.9182671 cumulative scan seconds, maximum 0.6131811s; the five most expensive owned paths were source revlog indexes under `mobile/android`. This proves scanning of this dataset, not its share of total wall time. Scan durations may overlap. **No exclusion or protection setting changed**; unrelated trace paths remain private.

## SSH process costs

Comparable interval: **05:46:23.3672410Z–06:11:33.2851438Z**,1509.9179028s. Receive staging grew 4,039,606,272B, about 2.675MB/s. This is partial native payload staging growth, **not measured network wire throughput**. Gogs totals below are interval deltas, excluding its earlier startup/HTTPS lifetime.

| Process | CPU seconds | Kernel CPU seconds | Read operations | Write operations |
| --- | ---: | ---: | ---: | ---: |
| native-client | 325.750 | 129.297 | 259,232 | 1,973,234 |
| openssh-client | 458.938 | 315.078 | 1,995,421 | 0 |
| native-backend | 43.328 | 34.250 | 1,989,376 | 493,309 |
| gogs | 152.625 | 90.828 | 0 | 1,989,812 |

Gogs wrote 4,046,128,920 process-I/O bytes in 1,989,812 operations, about 2033B/write; native backend read counts were similar. Native SSH emits 4096B payload frames plus separate length headers. This supports investigating pipe/framing overhead, but does not prove a Go bottleneck or a batching speedup. OpenSSH consumed more CPU than Go, and client generation/file access remained coupled to transport. OS process I/O counters are not wire-byte counters. No Go allocation/blocking profile or exclusive component-latency attribution was obtained.

## Five-sample source-only baselines

After the original suite and transport workers exited, exactly five samples per query ran with 30-second operation bounds and a10-minute outer/resource guard. The cohort took 103.587s including full node/ref/requirements revalidation before and after. All 15 queries succeeded with stable output hashes; both identity checks matched the original 886,697-node inventory. No source verification was rerun. The test still exited1 because its source-integrity gate remained failed.

These are **source-only, warm shared-workstation** observations, not hosted/UI measurements. Each sample starts a fresh native process; the filesystem is warm. No percentiles are justified from five samples, and subtraction from future UI times would not prove causal component cost.

| Native query | Successful/attempted | Min s | Median s | Max s | Output bytes each |
| --- | ---: | ---: | ---: | ---: | ---: |
| startup | 5/5 | 0.430 | 0.443 | 0.459 | 47 |
| code-manifest | 5/5 | 5.356 | 5.458 | 5.540 | 38,178,103 |
| global-history | 5/5 | 0.501 | 0.514 | 0.545 | 30,721 |

Queries: `version -T json`; `manifest -r 6394817c482c7ed8baa4021802f4f231315ef0be`; `log -l 30 -T json`. All use isolated HGRCPATH/HGPLAIN and `--config http.timeout=120 --config http_proxy.host=`. Median first stdout observations were 0.419s,1.950s and 0.476s respectively; these are not HTTP TTFB. Push stdout was empty, so its recorded zero first-byte field means unavailable timing.

## Approved temporary Defender comparison

On2026-10-27, after the original uploads had ended, the user authorized an exclusion of **only** `source-stream/.hg/store` for up to10 minutes and another15-second local Defender recording. A separate native read workload sampled2,048 deterministic ASCII paths from the frozen code manifest and requested their sizes using NUL-delimited native patterns. It did not retry verification or an upload. These source-only results do not replace the full-workload measurements above.

| Source-store scanning state | First query s | Second query s | Native CPU s, first / second |
| --- | ---: | ---: | ---: |
| Enabled, before exclusion |32.266|14.289|16.453 /14.094|
| Temporarily excluded |13.244|13.647|12.594 /11.734|
| Enabled, after restoration |24.812|14.074|15.344 /13.922|

All six principal queries and three trace-load repeats exited0 with identical170,151-byte output and SHA256 `3e886c16a068b425180bcda7b23874c3546b1682c46f57cd561292c51a32f3a5`. Native executable/version and node/ref identities were checked; source integrity remains FAILED/UNVERIFIED. The fixed sample is not all historical filelogs. Shared-workstation load, sequential order and cache warming remain confounders; two samples per state do not support percentiles or a causal estimate of the original two-hour delays. Warm protected and excluded reads were all roughly13–14s. Slower first protected reads are consistent with scanning/cache effects, not proof that exclusion solves the full ingestion problem.

The exclusion was active from11:39:19.5292305Z to11:40:29.4281050Z (**69.899s**), restored early when the fixed workload ended. A one-time SYSTEM removal task had been registered before adding it, with a10-minute maximum; the helper removed that task after confirming restoration. Its elevated receipt confirms the exact exclusion absent, other exclusions unchanged, and antivirus/real-time protection enabled. No permanent exception remains.

The requested15-second recording contains16.769684s of events, overlapping portions of two native trace-load queries. Its complete reported file groups contain **zero recorded source-store scans and zero Hg-attributed scan process groups**; Hg processes were actively running. Raw ETL/CLIXML stays local because it can contain unrelated paths. Sanitized timing, trace aggregates and restoration evidence are under `raw/av-comparison-20261027T1140/`, including `sanitized-summary.json`, `samples.jsonl`, `restored.json` and `completed.json`. The pattern SHA256 is `39d18a8f737c081922839699beb318d62bffc90dbc71a4b9f4b712bab10b4426`. This trace uses a different workload from the earlier bundle trace; their event rates are not a controlled full-push comparison.

## Native and SSH profiling follow-up

The user authorized further profiling and completion work on2026-10-27. No product optimization or shipping-limit change was made. These diagnostic workloads are separate from the failed full-scale attempts.

The fixed2,048-file size query has a demonstrated CPU cost: native `path:` patterns cause a walk of485,689 manifest paths and4,836,556 regex matches. The deterministic profile attributes10.671s self time to regex matching and13.982s inclusive to matcher calls; those overlapping times must not be added. A sampling run took13.184s wall/13.094s CPU with identical output. This is one revision's manifest, not all historical filelogs. Exact native `filepath:` patterns are a candidate experiment, with compatibility and raw-path behavior still to verify; no speedup was measured. Compiled C parser/mpatch and zstd execution was confirmed. Missing Rust alone is not a demonstrated cause.

Separate30-second graceful-interruption profiles reached only changelog verification and initial changelog bundle emission. The verifier recorded235,081 changelog-read calls; the generator recorded all-history discovery plus early revision emission. Both prefixes were CPU dominated, but neither reached manifest/filelog processing. They do not explain the original120-minute wall times. Both exited255 intentionally, released their locks and retained profiles; the partial67,674,299-byte bundle is explicitly invalid and nonreusable. Windows native sampling requires the CPU clock here: its real-clock profile reported zero wall weights and no useful hotpath tree.

Thirty-nine byte-checked16MiB synthetic pipeline cells tested default pipes against64KiB pipes. Increasing capacity improved a direct pipe case, but the measured production `ServeStdio`, Go SSH and Windows OpenSSH cases changed from0.102/0.224/1.335s to0.097/0.217/1.342s. Enlarging the producer-to-OpenSSH pipe also showed no benefit. These hash-sink experiments exclude history generation and native apply; they provide no basis for a pipe-buffer product change.

Six genuine small native pushes through production SSH/stdio, packaged writer hooks and the real authorization callback succeeded with exact node/content checks. Unprofiled whole-push times were2.927–3.118s. The profiled native client spent1.155s in8,215 file writes,0.143s in12,340 pipe-peek calls,0.058s in generation and about1s waiting in the reply path. These costs overlap. Process polling every50ms materially inflated the diagnostic parent's CPU; the1s control reduced that overhead. Parent CPU therefore cannot be attributed solely to the Go relay. Encrypted socket reads commonly returned2,084 bytes; these are syscall return sizes, not parsed SSH packet lengths.

Three further genuine native AES128-GCM pushes passed the same identity/hook checks, but their fresh default-cipher controls failed memory preflight. There is no valid cipher speed comparison and no cipher change recommendation. All scratch private keys and diagnostic processes were removed.

A prepared full ordinary verification with10Hz native CPU sampling and5s process observation did not start: fresh free RAM was3.94GiB, below the unchanged4GiB reserve. A later tiny harness probe was also stopped on resource pressure with about250MiB in its owned process tree. No WSL distribution was running during a subsequent low-memory observation. Shared host memory, rather than proven benchmark memory growth, currently blocks these new runs; no unrelated process was stopped and no reserve was lowered.

Evidence: `raw/native-profile-20261027T1230/`, `raw/native-partial-profile-20261027T1230-retry1/`, `raw/ssh-pipe-profile-20261027/`, `raw/ssh-native-small-20261027/`, `raw/ssh-cipher-small-20261027/` and `raw/native-full-verify-20261027T130915/`. Source integrity and full hosted acceptance remain FAILED/UNVERIFIED.

## Reduced-scope UI smoke run (approved 2026-10-27)

On 2026-10-27 the user replaced the full-history requirement with a smoke run of only the latest 100 changesets on `F:` (`F:/gogs-mozilla-smoke-20261027`, sentinel `gogs-task16-smoke-20261027`). **This does not satisfy full-scale acceptance**, which remains FAILED/UNVERIFIED above; it checks that hosting, transports and the UI work on real Mozilla content.

Dataset: `hg convert` with `convert.hg.startrev=886595`, end `6394817c482c7ed8baa4021802f4f231315ef0be` (rev 886694), and a filemap of the directories touched by those changesets (280 includes). Result: **98 changesets** (two became empty), one head `a94794d5cf0f` on default, 41,127 files, 211MB `.hg`, linear (no merges), all-ASCII paths, largest sampled file 246,995B. Node IDs differ from upstream, so no upstream identity claim. The source passed ordinary `hg verify` ("checked 98 changesets with 41761 changes to 41127 files"). The harness accepts it with `GOGS_TEST_MOZILLA_SUBSET=1`, which skips only the upstream clone receipt and default-URL check; the server `PATH` comes from `GOGS_TEST_SERVER_PATH` (venv Scripts plus the Python 3.13 directory).

Outcomes (Windows 11, hg 7.2.4 venv client, loopback, unchanged shipping limits):

| Test | Result |
| --- | --- |
| `TestMozillaCentralIngestion` | PASS: full push over HTTPS and SSH to empty stores, node/ref comparison |
| `TestMozillaCentralCorrectness` | PASS: HTTPS/SSH clone and pull, workflows, denial, raw identity, restart |
| `TestMozillaCentralPerformance` | FAIL only on named unverified rows: active-stream cancellation (largest candidate below 1MiB) and merge-diff (no merge in the subset); non-ASCII path also absent |

Initial push took about 101-125s per transport (full history needed over 2h on HTTPS and over 30min on SSH without finishing). UI samples (5 each unless a row says otherwise; all successful, warm shared workstation; min/median/max seconds):

| Operation | Min | Median | Max |
| --- | ---: | ---: | ---: |
| home | 19.850 | 20.219 | 20.709 |
| directory (`dom`, 16.7k files below) | 26.395 | 26.674 | 27.457 |
| repository history | 0.770 | 0.788 | 0.809 |
| file history | 1.244 | 1.307 | 1.372 |
| ref selection (302) | 0.734 | 0.743 | 0.786 |
| raw text (5 samples) | 0.810 | 0.825 | 0.893 |
| raw binary (5 samples) | 0.815 | 0.837 | 0.876 |
| raw large, 246,995B (5 samples) | 0.889 | 0.916 | 0.955 |
| native no-op pull, isolated HTTPS | 3.862 | 3.946 | 4.954 |
| native incremental push, isolated HTTPS | 9.935 | 10.094 | 10.327 |

Native baselines for the same repository: manifest 0.639/0.675/0.704s, history 0.418/0.427/0.473s, process startup 0.348/0.368/0.379s. Concurrent history readers: cohorts of 1/4/8/12 requests (one cohort each, so 1/4/8/12 samples, fewer than the five the plan asks for) gave medians 0.84/2.07/3.71/4.33s with no failed requests. The `incoming`/`outgoing` and denied-push exits in the sample file are the expected exit-1 and authorization-denial outcomes.

**Correctness fix exposed by the data.** The first correctness run returned 404 for every raw download. `SingleDownload` listed the whole manifest through the runner's 2MiB output cap (`MaxCommandOutput`), and this subset's `hg manifest -v` is 3,028,484B; full mozilla-central's is about 38MB, so raw download could never work there. The manifest scan was removed because `ReadFile` already reports a missing file before writing, and `Runner.Manifest` (now unused in production) was deleted with its tests. Attachment headers are now sent with the first byte, so a missing file returns a plain 404. Regression: `TestRawFileWithManifestOverCommandLimit` (fails on the old handler, passes now). The fake `hg cat` now exits 1 for empty content, so at that level `TestRawPathIdentity` only proves that a missing file maps to 404; path matching is covered by the native integration tests. The post-fix runs used a binary built from the working tree, not the pinned c459032 archive. A harness issue was also fixed: branch selection answers 302 by design and is no longer counted as an error.

**Observed bottlenecks on this data (ranked):** (1) home and directory pages take 20-27s against 0.7s native manifest; at 41k files this is roughly 30x the native cost and is the clear first target. Cause is not yet profiled; the flat-directory scan (`flatDirectoryScanLimit`, 32MiB) and per-request manifest reads in `modules/hg/files.go` are hypotheses. Validate by a Go CPU profile and the `home`/`directory` rows above. (2) A no-op pull costs about 3.9s versus 0.4s for a native query; unprofiled. Neither was optimized here.

Tables are reproduced from `samples.jsonl` with `GOGS_TEST_MOZILLA_SAMPLES=<path> go test -tags integration ./tests/integration -run TestMozillaSummarizeSamples -v`, which counts failed samples separately and excludes them from min/median/max (median is the upper middle value for even counts).

Raw evidence: `F:/gogs-mozilla-smoke-20261027/raw/` (`TestMozillaCentral*` directories; earlier failed runs under `failed-run1..3`).

## Browser page profile on the 98-changeset instance (2026-10-27)

A user-run demo instance (shipping limits, the post-fix binary, 41,127-file default tip) was observed per request by polling the native `hg` child processes while `curl` fetched each page. Windows start/end times are polled at roughly 0.25s resolution, one cold request each, on a shared workstation with other work running, so they locate the cost and are not samples. **No Go CPU profile was taken:** each request's wall time is accounted for by native process windows plus about 0.3s of process starts, which leaves little for Go to explain.

| Page | Wall s | Native processes | Where the time went |
| --- | ---: | ---: | --- |
| repo home (`/`) | 18.7 | 6 | `hg files -T '{if(flags, flags, ".")} {path}\n'` of the whole tree ran about 16.5s; five other commands (branches, refs, node lookup) about 0.3s each |
| top-level directory (`/src/<node>/dom`) | 24.3 | 6 | the same whole-tree `files` ran **twice**: 16.4s for the parent listing (`browseTree` lists the parent to learn whether `dom` is a directory) and 6.4s with `path:dom` for the directory itself |
| file view (`dom/base/AbstractRange.cpp`) | 3.0 | 5 | the parent directory listing about 1.3s; refs and node lookup about 0.3s each; `cat` fast |
| repository history (`/commits`) | 0.9 | 2 | two process starts |
| commit with diff, 4 files | 1.2 | 3 | the node lookup runs twice; `diff` about 0.3s |
| commit with diff, 39 files (504KB page) | 1.5 | 3 | `diff` about 0.3s |
| commit with diff, 155 files (322KB page) | 1.5 | 3 | `diff` about 0.3s |

Native baseline on the same store, three runs each (min/median/max s): `files -T '{if(flags, flags, ".")} {path}\n'` 18.54/18.73/18.83; the same `files` without a template 0.32/0.33/0.33; `files` with `path:dom` and the template 7.00/7.08/7.11; `manifest -v` 0.57/0.57/0.58 (it includes the flags); `version` 0.24/0.24/0.30; `log -r <node>` 0.27/0.27/0.28.

**Observed bottleneck:** the `flags` template keyword makes native Mercurial look up flags per file, which costs about 0.45ms per file here: 56x the cost of listing the same paths without it, and 33x the cost of `manifest -v`, which returns the same flags. It is the sole cause of the 18-24s home and directory times on this tree. Scaled linearly to the 485,689 paths of full mozilla-central it would be roughly 220s per listing, against the 5.4s measured for the plain native manifest. This comes from `Runner.listFlatDirectory` in `modules/hg/files.go`, which already filters entries by prefix in Go.

**Ranked next work (hypotheses with the evidence above; rank 1 was implemented and re-measured below):**

| Rank | Change | Expected effect | Validation |
| --- | --- | --- | --- |
| 1 | List flat directories from `hg manifest -v` (about 0.57s here) instead of `files -T {flags}`; keep the prefix filtering in Go. Check the 32MiB scan limit at full scale (the full manifest is about 38MB) | home and directory about 18-24s down to about 1-2s on this tree | rerun the home and directory rows; native `ListDirectory` tests with symlink, executable and non-UTF-8 names |
| 2 | `browseTree` lists the parent directory only to find whether the last path segment is a directory; it could read the target's listing directly and treat an empty result as a file check | removes the first listing, about 16s of the 24s directory time; a file view saves about 1.3s | directory and file rows; not-found and file-versus-directory tests |
| 3 | Each page starts 2-6 `hg` processes at about 0.27s each (the node is resolved twice on commit pages; refs are fetched after the listing) | about 0.3-0.6s per page; needs a design if it means a long-lived process (out of scope) | process counts per page in the table above |

The diff itself is cheap on these commits (about 0.3s for up to 155 files); commit pages are dominated by process starts. The converted root changeset (41,092 files) was not requested. Tree-manifest repositories take the `debugdata` path, which these numbers do not cover.

Two `hg clone http://127.0.0.1:64676/ C:\src\mozilla-central-http` processes (started 20:27, scoop `hg`, not launched by this work) were running throughout. They did not change the native timings noticeably and were left alone.

The full-history ordered run was stopped by the user's request during the source `hg verify`; the Mozilla clone had completed and no hosted push was attempted. Its one-shot lease at `F:/gogs-mozilla-full-20261027/ordered-run-lease.json` stays, so that root cannot be reused without review.

### Re-measurement after change 1 (manifest -v listing)

`Runner.listFlatDirectory` now runs `hg manifest -r <node> -v` and selects the directory in Go; the per-file `flags` template is gone. Same instance, store and node, rebuilt binary from the working tree, shared workstation (the full-history run was already stopped). Five consecutive `curl` fetches per page, all successful (min/median/max s); the "before" column is the single cold observation above, so the comparison is indicative, not a controlled A/B:

| Page | Before | After min | After median | After max |
| --- | ---: | ---: | ---: | ---: |
| repo home | 18.7 | 2.492 | 2.633 | 2.670 |
| top-level directory (`dom`) | 24.3 | 2.514 | 2.573 | 2.628 |
| file view | 3.0 | 2.213 | 2.244 | 2.309 |
| repository history | 0.9 | 0.753 | 0.773 | 0.803 |

Process polling of one cold request per page shows the remaining time: home runs 6 native commands (the `manifest -v` about 0.6s, the other five about 0.3s each); the directory page still runs `manifest -v` twice (once for the parent listing, once for the directory, about 0.6s and 0.3s) plus four 0.3s commands; the file view runs `manifest -v` once plus four 0.3s commands. Native `manifest -v` on this tree is 0.57s. What remains is mostly process starts, so ranked changes 2 and 3 are now the next candidates and are worth about 0.3-0.9s per page here.

Validation: `go test ./cmd ./routers/... ./modules/hg` and the native `go test -tags integration ./modules/hg` (real Mercurial 7.2.4, including symlink, executable and non-UTF-8 names) pass. At that point a full mozilla-central listing would still have exceeded the 32MiB buffer (a roughly 38MB plain manifest, more with `-v` modes); the streaming change below removes that limit for flat manifests.

### Re-measurement after change 2 (one manifest read per path)

`Runner.ListPath` replaces the router's parent-then-directory listing: a flat manifest is read once and answers both "is this a directory" and its children (tree-manifest repositories keep the two-step lookup). Same instance and method as above, five consecutive `curl` fetches (min/median/max s):

| Page | After change 1 (median) | After change 2 min | median | max |
| --- | ---: | ---: | ---: | ---: |
| repo home | 2.633 | 2.559 | 2.568 | 2.678 |
| top-level directory (`dom`) | 2.573 | 1.823 | 1.875 | 1.995 |
| nested directory (`dom/base`) | not measured | 1.781 | 1.847 | 1.984 |
| file view | 2.244 | 2.416 | 2.448 | 2.691 |

Controlled A/B for change 2 (the earlier rows above were taken while other work ran on the workstation): binaries built from `2213601` (change 1) and `0e39de8` (change 2) alternated over two rounds on the same instance, with the server restarted for each, one warm-up request, and six requests per page per round (12 samples each, nothing else running; min/median/max s):

| Page | Change 1 | Change 2 |
| --- | --- | --- |
| repo home | 2.451 / 2.540 / 2.661 | 2.470 / 2.559 / 2.626 |
| top-level directory (`dom`) | 2.376 / 2.412 / 2.497 | 1.724 / 1.837 / 1.919 |
| nested directory (`dom/base`) | 2.379 / 2.464 / 3.090 | 1.732 / 1.766 / 1.839 |
| file view | 2.161 / 2.255 / 2.396 | 2.166 / 2.251 / 2.363 |
| missing path (404) | 1.020 / 1.064 / 1.119 | 1.022 / 1.070 / 1.136 |

Change 2 saves about 0.6-0.7s on directory pages (one `manifest -v` instead of two) and leaves the home, file and 404 pages unchanged, as expected. The apparent file-view regression in the unaccompanied samples above was workstation noise.

Validation: `go test ./cmd ./routers/... ./modules/hg` and native `go test -tags integration ./modules/hg` pass, with new `TestListPathFlatReadsManifestOnce` and `TestListPathTree`.

### Streaming flat-manifest scan (removes the 32MiB buffer)

`flatScan` in `modules/hg/files.go` now consumes `hg manifest -v` as a stream instead of buffering it, keeping only the immediate children of the requested directory and the entry for the path itself. The byte cap is gone for flat manifests; memory follows the directory being shown. Replacement bounds: the 30s browser command timeout, at most 100,000 distinct children (`ErrOutputLimit`), and a 64KiB line limit. Tree-manifest directory texts keep their 32MiB bound (`flatDirectoryScanLimit`, now tree-only), since each is one directory's text.

Validation: new tests feed the fake `hg` a 33MiB-plus manifest (directory, file and root lookups correct past the old limit), an over-long line and 100,001 root children; `go test ./...`, `go vet ./...` and the native `go test -tags integration ./modules/hg` pass. On the demo instance (41k files, nowhere near the old limit) responses are byte-identical in size and timing is unchanged (home median 2.56s, `dom` 1.84s, file view 2.30s, 404 1.07s; six requests each), as expected.

**Not measured:** full mozilla-central browsing. The hosted full store does not exist, so the first-page cost is a hypothesis: the native manifest alone took about 5.4s for 485,689 paths in the earlier source-only baseline, plus scanning 485k lines in Go. The 100,000-child cap also means a single directory with more entries now returns an error rather than a truncated list.

## Outstanding acceptance and follow-ups

**Unverified/blocked:** hosted full node/ref/bookmark/requirements/manifests/content comparison; hosted/clone native verification; full clones and pulls on both transports; five hosted incremental/UI samples; reader/writer denial on full stores; rename/merge/history pagination during writes; large raw streaming/cancellation; full-history issues/attachments and restart persistence; beyond-capacity concurrency/queue behavior. Hosted/UI successful sample count is zero. The original compiled suite later exercised issues/attachments on an empty fixture after cached seed failures; those receipts are not full-history evidence. Linux/macOS, legacy native versions and unresolved Task14 filesystem failures retain their [compatibility statuses](compatibility.md).

The corrected harness can preserve/adopt a proven successful HTTPS fixture once, validate SQLite/static identities/ACLs, run pristine reads before workflows and keep pulls off the frozen source. This path passed a tiny actual-package recovery probe but was not exercised with Mozilla because no seed succeeded. Representative content selection uses one complete manifest plus at most 64 deterministic media size probes; "large" means largest among those candidates, never global-largest. Small/finished responses leave active-stream cancellation unverified.

Ranked next work, **not implemented or proven improvements**:

| Rank | Evidence and next change/experiment | Expected impact/confidence | Risk, owner and validation |
| --- | --- | --- | --- |
|1|Profile native preparation/verification and compare native builds/platforms on the same snapshot. HTTP spent120min before hosting; source verify also exhausted120min. The approved small scanning experiment above did not establish a large warm-read benefit or quantify either delay.|Highest observed blocker; full-workload cause remains unresolved.|Diagnostic first; native runtime/benchmark environment and this harness. Preserve identity and security consent; require completed native verify and full initial pushes before claiming benefit.|
|2|Reach the late manifest/filelog stages with native CPU profiling and stage/resource observations. Small SSH experiments found no useful larger-pipe benefit; avoid speculative relay batching.|Full-workload cause remains unresolved. Client writes/waits are measurable on small inputs, with no exclusive attribution to Go.|Diagnostic first; this harness and native runtime. Preserve source identity, process/resource bounds and full-workload receipts. Any later relay change still requires byte-exact handshake/EOF/cancellation/backpressure tests and full native transport evidence.|
|3|Evaluate native `server.streamunbundle` separately to avoid receive-temp staging.|Potential disk/phase benefit only; does not fix HTTP client spool or source generation.|Native help warns it effectively prevents concurrent pushes. Own deployment/native configuration and integration tests; test locking, concurrent pushes, cancellation, hooks and supported native versions. Do not enable from this result alone.|
|4|Expose a safe, specific authenticated SSH timeout diagnostic instead of generic session-ended/Invalid-argument feedback.|Observed usability defect; no throughput improvement claimed.|Low/medium risk in `modules/ssh/ssh.go`; focused deadline-vs-cancellation/error tests, preserving wire behavior and excluding internal paths/credentials.|

The native manifest median5.458s also motivates measuring immutable-node caching/batched reads **after ingestion works**, but no hosted data currently justifies ranking a browser-cache optimization. Limits must not be silently raised to make a benchmark pass.

## Reproduction, evidence and validation

Raw artifacts remain under the owned root above, outside source/release assets. Key receipts: `raw/acquire.result.json`, `raw/stream-acquire.result.json`, `raw/source-verify-process-exit.json`, `raw/TestMozillaCentralIngestion-2169555946/samples.jsonl`, `raw/first-https-push-process-exit.json`, `raw/first-ssh-*-exit.json`, `raw/first-ssh-push-progress.jsonl`, `raw/measurement-summary.json`, `raw/native-baseline-summary.json`. Exact redacted arguments and output files are in JSONL; the frozen identity/acquisition/checkpoint files bind reuse. Failed original receipts are preserved verbatim.

```text
hg --debug clone --noupdate https://hg-edge.mozilla.org/mozilla-central/ <root>/source
hg --verbose clone --stream --noupdate https://hg-edge.mozilla.org/mozilla-central/ <root>/source-stream
hg -R <root>/source-stream verify
go test -tags integration ./tests/integration -run '^TestMozillaCentral(Ingestion|Correctness|Performance)$' -count=1 -timeout=0 -v
go test -tags integration ./tests/integration -run '^TestMozillaCentralNativeBaseline$' -count=1 -timeout=0 -v
```

Opt-in requires `GOGS_TEST_MOZILLA=1`, absolute `GOGS_TEST_MOZILLA_ROOT`, ownership/preflight receipts, supplied `GOGS_TEST_HG`, fresh extracted `GOGS_TEST_BINARY`/`GOGS_TEST_ASSETS`, `GOGS_TEST_PACKAGE_DIRECT=1`, complete native runtime PATH and owned TEMP/TMP. Ordinary CI explicitly skips the dataset. Native operations are bounded even with Go timeout0; the original suite requires the separate finite resource supervisor. Conditional source reuse additionally requires `GOGS_TEST_MOZILLA_ALLOW_TIMED_OUT_SOURCE=1` and this exact proven timeout checkpoint, not a generic skip. The optional retained mode rejects these failed-seed receipts and cannot replay them. Fresh future runs need a live budget; these historical receipts are not permission to consume arbitrary resources.

Pinned-baseline ordinary tests/vet/build and actual extracted-package recovery/diagnostics passed (combined recovery/diagnostics168.517s). Test-only regressions fixed native exit-status recording, flushed human-readable native status, bounded/cancellable inventory handling, cohort labels, source-timeout proof checks, code-node/cursor selection and fixture cleanup identity. The tiny retained rebind passed 11.62s within a15.899s focused run; final added-cohort focused checks passed 3.686s and tagged vet passed. The full conditional suite **failed** in 9160.724s (outer 9162.858s, supervisor failure=null). These statuses are separate; no broader 22-minute native wire suite was rerun for harness/docs.
