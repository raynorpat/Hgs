# Compatibility and release evidence

Policy: Mercurial server >=5.0 and native clients >=2.0. Acceptance policy is not the same as an executed compatibility matrix. Prefer a maintained release; 5.0.2 needs the end-of-life Python 2.7 runtime in the planned legacy fixture. Intermediate versions are accepted but untested here.

| Native row | Actual status |
| --- | --- |
| Windows 11 IoT Enterprise LTSC 2024 10.0.26100.9711 amd64; Go 1.27.3, CGO_ENABLED=0; Hg 7.2.4 server/client; Python 3.13.9 | Task14 ordinary tests/vet/build passed; actual configured-process roles/restart/browse passed. Full tagged native gate **failed** on two Windows filesystem lock permission errors. Isolated C: rechecks passed without a production fix; root cause remains unresolved. |
| Windows extracted portable package + cold SQLite restore | Final combined native package/recovery command passed in 152.189s, including actual HTTPS issue creation, both transports and cold restore. Attribution-only final repack retained all 242 runtime entries byte/mode identical and passed a fresh 10.649s startup/diagnostic check. Earlier failures remain recorded below. |
| Linux/macOS native execution and package extraction | Unverified locally; CI configured, not evidence of execution. Unix executable permission/extraction and process-group cleanup need native validation. |
| Server 5.0.2 / client 2.0.2 | Unavailable locally (Python 2.7 absent); planned/configured matrix, not executed evidence. |
| Other server/client versions or architectures, FreeBSD/ARM | Unverified; successful cross-build is not platform support. |
| Race detector | Native compiler unavailable locally; CI configured, not executed evidence. |
| SQLite | Native acceptance exercised. |
| MySQL/PostgreSQL | Drivers/isolated CI fixtures present; no local native execution or restore evidence. |

## Reproducible commands

Build/archive from the source root with available pinned dependencies:

```text
go build -o dist/gogs.exe .
go test scripts/package.go scripts/package_test.go -count=1 -v
go run scripts/package.go -binary dist/gogs.exe -out dist/gogs-mercurial-windows-amd64.zip
```

Extract into a fresh directory outside the checkout. Supply absolute `GOGS_TEST_BINARY=<extracted>/gogs.exe`, `GOGS_TEST_ASSETS=<extracted>`, `GOGS_TEST_PACKAGE_DIRECT=1`, `GOGS_TEST_HG=<complete native hg executable>` and a fresh owned TEMP/TMP; run:

```text
go test -tags integration ./tests/integration -run '^TestPortablePackageRecovery$' -count=1 -timeout 10m -v
```

The test launches the actual extracted executable, with only extracted conf/templates/public, from a separate CWD. It uses isolated production-model-provisioned data and native clients, not the interactive installer. Set `GOGS_TEST_SERVER_PATH` to only the complete runtime directory (this Python installation needs its Python313 DLL directory). The server PATH excludes Git, sshd and ssh-keygen; a native OpenSSH client is separately required. Do not run this fixture against user data. Test cleanup owns only fresh temporary roots/processes. The package and backup evidence does not establish arbitrary Unicode paths or universal CWD independence; use absolute DB/TLS/custom paths and see [installation](installation.md).

## Task14 evidence carried forward

The full native command was `go test -tags integration ./cmd ./models ./modules/hg ./modules/hgbridge ./modules/ssh ./modules/hosting ./routers/hg ./routers/repo ./tests/integration -count=1 -timeout=60m`. It failed in `TestRealHookRejectsTransaction/current` (native store lock PermissionError before its hook) and `TestTransportMutationMatrix/current/https/reader/bookmark_delete,_legacy_pushkey` (expected denial/snapshot succeeded; subsequent `hg verify` could not lock). Isolated C: rechecks passed in 4.431s and 43.006s. No root-cause fix or whole-gate green claim. Earlier transient fake-Hg rename and HTTPS disconnect-polling failures also remain unexplained; Task14 retains those receipts.

Ordinary `go test ./... -count=1 -timeout=5m`, final `go vet ./...` and `go build ./...` have Task14 passing receipts. Task15 affected checks and fresh ordinary verification are recorded in its report. The transport evidence records older feasibility runs separately; authorization now checks writers at transaction open/key mutation with best-effort cancellation, not a close-time rollback promise. Task16 now has full-source acquisition and failed bounded ingestion evidence; no successful full-scale hosted result exists. The source checkout contains the report at `docs/mercurial/mozilla-central-benchmark.md`; it is not included in the release archive.

## Task15 native package and recovery receipts

