# Shared UI and Mercurial pull requests

Date: 2026-10-26

Status: Approved by the user on 2026-10-26, including the Issues coverage and shared design system. Implementation planning is authorized. The Hgs landing page and lab-coat/beaker Go Gopher artwork were also approved on 2026-10-26, with public-only landing Explore omitted when anonymous browsing is blocked. Approval does not waive the transport or merge-feasibility prerequisites below.

Builder check: A fresh reviewer identified two consequential ambiguities: cross-named-branch ancestor merges and authority to assign reviewers/block merging. Both are resolved below as explicit author-proposed defaults, and the reviewer confirmed both questions are answered. This review checks design completeness, not implementation correctness or transport feasibility.

## Intent

Give this self-hosted Mercurial service the navigation and visual character of the Bitbucket screenshots supplied by the user: a blue left navigation area, a narrow utility rail, white content surfaces, compact tables, and contextual repository navigation. Replace the two existing presentation systems with shared components while retaining the Go application. Include a working fork-based pull request workflow and apply the same design to administration.

The screenshots and iterated mockup are the visual reference, rather than an attempt to reproduce an exact historical Bitbucket release. Use the subsequently approved Hgs product name; preserve existing legal attribution and internal import paths.

## Decisions made with the user

- Use a shared UI refactor, rather than separately reskinning the old Bootstrap and LESS template systems.
- Put the main navigation on the left in blue.
- Put Explore in the narrow utility rail directly below Search, using a compass icon. Explore opens a full page listing all repositories the viewer can access on the server, including permitted private repositories.
- Put Recents below Explore. It opens a popout of recently visited repositories.
- Search opens a text-field popout. The create control opens a popout containing the Create repository action.
- Put global Pull requests below Organizations. Put repository Pull requests above Issues.
- Center the account avatar horizontally and vertically inside its hover/focus square.
- Include the Admin panel in the shared design.
- Include global and repository Issues pages, issue creation/detail/discussion, and label/milestone management in the shared UI refactor.
- Deliver working pull request creation, review, and merging, including contributions from forks.

Anything below described as a proposed default or implementation choice is the author's recommendation for approval, not an earlier user instruction.

## Approaches

### Selected: shared components and application layout

Keep Go, Macaron handlers, server-rendered templates, and the existing data layer. Build one application layout and shared navigation, forms, buttons, tables, pagination, alerts, dialogs, and review components. Migrate the supported pages to them, then retire obsolete presentation assets where no supported page uses them.

This requires more initial template work, but produces consistent interaction and accessibility across repository, workspace, account, organization, and Admin pages. The user selected this approach on 2026-10-26.

### Considered: separately restyle both existing systems

Wrap the existing Bootstrap and custom LESS templates in similar-looking navigation and override their CSS independently. It gives an earlier visual result, but retains duplicated components, JavaScript behavior, and styling. The user did not choose it.

Neither option supplies the missing Mercurial fork and PR backend; that is substantive work under either approach. A new SPA framework was not proposed or selected.

## Existing system and integration boundary

Read this alongside [the Mercurial hosting design](2026-10-25-mercurial-hosting-design.md), [its implementation plan](../plans/2026-10-25-mercurial-hosting.md), and [transport evidence](../../mercurial/transport-evidence.md).

Observed on 2026-10-26:

- Repository browsing and much of the application use `templates/ng/base/*`, `public/ng/less`, and compiled assets under `public/ng/css`. Issue pages still use `templates/base/*` and Bootstrap. Both have page-specific JavaScript dependencies.
- `templates/user/dashboard/pulls.tmpl` is an empty shell; `routers/repo/pull.go` renders a sample PR template rather than implementing a review lifecycle. These are not a working PR backend.
- `models.ForkRepository` invokes Git. It cannot be reused as the Mercurial fork implementation.
- The shared Mercurial runner and transport code exist, but the ID-based application authorization/lifecycle and browsing conversion remain prerequisites in the earlier plan. Do not assume proposed packages such as `modules/hosting` already exist.
- The recorded transport gate is **FAILED**, citing stream-bundle writes surviving revocation at transaction close. Linux/macOS and legacy-version evidence is also incomplete. A passing test that reproduces the gap does not satisfy the gate.
- Concurrent work is changing transport code in this checkout. Recheck the baseline before planning or implementation; do not modify or commit unrelated changes as part of this design.

### Relationship to earlier scope

This design deliberately replaces the earlier exclusions of broad UI redesign, forks, and pull requests. It also permits application-owned temporary working directories solely for safe PR merge preparation; hosted repository stores remain without checkouts. The new UI must expose these new capabilities once implemented.

