# Transport and authorization feasibility evidence (Task 6)

Gate: FAILED — revocation-at-transaction-close does not hold for stream-clone bundles unbundled into empty repositories (design decision required); all other rows passed on Windows; Linux/macOS and 5.0.2/2.0.2 unavailable.

See [The gap: stream bundles survive revocation](#the-gap-stream-bundles-survive-revocation).

## Environment of the recorded run

| Item | Version |
| --- | --- |
| OS | Windows 11 IoT Enterprise LTSC 2024, 10.0.26100.9711, amd64 |
| Go | go1.27.3 windows/amd64, `CGO_ENABLED=0` |
| Mercurial (server and client) | 7.2.4 (pip, Python 3.13.9, pure Python) at `D:/github/gogs/.venv-hg/Scripts/hg.exe` |
| SSH client | OpenSSH_for_Windows_9.5p2, LibreSSL 3.8.2 (`C:/Windows/System32/OpenSSH/ssh.exe`) |
| Mercurial 5.0.2 / 2.0.2 | not installed (no Python 2.7); `GOGS_TEST_HG_OLD_SERVER` and `GOGS_TEST_HG_OLD_CLIENT` unset |

Every Mercurial process, server and client, ran through `modules/hg`'s runner
with an application-owned `HGRCPATH`; the operator's `hg` (7.0.3, with a broken
ambient `hggit` extension) was never used.

## Commands

```sh
GOGS_TEST_HG=D:/github/gogs/.venv-hg/Scripts/hg.exe \
  go test -tags integration ./tests/integration -run 'TestTransport|TestSupervision|TestKnownGap' -count=1 -v -timeout 60m
```

Wall time of the full run: 15m37s, result `ok` (TestTransportMutationMatrix
671 s, TestTransportRevocation 108 s, TestSupervisionHookCallbackUnderSaturation
59 s, TestTransportAmbiguousCompletion 47 s, TestTransportRemoteChangegroup 24 s,
TestKnownGapStreamBundleRevocation 17 s, plus building the binary). The
`modules/hg`, `modules/hgbridge` and `modules/ssh` integration tests also pass
with the new managed configuration. Most of it is
Python start-up of the per-request `hg serve` workers and snapshot commands.

The suite (`tests/integration`) assembles the real `hg.Runner`,
`hgbridge.Controller`, `hgbridge.HTTPBridge` behind a TLS frontend with Basic
authentication, `hgbridge.StdioBridge` and `modules/ssh.Server`, with an
in-memory authorizer. Native clients reach HTTPS with a self-signed localhost
certificate (`web.cacerts`) and SSH through Windows OpenSSH with a test key,
`-F none` and a pinned `known_hosts`. Requests no current client emits are sent
by test-only wire drivers (raw HTTP requests through the frontend; raw SSH
protocol v1 over `golang.org/x/crypto/ssh` to the embedded server).

`snapshotRepository` captures, with independent `hg` commands on the server
repository, `log --hidden -T json` (nodes, phases, bookmarks, tags), the
topological heads, `bookmarks -T json`, `tags -T json` and `debugobsolete`, plus
a SHA-256 of every file under `.hg` except `cache/`, `wcache/` and the lock
files. Reader cases require the snapshot to be identical. Refused writer cases
(revocation, unsupported) also ignore `.hg/journal.desc`, which Mercurial
writes only after `pretxnopen` passed and leaves behind when it aborts;
readers never pass `pretxnopen`, so their comparison keeps it.

## Outcomes

### Version and platform rows

| OS | Server | Client | HTTPS | SSH |
| --- | --- | --- | --- | --- |
| Windows | 7.2.4 | 7.2.4 | readers, writers, journaled revocation: passed; stream-bundle revocation: **gap** (known-gap test reproduces it) | same |
| Windows | 5.0.2 | 7.2.4 | unavailable | unavailable |
| Windows | 7.2.4 | 2.0.2 | unavailable | unavailable |
| Windows | 5.0.2 | 2.0.2 | unavailable | unavailable |
| Linux | all rows | | unavailable (not run; CI job written, never executed) | unavailable |
| macOS | all rows | | unavailable (not run; CI job written, never executed) | unavailable |

### Tests (Windows, 7.2.4/7.2.4)

