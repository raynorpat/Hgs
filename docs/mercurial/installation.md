# Installing portable Mercurial hosting

This branch hosts Mercurial repositories, users, permissions, organizations and issues. It requires a fresh database; there is no Git import, database migration or Git wire compatibility. See [compatibility and actual evidence](compatibility.md) before choosing a platform. Windows amd64 has native evidence; Linux/macOS and other architectures require native validation. Compilation alone does not establish support.

## Build and extract

With the pinned Go toolchain/dependencies already available, run from this source root:

```text
go build -o dist/gogs.exe .
go run scripts/package.go -binary dist/gogs.exe -out dist/gogs-mercurial-windows-amd64.zip
```

On Unix build `dist/gogs` instead. The builder uses only Go's standard library, does not install/download Mercurial or execute deployment scripts, and refuses an existing output. Keep sources quiescent while packaging. It verifies the executable's Go main-package identity; build it from the same revision as the assets. Startup checks `templates/.VERSION`, which is an additional version check, not proof of matching source revisions.

The zip has no enclosing directory: executable at root, public assets (without avatar cache), templates including `.VERSION`, distributed `conf/app.ini`, `conf/locale` with translator attribution, LICENSE, the current README (and Chinese README if present) and selected Mercurial documentation. `conf/locale/TRANSLATORS.md` is required attribution. It excludes operator custom configuration, repositories, databases, uploads, caches, keys, certificates and runtimes. Source symlinks/special files and unsafe binary/output locations are rejected. Never repurpose the retired shell build recipes: they copied private configuration and TLS keys.

Extract into an owned install directory outside any Mercurial repository. Preserve the executable permission on Unix (native extraction unverified here). Keep `conf`, `templates` and `public` with the executable. All are external assets, not embedded. Windows repository storage roots must contain only ASCII characters. Executable/asset paths must fit the active ANSI code page; spaces and `café` were exercised under ACP 1252, not arbitrary Unicode. Hook executable paths additionally reject shell metacharacters; choose simple deployment paths.

## Runtime and configuration

Install Mercurial separately with its complete native/Python runtime, including any Python DLL directory required on PATH. Server policy is Mercurial >= 5.0; native clients >= 2.0. The native validated combination is 7.2.4 server/client, Python 3.13.9 on Windows amd64. Server 5.0.2 and client 2.0.2 are planned matrix targets, not executed local results. Old 5.x installations may rely on Python 2.7, which is end of life; prefer a maintained current release and validate that exact build. No Git executable, sshd, ssh-keygen or server deployment shell script is needed. Mercurial itself invokes its native shell for hooks; this is not a shell-free runtime.

Keep the shipped `conf/app.ini` as defaults. Create a separate, access-controlled `custom/conf/app.ini` or pass `web --config <absolute file>`. Use an absolute `GOGS_CUSTOM` if overriding the custom directory. The shipped sample secret is public: generate a unique random `SECRET_KEY` before service use. `RUN_USER` must match the service account. The sample defaults to MySQL; select and configure your database deliberately. For a new install, leave `INSTALL_LOCK=false`, start on a protected listener and complete the installer/admin setup; retain the resulting config and set the installation lock before exposure. The package acceptance fixture provisions fresh test users and repositories with production models; it does not validate the interactive installer.

Example fields for an already installed SQLite service (replace every placeholder; merge with other required settings):

```ini
RUN_USER = service-account
RUN_MODE = prod
[database]
DB_TYPE = sqlite3
PATH = C:/gogs-state/application.db
[repository]
ROOT = C:/gogs-state/repos
[security]
INSTALL_LOCK = true
SECRET_KEY = replace-with-unique-random-secret
[server]
PROTOCOL = https
HTTP_ADDR = 127.0.0.1
HTTP_PORT = 3000
ROOT_URL = https://hg.example.test/
CERT_FILE = C:/gogs-state/tls/cert.pem
KEY_FILE = C:/gogs-state/tls/key.pem
SSH_ADDRESS = :2222
SSH_HOST = hg.example.test
SSH_PORT = 2222
SSH_HOST_KEY_PATH = C:/gogs-state/ssh/host_ed25519
OFFLINE_MODE = true
[mercurial]
EXECUTABLE = C:/Mercurial/hg.exe
CONFIG_PATH = C:/gogs-state/managed/hgrc
MAX_PROCESSES = 8
SESSION_TIMEOUT = 30
```

Use OS-appropriate absolute paths on Unix. Invoke the actual binary directly, for example `C:/gogs/gogs.exe web --config C:/gogs-state/app.ini`. Executable-root discovery locates distributed configuration and default assets. Explicitly use absolute SQLite, TLS, custom, static-root overrides, log and persistent-session paths when starting from another CWD; relative SQLite/TLS/session paths depend on CWD. Attachment and uploaded-avatar path settings are joined under the executable root: retain their relative `data/attachments` and `data/avatars` defaults and include those install-root directories in backups. Gravatar cache/default-image discovery uses the configured static root. Do not infer universal CWD independence.

## Exposure and host identity

HTTPS password/token credentials must travel over TLS. A TLS-terminating reverse proxy must send `X-Forwarded-Proto: https` only from peers enumerated in `TRUSTED_PROXY_CIDRS`; prevent untrusted direct access to that backend. Set `ROOT_URL` to the externally visible URL. Configure the proxy for long transfers and streaming; no native external-proxy deployment was exercised here. Use a trusted certificate for clients; test-only self-signed trust is not a production TLS recipe.

Embedded SSH generates an Ed25519 host key once at `SSH_HOST_KEY_PATH`, with owner-only permissions (protected owner DACL on Windows). Keep that key across restarts, upgrades and restores; distribute/verify its public fingerprint to clients. Never replace it casually or put it in a package. SSH authenticates a registered account key even for public clones. Native OpenSSH is a client test dependency, separate from the embedded server.

Mercurial workers bind loopback, which is not authentication against hostile local processes. Deploy on a trusted host with restricted storage/config access, not a machine shared with untrusted users. The service writes only its managed `HGRCPATH`, not operator-wide Git/Mercurial settings. Managed config must be writable, outside any `.hg` ancestor and free of the OS path-list separator. See [operations](operations.md) for recovery, process limits and diagnostics.