Other prior constraints remain: Mercurial-only hosting, fresh database/no Git migration, ID-based repository storage and access, Go-managed Mercurial processes, no custom Python helper or production wire-protocol parser, no arbitrary hooks/extensions, and native Windows/macOS/Linux support. Remote imports, mirrors, releases, web file editing, CI/pipelines, and subrepository traversal remain excluded.

Design and planning can proceed now. Product implementation of this project must not bypass the earlier transport feasibility gate. The implementation plan must identify the owner/status of that prerequisite, reconcile overlapping work with the original plan, and require an approved resolution plus passing evidence before broad UI/model conversion. This document neither selects a remedy for that gap nor claims it has been fixed.

## Goals and completion boundaries

1. Every supported user-facing page uses the same visual language and shared components, with no abrupt switch back to the old issue or Admin UI.
2. Navigation, Search, Explore, Recents, and Create work with real authorization-filtered data.
3. Users can create a local fork, push changes to it, open a PR against its upstream, review it, and merge it through the web UI when conflict-free.
4. Same-repository PRs also work; Mercurial named branches, bookmarks, and immutable changesets retain their distinct meanings.
5. Existing account, organization, team, issue, and administration operations survive the refactor.
6. The service remains usable with keyboard navigation, narrow screens, application sub-URLs, and the supported locale mechanism.

Not included: Bitbucket branding or proprietary assets; a new frontend framework; full-text source-code search; external-server forks; pipelines/build checks; merge queues; branch protection policies; arbitrary merge strategies; browser conflict editing; repository content execution; and replacing existing account/authentication policies.

The mockup contains sample data and placeholder controls. Those are design aids only: no fabricated counts, activity, status, or inactive imitation controls ship in the product. This project does not implicitly restore the Git-format push activity feed excluded by the Mercurial plan. The workspace uses supported issue/account/repository events and honest empty states.

## Shared interface

### Layout and navigation

Use the [frozen layout study](references/2026-10-26-bitbucket-layout-study.html) for visual direction and [the shared design system](../../DESIGN.md) for tokens, typography, spacing, components, and interaction rules. The HTML is an illustrative artifact, not application source or a complete behavioral specification. This document governs product behavior; `docs/DESIGN.md` specifies the shared visual system. Both take precedence over the prototype's sample behavior.

At desktop widths, the left side contains a roughly 56px utility rail and a roughly 208px contextual menu. Use shared spacing/color/type tokens so these measurements can be tuned without editing every page. The main area expands with the viewport; dense tables can scroll within their content region.

Utility order from the top: identity/home mark, Search, Explore, Recents, Create. Administration (administrators only), Help, and the account avatar sit at the bottom. Buttons have a common square hit target, centered content, accessible names, keyboard focus, hover state, and tooltips. The actual avatar uses a circular crop and `object-fit`; fallback initials use the same centered bounds.

Workspace menu order: Overview, Repositories, Organizations, Pull requests, Issues. Workspace Repositories is the user's owned/collaborative repository view; Explore is the full accessible server catalog. Retain personal/organization context switching, with membership-aware choices. The global PR view covers accessible requests across repositories.

Repository menu: back to workspace, owner/repository identity, Source, Changesets, Branches, Pull requests, Issues, Settings. Only show permission-dependent settings and actions when allowed. Branches contains typed views for named branches/heads, bookmarks, and tags. The source view retains the revision selector, path breadcrumbs, clone access, file listing, and README. Add a visible Fork action for eligible users; prototypes preceding this design do not yet illustrate that control.

Admin menu: back to workspace, Overview, Users, Organizations, Repositories, Authentication, Configuration, Notices, Monitoring. Preserve the actual existing administrator authorization rules and action capabilities. Do not invent a new ordinary repository-access bypass merely because a user can see server administration.

Proposed responsive behavior: collapse the expanded menu into an accessible drawer at a content-driven breakpoint around 800px. Keep the utility controls reachable. The drawer traps focus while open, closes with Escape, and restores focus to its trigger. Avoid a permanently hidden mobile navigation with no opener.

### Popouts and discovery

Only one popout opens at a time. Escape or outside click closes it; focus returns to its trigger. Search focuses its field on opening. Account opens profile/settings/sign-out actions. Use actual buttons and links, not clickable generic elements.

Proposed Search scope: repository name and owner name among accessible repositories, with a short debounce, bounded results, loading/error/empty states, and links to the complete Explore results. It does not search file content or users in this milestone. Keyboard users can reach results through normal focus order; add arrow-key navigation only if its full combobox semantics are implemented.

