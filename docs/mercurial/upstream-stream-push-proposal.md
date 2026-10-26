# Draft: let servers refuse stream-clone data in pushes

Status: local draft, 2026-10-26. Not sent, posted or filed anywhere. Written
for Mercurial developers (mercurial-devel / Heptapod issue tracker) should the
project owner decide to send it. Line numbers refer to Mercurial 7.2.4.

## Summary

A client can push a stream-clone bundle through the wire `unbundle` command
into an empty repository. The stream data is written to the store outside the
transaction journal, so a `pretxnclose` hook that refuses the push cannot undo
it: the transaction "rolls back", yet the full history stays. We propose a
`server.*` option that makes the server refuse stream-clone parts in pushes.

## The problem

Hosting servers commonly authorize writes with `pretxnopen` and recheck them
in `pretxnclose`, relying on transaction rollback when the recheck fails (for
example, a user's write access was revoked while the push was running).

That holds for changegroups, bookmarks, phases and pushkeys, which write
through the transaction. It does not hold for stream-clone parts:

1. `exchange.unbundle` opens the transaction before reading bundle2 parts
   (`exchange.py:2852-2870`); `pretxnopen` runs.
2. `bundle2_part_handlers.handlestreamv2bundle` (`:621-634`) accepts the
   `stream2` part whenever the repository has no changesets and calls
   `streamclone.applybundlev2` (`streamclone.py:1686-1713`).
3. `consumev2` (`:1131`) opens a nested `repo.transaction(b'clone')`, then
   `_v2_parse_files` (`:1535`) and `_write_files` (`:1593-1610`) write every
   store file with `os.open`/`os.write`. Nothing is added to the journal.
   `applybundlev2` then rewrites `.hg/requires` and `store/requires` from the
   part's `requirements` parameter (`scmutil.writereporequirements`).
4. `pretxnclose` refuses. The transaction aborts and prints
   "rollback completed", but only journaled files are restored. The streamed
   revlogs, the phase roots and the new requirements stay.

Reproduction: `hg bundle -a -t "none-v2;stream=v2" s.hg` in any repository;
`hg init empty`; serve `empty` with a `pretxnclose` hook that exits 1; send
`s.hg` as the body of a forced `unbundle` (`heads=666f726365`). The client
gets an error, and `hg -R empty log` shows the whole history.

No stock client sends stream bundles in a push, so this needs a hand-built
request; but hosting servers must assume hand-built requests.

Affected handlers in 7.2.4:

| Path | Handler | Notes |
| --- | --- | --- |
| bundle2 `stream2` part | `handlestreamv2bundle` → `applybundlev2` → `consumev2` | reproduced |
| bundle2 `stream3-exp` part | `handlestreamv3bundle` (`:637-650`) → `applybundlev3` (`streamclone.py:1716`) → `consumev3` (`:1613-1685`) | same structure, writes through `vfs(name, 'w')`; by source |
| bundle1 `HGS1` body to `unbundle` | `exchange.readbundle` returns a `streamcloneapplier` (`exchange.py:102`); `bundle2.applybundle` (`bundle2.py:388`, `:523`) calls `cg.apply(repo, tr, source, url)` | `streamcloneapplier.apply(repo)` (`streamclone.py:617`) has a different signature, so this appears to fail before writing; by source, untested — it should be refused explicitly rather than by accident |

Hooks cannot close the gap: `pretxnopen` runs before the part is read and
receives nothing that distinguishes a stream part from an ordinary first push
into an empty repository; `server.bundle2.stream` only controls what is
offered to pullers; and there is no per-part switch for unbundling.

A related robustness issue seen while testing: after applying a `stream2`
part over SSH, `_runsshserver` fails formatting the reply
(`wireprotoserver.py:520`, `TypeError: %d format: ... not NoneType`) because
the result is `None`, after the transaction has committed.

## Proposal

Add a server option, for example:

```ini
[server]
# Refuse stream-clone data (bundle2 stream2/stream3-exp parts, bundle1 HGS1
# bodies) received through the wire "unbundle" command.
unbundle.stream = no
```

- Checked on the wire `unbundle` path only, so local `hg unbundle` and
  `debugapplystreamclonebundle` of stream bundles are unaffected.
- When disabled: `handlestreamv2bundle` and `handlestreamv3bundle` raise
  `error.Abort` before reading the part payload when the operation came from
  the wire (`op.source == b'push'`, which `exchange.unbundle` sets at
  `exchange.py:2875-2879`), and
  `exchange.unbundle` rejects a `streamcloneapplier` explicitly.
- Default: we suggest `no` (refuse), since no Mercurial client produces such
  pushes; `yes` keeps today's behaviour for anyone who relies on it.

An alternative that also fixes local use would be to journal streamed files
(record each new file in the transaction so abort removes it), but that is a
larger change to the stream-clone writer, which deliberately avoids
per-file overhead.

## Why it matters

Hosting software that rechecks permissions at transaction close today has to
document an exception for stream-clone pushes into empty repositories, or
filter the wire protocol itself. A server option makes the transaction-close
recheck complete without either.