Final Task15 verification passed: `go test ./... -count=1 -timeout=5m`, `go vet ./...`, `go build ./...`, and the native binary build. The rebuilt extracted-package command `go test -tags integration ./tests/integration -run '^TestPortablePackage' -count=1 -timeout 10m -v` passed in **152.189s** (diagnostics 8.64s; recovery 141.88s), exercising actual HTTPS login/CSRF/issue creation, HTTPS and SSH clone/push/pull, reader/stranger denials, clean restart and cold SQLite restore. The subsequent attribution-only final archive retained **all 242 runtime entries byte-for-byte and mode-for-mode identical**; its fresh extracted startup/diagnostic command passed in **10.649s**. This final evidence supersedes the intermediate package results below without erasing their failures, the unresolved Task14 native filesystem failures, or any unavailable platform/version row.

Executed on the Windows/Python/Hg versions above, with ACP=1252, executable/assets under `C:/Users/raynorpat/.codex/visualizations/2026/10/26/01a17590-c65d-79f2-8b54-b8061fd69373/task15/portable green café`. The process CWD and all DB/TLS/custom paths were separate owned C: fixtures. Server PATH contained only `C:/Users/raynorpat/AppData/Local/Programs/Python/Python313` (required by this Python installation's DLL loader); Git, sshd and ssh-keygen were absent. Empty server PATH exposed the missing-Python-DLL diagnostic, not a shell-free/runtime-free deployment.

`TestPortablePackageRecovery` passed in 190.22s: native HTTPS and embedded SSH clone/push/pull, writer permission, reader write denial with unchanged store snapshot, stranger private-read denial, issue page, clean restart, cold snapshot and new-path restore. Both applications exited naturally after targeted owned-group CTRL_BREAK; no forced stop counted as clean. The snapshot included a 245760-byte SQLite DB (no `-wal`, `-shm` or `-journal` remained), both whole ID-keyed stores, host key/config/TLS, install-root persistent-file canaries and file-backed sessions. Original DB/repository paths were disabled before restored startup. Nodes and `hg verify`, issue/account/access state, retained files, key bytes/fingerprint and protected owner-only host-key DACL matched; both transports were exercised again after restore. Fingerprint for that isolated fixture was `SHA256:BPsr7JGflzF/MMe4FQWjNfU82NvD49DjxuXfti4a2nQ`.

The initial restore attempt correctly rejected a café **repository-root** path; Windows repository roots require ASCII. The corrected fixture uses ASCII storage with a separate café executable/assets directory. No production path restriction was relaxed. The fixture's initial diagnostic used human-readable output where the runner requires JSON; that combined command failed even though recovery passed. The corrected separate `TestPortablePackageDiagnostics` passed in 12.072s: actual extracted startup reports a missing executable and rejects a controlled native fixture producing JSON version 4.9. This does not prove native Mercurial 4.9 or legacy 5.0.2 compatibility.

Packager tests passed in 23.339s: archive contents/permissions, sample-versus-custom separation, excluded private trees and both avatar caches, unsafe output placement/overwrite, executable identity, missing required assets, rejected runtime DLL/credentials/template extras. **Three symlink tests skipped** because this Windows account lacks symlink privilege; no privilege/security-setting change was attempted. Source rejection logic is present, but native symlink/special-file coverage remains incomplete here.

Fresh Task15 ordinary `go test ./... -count=1 -timeout=5m` failed only `TestHostingMonitorKeyTokenMetadataAndOriginalGrant` (`valid reader canceled after permission increase`, hosting 125.950s). An unchanged-source isolated recheck passed in 4.434s. A deterministic duplicate-check regression then confirmed the fixture reused a non-idempotent cancellation channel across registrations. It now uses a separate context cancellation per registration; the same duplicate-check case passes without a production authorization change. The original failed receipt is retained; final whole-tree verification is recorded in the implementation report. The affected command/model/runner/route packages passed in the full run. The full 22-minute Task14 native matrix was not repeated for this packaging/narrow avatar-path change. Final candidate-specific receipts are retained in the Task15 implementation report.


Actual HTTPS issue creation with multiple collaborators exposed reused generated `issue_user.id` values. A two-collaborator model regression reproduced it for both collaborator and non-collaborator posters; the minimal repair resets the auto ID before each insert, retaining recipient/assignee semantics. Model, existing issue workflow and rebuilt-package receipts are recorded in the implementation report. The package fixture creates the issue via HTTPS login/CSRF/form submission, rather than seeding the issue model directly. Persistent upload/avatar file canaries still do not claim a browser upload acceptance test.

## Task16 full-dataset limit evidence

On the pinned `c459032` Windows/Hg 7.2.4 package, native full streaming acquisition completed, but source `hg verify` exhausted its two-hour limit. HTTPS initial push exhausted two hours in native client preparation; SSH ended at the shipped 30-minute server-session bound during native receive staging. Both hosted stores remained empty. Five source-only startup/manifest/history samples per query succeeded, with unchanged full node/ref/requirements identity, but source integrity and all dependent full-scale hosted gates remain **FAILED/UNVERIFIED**. Task16 remains in progress; this is not a new platform, release or large-repository support claim. Detailed commands, timings, limits and remaining gaps are in the source-tree report named above. Existing Task14 failures and unavailable native rows above are unchanged.