Explore is a real route with pagination, text filtering, and visibility filtering. Rows show repository name/description, owner, public/private state, and a reliable last-updated value. Filters, counts, and pagination must apply authorization before results are returned. Anonymous users see only what the existing anonymous/sign-in policy permits. Never fetch a global list and hide unauthorized entries in JavaScript.

Proposed Recents persistence: store the last successful repository-page visits by signed-in account and immutable repository ID, up to 20 distinct entries, displaying the latest 10 in the popout. Repository pages include source, history, issues, PRs, and settings. Refresh the existing entry instead of duplicating it; exclude background requests and static assets. Recheck read authorization and readiness when displaying, omit deleted/revoked repositories and their names/counts, and resolve current names after rename/transfer. This follows the account across browsers. Anonymous Recents is omitted for this milestone. Provide a Clear recents action affecting only that account.

Create opens a menu with Create repository, linking to the real shared form. Offer permitted personal/organization owners, name, description, and privacy using current creation permissions. A fork has its own source-aware form and does not misuse the empty-repository form. Forms retain input on validation errors and announce errors accessibly.

### Page coverage and components

Migrate workspace dashboard/list views; repository source/file/history/diff/branches, issues/comments/labels/milestones, create/fork/settings/collaborators; organization/team/member/settings pages; account profile/settings/security; and all existing Admin pages. Authentication, installer, and error pages use the same tokens/forms/alerts with a reduced layout appropriate to anonymous access. Existing mail templates are included for Hgs branding and readability, preserving delivery behavior, action links, token lifetimes and legal attribution. New PR mail remains excluded.

Issues coverage explicitly includes the global cross-repository Issues list, repository issue lists and existing filters/pagination, creation forms, issue detail/discussion, attachments, label management, and milestone pages. Reuse the shared page header, list/table, badges, Markdown editor, metadata controls, alerts, and discussion components. Preserve existing assignment, labeling, milestone, edit/comment, close/reopen, attachment permissions, and notification behavior. Global rows identify their repository; counts and filters exclude inaccessible data. Repository Issues stays immediately below Pull requests. No legacy Bootstrap issue-page shell remains at completion. This is a presentation refactor of existing issue workflows, not a new issue state machine or Kanban board.

Admin Overview presents actual service statistics and supported maintenance operations. User, organization, repository, and authentication-source management use shared tables/forms. Configuration stays read-only where it is read-only today. Notices/monitoring show supported data. Old Git-only maintenance must not be relabeled as Mercurial actions. Mutation controls use CSRF-protected POST requests and explicit confirmation for destructive operations, preserving their authorization and intended behavior.

Use shared template partials and CSS custom properties for colors, spacing, typography, borders, focus, and disabled/error states. Proposed asset choice: ordinary checked-in CSS plus small scoped JavaScript modules, with no frontend build tool required at deployment. Prefer existing libraries where still necessary for specialized functionality, but avoid loading both legacy global application scripts on converted pages. Inventory selector/plugin dependencies before removing assets. No CDN dependency for the new layout/icons. Use a small consistent local icon set (including compass, history, search, create, PR) with decorative SVGs hidden from accessibility APIs.

Keep product copy in locale resources. Avoid broad selector overrides against the old systems: migrate markup to shared components, preserving form names, route contracts, CSRF fields, and applicable interaction behavior. Delete old templates/assets only when no supported route references them. Production must converge on one layout, not permanently maintain two reskinned stacks.

## Forks

### User experience and permission defaults

Support forks between repositories on this server, owned by the user or an organization in which they may create repositories. Users need source read permission and destination-owner create permission; source write permission is not required. Record the direct source and stable fork-family identity by repository ID. Forking a fork clones that selected source, rather than silently replacing it with the family root.

Proposed initial policy: public repositories produce public forks; private repositories produce private forks. Private-fork access requires both an ordinary grant on the fork and read access to its private ancestry. The fork owner cannot grant upstream read access through a fork, and upstream writers are not silently made fork writers. Repository lists, PRs, raw files, transport, and search share this policy.

To keep lineage rules explicit in the first version, block visibility changes and deletion of a repository while it has dependent forks; present the affected fork count only to authorized administrators. Leaf forks can be deleted after draining operations. Private-fork transfer is allowed only if the new owner's effective readers still satisfy the upstream read restriction. Normal transfers/renames retain IDs and links. Expanding these lifecycle policies later is separate work; these are proposed defaults for approval.

### Storage and provisioning