| Test | HTTPS | SSH |
| --- | --- | --- |
| `TestTransportMutationMatrix` readers, 18 cases | passed | passed |
| `TestTransportMutationMatrix` writers, 17 cases | passed | passed |
| `TestTransportRevocation` bundle2 push | passed | passed |
| `TestTransportRevocation` bundle1 push | passed | passed |
| `TestKnownGapStreamBundleRevocation` (asserts the gap still exists) | passed: **gap reproduced** | passed: **gap reproduced** |
| `TestTransportAmbiguousCompletion` | passed | passed |
| `TestTransportRemoteChangegroup` (reader and writer: no fetch, state unchanged) | passed | passed |
| `TestSupervisionHookCallbackUnderSaturation` | passed | passed |

### Matrix cases

Each case runs for a reader (must fail, state identical) and a writer (must
succeed and make the expected change), over both transports. Before the cases,
each user clones and pulls over each transport through the deny hooks with the
server unchanged: serving a publishing repository opens no transaction.

| Case | Path exercised | Reader refused by (observed) | Writer |
| --- | --- | --- | --- |
| push changeset | bundle2 `unbundle` (changegroup, phase-heads, phase move of the server's draft) | HTTPS: hgweb push gate (403); SSH: `pretxnopen` | passed |
| push new head (`-f`) | bundle2 `unbundle` | same | passed |
| push changeset, bundle1 | bundle1 `unbundle`, then legacy `pushkey phases` | HTTPS: 403; SSH: `pretxnopen`, then `prepushkey` | passed |
| push tag | bundle2 `unbundle` (`.hgtags` changeset) | same as push | passed |
| bookmark create / move / delete | bundle2 `unbundle` with bookmark part | HTTPS: 403; SSH: `pretxnopen` | passed |
| bookmark create / move / delete, legacy pushkey | wire `pushkey bookmarks` | HTTPS: 403; SSH: `prepushkey` | passed |
| phase publish (phase only) | bundle2 `unbundle` with phase update | HTTPS: 403; SSH: `pretxnopen` | passed |
| phase publish, legacy pushkey | wire `pushkey phases` | HTTPS: 403; SSH: `prepushkey` | passed |
| push with obsolescence marker (client `experimental.evolution=all`) | bundle2 `unbundle`; server advertises no `obsmarkers` | HTTPS: 403; SSH: `pretxnopen` | passed; server stores no markers |
| wire pushkey bookmark | driver: `pushkey bookmarks` | HTTPS: 403; SSH: `prepushkey` | passed |
| wire pushkey phase | driver: `pushkey phases` | HTTPS: 403; SSH: `prepushkey` | passed |
| wire pushkey obsolescence markers | driver: `pushkey obsolete dump0` | HTTPS: 403; SSH: `prepushkey` | refused as required: obsstore is read-only ("creating obsolete markers is not enabled"), no markers stored |
| wire batch with pushkey | driver: `batch` = `heads ;pushkey bookmarks …` | HTTPS: 403 (batch checks each sub-command's permission); SSH: `prepushkey` | passed |
| wire unbundle stream bundle into empty repository | driver: `unbundle` with a `stream2` part | HTTPS: 403; SSH: `pretxnopen` | no writer control (no client pushes stream bundles); revoked writer: see the gap |

HTTPS readers never reach the hooks: hgweb's `checkauthz` refuses every
push-permission command before any repository code runs. Reader workers keep
`web.push_ssl` at its default, so the observed refusal is "403 ssl required"
on the loopback hop; `web.deny_push=*` would refuse next, and the deny hooks
remain installed behind both.

### TDD evidence

All coverage existed before the suite, so RED was shown by mutation: with the
deny hook changed to allow (`cmd/hg_hook.go`) and HTTPS reader workers given
`web.allow-push=*`, all 36 reader cases failed on the assertion
`operationSucceeded || !reflect.DeepEqual(before, after)` (33 with the
operation reported as succeeded, 3 caught only by the state comparison). The
mutation was reverted; the same cases pass.

The SSH hook-encoding bug (see "Other observations") was found by the first
matrix run: every SSH writer case failed with "The system cannot find the path
specified." RED: the new `modules/hgbridge` test
`TestStdioSessionConfigRunsHooks` failed with that message instead of the deny
hook's own. GREEN after the fix (commit `59ef0b3`), and the SSH writer cases
pass.

## Core wire write commands and the hooks that reject them

Mercurial 7.2.4 (paths under `mercurial/` of the installed package):

- `unbundle` (`wireprotov1server.py:692`, permission `push`). bundle1 payloads
  are applied inside `repo.transaction` (`exchange.py:2845-2849`); bundle2
  payloads open the transaction before the first part is processed, because
  `experimental.bundle2lazylocking` defaults to false (`exchange.py:2852-2870`).
  `localrepo.transaction` runs `pretxnopen` before it writes anything,
  including `journal.desc` (`localrepo.py:2289-2291`). Parts that write
  (`changegroup`, `remote-changegroup`, `bookmarks`, `phase-heads`, `pushkey`,
  `obsmarkers`, `hgtagsfnodes`, `stream2`, `stream3-exp`;
  `bundle2_part_handlers.py:79-643`) all run inside it, and
  `pretxnclose-bookmark`, `pretxnclose-phase` and `pretxnclose` run at close
  (`localrepo.py:2395-2420`). `obsmarkers` parts are ignored unless the server
  enables marker creation (`bundle2_part_handlers.py:538`).
- `pushkey` (`wireprotov1server.py:654`, permission `push`), and the bundle2
  `pushkey` part: `localrepo.pushkey` runs `prepushkey` before any namespace
  handler, for every namespace (`localrepo.py:3382-3393`). The `obsolete`
  namespace handler ignores the evolution settings
  (`obsolete.py:897-907`) but fails on a read-only obsstore
  (`obsolete.py:679`), which is what an unconfigured server has.
- `batch` (`wireprotov1server.py:201`, permission `pull`) checks the permission
  of every sub-command (`:214-217`). Over HTTP that is hgweb's `checkauthz`
  (`hgweb/common.py:200-222`: POST only, `deny_push`, `allow-push`); over SSH
  `checkperm` is a no-op (`wireprotoserver.py:459`), so batched `pushkey`
  reaches `prepushkey`.
- Every other command in the table is `pull`-permission and read-only.

Mercurial 5.0.2 (release tarball, sha256
`29b850792f1f054cece5b8de8e815dbbe266e1d11814c3755290c0879883c0f8`, read, not
run): the same structure — `pushkey` and `unbundle` with permission `push`
(`wireprotov1server.py:521`, `:551`), per-sub-command `checkperm` in `batch`
(`:183`), greedy bundle2 locking (`exchange.py:2408`), `pretxnopen` at
transaction start (`localrepo.py:1785`), close hooks (`localrepo.py:1872-1887`),
`prepushkey` before namespace handlers (`localrepo.py:2867`), part handlers in
`bundle2.py:1805-2285`, and the same `obsolete` pushkey handler
(`obsolete.py:813`) and read-only obsstore check (`obsolete.py:625`). 5.0.2
behaviour is source-derived only.

## The gap: stream bundles survive revocation

This is why the gate failed. `TestKnownGapStreamBundleRevocation` reproduces
it on both transports and asserts the current, unsafe behaviour, so it fails
visibly once Mercurial or a later design closes the gap.

### Reproduction

1. Make a stream-clone bundle of any repository:
   `hg bundle -a -t "none-v2;stream=v2" stream.hg`. The result is a bundle2
   (`HG20`) with one `stream2` part.
2. Create an empty server repository and serve it to a writer.
3. Send it with the test wire driver as one forced `unbundle`
   (`heads=666f726365`, hex of `force`). Over HTTPS that is a POST of
   `?cmd=unbundle&heads=666f726365` with the bundle as body
   (`Content-Type: application/mercurial-0.1`) through the frontend. Over SSH
   it is `unbundle` / `heads 10` / `666f726365`, then the bundle as one
   length-prefixed chunk and `0`, on `hg -R owner/<repo> serve --stdio`.
4. The in-memory authorizer allows the first hook callback (`pretxnopen`) and
   then revokes the writer.

Observed (Windows, 7.2.4): the second callback (`pretxnclose`) is refused,
Mercurial prints "transaction abort!" / "rollback completed", and the client
gets an error. Even so, the repository then holds the bundle's complete
history (every changeset, as public, with its tags), and `.hg/requires` is
rewritten from the bundle's requirements.

### Code path (Mercurial 7.2.4)

- `wireprotov1server.unbundle` → `exchange.unbundle`: the bundle2 path opens
  the transaction before any part is read (`exchange.py:2852-2870`), so
  `pretxnopen` runs first and only once.
- `bundle2_part_handlers.handlestreamv2bundle` (`:621-634`): it aborts only if
  the repository is non-empty, then calls `streamclone.applybundlev2`.
- `streamclone.applybundlev2` (`streamclone.py:1686-1713`) calls `consumev2`
  (`:1131`). `consumev2` opens a nested `repo.transaction(b'clone')`, which
  joins the outer transaction and runs no hooks, and writes every store file
  directly through the vfs or `_write_files` (`:1593-1610`, plain `os.open`
  and `os.write`). It never records them in the transaction journal.
  `applybundlev2` then calls `scmutil.writereporequirements` (`:1712`).
- When `pretxnclose` refuses, the abort rolls back only journaled files. The
  stream-written store files and the new `requires` stay.

### Why hooks and configuration cannot stop it

- The only veto points are `pretxnopen`, which runs before the bundle is read,
  and the close hooks. The close hooks do refuse, but their refusal relies on
  a rollback that does not cover these files.
- No setting disables the `stream2` part handler; `server.bundle2.stream` only
  controls what the server offers to pullers. Lazy bundle2 locking would not
  help either.
- The hooks see no information that would tell a stream bundle apart from a
  first push of changesets into an empty repository.

### Variants

| Variant | Affected | Basis |
| --- | --- | --- |
| bundle2 `stream2` part in `unbundle` | yes | tested, both transports |
| bundle2 `stream3-exp` part | yes, by source | `handlestreamv3bundle` (`:637-650`) → `applybundlev3`/`consumev3` (`streamclone.py:1613-1685`, writes through the vfs; `writereporequirements` at `:1738`); not tested |
| bundle1 `HGS1` stream bundle as the `unbundle` body | apparently not, by source | `exchange.readbundle` returns a `streamcloneapplier` (`exchange.py:102`), but `bundle2.applybundle` calls `cg.apply(repo, tr, source, url)` (`bundle2.py:388`, `:523`) while `streamcloneapplier.apply` takes only `repo` (`streamclone.py:617`), so it should fail before writing; not tested |

### Who is and is not affected

- Affected: a writer who has write access when the push's transaction opens
  and loses it before the transaction closes, pushing a hand-built stream
  bundle into an empty repository. No Mercurial client sends one. The writer
  could have pushed the same history a moment earlier; what fails is
  revocation at transaction close.
- Not affected: readers on both transports (`pretxnopen` refuses before the
  bundle is read: matrix case "wire unbundle stream bundle into empty
  repository"), non-empty repositories (the handler aborts), and every
  journaled write path (`TestTransportRevocation`).

### Candidate remedies (not implemented; the user decides)

1. Accept and document it as a known limitation: revocation is enforced at
   transaction close except for stream bundles into empty repositories.
2. Refuse writer pushes into empty repositories at `pretxnopen`, and have the
   application seed initial content through a different flow (for example a
   first push through a restricted path, or an initial commit made by the
   application). This changes the "create an empty repository, clone, push"
   workflow.
3. Ask upstream Mercurial for an option that disables applying stream bundles
   through `unbundle` (or journals them), and require that version.
4. A restricted architecture change: filter `unbundle` bodies, or install an
   application-owned Mercurial extension that rejects stream parts. Filtering
   in Go conflicts with the design's rule against parsing the wire protocol,
   and over SSH it is impossible without parsing the stdio stream. An
   extension conflicts with "no custom Python helper".

## Other observations

- Fixed during this task: the SSH session hook files were written as UTF-8,
  but Mercurial on Windows decodes hook commands with `HGENCODING` (the ANSI
  code page) before passing them to `cmd.exe` (`utils/procutil.py:488`,
  `:515-526`). With the application binary under a non-ASCII path, every SSH
  hook failed to start: writers could not push and readers were refused only
  because `cmd.exe` failed. `hgbridge.NewStdioBridge` now writes those files
  in that encoding (`hg.LocalBytes`).
- Fixed during this task: server-side fetches through `remote-changegroup`.
  See the next section.
- Windows OpenSSH prints `client_loop: send disconnect: Connection aborted`
  or `Connection to … closed by remote host` when the embedded server closes
  the connection after the exit status. Pushes are unaffected; the tests ignore
  exactly those lines.

## Server-side fetches through remote-changegroup (fixed)

A bundle2 `remote-changegroup` part tells the server to download a bundle
from an `http` or `https` URL that the client names, and to apply it.

- In 7.2.4, `bundle_caps.capabilities` advertises the part by default
  (`exchanges/bundle_caps.py:32`). `get_repo_caps` never removes it
  (`:40-90`).
- `handleremotechangegroup` (`bundle2_part_handlers.py:153-235`) accepts it
  whenever the scheme is `http` or `https`, and opens the URL with
  `url.open(op.ui, …)`. That call happens inside the push transaction, but
  before any changeset is applied.
- 5.0.2 behaves the same (`bundle2.py:1507`, `:1851-1873`).
- No setting disables the part or its handler.

Without a fix, any writer could make the server send a GET request to an
address of the writer's choosing, including loopback services and other
users' `hg serve` workers. That is a server-side request forgery. It is
mostly blind, but the error the writer sees reveals the target's HTTP status
(observed: "abort: HTTP Error 404: Not Found"). For responses that start with
`HG`, it also reveals the next two bytes (`exchange.readbundle`).

`TestTransportRemoteChangegroup` sends this part with the test wire driver,
pointed at a local `httptest` server, as a reader and as a writer over both
transports. It requires that no request reaches the target and that the state
is unchanged.

- RED, before the fix: both readers passed. They were refused at hgweb's
  push gate (HTTPS) or at `pretxnopen` (SSH), before the part was read. Both
  writers failed with "the server fetched the client-named URL 1 times".
- Fix, within the architecture: the managed configuration (`modules/hg`)
  now sets `[http_proxy] host = 127.0.0.1:0` and `always = true`.
  - `url.opener` then sends every HTTP and HTTPS request Mercurial makes
    through that proxy, loopback included, because `always` empties the
    no-proxy list (`url.py:131-178`; the same code in 5.0.2 `url.py:122-137`).
  - Nothing can listen on port 0, so the connection fails. On Windows the
    error is "The requested address is not valid in its context"; on Unix,
    `connect` to port 0 is refused (source-derived, not run).
  - The runner passes no `http_proxy` or `no_proxy` variables to Mercurial.
  - A Gogs server never needs Mercurial to make outbound requests: mirrors
    and remote imports are out of scope.
- GREEN: no request reaches the target on either transport, for readers or
  writers. The writer's transaction aborts and only `journal.desc` remains.
- Unit test: `modules/hg` `TestManagedConfigBlocksOutboundFetches`.
- Test clients that do need HTTP (this suite and the `modules/hgbridge`
  integration tests) override the setting with `--config http_proxy.host=`
  on their own command lines.

## Process cleanup

Each test stack shuts down in production order (SSH `Shutdown`, frontend
close, `HTTPBridge.Close`, `StdioBridge.Close`, `Controller.Close`) and then
lists every process's command line (`Get-CimInstance Win32_Process` on
Windows, `ps -A -o args=` elsewhere). It requires that none mentions the
stack's directory (every worker's `-R` path) or the hook executable's
directory. Every stack in the recorded run logged "process check: no Mercurial
worker or hook process left after shutdown". The saturation test shows the
listing does see a live worker. `TestTransportAmbiguousCompletion` also
requires the disconnected push's worker to be gone. Leaked capabilities are
not observable from outside the controller; the bridges revoke them when each
request or session ends (Task 4/5 unit tests).

## Behaviour under supervision

- Saturation: with one process slot, a writer's hook callback held for two
  seconds while a reader's clone waits for the slot. Hooks are not runner
  processes, so the push completed; the waiting clone completed afterwards
  (HTTPS after about 11 s across its requests, SSH after about 3 s).
- Ambiguous completion: a raw `unbundle` is committed, then the connection is
  dropped unread. The changeset is present once, exactly one `unbundle`
  request (HTTPS) or session (SSH) reached the server, no hook callback ran
  afterwards (no replay), no worker remained, and the client's next push
  reports "no changes found".

## CI

`.github/workflows/mercurial.yml` jobs `integration` (Windows, Linux, macOS;
7.2.4) and `integration-legacy` (Linux; adds 5.0.2 and 2.0.2 under Python 2.7
from the runner's conda) run this suite. Neither has ever been executed: the
repository has no remote.
