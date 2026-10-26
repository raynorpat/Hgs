# Mercurial-only Gogs design

Date: 2026-10-25
Status: Approved by the user on 2026-10-25; implementation is in progress. Continuation with subagents authorized on 2026-10-26. Subsequent user-approved scope and validation overrides are recorded below and in the execution plan.
Revision: 2026-10-25, after design/plan review, at the user's request: Mercurial version policy (server ≥ 5.0, native clients ≥ 2.0), repository-ID-keyed access records, static deny hooks for read-only sessions, browse performance, readable file URLs, and explicit Mercurial transport details.

Builder check: a fresh reviewer reported "nothing important left" to resolve before planning. This is a design review, not validation of an implementation.

## Intent and agreed direction

Turn this Gogs source tree into a practical, self-hosted Mercurial service. The first release must create and host Mercurial repositories, support native clone/push/pull over SSH and HTTPS, and browse files and history in the existing web application. Keep Gogs' users, permissions, and issues. Remove Git hosting entirely.

The user explicitly chose a fresh database and new Mercurial repositories, with no migration from Git. The code must remain portable across Windows, macOS, and Linux. The selected architecture keeps the application in Go and manages installed Mercurial processes.

This is a substantial project, not a library substitution. Scope and behavior below labeled as design decisions originated as the author's proposals and were accepted through the user's approval of this document.

## Existing system and constraints

- The README identifies this source as Gogs 0.5.13 Beta. Dependencies use `.gopmfile`, not Go modules. Establishing a reproducible build is prerequisite work.
- `modules/git` models Git commits, trees, branches, and tags. `models/repo.go` also directly runs Git for creation, mirroring, maintenance, and other operations, and builds `.git` paths and clone URLs.
- `routers/repo/http.go` implements Git HTTP transport. `cmd/serve.go` dispatches Git commands over external SSH. `modules/ssh/ssh.go` is an unfinished embedded SSH prototype with hard-coded paths and key identity; it is not a production authentication implementation.
- Repository middleware, source/history handlers, diffs, release handling, API routes, and push notifications depend on Git concepts. Issue discussion and account management provide reusable application behavior.
- The directory had no version control when this design was written; at the user's request it is now a Git repository with no remote. Work is committed there; do not add a remote or push without being asked.
- Local inspection found Mercurial 7.0.3 installed. Its normal startup attempts to load a missing `hggit` extension from ambient configuration. This is evidence that server processes must use isolated configuration, not a supported-version declaration.

## Approaches

### Selected: Go with managed Mercurial processes

Go owns the application, authorization, repository lifecycle, public HTTPS, and embedded SSH. A small Mercurial-specific Go package invokes `hg` for browsing and lifecycle operations. Mercurial itself implements the wire protocols using `hg serve` for private HTTP workers and `hg serve --stdio` for SSH sessions.

Advantages: deployment consists of the application, its assets, and a supported Mercurial installation; no custom Python service or replacement wire protocol. Most maintenance stays in Go. Costs: process startup, supervision, command output parsing, and careful authorization across both transports.

The user selected this approach for portability and practical deployment.

### Considered: Go with a custom Python Mercurial helper

Use Mercurial's Python APIs and hgweb from an application-owned helper. This gives closer access to repository and protocol internals, but introduces Python packaging and API-version coupling. The user did not select it. Do not silently introduce such a helper if a transport integration problem appears; bring an architectural change back for approval.

## First-release scope

### Included

- Create empty public/private repositories owned by users or organizations; list, describe, rename, transfer, change visibility, and delete them under existing ownership rules.
- Native Mercurial clone, pull, and push using SSH keys or HTTPS credentials, with Gogs' repository read/write access and organization/team permissions.
- Existing account and administration functions, SSH key management, collaborators, teams, and issue discussion, labels, milestones, and assignments.
- Browse directories and files at immutable changesets, display sanitized README content, download individual raw files, and navigate repository and file history.
- Changeset details, parents, affected paths, and bounded text diffs; indicate binary or truncated content explicitly.
- Distinct named branches, bookmarks, and tags, including repositories with multiple heads.
- Deployment and integration validation on Windows, macOS, and Linux.

### Proposed exclusions

These are scope decisions proposed by the author: no Git migration or dual-backend mode; no forks, mirrors, remote import wizard, pull requests, releases, archive downloads, web editing, or arbitrary server-side hook editor in this milestone. Users can seed a new repository with a normal Mercurial push.