Use the same provisioning/ready/deleting lifecycle as the approved Mercurial repository design. Clone only server-resolved repository ID paths into an application-owned staging directory, without a working checkout or shared mutable store. Do not accept clone URLs, shell fragments, or filesystem paths from the client. Capture a consistent source snapshot and revalidate permissions before publishing the new repository. Replace any automatically generated clone configuration with managed configuration; do not expose the source's internal path.

A fork copies visible repository history and supported branch/bookmark/tag state, not issues, PRs, credentials, webhooks, collaborators, or hooks. Empty repositories can be forked but cannot supply a PR until source and destination revisions exist. Do not copy hidden/secret data or arbitrary repository configuration. Retain the restriction against outbound Mercurial HTTP requests; local forks do not require relaxing it.

Do not publish a partially cloned repository. Record/reconcile interrupted allocations as in the existing lifecycle design. Use bounded work and a visible provisioning state for large forks; completion/failure can be polled through an authorized status page.

## Pull request workflow

### Creation and lists

A PR belongs to a destination repository. Creation selects a source repository (the same repository or a related fork in the same family), typed source revision, destination named branch/head or bookmark, title, description, and optional reviewers. The author must read both repositories and write the source. Requested reviewers must read both. Allow submissions to an upstream repository without granting its write permission.

Named branches may have multiple heads. Require an explicit head selection when ambiguous; do not merge an arbitrary tip. Tags and immutable changesets can be source selections but cannot be mutable merge destinations. Empty destinations, unrelated histories, identical endpoints, and no-change proposals receive a clear explanation rather than a fabricated diff.

Persist selector identities and full source/destination node IDs with a numbered comparison revision. For a bookmark or unambiguous named-branch selector, new pushes can create a fresh comparison revision; ambiguous heads or missing refs require reselection. Old comments stay attached to their old comparison. On page load/merge, compare live selectors again even if a background refresh has not run. Refresh failures show a pending/error state and disable merge.

Show repository PR tabs Open, Merged, Declined and global filters Needs your review, Created by you, All open, with pagination and permission-filtered counts. Global list entries link to the owning destination repository's PR. Use a PR-specific repository-local sequence; do not silently collide with existing issue IDs/routes.

### Detail and review

Provide a PR detail page with title/status, author, source fork and target, revision information, reviewers, and Conversation, Changesets, Files changed tabs. Reuse sanitized Markdown and safe diff rendering, not the hardcoded sample PR template. The comparison shows source-side changes since the relevant common ancestor and the changesets being proposed. If there are multiple valid merge bases and the selected Mercurial implementation cannot produce an unambiguous safe comparison, require the author to update the source rather than showing an arbitrary base.

Support general comments, inline comments anchored to immutable comparison/file/side/line, reviewer assignment, Approve, Request changes, and resolution of discussion threads. After updates, keep old inline comments visible as outdated rather than silently moving them to different code. Binary/large diffs show clear notices and permission-checked content links; never load unbounded files into HTML.

Proposed review policy: approvals are informational by default, with no mandatory reviewer count. Any current target writer who can read the source can merge, including the author. Authenticated readers of both repositories may leave discussion comments; formal Approve/Request changes actions are reserved for assigned, non-author reviewers who currently have target write permission and source read permission. Thus an arbitrary public-repository reader cannot block a merge.

The PR author and target writers may assign eligible reviewers. A target writer may assign themselves if they are not the author. Either may remove a reviewer who has no unresolved Request changes; removing an assignment must not erase or bypass a blocking review. A current Request changes blocks merging until its reviewer clears it or a target administrator dismisses it with a recorded reason. If a reviewer loses the required access or their account is disabled, their formal review becomes inactive and stops blocking, with an audit event; regranting access requires a new review. Approvals expire when either compared node changes; requests for changes remain until explicitly resolved/dismissed or made inactive by access loss. An author cannot approve their own PR. Configurable branch protection and mandatory-review policies are deferred.

PR authors may edit their title/description, refresh/reselect their permitted source, and decline their open requests. Target writers may decline and reopen requests; authors may reopen their own declined requests if current permissions and refs remain valid. Changing a destination after creation is deferred: decline and create a new PR. Merged requests cannot reopen. Repository lifecycle events can close unavailable requests with a visible reason.

### Visibility and deletion

PR content, inline diffs, comparisons, reviewer suggestions, notifications, and counts require read permission to both source and target while both exist. Source/target privacy must match in this initial version, so opening a PR cannot publish private source data into a public target. Private-fork ancestry rules still apply. PR comments/metadata inherit this access check; do not expose them via an unguarded issue route.

