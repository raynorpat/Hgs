# Hgs - Mercurial Service

A Mercurial-only branch derived from Gogs. Keep users, permissions, organizations and issues; host fresh, initially empty Mercurial repositories over HTTPS and embedded SSH.

Read [installation](docs/mercurial/installation.md), [operations and cold restore](docs/mercurial/operations.md), and [actual compatibility evidence](docs/mercurial/compatibility.md). Build a native executable with matching assets, then use `go run scripts/package.go -binary <native executable> -out <new zip>`. Mercurial is installed separately; server >=5.0 and native clients >=2.0 are the policy.

Run `gogs web --config <absolute config file>` directly. No Git executable, sshd, ssh-keygen or deployment shell script is required server-side. Generate a unique application secret; preserve the SSH host key and consistent DB/repository/config/upload backups. Only `/hg/<owner>/<repo>` is public HTTPS transport; `.hg` is private storage.

Users, organizations/teams, repository lifecycle/access, browsing/history/diff and issues remain. Forks, mirrors/imports, pull requests, releases, archives, web editing, editable hooks, push webhooks/feeds, automatic issue closing, subrepository traversal and extension-specific protocols are excluded. [Removed features](docs/mercurial/removed-features.md) and [authorization limits](docs/mercurial/operator-notes.md) describe the boundaries. No scale benchmark or production deployment is claimed.

## Acknowledgments

- Router and middleware mechanism of [Macaron](https://github.com/Unknwon/macaron).
- Mail Service, modules design is inspired by [WeTalk](https://github.com/beego/wetalk).
- System Monitor Status is inspired by [GoBlog](https://github.com/fuxiaohei/goblog).
- Thanks [lavachen](http://www.lavachen.cn/) and [Rocker](http://weibo.com/rocker1989) for designing Logo.
- Thanks [gobuild.io](http://gobuild.io) for providing binary compile and download service.
- Thanks [Crowdin](https://crowdin.com/project/gogs) for providing open source translation plan.

## Contributors

- See [CONTRIBUTORS](CONTRIBUTORS.md) for full list of contributors.
- See [TRANSLATORS](conf/locale/TRANSLATORS.md) for full list of translators.

## License

This project is under the MIT License. See the [LICENSE](https://github.com/gogits/gogs/blob/master/LICENSE) file for the full license text.