Preserve manual issue behavior, but defer commit-message issue auto-closing, outbound push webhooks, and Git-format push activity feeds. Retained issue notifications should continue to work. Remove unavailable features from navigation, route registration, background jobs, and configuration rather than leave broken Git implementations reachable.

Subrepositories and extension-specific storage/protocols such as largefiles, LFS, evolve, and hg-git are not supported in the first release. Ordinary versioned files such as `.hgsub` may be displayed as text; the server does not fetch nested repositories or execute repository content.

### Anti-goals

No Git executable required at runtime, no Git repository fallback, no Git wire endpoints, and no Git compatibility layer disguised as Mercurial. No shell-script-based deployment requirement. No broad UI redesign or replacement of Gogs' account/issue application. No claim that successful cross-compilation proves platform support.

Historical attribution, license notices, and source import paths containing `github.com/gogits` need not be renamed merely because hosting becomes Mercurial-only. Product copy, clone instructions, settings, and errors must accurately describe Mercurial.

## Repository model and storage

Use a Mercurial-specific package, proposed as `modules/hg`, rather than a generic multiple-VCS abstraction. Its public values describe changesets, parent nodes, manifest entries, named branches, bookmarks, tags, and diffs. Do not reuse Git tree-object IDs, refs, or a separate committer field as if they were Mercurial concepts.

Proposed storage: `<repository-root>/<immutable repository ID>/`, containing `.hg/` and no checked-out working files. Names and owners are database routing metadata. Renames and transfers therefore do not move repository storage. A fresh schema may replace Git-specific fields; retain issue foreign keys to the repository ID.

The fresh schema keys access records by repository ID and user ID, not by the legacy lowercase `owner/repo` and user-name strings. Renames and transfers then require no access-row rewriting; only ownership and team/collaborator membership changes alter access records.

Creation reserves a database record in a non-visible provisioning state, runs `hg init` in an application-owned staging directory on the same volume, installs managed configuration, moves it into its ID path, and marks it ready. On failure, clean up only the matching staged allocation. Startup reconciliation finds incomplete allocations; never infer that arbitrary directories are safe to delete.

Deletion first makes the repository unavailable and drains its sessions, then removes its owned storage and related database records using a retryable state. Visibility/ownership changes invalidate authorization state. Administrative filesystem operations use native Go APIs with containment checks.

Initial repositories are empty. The web UI presents clone/push instructions and issues without requiring a first changeset. No mandatory initial README or license commit.

## Mercurial execution boundary

Resolve an explicitly configured Mercurial executable at startup and report its version and supported capabilities. Use direct process execution with argument arrays, absolute paths, explicit working directories, contexts, and bounded stderr. Do not interpolate names, paths, or revisions into shell commands or arbitrary revsets.

Set a controlled child environment: application-owned `HGRCPATH`, `HGPLAIN`, explicit encoding, noninteractive behavior, and disabled paging/color. Strip inherited Mercurial configuration overrides and extension injection variables. Repository configuration is written only by the application; it is outside the versioned file tree. No arbitrary user-supplied hooks, config, extensions, or remote command options are accepted.

Use documented structured output/templates, not localized human-readable tables. Resolve typed reference selections to a full node ID before reading files/history. Keep user-supplied reference names out of executable revset syntax, and keep file paths separate from revision arguments.

Configure limits for concurrent processes, startup time, idle transfer time, browser command duration, displayed bytes, and diff size. Stream transport bodies and large raw files instead of buffering them in memory. Mercurial owns repository transaction locking; the app coordinates lifecycle operations and bounds concurrent work.

Version policy: the server requires Mercurial 5.0 or later. Every hook and structured-output feature this design relies on (`pretxnopen`, `pretxnclose`, `pretxnclose-bookmark`, `pretxnclose-phase`, `prepushkey`, JSON templates, `hg serve` dynamic ports) exists by 5.0; the implementation must confirm this against 5.0 sources rather than assume it. Startup rejects older or unrecognized versions with actionable diagnostics. Supported server versions are those the integration matrix actually covers, at minimum the oldest supported 5.0 patch release and the newest tested release; untested versions between them are accepted but documented as such. Mercurial 5.x primarily runs on Python 2.7, which is end-of-life; operators choosing 5.x accept that runtime, and documentation recommends a current release.