Persist immutable comparison artifacts in application-owned, authorization-checked storage, separate from the target's published repository. Unmerged source commits must not become cloneable from the target merely because someone opened or reviewed a PR. On source deletion, automatically decline open PRs and purge unmerged comparison payloads; retain a target-visible audit tombstone without source content/comments. For merged PRs, retain review history and merged content under target read policy because that content was intentionally accepted into the target. Target deletion removes its PR records/artifacts through the normal retryable deletion lifecycle.

### Merge behavior

Proposed strategy: preserve source changeset identities and the named branch of the selected destination node. If the target is an ancestor of the source **and both nodes have the same named branch**, accept the source history and advance the selected target bookmark where applicable. If the nodes have different named branches, create a normal two-parent merge changeset on the destination's named branch with the target node as first parent and source as second parent, even when the source descends from the target. Diverged histories also produce a two-parent merge on the destination branch. A selected target bookmark advances to the resulting node; its prior node's named branch remains the integration branch. Merely importing a different named branch does not count as merging into the destination branch.

Do not squash, rebase, rewrite source changesets, automatically delete branches, or change unrelated bookmarks. Do not close the source named branch automatically. If the selected source is already an ancestor of the target, record the request as already integrated without creating another merge. Cross-named-branch ancestor merging must be verified against the supported Mercurial versions during feasibility work; the installed 7.2.4 `mercurial/merge.py` explicitly permits that case while rejecting a same-branch ancestor merge. No debug-parent manipulation or history rewriting is an acceptable fallback.

Merge commit metadata identifies the acting authenticated user and references the PR. The UI previews the action and resulting target. Server-generated time/identity fields cannot be supplied as arbitrary command arguments. Preserve the source's original authors on its changesets.

Conflicts block merge. Show affected paths and native Mercurial guidance for resolving locally and pushing to the source; do not pick ours/theirs silently. There is no web conflict editor in this milestone.

The merge engine must check out only an isolated, disposable, application-owned working repository. No checkout is added to the live hosted store. Pin both endpoint nodes; collect only required visible history from server-resolved local paths; use controlled config, bounded processes, and explicit built-in noninteractive merge tools. Never execute repository scripts, builds, arbitrary hooks, external merge tools, or nested repositories. Preflight every materialized path and supported file kind; path traversal, metadata collisions, unsafe symlinks, and files the host cannot safely represent must fail closed with local-resolution guidance. Source browsing remains supported for such repositories. Subrepository declarations must not trigger fetch/update behavior.

Before publishing, revalidate source read/target write access, current selector values, review state, repository readiness, and the exact source/target nodes the user confirmed. Concurrency checks must cover all writers, including SSH and HTTPS pushes, not just another web merge. A stale target/source causes a refresh-and-review response; never silently retry with new code.

Publishing the accepted history and moving the selected target bookmark must either complete as one guarded operation or remain recoverably incomplete behind the operation state, with no false Merged status. Add a durable merge-operation record before publication containing actor, PR/comparison revision, expected nodes, planned resulting node, and operation identity. Use destination serialization coordinated with the normal transport transaction hooks/locks and recheck expectations inside the write transaction. Mercurial owns its locks; do not acquire its lock externally and then spawn a child that deadlocks waiting for it.

After repository publication, finalize the PR/audit event in the database. On crash or ambiguous completion, reconcile the persisted result node/operation before any retry. Never create a second merge commit or advance a different bookmark because a response was lost. A worker restart must distinguish unstarted, prepared, published, finalized, and failed operations. Source deletion/transfer/permission changes participate in the same lifecycle coordination.

This transaction design requires a bounded feasibility milestone after the existing transport gate is satisfied, before building out the PR forms. Prove controlled local import, noninteractive merge, guarded target publication/bookmark update, concurrency with native pushes, cancellation, and crash reconciliation within the existing Go/managed-Mercurial architecture. Do not assume several CLI commands form one transaction. If the required atomicity or safety cannot be achieved without changing that architecture, return with evidence and a revised proposal; do not quietly introduce Python extensions or weaker authorization.

## Data and service boundaries

Reuse the planned shared authorization and repository lifecycle services. Extend them for fork ancestry and web merge authority rather than inventing separate permissions in handlers. Suggested records:

