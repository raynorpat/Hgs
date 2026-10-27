# Hgs design system

Status: Shared UI, Hgs landing-page composition, lab-coat/beaker Go Gopher artwork, profile pages, contribution tracking, and maintenance/500/404 pages with their Gopher illustrations approved on 2026-10-26. The public-only, configuration-controlled landing Explore rule below is part of that approval. Production implementation remains subject to the existing prerequisite gate.

Updated: 2026-10-27

## Purpose and references

Use one visual and interaction system across workspace, repositories, **Issues**, pull requests, organizations, accounts, and administration. Retain the Go server-rendered application and Mercurial terminology.

The visual direction comes from the user's Bitbucket screenshots: blue navigation on the left, a slim utility rail, white content, restrained borders, and compact readable lists. Use Hgs as the requested product identity and retain existing legal attribution. The landing-page and Mercurial-focused mascot direction is recorded in the follow-on phase below. Do not introduce Bitbucket logos or proprietary assets.

- [Project design and behavior](superpowers/specs/2026-10-26-shared-ui-and-pull-requests-design.md)
- [Saved interactive layout study](superpowers/specs/references/2026-10-26-bitbucket-layout-study.html)
- [Hgs design system artifact](https://claude.ai/artifact/ECMF3DDZwm2bijjktqfPJ6): the tokens, shared `hg-` component stylesheet, live component previews, icons and approved Gopher artwork from this document. When the two disagree, this document wins; update the artifact to match.

This document defines the shared design system. The project design defines scope, permissions, Mercurial behavior, and implementation prerequisites. The layout study illustrates appearance; its sample data and placeholder controls are not production requirements. Token names and values below are the implementation baseline, not a claim that the application already uses them.

## Principles

1. Keep the location and primary action clear: context, page title, then content.
2. Reuse the same components across Issues, PRs, settings, and Admin. Avoid independent page-specific themes.
3. Favor useful information density over decorative cards, gradients, and large empty headers.
4. Make navigation predictable and distinguish the workspace from the current repository or administration context.
5. Make every interaction accessible by keyboard and provide text alongside meaningful state indicators.
6. Render real authorized data. Empty, loading, and failed states are first-class designs.
7. Use Mercurial concepts accurately: changesets, named branches, heads, bookmarks, and tags are distinct.

## Application layout

| Region | Desktop baseline | Behavior |
| --- | --- | --- |
| Utility rail | 56px wide, full viewport height | Global tools at the top; Admin, Help, and Account at the bottom |
| Context navigation | 208px wide beside the rail | Workspace, repository, or Admin links with a clear active item |
| Content | Remaining width; approximately 32–40px outer padding | Expand for tables and source views; constrain long prose/forms |
| Page header | Breadcrumb when useful, title, primary action | Wrap actions naturally on narrow screens |
| Detail metadata | Approximately 240px side column when space permits | Move below the heading or main content on narrow screens |

The combined desktop navigation width is 264px. Keep one scrollable content area and avoid multiple unnecessary nested scroll regions. Navigation may scroll independently when its contents exceed the viewport. Do not add a second global top navigation bar.

At roughly 800px, replace the expanded context navigation with a labeled menu trigger and drawer. The final breakpoint should follow actual content. Keep Search, Explore, Recents, and Create reachable; do not simply hide the menu. Drawers restore focus to their trigger when closed. Dense tables and code can scroll horizontally inside their own region without widening the entire page.

### Navigation order

| Context | Items, in order |
| --- | --- |
| Utility rail, top | Home/identity mark, Search, Explore, Recents, Create |
| Utility rail, bottom | Administration for authorized administrators, Help, Account |
| Workspace | Overview, Repositories, Organizations, Pull requests, Issues |
| Repository | Back to workspace, repository identity, Source, Changesets, Branches, Pull requests, Issues, Settings |
| Administration | Back to workspace, Overview, Users, Organizations, Repositories, Authentication, Configuration, Notices, Monitoring |

The active navigation item fills `--color-nav-active`, sets its label in bold `--color-on-strong`, and shows a `--border-active` `--color-on-strong` bar on its left edge (11.1:1), so it is never distinguished by fill alone; hover shows the fill without the bar. Mark it with `aria-current="page"`.

The workspace repository list emphasizes owned/collaborative repositories. Explore is the server-wide catalog of all repositories the viewer may read. Permission-dependent links follow the shared authorization service. Hidden controls are not an authorization mechanism.

## Color tokens

Use semantic CSS custom properties. Do not scatter literal colors through page templates.

| Token | Value | Use |
| --- | --- | --- |
| `--color-nav` | `#0747A6` | Expanded blue navigation |
| `--color-rail` | `#064299` | Narrow utility rail |
| `--color-nav-active` | `#003884` | Active/hover item on blue navigation |
| `--color-nav-text` | `#DEEBFF` | Navigation text and icons |
| `--color-nav-caption` | `#B3D4FF` | Small context captions on blue navigation |
| `--color-on-strong` | `#FFFFFF` | Active navigation and primary-button text |
| `--color-primary` | `#0052CC` | Primary buttons, links, selected light-surface tabs |
| `--color-primary-hover` | `#0747A6` | Primary-button hover |
| `--color-selected` | `#DEEBFF` | Selected item on light surfaces |
| `--color-surface` | `#FFFFFF` | Main content and popouts |
| `--color-surface-subtle` | `#FAFBFC` | Secondary section backgrounds |
| `--color-surface-muted` | `#F4F5F7` | Neutral controls and code backgrounds |
| `--color-text` | `#172B4D` | Primary text |
| `--color-text-secondary` | `#42526E` | Secondary labels |
| `--color-text-subtle` | `#5E6C84` | Metadata and helper text |
| `--color-border` | `#DFE1E6` | Decorative separators and containers |
| `--color-control-border` | `#7A869A` | Essential input/control boundaries |
| `--color-focus` | `#0052CC` | Focus ring on light surfaces |
| `--color-focus-inverse` | `#FFAB00` | Focus ring on blue navigation |
| `--color-success-text` / `--color-success-bg` | `#006644` / `#E3FCEF` | Success and completed actions |
| `--color-warning-text` / `--color-warning-bg` | `#7A4D00` / `#FFFAE6` | Attention or unresolved conditions |
| `--color-danger-text` / `--color-danger-bg` | `#BF2600` / `#FFEBE6` | Errors, failure, destructive-action context |
| `--color-info-text` / `--color-info-bg` | `#0747A6` / `#DEEBFF` | Informational and open states |
| `--color-neutral-text` / `--color-neutral-bg` | `#42526E` / `#EBECF0` | Neutral labels and closed items |
| `--color-merged-text` / `--color-merged-bg` | `#403294` / `#EAE6FF` | Merged pull-request state |
| `--color-backdrop` | `#091E4266` | Scrim behind the navigation drawer and modal dialogs |
| `--color-contribution-0` … `-4` | `#EBECF0`, `#C6E8DB`, `#79D2AD`, `#36B37E`, `#00875A` | Contribution-calendar levels, none to highest |

Require readable contrast for each actual foreground/background pairing, including hover and selected states. Use at least 4.5:1 for ordinary text and 3:1 for essential control boundaries/focus indicators. Subtle decorative dividers need not substitute for a visible control boundary. Do not communicate status through color alone.

`--color-text-subtle` passes on `--color-surface`, `--color-surface-subtle` and `--color-surface-muted` but not on `--color-selected` (4.4:1); metadata inside selected items uses `--color-text-secondary` (6.5:1). `--color-focus-inverse` is for blue navigation only (1.9:1 on white). Contribution levels 0–3 are below 3:1 on white, which is acceptable only because every day also carries its date and count in text.

State mapping: Issues Open uses info and Closed uses neutral. Pull requests Open uses info, Merged uses merged, and Declined uses neutral. Success, warning and danger describe checks, conditions and errors, not issue or PR lifecycle.

Repository-specific issue-label colors remain supported. Choose a readable foreground for each label and preserve its text; do not map every user label into a fixed status palette.

## Typography

| Role | Style | Size / line height | Weight |
| --- | --- | --- | --- |
| Page title | `page-title`, `page-title-narrow` | 28px / 36px; 24px / 32px on narrow screens | 500 |
| Section heading | `section-heading` | 20px / 28px | 600 |
| Subheading | `subheading` | 16px / 24px | 600 |
| Body and navigation | `body`, `body-strong` | 14px / 21px | 400; active/emphasized 600 |
| Dense table text | `table` | 13px / 20px | 400 |
| Metadata and helper text | `meta` | 12px / 18px | 400 |
| Compact status label | `status-label` | 11px / 16px | 600 |
| Source, hashes, clone commands | `code` | 13px / 20px monospace | 400 |
| Public wordmark | `wordmark` | 30px / 36px, −1.4px tracking | 750 |
| Landing headline and lead | `landing-headline`, `landing-lead` | 58px / 65px (38–42px on narrow screens), −2.2px tracking; 18px / 30px | 650; 400 |

The public rows come from the approved landing prototype and apply only to the reduced public layout.

Use `-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif` for interface text and `ui-monospace, SFMono-Regular, Consolas, monospace` for code. Prefer normal sentence case. Reserve uppercase for short status labels, not ordinary navigation or instructions. Do not require downloaded fonts.

Long repository/issue titles wrap in primary content. Truncate only constrained navigation or metadata, with the full value available to keyboard and assistive-technology users. Do not shrink essential text to fit.

## Spacing, shape, and elevation

| Token family | Values |
| --- | --- |
| Spacing | `--space-1: 4px`, `--space-2: 8px`, `--space-3: 12px`, `--space-4: 16px`, `--space-5: 24px`, `--space-6: 32px`, `--space-7: 40px`, `--space-8: 48px` |
| Radius | `--radius-control: 3px`, `--radius-panel: 4px`, `--radius-popout: 6px`, `--radius-round: 50%` |
| Borders | `--border-divider: 1px` ordinary divider; `--border-selected: 2px` selected-tab indicator; `--border-active: 3px` active-navigation indicator |
| Popout shadow | `--shadow-popout: 0 8px 28px rgb(9 30 66 / 25%)` |
| Focus | `--focus-width: 3px` outline with `--focus-offset: 2px`; inverse focus color on the rail and context navigation |
| Sizes | `--control-height: 36px` buttons/inputs/selects; `--utility-target: 40px`, `--utility-target-coarse: 44px`; `--icon-size: 20px`, `--icon-size-inline: 16px` |
| Layers | `--z-content: 0`, `--z-sticky: 10`, `--z-popout: 20`, `--z-backdrop: 30`, `--z-dialog: 40`, `--z-notice: 50` |

Use flat surfaces for ordinary lists and forms. Reserve elevation for floating surfaces. Keep table cells around 12px vertical and 12–16px horizontal padding; allow rows to grow for multiline information.

## Icons and avatars

Use one local outline icon family with consistent 1.5–2px stroke weight. Default icons are 20px; inline table icons may be 16px. Use a compass for Explore, history/clock for Recents, magnifier for Search, plus for Create, and a branching-request icon for Pull requests.

Icon-only controls need accessible names and hover/focus tooltips. Decorative icons use `aria-hidden="true"`. The icon is not the hit target: utility buttons have a common 40px square target, expanded to at least 44px for coarse pointers where practical.

The account image or fallback initials must share the same centered geometry. Use a circular 28px image inside the square button, with grid/flex centering, zero internal offset, and `object-fit: cover`. Hover/focus background belongs to the button, not the image. Fallback initials use `--color-info-text` on `--color-selected`. A fallback initial must not shift when replaced by an image. Test both axes, image loading/failure, browser zoom, and focus/hover states.

## Shared components

| Component | Design and behavior |
| --- | --- |
| Page header | Optional breadcrumb, one page title, primary action, restrained secondary actions |
| Button | Primary blue, neutral, or destructive; consistent height/padding; text labels for consequential actions |
| Text field/select | Visible label, helper/error text association, clear border/focus, retained values after errors |
| Table/list | Semantic headings, aligned columns, stable row identity, permission-filtered pagination/counts |
| Tabs | Selected underline plus text/state semantics; keyboard-appropriate links or tab behavior |
| Badge | Text label with semantic background; never color-only |
| Alert | Severity icon/text, concise explanation, actionable recovery where possible |
| Empty state | Explain why the list is empty and show a relevant permitted action |
| Loading state | Reserved layout space and an accessible status; prevent duplicate submission |
| Popout | Anchored surface, one open at a time, bounded width/height, keyboard-reachable content |
| Modal/drawer | Clear title, focus management, Escape behavior, visible close/cancel, return focus |
| Pagination | Preserve filters and context in URLs; announce current page and disabled controls |
| Markdown editor | Shared Write/Preview behavior, labeled textarea, safe rendered preview, preserved draft on errors |
| Discussion | Author/avatar/time, readable body, attachments, permitted actions, separate status-event rows |
| Diff viewer | Monospace lines, explicit old/new sides, `+`/`−` markers on `--color-success-bg`/`--color-danger-bg` lines and hunk headers on `--color-info-bg`, binary/truncation notices, safe immutable anchors |

Prefer real navigation links for page transitions and buttons for actions. Menus/popouts do not require JavaScript to emulate ordinary links. Do not use tables for non-tabular page layout. Do not copy legacy global selectors into the shared stylesheet.

### Utility interactions

- **Search:** opens and focuses a repository/owner search field; show authorized matches, a no-results state, and a path to the full Explore results. Prevent stale responses replacing a newer search.
- **Explore:** navigates to the full accessible-repositories page; it is not a popout or a public-only catalog. The anonymous landing page's public-only catalog is a separate destination; see Public repository discovery.
- **Recents:** opens the account's latest accessible repository visits; deduplicate by repository ID and resolve current names. Include Clear recents. The project design specifies retention and permission rules.
- **Create:** opens a Create repository action leading to the real form. Fork creation has its own source-aware action.
- **Account:** opens profile, settings, and sign-out actions; center its image inside the same hit-target geometry as the other utilities.

Escape/outside click closes popouts; return focus to the trigger on keyboard dismissal. Standard popouts use natural tab order; do not trap focus unless presenting a modal dialog/drawer. Ensure floating panels remain inside the viewport and usable with the onscreen keyboard.

## Issues

Issues is a first-class part of the shared UI refactor, including the global Issues page and the complete repository issue experience. It must not retain the old Bootstrap layout while the rest of the application changes.

### Global and repository lists

- Global Issues aggregates only repositories the account may read; each row identifies its repository and links to the canonical issue detail.
- Repository Issues appears immediately below Pull requests and uses the same page header, tabs, filters, table/list, badges, and pagination components.
- Present Open/Closed states and existing filters such as All, Assigned to you, Created by you, and Mentioning you. Preserve supported label, milestone, and assignee filtering; do not invent unsupported filter behavior.
- Show issue number, title, labels, author, last relevant activity, assignee when available, and comment count without making every value an equally prominent badge.
- Put New issue in the page header when permitted. Creating from a global context must identify the target repository before submission.
- Include distinct empty states for no issues, no filter matches, unavailable data, and insufficient permissions. Counts and filter choices must not reveal private repository data.

### Create and detail

- Creation uses the shared form and Markdown editor, preserving title/body validation, existing attachment behavior, and permitted assignment/label/milestone controls.
- Detail shows the issue title and number, explicit Open/Closed status, author/timestamps, body, and chronological discussion. Place metadata in a secondary column on desktop and an accessible stacked section on narrow screens.
- Reuse the discussion component for comments and attachments, with visually distinct close/reopen events. Keep existing comment anchors and canonical links working.
- Preserve permitted edit, comment, assign, label, milestone, close, and reopen operations. Do not add permission merely by displaying a control or redesigning its placement.
- Labels and milestone management use shared forms/tables and retain existing behavior and validation. Issue attachments remain authorization-checked.

Do not add a Kanban board, issue priorities, new workflow states, or issue-to-PR conversion as an incidental part of the visual refactor. Issues and PRs may share components but retain their own routes, records, counts, and lifecycle rules.

## Other page families

| Family | Shared-system application |
| --- | --- |
| Workspace | Activity and repository lists; honest empty states; organization context switcher |
| Repository source | Revision selector, path breadcrumb, file table, README, Clone and Fork actions |
| History/branches | Typed Mercurial selectors, explicit heads/bookmarks/tags, changeset links and bounded diffs |
| Pull requests | Global/repository lists, source/target identity, comparison tabs, conversation/review controls, merge state |
| Organizations/accounts | Membership/profile lists, settings forms, SSH keys, existing security and account controls |
| Admin | Contextual blue navigation and shared lists/forms for users, organizations, repositories, authentication, configuration, notices, monitoring |
| Authentication/installer/errors | Shared tokens and components with a reduced anonymous layout |

Admin statistics must be real. Configuration remains read-only where appropriate. Maintenance operations must match supported Mercurial behavior; do not relabel an old Git operation. Confirm destructive mutations, preserve CSRF and authorization, and show progress or failure without implying completion prematurely.

PR controls must reflect actual permission and comparison state. “Open,” “Merged,” and “Declined” are text states, not just colors. Display source fork and target clearly. Full fork privacy, review authority, and merge behavior live in the project design.

## Content, motion, and accessibility

- Use concise action labels: Create repository, New issue, Create pull request, Save changes. Errors explain the problem and how to recover.
- Put translatable product copy in locale resources; respect existing language selection and application sub-URLs. Allow room for longer translated labels.
- Sanitize rendered Markdown and escape source/user text. Display untrusted strings as content, never executable markup.
- Use semantic headings, landmarks, form labels, links, and table headers. Include a skip-to-content link and appropriate current/expanded state attributes.
- Announce meaningful asynchronous results and errors without repeatedly reading entire lists. Preserve draft input after failed submissions.
- Support text zoom and narrow viewports without hiding core actions. Keep keyboard focus visible and out from behind sticky overlays.
- Use short, subtle transitions only where they help track a state change; honor reduced-motion preferences. No essential meaning depends on animation.
- Ensure disabled controls have an understandable reason nearby. Unauthorized data/actions may be omitted; do not expose private details in tooltips or hidden markup.

## User and organization profile READMEs

Render a prominent Markdown introduction below the profile identity and above repository/activity tabs or repository lists, using the shared README typography and responsive content surface.

| Profile | Special repository | File |
| --- | --- | --- |
| User alice | alice/alice | README.md at the repository root |
| Organization example | example/.github | profile/README.md |

Repository matching follows existing canonical, case-insensitive owner/repository name lookup; file paths are exact and case-sensitive as stored by Mercurial. The organization convention is literally .github, not .hgs. It grants no Git-specific behavior: these are ordinary local Mercurial repositories. Verify that creation and rename validation allow this safe repository name without relaxing traversal or metadata-path restrictions.

Read from the same default/landing revision policy as the source browser, pinning the resolved node for Markdown, relative links and images. Do not hard-code main/master or check out a working tree. For user content resolve relative paths from the repository root; for organization content resolve them from profile/. Route local images/files through existing authorization-checked source/raw handlers and respect AppSubUrl. Reject traversal outside repository content, unsafe URL schemes, metadata paths and active content. Use the existing Markdown sanitizer rather than treating repository HTML as trusted.

The panel is visible only to visitors with current read access to the special repository, including private-fork ancestry rules. Public visitors never receive private text, filenames, source links or cached fragments; authorized viewers may see private profile content. Retain instance sign-in policy. Cache by repository ID, immutable revision and rendering context, with authorization rechecked on every response; never publish a private rendered fragment through a shared public cache.

Missing repository/file, empty content or an empty repository leaves the normal profile intact with no empty README panel. Render only ordinary UTF-8 text up to 1 MiB; binary, oversized or symlink README files are not rendered as Markdown. For an accessible but unrenderable README show a compact unavailable message and safe source link; operational failure must not take down the whole profile. Updates, deletion, visibility/access changes and transfers take effect on the next authorized view. Renaming a user or repository re-evaluates the name convention; do not silently rename a repository or keep displaying the old match.

Show a subtle source-repository/README link beneath rendered content. Management happens through the existing repository workflow; no separate web editor or automatic repository creation is introduced.

Organization People entries link directly to the selected user profile. Make the avatar/name row a clear keyboard-accessible link; preserve normal navigation and permission rules for which memberships are visible.

### Approved profiles and contribution tracking

Approved on 2026-10-26: the [interactive profile study](prototypes/profiles/index.html), user and organization profile layouts, README placement, avatar presentation, clickable People entries, contribution calendars, category summaries and grouped activity. [Prototype notes](prototypes/profiles/README.md) record the sample-data limitations. Rebuild the approved presentation with shared production components and real authorized data; omit prototype switches, fictional members, generated counts and placeholder destination dialogs.

User profiles have Overview, Repositories and Activity tabs. Organization profiles additionally have People. People rows and sidebar member avatars/names are real links to the selected person's canonical profile, supporting keyboard use, opening in another tab and browser Back. Show only memberships visible under existing organization policy. Preserve existing organization management routes and owner-only controls. README content remains prominent below identity and above the tabs; Gravatar/upload/fallback rules below apply throughout.

Overview combines repository summaries, the contribution calendar, activity breakdown and monthly timeline. Activity provides a focused calendar/timeline view. Keep independent Watch and Star controls. Organization calendars aggregate contributions to repositories currently owned by that organization, not members' unrelated work elsewhere; contributors need not currently be members. Issues belong to their containing repository; PR and review contributions belong to the destination repository, with source read access still required for visibility. Public People listings and repository contributor identity are distinct concepts.

#### Counting and attribution

The approved categories are changesets, opened issues, opened pull requests and submitted formal reviews. Repository creation appears in the timeline but adds no calendar count. Comments, stars, watches, pushes, reopening, merging and repeated review edits do not create extra contributions. A PR has one opening contribution; a reviewer has at most one review contribution per PR, dated at the first formal approval/request-changes submission. Current status can be shown separately from the historical contribution event.

Implementation defaults: count every visible non-secret changeset in a ready repository, including non-default named branches and merge changesets. Attribute only by a uniquely matched, verified author-email association; do not credit the pusher, display-name match, avatar email or co-author text. Normalize email with trimming and case folding; do not strip plus tags or provider-specific dots. Preserve the full Mercurial node as the changeset identity. Within a user or organization scope, count the same node once even if several accessible repositories/forks contain it. Choose a stable accessible repository link for display; do not let an inaccessible copy suppress an accessible copy.

Verification means completed email-ownership confirmation, not account activation alone. Existing code may automatically activate primary/additional email addresses when registration confirmation is disabled; those flags alone are insufficient. Record explicit verification evidence on successful confirmation, reuse existing account-email confirmation templates and token protections, and offer an owner-initiated Verify action with the existing 180-second resend limit when mail is configured. This does not change sign-in/registration eligibility or send unsolicited mail. Without verification, changesets remain unattributed to a user; organization repository totals can still include them. Never publish author-email lookup material. Email addition, removal or verification recomputes historical attribution; disputed/nonunique matches stay unattributed. These are authorship associations, not cryptographic proof that a commit author was truthful.

Use original changeset author timestamps and canonical application creation/submission times, grouped into UTC dates with an explicit UTC label/help text. Do not use push/import time as authorship time. Exclude invalid/future timestamps until eligible. The current year ends at the current UTC date; earlier years include every date, including leap day. Year choices run from the first visible contribution year to the current year; show the current year with zeros for an empty profile. Ignore client-supplied sample years and totals.

#### Visibility, correctness and lifecycle

Apply current repository readiness, read permissions, private-fork ancestry and source-object visibility before counting, deduplication, grouping, pagination, year lists and links. PR/review events require read access to both endpoints. Public visitors see only public activity; do not expose anonymous private counts, empty private-year hints, repository names or identifiers. Authenticated visitors see only the subset they can currently read. Instance sign-in policy still applies.

Repository transfer changes current organization scope; rename updates links without duplicating events. Deletion and visibility/access changes remove inaccessible contributions from the next response. Account deletion removes user attribution while retaining only source events allowed by the underlying deletion policy. Email reassignment requires renewed verification and no simultaneous ambiguous owner. Editing/reopening issues or PRs, retrying jobs and pushing identical nodes cannot inflate totals. Show current accessible titles/status from their canonical source, with escaped text; do not trust historical privacy flags or legacy recipient-fanned activity rows.

Store a rebuildable event index using immutable IDs and per-repository node membership. Index committed changes asynchronously in bounded batches and reconcile on startup/periodically; no profile request scans full repository history. Do not modify the transport authorization/transaction gate to emit activity. Jobs use internal read-only maintenance authority with current repository lifecycle checks, never a saved user's grant. Rebuilding catches missed enqueue signals and excludes aborted pushes and unpublished PR comparison/merge artifacts. Query-time authorization remains authoritative during index lag. Expose an honest updating/unavailable state and last successful index time; never claim partial data is complete. Canonical-object deletion/permission checks take effect even before stale index entries are purged. Do not cache rendered profiles or totals across viewers.

#### Interaction and acceptance

Year selection updates the calendar, summary and timeline together. Day selection and category filters narrow the same underlying event set; Clear restores the yearly view. Summary totals reconcile with calendar totals and fully paginated activity; repository creation is separately identified as timeline-only. Group rows by UTC month, category and repository, with issue/PR/review status cards and accessible links to the actual event. Use deterministic newest-first cursor pagination, 25 groups by default and 50 maximum; Show more does not repeat/skip entries in a stable index snapshot. Revalidate visibility on every page and linked destination.

Use shared light-theme tokens, the five `--color-contribution-0` … `-4` levels and a Less/More legend. Provide dates/counts through text and focus/hover details, keyboard day selection or an equivalent labeled date field, visible focus and an accessible list alternative. Do not rely on color alone or introduce hundreds of mandatory tab stops. On mobile the calendar scrolls within its own region; the document stays within the viewport. Keep calendar controls, clickable People rows and empty/loading/error states usable with keyboard, touch and text zoom.

Tasks 19–20 supply READMEs/avatars; Tasks 21–23 supply verified attribution, event indexing, authorized aggregates and production profile presentation. Existing prerequisite gates and execution ownership remain unchanged.

## User profile icons and Gravatar

Carry existing Gravatar support into all shared user-avatar components, including profile pages, account rail, lists, comments and PR review. Preserve uploaded-avatar preference: selected custom upload first, otherwise Gravatar when enabled, otherwise the local fallback/initials. A missing/broken custom upload or failed Gravatar load falls back locally without collapsing layout. Only the profile owner sees avatar-edit controls.

Respect [picture] DISABLE_GRAVATAR, GRAVATAR_SOURCE, and [service] ENABLE_CACHE_AVATAR. Disabling Gravatar prevents remote image URLs and outbound avatar-cache requests; uploaded/local avatars remain usable. Retain the configurable avatar email, falling back to the account email under existing rules. Do not expose the raw email address in markup or URLs merely to render an avatar.

For the official Gravatar source, normalize the avatar email by trimming and lowercasing and use its documented SHA-256 identifier over HTTPS. Preserve explicitly configured legacy-provider compatibility without silently switching providers or downgrading HTTPS. Keep lookup consistent across creation, profile edits and avatar caching; do not append malformed duplicate size parameters. The source and cache path must use the same hash format. No Gravatar profile API, API key or third-party profile import is needed.

Keep dimensions stable, meaningful alt text and image-error fallbacks. The account rail remains a centered 28px circle inside its 40px control; profile images can be larger using the same source. With local caching enabled, use bounded HTTPS fetches/timeouts, validated image data, and a local fallback on remote failure. Test using a fake remote image service, never real user emails.

References: [Gravatar identifier normalization](https://docs.gravatar.com/rest/hash/) and [avatar image requests](https://docs.gravatar.com/sdk/images/).

## Final phase: maintenance and error pages

The user approved all three page compositions and their exact Gopher illustrations on 2026-10-26. These remain the final parts of the implementation plan. Use a reduced Hgs layout with the beaker mark, shared typography, a single local illustration, a short heading/message and useful recovery links. Keep page text separate from the artwork. These pages must remain usable without a session, database, remote image server or full application navigation.

| State | Illustration | Heading and actions |
| --- | --- | --- |
| Maintenance, HTTP 503 | The same Go Gopher wearing a hardhat and white lab coat, holding a wrench, ready to work | "We'll be back soon." Explain that Hgs is undergoing maintenance; offer manual Check again and a minimal local Help view. |
| Internal error, HTTP 500 | The Go Gopher in a lab coat, with soot/ruffled appearance as though a beaker experiment just exploded in front of him | "Something went wrong." Say the request could not be completed; offer Home and Help, and an opaque request ID when available. |
| Not found, HTTP 404 | The Go Gopher in a lab coat, using a magnifying glass to search for something | "We couldn't find that page." Offer Home and Help without implying whether a private resource exists. |

Match the approved landing Gopher's character, palette and illustration style. The 500 scene is a lighthearted, non-graphic lab mishap; no injury is needed. Keep a transparent background, retain Renee French/Go Gopher CC BY 4.0 attribution and adaptation credits, and package all images locally. The [status-page prototype](prototypes/status-pages/index.html) contains the three approved illustrations and page compositions matching the existing mascot, with [prompts and attribution](prototypes/status-pages/README.md). The user additionally requested the maintenance Gopher retain a white lab coat. Reuse the exact approved hgs-maintenance.png, hgs-error.png and hgs-not-found.png files from docs/prototypes/status-pages during production integration; do not regenerate or replace them unless the user requests an art change. Keep the maintenance Gopher in its yellow hardhat and white lab coat with the wrench. Approval covers the visual design and artwork, not the prototype switches, simulated actions or fictional reference ID. Use alt="" for decorative illustrations when adjacent text already communicates the status; image failure must not remove the explanation or actions.

Implementation default: add startup configuration [server] MAINTENANCE_MODE=false, MAINTENANCE_MESSAGE="" and MAINTENANCE_RETRY_AFTER=300. The operator changes mode with a normal process restart; do not add a live Admin toggle, countdown, automatic restart, or promise that a page toggle drains writes. Retry-After accepts 60–86400 seconds and is a suggested retry interval, not a completion estimate. Escape the optional plain-text message and limit it to 300 characters.

Configured maintenance-only startup runs before database/session/hosting initialization. Serve only the minimal status/help view and allowlisted local status assets, with all other web requests returning 503 and Retry-After. No login, installer, Admin, API mutation or repository transport bypass. API/native Mercurial clients receive a suitable non-HTML error; do not start the embedded SSH service in maintenance-only mode, and reject direct command transport entry points before opening storage. Outside maintenance mode preserve all normal authorization and transport guarantees. Existing operator graceful-stop procedures remain necessary before a restart.

Return real 404, 500 and 503 status codes, Cache-Control: no-store, correct HEAD behavior, sub-URL-safe links and escaped localized text. Do not automatically refresh or replay a failed POST. Check again is a manual GET to the configured application root. Do not link to unavailable search or include GitHub bug-report marketing. Keep private-denial 404s indistinguishable from missing pages. Never expose raw ErrorMsg, traces, SQL, filesystem paths, credentials or request query strings; log diagnostics server-side, correlated by an opaque request ID.

Render from preloaded standalone templates with a minimal plain-text fallback if rendering fails before headers are committed. A late streaming failure is logged and terminated; it cannot be converted into a clean new HTTP status after the response has begun. Verify all pages on desktop/mobile, keyboard/text zoom, two locales, missing images, unavailable dependencies, malformed URLs and simulated internal errors. Tasks 24–25 cover artwork, real routes/recovery, maintenance admission, packaging and final whole-branch acceptance.

## Account creation and first-run installation

Both are explicit redesign deliverables, including the real /user/sign_up and /install routes. They must not retain legacy form styling after the shared UI refactor.

### Create an account

Use the reduced public layout with the Hgs/beaker identity, shared footer, and a focused single-column form approximately 440px wide on desktop. Use the heading Create an account, visible labels above fields, consistent input/button sizing, field-level errors and an accessible error summary. Preserve username, email, password/confirmation, configured CAPTCHA and supported social-registration behavior. Provide a clear Sign in link for existing users.

Cover registration enabled and disabled states, invalid/duplicate details, CAPTCHA errors, submission in progress, success and email-activation guidance. Respect existing registration and provider policy. Preserve non-secret submitted values after validation failure; never echo passwords or CAPTCHA answers back into HTML or save them in draft recovery. Support password managers and appropriate autocomplete semantics. Admin-created users keep the shared Admin layout and the same form components.

### Install Hgs

Style the actual first-run installer as a public setup page, not an Admin mockup. Use the Hgs/beaker identity, a clear Install Hgs heading, a readable form approximately 800px wide, and labeled sections for database, repository/storage and service settings, mail/registration options where currently supported, and the initial administrator. Use one coherent form; a new multi-step wizard is not required.

Retain the prerequisite hosting plan's actual supported settings and Mercurial terminology. Database selection shows only applicable inputs: SQLite path, server database connection fields, and PostgreSQL SSL options as appropriate. Preserve configuration defaults, validation, and installation semantics. Inline help explains fields without leaking internal errors or credentials.

Show field errors and an error summary for invalid paths, database connection/permission failures, and invalid administrator details. Preserve safe non-secret values, never echo submitted secrets, and keep entered credentials out of draft recovery. Disable repeat submission while installation is in progress, show honest failure/success state and the existing next-step destination, and preserve the installed/locked guard so a styled page cannot rerun setup.

Verify both real forms at 390px and 1440px, with keyboard navigation, long translated labels, visible focus, and no page-level horizontal overflow. Installer submission tests must use disposable configuration and databases; visual inspection must never reconfigure a live instance.

## Remaining shared UI coverage

### Repository Watch and Star

Keep both existing actions in the repository header beside Clone and Fork. Watch/Unwatch controls existing subscriptions; Star/Unstar is a separate bookmark-like preference. Show their independent selected states and authorized counts with accessible text labels. Preserve existing notification behavior; this does not introduce PR email delivery. Use CSRF-protected POST actions, show failures without changing the displayed state, and prevent duplicate submissions. Anonymous users receive a sign-in path rather than a mutation control.

### Help, footer, and language

The utility-rail Help link opens a local /help page using the shared shell. Provide concise Hgs guidance for repository navigation, configured HTTPS/SSH clone methods, issues, and pull requests. Link to official Mercurial documentation where needed; do not send users to obsolete Gogs or Git help. Show only supported capabilities and public configuration information. Respect AppSubUrl and anonymous/sign-in policy.

Keep language selection in the shared footer, reachable on desktop and mobile. Changing language preserves safe route/query context, with a safe fallback when necessary. The footer carries Hgs product identity, instance/version information already exposed by policy, and existing legal attribution. Preserve operator-selected instance names and destinations; remove obsolete hard-coded Gogs marketing links.

### Existing email presentation

Include all existing activation, email-confirmation, registration-success, password-reset, collaborator and mention emails in the branding pass. Use Hgs/default instance identity, a readable compact HTML layout, and the beaker identity mark with equivalent text when images are blocked. Preserve existing subjects' purpose, recipient rules, token/link generation and expiration, mail triggers, and legal notices. Use absolute AppUrl-based asset/action URLs, escaped content and inline email-compatible styling. Keep instructions and action links usable without images. Add no new mail delivery events or PR notifications.

### Draft recovery after login expiry

Scope: issue and PR titles/descriptions, comment/review text, and valid non-secret metadata in those editors. Do not store passwords, authentication codes, API/SSH secrets, file contents or attachment binaries. File inputs must be reselected; any already-uploaded attachment must still pass its normal authorization check.

The user approved a cookie-held random draft ID with temporary server-side draft content, because cookie capacity is too small for normal issue/PR bodies ([MDN cookie limits](https://developer.mozilla.org/en-US/docs/Web/HTTP/Guides/Cookies)). The cookie contains no text or credential. Use an opaque browser draft-collection ID and a separate unpredictable per-editor capability kept only in the current document's memory. Bind records to the account, repository, canonical editor target, and page instance. Never use URL matching alone to restore work; never put the page capability in a URL, localStorage, sessionStorage or history.

While signed in, autosave after one second of idle typing and at least every five seconds during continuous typing. Show Saving, Saved temporarily, or Save failed accurately; an unsaved latest edit must remain in the open editor. Allow each body the existing 64 KiB limit and titles 255 characters. Records expire after 30 minutes without a successful save; show the recovery window on session expiry. Limit each account to 10 live editor drafts, with a clear recovery-unavailable message rather than silently evicting another open page.

On session expiry, keep the editor and its current text visible and block submission. Offer Sign in in a separate tab/window through the existing supported login flow, then a Check sign-in action or focus-triggered session check. Never navigate the editor to login. After the same account logs in, obtain fresh CSRF state, recheck current permissions and target validity, and recover only this page's draft. Newer unsaved text still present in the editor wins over older server snapshots. Restore inputs only; never automatically submit a comment, change metadata or merge. If a different account signs in or access is revoked, clear the previous account's editor/recovery state and show a non-sensitive explanation.

Leaving, reloading, or closing the editor discards recovery. A new visit, duplicate tab, or browser Back/Forward restoration cannot recover it. Same-document Write/Preview changes and local anchors are not leaving. Invalidate the page capability immediately and issue best-effort deletion on pagehide; on a bfcache return, clear stale editor fields before interaction and initialize a fresh page instance. Successful submission or explicit cancellation also clears recovery. Unload delivery is not guaranteed, so server TTL cleanup removes unreachable leftovers; those leftovers must not be recoverable without the original live capability. One tab's discard must not delete another tab's draft.

Use host-only, HttpOnly, SameSite=Lax cookies scoped to the draft API under AppSubUrl, Secure on HTTPS, with a 30-minute lifetime refreshed on successful saves. Authenticated save/load require CSRF and current authorization. A narrowly scoped disposal capability may delete its own record after login expires, but may never read or modify draft contents. Cookie IDs alone confer no read access. Apply request size limits and cleanup on account/repository deletion; never log draft text or capabilities.

## Implementation and verification

Implement shared Go template partials, semantic CSS variables, and small scoped JavaScript modules. Ship local assets; no frontend runtime service or new SPA framework is required. Migrate supported pages to the common components before retiring their unused Bootstrap/custom-LESS assets. Preserve needed specialized behavior rather than loading both legacy global scripts on every page.

Verify representative pages together: workspace, Explore, source, **global and repository Issues lists, issue creation/detail, labels and milestones**, PR list/detail, account/settings forms, Admin overview/users/configuration, and sign-in/errors. Inspect desktop and narrow layouts, real avatars and initials, long names, translated text, empty/loading/error states, keyboard operation, and permission-dependent actions.

The visual prototype is not evidence that backend workflows work. Production acceptance additionally requires the regression, authorization, native Mercurial, concurrency, and recovery checks in the project design. Design approval and the existing transport prerequisite still govern implementation.

## Follow-on: landing page and Mercurial identity

The requested product name is **Hgs**, described as a **Mercurial service**. Use the exact landing-page tagline: **A painless, self-hosted Mercurial service.** Phase out Gogs from user-facing product copy in a later implementation while preserving existing copyright, license and attribution notices. Internal package/import paths and repository identifiers are not renamed by this visual prototype.

[Open the throwaway landing-page prototype](prototypes/landing-page/index.html). Its adjacent gopher-lab.png is the approved mascot artwork. The user approved this composition with the public Explore correction below. The prototype does not change the production home template, public logo, authentication, routes, or backend behavior. Sample repositories and controls are clearly labeled; prototype appearance is not evidence that backend workflows work.

### Landing-page composition

Use a compact Hgs identity with the descriptive “Mercurial service,” Explore repositories, and Sign in. Lead with the exact tagline and a short factual explanation of the service. Pair the text with the Go Gopher wearing a laboratory coat and holding a beaker. The earlier beaver direction is superseded.

Keep the public layout distinct from the application's side navigation while using the same blue (#0747A6, #0052CC), ink (#172B4D), system typography, borders and spacing. Use a white, flat surface, restrained feature columns, and a native hg clone example with a working Copy action. Avoid oversized decorative cards, gradients, unsupported performance/security/platform claims, or Git-era feature promises. Review at 1200–1440px desktop widths and a 390px mobile width, with legible text, no page-level horizontal overflow, visible keyboard focus, and reachable actions.

The prototype has sample repository rows, section links, and a dismissible accessible preview dialog. It does not collect passwords, submit forms, authenticate, or trigger external application changes. A separate Design preview notice identifies the artifact as a throwaway concept. The clone command uses an explicitly labeled placeholder address.

### Public repository discovery

The landing-page Explore repositories links and any repository preview show **public repositories only**, with public-only counts and pagination enforced on the server. Omit all landing Explore links, repository previews, counts, and related empty states when anonymous browsing is blocked in app.ini. In the current configuration this is [service] REQUIRE_SIGNIN_VIEW = true, exposed as setting.Service.RequireSignInView; reuse this setting rather than inventing another switch. Direct anonymous Explore/search requests must still obey the sign-in requirement and return no repository data. Hiding navigation alone is insufficient.

This rule applies to the public landing page and its public catalog destination. Signed-in workspace Explore continues to list all repositories the viewer can access, including permitted private repositories. Landing links select the public catalog filter; anonymous requests cannot widen it with query parameters. If repository previews are retained in production, load real public data and omit them entirely when public browsing is disabled.

### Authentication and policy in later implementation

Preserve supported sign-in methods, including social sign-in, and existing conditional registration behavior. Do not imply public registration is always available. Production controls must reflect the instance's policy and permitted authentication providers; a redesign must not remove supported paths or weaken CSRF/authorization checks. The prototype explains these policies in a sign-in preview dialog instead of presenting a credential form.

### Mascot artwork and attribution

The requested composition is the recognizable Go Gopher in a white laboratory coat holding a beaker. Keep the silhouette friendly and readable, with adequate separation between coat, beaker and body. Ship the reviewed artwork locally with a transparent background; inspect the actual PNG for stray backgrounds, rough edges, silhouette clarity, and contrast on white and the shared blue palette. Check both the large landing illustration and reduced-size use during implementation verification. Provide descriptive alternative text for the meaningful illustration.

Credit the source and adaptation: **Go Gopher by Renee French, CC BY 4.0. Lab-coat and beaker adaptation.** Link [the original Go Gopher attribution](https://go.dev/blog/gopher) and [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). Preserve existing Gogs legal notices separately from the Hgs product identity.

Implementation plan Task 16 now includes the approved landing page and replacement of the main landing PNG with public/img/hgs-mascot.png, using the approved local artwork and attribution. Phase out Gogs product labels in favor of Hgs or Mercurial service while retaining legal notices and internal identifiers. Use the beaker held by the approved Gopher as the favicon and navigation identity mark; this small-asset adaptation is now in scope. Preserve its pouring lip, graduation marks and blue liquid, simplifying only details that cannot survive small sizes. Use one master mark for both placements, with a light/inverse rendering for white and blue surfaces. Verify favicon sizes 16px and 32px plus navigation sizes 24px and 32px; retain the full mascot for the landing hero and credit the adaptation. The prototype is a visual reference, not a replacement for the real template and authorization checks.
