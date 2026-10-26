# Retired deployment recipes

The legacy shell build/start/autoboot/supervisord scripts and init/systemd recipes have been retired. The build scripts copied operator custom configuration and TLS private keys into output; do not recover or run them to make release archives. The FreeBSD cross-build recipe did not establish native platform support.

Native startup is `gogs web --config <absolute config path>`. Task 15 owns the replacement Go archive builder and operator instructions. No shell wrapper, Git executable, or SSH server utility is required by the application. `mysql.sql` remains a generic database setup example, not a package builder.