Native clients from Mercurial 2.0 onward must be able to clone, pull, and push. These clients predate bundle2 and phases, so the server keeps legacy bundle1 exchange and legacy `pushkey` enabled; those paths remain covered by the same transaction hooks. Old clients on old Python/OpenSSL may lack SNI or modern TLS defaults; this is validated rather than assumed.

Disable obsolescence-marker exchange explicitly in managed configuration (no `experimental.evolution` features), so a future default cannot open a write path the hooks were not proven to cover.

## Authentication and permissions

Reuse the existing users, organization/team membership, collaborators, and read/write access decisions through a single Go authorization service shared by web UI and transports.

| Actor | Public read | Private read | Repository writes |
| --- | --- | --- | --- |
| Anonymous HTTPS client | Yes, unless sign-in is required | No | No |
| Authenticated user | Yes | Only with read access | Only with write access |
| Owner or authorized administrator | Per existing policy | Per existing policy | Per existing policy |

SSH always authenticates an account key, including public clones. HTTPS uses Gogs username/password or an existing supported access-token mechanism over TLS; no new token product is required. Authentication is per request/session, not a global environment variable.

The public web app checks read access before starting any transport backend. Mercurial must also reject unauthorized writes inside the repository operation: checking only HTTP methods or the SSH command name is insufficient. A read-only SSH connection still runs a server capable of receiving protocol commands.

Proposed enforcement uses managed Mercurial hooks calling a restricted internal subcommand of the same Go executable. `pretxnopen` rejects unauthorized transactions; `prepushkey` covers legacy key mutations. As in Heptapod, write access is checked when a transaction opens and not rechecked at transaction close (decided 2026-10-26); readers keep deny hooks at close as well. Only core protocol features proven covered by this policy are enabled. No post-commit hook is treated as a write veto.

Read-only workers/sessions receive no capability. Their managed configuration disables push and installs fixed deny hooks (the internal Go subcommand in an unconditional-deny mode) on every veto-capable hook, the same model as Mercurial's own `hg-ssh` read-only mode. They fail closed with no control-channel round trip and cannot be upgraded, because there is no authority to upgrade.

Each write-capable worker/session receives an unguessable, short-lived capability through its private environment, bound to repository ID and authenticated actor. The internal Go hook calls a parent-owned loopback control endpoint with that capability; the parent rechecks current credential validity, repository access, and worker state on every writer hook (`pretxnopen`, `prepushkey`). Missing, expired, mismatched, or unverifiable context denies writes. New credentials require a new request/session. Each transaction opened after revocation is refused; explicit revocation also terminates live sessions; it is not a second authorization path.

Generated hook command strings contain only the correctly quoted application executable and fixed internal arguments. Repository names, client commands, and credentials never enter those strings. Mercurial runs external hooks through the platform shell (`cmd.exe` on Windows, `/bin/sh` elsewhere), so quoting of executable paths with spaces and Unicode must be verified through real hook execution on every OS, not only by string construction. No separate shell script is required.

Reads are authorized at request/session start; already-streamed data cannot be revoked. Bound session duration. Writes are authorized when their transaction opens; a transaction already open when access is revoked may complete without a further authorization check; live cancellation is best-effort. This guarantees neither completion nor rollback/store integrity after cancellation. A committed transaction cannot be revoked retroactively. This matches Heptapod and covers stream-clone bundles, whose store files bypass the transaction journal, the same way as any other push (decided 2026-10-26, see `docs/mercurial/transport-evidence.md`). Account/key revocation blocks new sessions and cancels tracked sessions where possible.

## HTTPS transport

Proposed clone URL: `https://host/hg/<owner>/<repository>`. The `/hg/` namespace is reserved for transport, separate from ordinary repository pages. The Go server terminates TLS directly or accepts traffic from a configured trusted TLS reverse proxy. Trust forwarded identity/scheme information only from configured proxy addresses. Reject credential-bearing or write traffic over public plaintext HTTP; never redirect a push body to another endpoint.

For the initial release, use a short-lived `hg serve` worker per authorized HTTP request, serving exactly one resolved repository on an automatically allocated loopback port. The worker has immutable actor context: a capability for writers, fixed deny hooks for readers. This deliberately favors isolation and correct attribution over process reuse; the process count and startup cost are accepted first-release tradeoffs. Pooling across users is out of scope.