| Record | Essential information |
| --- | --- |
| Repository fork relation | Direct source ID, family ID; existing owner, privacy, readiness and storage IDs remain authoritative |
| Recent visit | Account ID, repository ID, last successful visit time; unique account/repository pair |
| Pull request | Target repository/local number, source repository, author, title/body, selectors, state, latest comparison revision, timestamps |
| PR comparison | Immutable source/target/base nodes, safe bounded artifact reference, creation time and availability state |
| PR reviewer/review | Reviewer account, approval/request-change state, comparison revision, dismissal actor/reason |
| PR comment/thread | Author, comparison anchor when inline, sanitized-body source, resolved/outdated status |
| PR event | Actor/time, source update, reviewer/state/merge/lifecycle event; no secrets |
| Merge operation | Stable operation ID, expected nodes, prepared result node, actor, state, reconciliation metadata |

Keep `modules/hg` independent of models/web code. A composing application service handles fork/review/merge transactions, injected authorization, persistence, and lifecycle coordination. Handlers validate forms and render shared view models; templates contain no permission policy or command construction. The exact package/table names and whether comments share an existing safe storage abstraction are builder choices; PR lifecycle and authorization must remain explicit.

Use repository IDs internally, typed revision selections, full immutable node IDs, parameterized database queries, and direct process argument arrays. Preserve `AppSubUrl`, locale selection, CSRF and existing session behavior. New mutations require authentication, CSRF, permission checks, and input limits. PR artifact endpoints receive the same authorization as their owning PR, including source-read revocation. No background job may inherit a previous actor's reusable grant.

Proposed new mail behavior is deferred; in-app PR lists and audit events are required. Existing issue mail continues. This avoids making email delivery a prerequisite for the core review workflow.

## Implementation sequence and acceptance

This is one coordinated application project with ordered milestones, not two separately approved deliveries. A later implementation plan must make these dependencies concrete without duplicating another in-progress task:

1. Resolve and verify the earlier transport gate; integrate the existing ID-based authorization, repository lifecycle, and Mercurial browsing prerequisites.
2. Prove the fork/merge transaction and checkout-safety boundary, with native-client concurrency and failure tests.
3. Establish shared layout/components; migrate existing supported workspace, repository, account, organization, and Admin pages and remove obsolete presentation paths once unused.
4. Wire Search/Explore/Recents/Create to real filtered data and add fork creation/lifecycle.
5. Complete global/repository PR lists, comparisons, discussion/reviews, and guarded merge/recovery flow.
6. Finish integrated visual, accessibility, permission, native platform, and regression validation.

Meaningful acceptance cases include:

- Side-by-side visual checks of dashboard, Explore, source, global/repository issue lists, issue creation/detail, label/milestone management, PR list/detail, forms, and Admin overview/users/configuration at desktop and narrow widths. Avatar centers exactly in its hit target. No leftover old top navigation or Bootstrap-style page islands. Check shared tokens/components against `docs/DESIGN.md`.
- Keyboard opening/closing/focus restoration for every popout and drawer; error announcement, visible focus, contrast, long names, empty/error/loading states, and sub-URL links. Search cancellation must not display stale results for a newer query.
- Explore/Search/Recents and all counts immediately omit deleted/revoked private repositories. Recents deduplicates, survives rename and a new browser session, and isolates accounts. Guest pages preserve sign-in policy.
- Existing issue create/comment/label/assign/milestone/close/reopen, team permissions, account settings, and Admin mutations retain behavior and authorization. No real maintenance action runs in a visual test.
- Fork provisioning succeeds for public/private/personal/organization/empty sources, handles interruption, copies the selected fork's history, and cannot widen private ancestry access or fetch arbitrary URLs.
- A read-only upstream contributor can fork, push using native Mercurial, create a PR, receive inline/general review, update it, and have a target writer merge it. Target read-only users and unrelated users cannot merge. Target clone content is unchanged by mere PR creation/review.
- Same-repository and fork PRs, named-branch multiple heads, bookmark changes, deleted refs, Unicode/long refs, source updates, approval invalidation, requests for changes, decline/reopen, and source/target deletion follow the rules above. Unassigned/read-only reviewers cannot submit blocking reviews; assignment removal cannot bypass a live block; permission loss makes formal reviews inactive.
- Same-branch fast-forward/ancestor, cross-named-branch ancestor, and diverged two-parent merge cases preserve changeset IDs, advance the intended destination named branch, and only move the intended bookmark. Conflicts, unsupported checkout paths, nested repositories, injected configuration, and executable merge-tool attempts do not mutate the target or run repository code.
- Concurrent web merges/native pushes, permission revocation, cancellation, full disks/process limits, and crash between repository publication and DB finalization leave a reconcilable state with no duplicate merge or incorrect success notice.
- Builds, targeted unit/model/handler tests, and actual native integration runs on Windows, macOS, and Linux. Apply the retained Mercurial version matrix to new commands; unavailable combinations are reported, never presented as passed. Validate SQLite plus the supported database semantics affected by new transactions.

