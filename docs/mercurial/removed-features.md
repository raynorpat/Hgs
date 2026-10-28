# Removed and deferred repository features

This edition hosts Mercurial repositories only. New repositories are empty and use immutable repository-ID storage. Existing Git repositories and databases are not migrated.

Git upload/receive endpoints, Git SSH forced commands, Git initialization and global configuration, Git maintenance, and Git-compatible repository API aliases have been removed. Repository search and listing remain available with Mercurial clone URLs; account, organization, and issue behavior remains supported.

Local Mercurial forks and pull requests are implemented with current permission checks, immutable comparisons, formal review and native merge publication. External-server forks, mirrors, imports and migration, releases, archives, web editing, editable hooks, push webhooks, push activity feeds, and automatic issue closing from changeset messages remain deferred. Their routes, jobs and forms remain absent. The old /repo/fork/:id URL stays absent; supported forks use /:owner/:repo/fork. Issue comments, mentions, mail notifications, watchers, stars, and generic create/transfer/issue activity remain supported. Managed Mercurial transaction hooks are internal authorization machinery and cannot be edited through repository settings.

The legacy Docker assembler/blocks, buildpack and Debian packaging, shell build/start/autoboot/supervisord scripts, and init/systemd recipes are retired. The old build scripts copied operator configuration and TLS private keys; do not run recovered copies to package this edition. Replacement archive creation and operator instructions belong to Task 15. No supported container or distro package is supplied by those retired recipes.

The old `dump` command is removed: it copied live mutable state and omitted the persistent SSH host key, so it was not a validated backup. No replacement backup command is provided here. Operator backup and restore instructions require consistent, stopped application state and are covered by the deployment work.

Historical attribution, license text, GitHub module/source links, rejection tests for Git-era inputs, and Mercurial's `hg diff --git` patch format do not provide Git hosting or require the Git executable. Git, `sshd`, and `ssh-keygen` are not server runtime dependencies. Native platform and Mercurial-version support must be assessed from the recorded native test evidence; cross-compilation alone does not establish it.


The DEV /template wildcard and unregistered issue2/milestone2 demonstrations are removed. Supported account, organization, administration and issue pages use shared components. The old Bootstrap base shell and its closed script/style graph are removed after caller and selector audit; see [cutover asset inventory](shared-ui-evidence.md#task-15-cutover-asset-audit-2026-10-28). Guest landing still actively loads ng assets until Task16. Local app/prettify.js remains a supported source-highlighting dependency; the active guest ng bundle also retains its own libraries and ZeroClipboard path. Final status art/layout belongs to Task25. None of these intermediate changes establishes final whole-branch acceptance.