The app waits for startup/readiness, then proxies the Mercurial wire request and streams the response. Preserve required Mercurial headers, query arguments, and bodies, including batching handled by Mercurial. Do not implement the wire protocol in Go. Strip public credentials and untrusted forwarding headers before proxying. Do not expose the backend's HTML browser, directory listing, configuration files, or arbitrary URLs.

For readers, backend push is disabled as well as guarded by hooks. For writers, enable push only in that worker and rely on the managed transaction policy for current authorization. Backend HTTP may be plaintext on loopback because public TLS terminates in Go; do not copy that setting to a public listener.

Deployment trust assumption: this is a dedicated service account on a trusted host. Loopback binding prevents remote access but is not authentication against hostile local processes. A local process reaching an active HTTP worker could use that worker's access, including write access. Hook capabilities authenticate the worker to the parent, not the caller of its HTTP socket. Multi-tenant operating-system hosts with untrusted local users require an additional isolation design and are outside this release's deployment contract. Do not advertise loopback binding or random ports as protection against local attackers.

Stop and reap the worker after the request finishes, disconnects, or times out. Never transparently replay a write after a worker failure: a disconnected client may have a committed push and must reconcile with the repository.

## SSH transport

Replace the prototype with a Go SSH server using the maintained Go SSH library. Persist an application host key under the service account's private data directory. Authenticate public keys against Gogs' database, reject disabled users and deleted keys, and map each accepted connection to an account.

Proposed clone URL: `ssh://hg@host:2222/<owner>/<repository>`; listener address and advertised port are configurable. The SSH username is a service name; the uploaded public key identifies the actual account.

Accept session execution only for the supported command shape emitted by native Mercurial clients (`hg ... serve --stdio` with a repository argument). Parse with a small explicit grammar tested against supported clients from 2.0 onward; do not pass a client command string to a shell. Clients quote the repository argument according to their own platform; the grammar accepts exactly the observed and source-derived forms. Corrected 2026-10-26: the Mercurial 7.2.4 Windows client was observed sending `hg -R owner/repo serve --stdio` with the repository unquoted, and a remote command path that needs quoting in single quotes, never double quotes; 7.2.4 Unix clients send the same shape and single-quote only paths that need it (derived from `sshpeer` and `procutil.shellquote` source); 2.0.2 source sends it unquoted. The earlier claim that Windows clients use double quotes was wrong; double-quoted forms are rejected. Ignore the supplied executable path, reject unexpected flags/configuration and ambiguous input, and construct a fresh argument vector using the configured local executable and resolved repository ID path.

Reject interactive shells, PTYs, port forwarding, subsystems, and environment injection. Connect channel input/output directly to a private `hg serve --stdio` process; keep stderr separate and return its exit status. Apply the same session capability and transaction hooks used for HTTPS, so a user with read access cannot push commits, move/delete bookmarks, or mutate phases.

Embedded SSH is the required portable deployment path. Integration with an external system SSH daemon is deferred, and the old Git forced-command path is removed from supported configuration.

## Web browsing and Mercurial semantics

Keep the existing application shell and issue UI. Adapt repository views to Mercurial values and remove unavailable feature links. Repository pages, raw files, and history all require the same read policy.

- Changesets use full immutable node IDs in canonical links; shortened IDs are presentation only. Local numeric revision numbers are not durable URLs.
- Branches, bookmarks, and tags have separate selection types, avoiding ambiguous names. Encode reference selection separately from file paths so slashes in names do not corrupt routing. Because canonical browse URLs carry a resolved node ID (which contains no slash), file paths appear as ordinary percent-encoded URL path segments after the node; an opaque encoded token is used only for paths that are not valid UTF-8.
- Proposed landing rule: the highest local revision among open heads of the `default` named branch; if none, the highest local revision among other open heads; if all heads are closed, the repository tip. Show the selected head and make all heads selectable. Resolving this selection immediately produces a canonical node ID.
- Bookmarks are movable pointers; named branches may have multiple heads; neither is presented as a Git branch/ref. Show closed branches as such and keep them browsable. No server-side ref editing UI in this milestone.
- Display tracked tags and bookmarks received through the supported protocol; do not invent Git-style release objects. Normal publishing-server phase behavior is used; secret/hidden changesets are not exposed through browsing.
- File and directory reads come from Mercurial's manifest/content commands at a changeset, never a checkout. Show symlinks as stored link content without following them. Windows hosting must preserve repository metadata even when the host cannot check out an equivalent symlink or case-sensitive path tree.
- History is paginated with a pinned node/snapshot so concurrent pushes do not duplicate or skip displayed entries. File history follows recorded copies/renames where Mercurial supports it. Changeset diffs default to the first parent, with an explicit second-parent choice for merges.
- Escape source text, sanitize rendered markup, bound syntax highlighting/diff output, and serve raw content with safe content types and content-disposition behavior.