## Author's proposed defaults to check during approval

- Search initially searches repositories and owners; Recents is account-persistent (20 retained, 10 displayed).
- All supported existing page families share the UI; standalone authentication/installation pages use a reduced layout.
- Forks stay on this server, inherit source visibility, and private forks remain constrained by upstream access. Parent deletion/privacy changes wait for dependent forks to be removed.
- Reviews support inline comments and formal reviews by assigned target writers. Approvals are advisory by default; unresolved authorized requests for changes block merging unless dismissed by a target administrator with a reason.
- Use history-preserving merges that advance the destination named branch, including a two-parent merge when integrating a descendant from a different named branch, with local conflict resolution and no squash/rebase or automatic branch deletion.
- Add no PR mail integration, CI checks, merge queues, or branch protection settings in this milestone.

## Left to the builder

Choose concrete shared partial/module names, exact responsive breakpoints, compatible local icon implementation, query indexes, schema/migration mechanics within the fresh-database baseline, bounded artifact formats, job scheduling, and process/time/disk quotas. Tune typography and spacing against the visual reference. Choose exact Mercurial commands only after testing the supported versions and proving the transaction/safety contracts. These choices must not change the product behavior or weaken a prerequisite.

## Approved landing-page and identity phase

After the shared application UI work, implement the approved Hgs landing page and Go Gopher wearing a white laboratory coat and holding a beaker. The exact tagline is **A painless, self-hosted Mercurial service.** The approved [prototype and artwork](../../prototypes/landing-page/index.html) replace the earlier tentative beaver concept. Phase out Gogs in product copy in favor of Hgs or Mercurial service, preserving legal attribution and internal identifiers.

Landing-page Explore links and any repository preview show public repositories only. Omit them entirely when app.ini blocks anonymous browsing; the current policy is [service] REQUIRE_SIGNIN_VIEW = true. Enforce public-only queries/counts/pagination and direct-route access on the server. Signed-in workspace Explore retains its accessible-repositories behavior. Preserve conditional registration, supported social sign-in, and existing authentication policy.

