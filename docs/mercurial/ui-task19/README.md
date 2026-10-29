# Profile README browser evidence

Source `3efecf43` passed independent Task19 spec and quality review with no blocking findings. The first actual app fixture used `9f74226`; its application/test sources are identical to `3efecf43`, which only removes the local implementation report from version control. The capture-only second fixture used `3efecf43`. Both used native Mercurial7.2.4 seeds and the production router with normal login.

- [Public user, desktop](user-readme-desktop.jpg), [mobile identity](user-readme-mobile.jpg) and [mobile content](user-readme-mobile-content.jpg): root README appears before tabs, retaining the identity sidebar. The local image and source links use one pinned revision. At1440px the document is1425/1425 client/scroll; at390px it is375/375. Long code and tables remain contained.
- [Private organization, authorized owner desktop](organization-owner-desktop.jpg) and [mobile](organization-owner-mobile.jpg): normal Alice login reveals `profile/README.md`. The first fixture verified the parent-relative image loaded from the repository-root banner at the same pinned node. The second mobile capture shows the private panel before its banner finished loading; it is not separate image-load evidence.
- [Guest organization](organization-guest-mobile.jpg): private README content and source links are absent. A fresh guest request after normal sign-out also omits them.
- [No README](no-readme-desktop.jpg): ordinary profile/repository/activity navigation remains available without an empty panel.

The organization member link navigates to the selected user's actual profile. Native tests separately cover canonical names, access/ancestry, updates, rename, transfer, deletion and invalid content. A new deterministic concurrent mid-read revocation test was not added; managed admission, cancellation and final live authority checks were inspected. Fake-runner symlink cases do not establish native symlink portability.

These are real JPEG browser captures. Original unframed mobile captures remain in local review scratch; corrected mobile images use the supported screenshot clipping API. Both fixtures stopped normally and joined with exit0; owned listeners and processes were absent. Controller test tabs closed and viewport reset; user preview tabs were preserved.

Focused/native and owning route checks passed with the precise scope in the Task19 report. An initial cmd invocation failed seven loopback-restricted cases; the affected-only retry passed. Build/vet exited0 before the final self-review repairs; later focused checks compiled the amended packages. The build's module stat-cache warning remains disclosed. This is development acceptance, not full native/platform/race/network-database or browser/accessibility release acceptance.
