# Deployment recipes

Native startup is `gogs web --config <absolute config path>`. Use `go run scripts/package.go -binary <native executable> -out <new zip>` from the source root; see [installation](../docs/mercurial/installation.md) and [compatibility](../docs/mercurial/compatibility.md). No shell wrapper, Git executable, or SSH server utility is required by the application. `mysql.sql` remains a generic database setup example, not a package builder.
