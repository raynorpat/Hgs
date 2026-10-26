# Hgs profile prototype

Open index.html in a browser. profiles.js is local and requires no build, dependencies, account or backend. This is a throwaway interaction study, not production profile routes. No external avatar or data requests are made.

## Included

- User and organization profiles in the approved blue-rail, light-content shell.
- Prominent rendered README examples from user/user/README.md and organization/.github/profile/README.md, with source previews and present/missing/private states.
- Local Gravatar-style identicon fixtures, uploaded-avatar illustrations and initials fallback. These exercise presentation; they do not verify live Gravatar lookup. The beaker is an outline placeholder pending the final mascot-derived identity asset.
- Contribution calendars for 2024, 2025 and 2026, daily selection, activity-type filters, four-category summaries and expandable monthly activity.
- Public versus authorized-member preview, repository filtering, independent Watch and Star controls, organization member list and responsive navigation.
- Every organization People entry and sidebar member row opens that person's individual profile directly. The four sample profiles have separate names, handles, avatars and special README paths; URL fragments support direct links. Names and descriptions are illustrative.

## Proposed activity semantics — awaiting design review

Count changesets, opened issues, opened pull requests and submitted reviews. Repository creation may appear in the timeline without adding a calendar contribution. Attribute changesets through verified author-email associations, not the identity of the person who pushed. Deduplicate the same changeset across forks. Organization totals cover contributions to its repositories, not all work by its members elsewhere.

Filter rows, repository names, daily counts and totals by the current viewer's repository access. Public views do not disclose private contribution counts. Use a documented timezone (UTC in this study); current-year data ends on October 26, 2026, and previous years are complete. Production design still needs precise attribution/history rules, indexing and rebuilding, permission revocation, edits/deletion and bounded pagination before implementation.

The deterministic calendar is generated sample data. Summary categories add up to its displayed total. Monthly cards illustrate presentation and do not reconcile to that calendar; they are not a production event ledger. Member profile activity uses shared fixtures, not those people's actual history. Private-view controls simulate presentation only and are not an authorization implementation.

## Checks performed

- JavaScript syntax: node --check profiles.js.
- Browser inspection at 1440px and 390px: no page-level horizontal overflow; the calendar scrolls within its own region on mobile.
- 2025 selection renders 365 days; issue filter shows only the issue event; selecting a zero-contribution day shows its empty state; Show more activity expands a preceding month.
- Private README is hidden for public visitors and shown for the authorized-member fixture. Member repository count and calendar totals change with that fixture.
- Uploaded and initials states render; the rail avatar measures 28px inside a 40px control.
- Repository search filters the private member fixture. Organization profile uses .github/profile/README.md and has a People tab.
- People tab navigation to Morgan Chen displays Morgan's name, avatar and morgan-chen/morgan-chen/README.md; updates the URL fragment and moves focus to the heading.
- Mobile menu opens and closes, returning focus to its trigger. No browser console errors observed in these checks.

Production code remains unchanged and the hosting prerequisite gate remains in force. This prototype is available for review; the new activity rules are not recorded as approved production scope.