Replace the main landing logo reference with a local Hgs asset based on the approved PNG, retaining the Go Gopher credit to Renee French and CC BY 4.0 attribution. Use the same beaker held by the approved Gopher as the favicon and navigation identity mark, with only small-size simplification and inverse contrast variants. [The design system](../../DESIGN.md#follow-on-landing-page-and-mercurial-identity) defines the composition, assets and public discovery rule; implementation plan Task 16 delivers this phase. Approval does not waive transport or backend acceptance gates.

## Explicit account-creation and installer coverage

Restyle the actual /user/sign_up and /install pages with the shared public layout, Hgs/beaker identity, labeled forms, validation/error feedback and responsive behavior. Registration includes disabled-registration, CAPTCHA, provider and activation states. Installation includes applicable database-specific fields, storage/service settings, supported mail/registration settings and first administrator, with real failure/success feedback and the installed/locked guard preserved. Passwords, database credentials and CAPTCHA answers are never echoed into returned HTML or captured by editor draft recovery. The [design system](../../DESIGN.md#account-creation-and-first-run-installation) defines the layout; Task 7 migrates the real forms and Task 16 verifies their final identity assets. Test installer submissions only against disposable configuration/databases.

## Approved completion details

Retain both Watch and Star in repository headers, preserving their distinct subscription/preference behavior and authorized counts. Convert their mutations to CSRF-protected POSTs and retain existing email behavior. Help opens a local shared-layout /help page with supported Mercurial workflows. Keep language selection in the shared footer, preserve operator-selected identity/links and legal notices, and replace obsolete Gogs/Git marketing/help destinations.

The existing mail-template families join the visual/branding scope; no new notifications are introduced. The full Gopher remains the landing mascot; its beaker supplies the favicon and navigation mark.

For issue/PR editors, recover unsaved work after session expiry only while the original page is still open. The user approved a cookie ID with temporary server-side draft storage. Bind recovery to the same account, authorized editor target and an unpredictable in-memory page capability; login in another tab preserves the editor. Navigation, reload, close, cancellation or successful submission discards recovery. A new visit or history restoration cannot resurrect it. Never auto-submit after recovery or overwrite newer in-page edits with an older saved version. Exclude credentials and attachment binaries.

The [design system](../../DESIGN.md#draft-recovery-after-login-expiry) specifies timing, limits, cookie attributes, failure feedback and cleanup. Its 30-minute inactivity TTL removes unreachable records when unload cleanup cannot be delivered; it is not a cross-navigation draft history. Implement and test these details in Tasks 17–18 after the existing core/landing work. This scope addition does not change ownership or status of the Mercurial prerequisites.

## Profile repositories and user avatars

Support the requested special repository conventions: a user's same-name repository supplies root README.md; an organization's .github repository supplies profile/README.md. Display sanitized Markdown prominently below the identity and above repository/activity content. Use the normal Mercurial landing revision, immutable links, bounded reads, and current repository/ancestry authorization; inaccessible content must not leak through a public profile or cache. Missing content leaves the ordinary profile unchanged. Repository renames/transfers and permission changes re-evaluate eligibility without automatic renames or repository creation. .github is a naming convention, not Git support.

Preserve and modernize the existing Gravatar path in shared user-avatar components. Uploaded avatars take precedence, then configured Gravatar, then local fallback; honor disabled/cached/source settings and avatar email selection. Use official-source HTTPS and normalized SHA-256 identifiers consistently, local failure fallbacks and centered stable geometry. No third-party profile import is added. [The design system](../../DESIGN.md#user-and-organization-profile-readmes) provides exact layout, file-path, size, permission and avatar rules; implementation Tasks 19–20 cover these additions.

## Approved profiles and contribution activity

On 2026-10-26 the user approved the [profile prototype](../../prototypes/profiles/index.html), including user/organization README sections, Gravatar presentation, contribution calendars, grouped activity, and organization People entries that navigate directly to individual profiles. Use real canonical profile URLs, shared templates and permission-filtered membership lists; keep the prototype's sample data and controls out of production.

Count changesets, opened issues, opened PRs and first formal reviews per reviewer/PR. Deduplicate changeset nodes across readable forks and attribute changesets by explicitly verified author-email ownership, never by pusher or automatically activated email flags. Repository creation is timeline-only. Organization activity covers its current repositories rather than all activity by its members. Dates use UTC; year/day/category controls, summaries and paginated groups derive from the same authorized data set.

The [approved design system](../../DESIGN.md#approved-profiles-and-contribution-tracking) defines attribution, confirmation, deduplication, visibility, rebuilding, lifecycle, pagination and accessible UI defaults. User-scope changesets require a unique verified email; organization totals may include unattributed authors without exposing their email. Public counts exclude private activity entirely. PR/review visibility requires both endpoints. Rebuildable indexing must not bypass the hosting gate or index unpublished comparison artifacts. Implementation Tasks 21–23 deliver this scope after README/Gravatar Tasks 19–20.

## Final maintenance and error-page phase

After the approved profiles/contribution work, deliver maintenance (HTTP 503), internal-error (HTTP 500) and not-found (HTTP 404) pages in the shared reduced Hgs style. The user requested a hardhat Gopher for maintenance, a lab-coat Gopher reacting to an exploded experiment for 500, and a lab-coat Gopher searching with a magnifying glass for 404. Preserve mascot continuity, local assets and attribution; exact new artwork will be generated and inspected during implementation.

The [design system](../../DESIGN.md#final-phase-maintenance-and-error-pages) defines copy, actions, startup-only configuration, protocol-aware maintenance admission, true status codes, no-store responses, sensitive-detail suppression and dependency-free rendering/fallbacks. Maintenance blocks web/API/transport work rather than simply changing the landing page; it does not introduce live toggles or replace safe operator shutdown procedures. Tasks 24–25 are the final implementation phase and include final validation of all earlier changes.

## Technical references

The following are command-level evidence, not proof of our complete merge design:

- [Mercurial clone](https://www.mercurial-scm.org/help/commands/clone): supports a no-working-copy clone and records a default source path; the application must replace generated configuration.
- [Mercurial pull](https://www.mercurial-scm.org/help/commands/pull): imports history and normally leaves the working directory unchanged.
- [Mercurial merge](https://www.mercurial-scm.org/help/commands/merge): merges into a working directory, requires a subsequent commit, and supports explicit revision/tool selection.
- [Mercurial merge tools](https://www.mercurial-scm.org/help/topics/merge-tools): tool selection can involve external programs, so the service must explicitly select and validate its noninteractive built-in behavior.

## Approval

The user approved the shared UI coverage, fork-based PR workflow, proposed defaults above, and dependency on the existing Mercurial gate on 2026-10-26. The next step is the [implementation plan](../plans/2026-10-26-shared-ui-and-pull-requests.md), not immediate product edits. The historical hosting design remains valid except for the explicit scope extensions recorded here; the plan defines their integration order.