Browse results are live from Mercurial in the first release; avoid persistent branch/head caches that can become incorrect after a push.

Each `hg` invocation pays interpreter startup cost (commonly 100–300 ms, more on Windows). A page must therefore use a bounded, small number of Mercurial invocations independent of directory size. The existing tree view's per-entry "last commit" lookup is not carried over as one process per entry: either compute it with a single history query over the directory, serve a page's commands through one `hg serve --cmdserver pipe` process, or drop the column. Page latency is measured alongside transport behavior. Repository list timestamps may be refreshed after committed transactions and reconciled on startup. They are advisory, not the source of truth for repository content.

## Build, packaging, and removal of Git

Introduce a pinned Go module build and update or replace obsolete dependencies only as needed for current buildability, secure authentication/transports, and preserved behavior. Retain Go templates and existing application structure. Do not turn this into a framework rewrite.

Preserve the existing database choices where practical, with SQLite as the documented small-install baseline. SQLite uses a pure-Go driver compiled in by default, so native builds need no C toolchain; that still does not make cross-compilation proof of platform support. The application ships with assets and the operator installs a tested Mercurial distribution, including whatever runtime that distribution requires.

Audit and remove Git-specific startup checks, settings, maintenance jobs, transports, model operations, archive seed files, hook UI, API behavior, and deployment dependencies. Replace incidental utilities such as version parsing if they currently come from `modules/git`. Retained API routes must use Mercurial semantics; unsupported repository APIs are unregistered, not misleadingly successful. Existing Git API compatibility is not promised.

Native filesystem/process operations must support spaces, Unicode, platform-reserved names, and path separators. Storage IDs eliminate name-derived filesystem paths. Supervision must cancel child processes and descendants on all platforms, including hook subprocesses, without relying on Unix signals alone or visible Windows command windows.

Configuration covers repository root, Mercurial executable, HTTPS/proxy settings, SSH bind/advertised addresses, host key path, process limits, and timeouts. Supply setup instructions for all three OS families. Service-manager integration is optional; running the binary directly is sufficient.

## Failure behavior and consistency

- Missing/unsupported Mercurial fails startup with a precise diagnostic; no Git fallback.
- Authentication or permission failures do not reveal private repository content. Unknown/private-inaccessible repository responses should follow a consistent non-disclosure policy.
- Worker startup failure returns a bounded service error; saturation queues for a bounded interval or rejects with a retryable error.
- Hook/control-channel failures deny mutations. If a post-commit metadata refresh fails, log/reconcile it without claiming the repository transaction rolled back.
- Interrupted pushes rely on Mercurial transaction recovery. Do not automatically break locks or run destructive repair commands. Surface recovery needs to the administrator.
- Shutdown stops new sessions, drains active work for a configured period, then cancels/reaps workers. Process handles, pipes, and loopback listeners must be closed.
- Back up database, repository stores, SSH host keys, and application configuration as a coordinated maintenance operation; stop writes for the documented baseline backup procedure.

## Validation and release criteria

First implementation milestone is a bounded transport feasibility check within the approved architecture, before broad UI conversion. Prove private HTTP proxying, SSH stdio, hook quoting, and denial of all core metadata writes on the oldest and newest supported server versions, with both a 2.0-era client and a current client. If this cannot be achieved with managed processes and Go hooks, stop and revisit the design rather than quietly broaden architecture or weaken permissions.

The release must pass native-client integration tests on Windows, macOS, and Linux:

1. Fresh installation without Git on the runtime PATH; create an empty repository, clone it, commit locally, push, and pull into a second clone over each transport.
2. Public/private repositories; anonymous HTTPS reads; valid/invalid passwords or tokens; authorized/revoked SSH keys; owner, collaborator, team, read-only, and unrelated-user access. Test both allowed and denied operations.
3. Read-only users cannot push changesets, create/move/delete bookmarks, change phases, or bypass checks through legacy pushkey, bundle-based updates, or batched commands. Verify repository state remains unchanged after denial, including new-head and bookmark-only operations.
4. Permissions changed during a session, concurrent actors, cancellation, worker crash, saturation, and failed hook callbacks cannot mix identities or bypass writes. Test ambiguous push completion without automatic retry.
5. Browse empty repositories, multiple named branches and heads, closed branches, bookmarks/tags sharing names, merges, renames, Unicode and unusual paths, symlinks, executable bits, binary files, and bounded large files/diffs.
6. Account/team/issue regressions: create/read/comment/label/assign/close/reopen an issue under public/private policies. Repository rename/transfer keeps issues linked; deletion respects ownership and cleans up coherently.
7. Malformed SSH commands, path traversal, client-supplied config/environment, unsafe revset inputs, and attempted `.hg` access are rejected. Ambient `hggit` or other user configuration cannot affect server behavior.
8. HTTPS works with a real test certificate both directly and behind a trusted TLS proxy; forged forwarding headers and public plaintext credential/write requests fail. Git clone/push endpoints and unsupported feature routes are absent.
9. Shutdown/failure tests leave no active workers or leaked capabilities. Build and integration results document the exact Go/Mercurial/OS versions tested.
10. Version matrix: server on the oldest supported 5.0 patch release and the newest tested release; clients from the 2.0 series (legacy bundle1/pushkey, no phases) and a current release. Old-client tests over HTTPS confirm TLS works with the configured minimum. Current clients are required on all three OS families; if a 2.0-era client or 5.0 server cannot be run on a platform (for example macOS arm64 without Python 2.7), report that combination as unverified rather than passed.

No performance or fleet-scale claim is made for per-request workers. Record basic process count, startup cost, clone/push behavior, and repository page latency (including large directories) with representative repositories; optimize only after correctness is established.

User-requested extension, 2026-10-26: after portable packaging, Task 16 ingests the full public Mercurial history from `https://hg-edge.mozilla.org/mozilla-central/` into isolated application repositories through the supported transports. Verify dataset identity, native integrity, hosted history/content, permissions, restart and cleanup; measure acquisition, ingestion, transport and web costs separately, and deliver a ranked, evidence-based list of the next code improvements. The acquisition must pass resource/access preflight; no Firefox build, Git mirror substitution, upstream write, public import wizard, or speculative optimization is included.

## Decisions left to the builder

Choose the precise Go version, the newest tested Mercurial release within the version policy above, dependency substitutions, package/function boundaries, structured command templates, supported native SSH command spellings, concrete process limits, and native process-tree cleanup mechanisms. These choices must satisfy the behavior above and be documented/tested.

Choose the hook capability representation and control endpoint details for write-capable workers, while retaining per-worker identity, expiry, fail-closed authorization, and no client-controlled elevation. Prove hook coverage against the pinned core protocol implementation rather than assume hook names alone guarantee completeness.

Product branding and historical source import-path renaming are deferred. Existing Gogs branding may remain with an accurate Mercurial service description.

## Approval points

The selected architecture is already agreed. Approval of this document additionally accepts the author's proposed first-release boundaries: empty-repository creation; embedded SSH; per-request HTTP workers on a trusted host; named branches/bookmarks/tags with the stated landing behavior; and deferral of forks, mirrors, releases, webhooks, automatic issue closing, and Git API compatibility.

The user approved this document, requested the implementation plan, and subsequently authorized its execution. On 2026-10-26 the user requested continuation with subagents after Claude's work through Task 9. Preserve the user-approved validation overrides and accurately report outstanding platform/version evidence.

## References

- [Mercurial: using scripts and automation](https://www.mercurial-scm.org/help/topics/scripting) describes process, HTTP, and command-server interfaces.
- [Mercurial: hg serve](https://mercurial-scm.org/help/commands/serve) documents the HTTP server, loopback/address configuration, dynamic ports, and the need for an authenticating frontend.
- [Mercurial configuration](https://www.mercurial-scm.org/help/topics/config) documents web authorization, transaction hooks, configuration isolation considerations, and server behavior.
- [Mercurial hgweb configuration](https://www.mercurial-scm.org/help/topics/hgweb) describes serving repositories through hgweb.
- Local read-only evidence: `hg version --quiet`, `hg help serve --verbose`, and `hg help config.hooks` on the installed Mercurial 7.0.3. Hook enforcement and cross-platform transport behavior remain to be tested during implementation.
