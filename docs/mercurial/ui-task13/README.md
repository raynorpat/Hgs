# Pull request browser evidence — 2026-10-28

These captures use the real server-rendered PR handlers, local assets, temporary SQLite repositories and native Mercurial 7.2.4 on Windows. The test-only account selector supplies synthetic owner, target-writer/source-reader and guest roles. It is not a production route. The configured application prefix was `/code`.

## Completed walkthrough

- Created a real fork PR through the form. An unavailable source name preserved the exact submitted repository name, bookmark selections, title and Markdown. Correcting the name saved the PR at its destination-local URL. The source field accepts an exact `owner/repository`; it does not truncate choices to the first catalog page. See [creation form](create-source-desktop.jpg).
- A newly saved PR explicitly says no comparison has been prepared and no preparation is running. It offers an explicit Refresh action. See [unprepared state](unprepared-desktop.jpg).
- Exercised Markdown Preview, assigned Request changes, removal of the merge action while blocked, and Needs your review excluding the current decided request while retaining an unreviewed request. These initial functional checks preceded the narrow final presentation fixes.
- Advanced the disposable source using native fixture setup. The page retained its immutable old pins, showed Stale and removed Merge. A normal CSRF-protected Refresh updated the comparison; the old inline comment kept its original comparison/file/side/line identity and became Outdated. See [stale comparison](stale-desktop.jpg) and [outdated thread](outdated-thread-desktop.jpg).
- Used the required confirmation checkbox with the keyboard. The confirmation shows the exact source, target and native fast-forward strategy. Submitted one merge and observed the same operation transition from Pending to confirmed Merged through the page's GET polling. No POST was replayed after browser automation timeouts. See [confirmation](merge-confirmation-desktop.jpg), [pending operation](pending-operation-desktop.jpg), [final merged desktop](merged-desktop.jpg) and [final merged mobile](merged-mobile.jpg). The final merged view has no Refresh comparison action; model and HTTP regressions separately verify that accepted comparison identity cannot be replaced.
- Made the existing source private through isolated fixture setup. The reader then lost PR content, and global Open/Merged/Declined/All counts were zero with no source name or PR title. This verifies both-endpoint reads while the source still exists, including after merge. See [revoked global list](revoked-global-desktop.jpg).
- Verified an actual empty repository, long repository names, contained horizontal diff scrolling, and mobile keyboard navigation. Enter opens Menu and focuses Close; Escape restores Menu. See [empty mobile list](empty-mobile.jpg) and [mobile files](files-mobile.jpg).

## Geometry and presentation

| Nominal viewport | Document client / scroll width | Result |
| --- | --- | --- |
| 1440 × 900, PR detail | 1425 / 1425 | No page overflow; 15px scrollbar |
| 1440 × 900, empty global list | 1440 / 1440 | No page overflow |
| 390 × 844, detail/files | 375 / 375 | Long names wrap; diff scroll remains inside its panel |

The account image measured 28 × 28 inside a 40 × 40 button, with coincident centers. Open uses the shared info colors (`#DEEBFF` / `#0747A6`); Merged uses `#EAE6FF` / `#403294`. Final browser console error/warning inspection during operation polling returned no entries. [Open desktop](detail-desktop.jpg) and [open mobile](detail-mobile.jpg) provide layout context.

## Provenance and limits

All work was over Task13 BASE `b9847b2`, before its commit. Owned fixtures used loopback ports 60643, 57633 and 53673. The final compiled fixture included the exact-name resolver and merged-comparison guard; creation, unprepared, final merged desktop/mobile and final open desktop captures came from it. Stale/outdated/confirmation/pending/revoked/empty/files and open mobile captures came from the preceding fixture on the same relevant product paths. A subsequent one-condition reviewer-assignment validation correction is covered by its focused HTTP test, rather than these captures. The implementer's task report records exact source/test boundaries.

Optional fixture-only Advance/Revoke HTML controls failed actual browser CSRF checks; their cookie-backed tests did not establish the browser's precise failure cause. They were removed instead of weakening CSRF or claiming success. Source updates and revocation above used owned native/database fixture setup; ordinary product Create/Refresh/Review/Merge used actual protected forms. These setup changes do not prove transport authorization. No live user storage, server configuration, mail or prototype servers were changed.

Initial before-fix images are retained only in ignored execution scratch. A stale screenshot backing-store composite after resizing was discarded and replaced after a DOM geometry check. Each agent-owned tab was closed and each temporary viewport override reset; the implementer owns graceful fixture shutdown and temporary root cleanup.

This is a focused Task13 browser walkthrough. It does not certify real 200% browser zoom, every locale/role/status combination, other browser engines, Linux/macOS, old Mercurial versions, network databases, full-disk or OS-kill recovery. Those acceptance rows remain explicit in the plan and evidence. Shared POST validation-rail and empty comparison-ID presentation polish observations are carried to Task15.
