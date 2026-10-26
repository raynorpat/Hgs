# Hgs design system

Status: Shared UI, Hgs landing-page composition, and lab-coat/beaker Go Gopher artwork approved on 2026-10-26. The public-only, configuration-controlled landing Explore rule below is part of that approval. Production implementation remains subject to the existing prerequisite gate.

Updated: 2026-10-26

## Purpose and references

Use one visual and interaction system across workspace, repositories, **Issues**, pull requests, organizations, accounts, and administration. Retain the Go server-rendered application and Mercurial terminology.

The visual direction comes from the user's Bitbucket screenshots: blue navigation on the left, a slim utility rail, white content, restrained borders, and compact readable lists. Use Hgs as the requested product identity and retain existing legal attribution. The landing-page and Mercurial-focused mascot direction is recorded in the follow-on phase below. Do not introduce Bitbucket logos or proprietary assets.

- [Project design and behavior](superpowers/specs/2026-10-26-shared-ui-and-pull-requests-design.md)
- [Saved interactive layout study](superpowers/specs/references/2026-10-26-bitbucket-layout-study.html)

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

The workspace repository list emphasizes owned/collaborative repositories. Explore is the server-wide catalog of all repositories the viewer may read. Permission-dependent links follow the shared authorization service. Hidden controls are not an authorization mechanism.

## Color tokens

Use semantic CSS custom properties. Do not scatter literal colors through page templates.

| Token | Value | Use |
| --- | --- | --- |
| `--color-nav` | `#0747A6` | Expanded blue navigation |
| `--color-rail` | `#064299` | Narrow utility rail |
| `--color-nav-active` | `#003884` | Active/hover item on blue navigation |
| `--color-nav-text` | `#DEEBFF` | Navigation text and icons |
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

Require readable contrast for each actual foreground/background pairing, including hover and selected states. Use at least 4.5:1 for ordinary text and 3:1 for essential control boundaries/focus indicators. Subtle decorative dividers need not substitute for a visible control boundary. Do not communicate status through color alone.

Repository-specific issue-label colors remain supported. Choose a readable foreground for each label and preserve its text; do not map every user label into a fixed status palette.

## Typography

| Role | Size / line height | Weight |
| --- | --- | --- |
| Page title | 28px / 36px; 24px on narrow screens | 500 |
| Section heading | 20px / 28px | 500–600 |
| Subheading | 16px / 24px | 600 |
| Body and navigation | 14px / 21px | 400; active/emphasized 600 |
| Dense table text | 13px / 20px | 400 |
| Metadata and helper text | 12px / 18px | 400 |
| Compact status label | 11px / 16px | 600 |
| Source, hashes, clone commands | 13px / 20px monospace | 400 |

Use `-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif` for interface text and `ui-monospace, SFMono-Regular, Consolas, monospace` for code. Prefer normal sentence case. Reserve uppercase for short status labels, not ordinary navigation or instructions. Do not require downloaded fonts.

Long repository/issue titles wrap in primary content. Truncate only constrained navigation or metadata, with the full value available to keyboard and assistive-technology users. Do not shrink essential text to fit.

## Spacing, shape, and elevation

| Token family | Values |
| --- | --- |
| Spacing | `--space-1: 4px`, `--space-2: 8px`, `--space-3: 12px`, `--space-4: 16px`, `--space-5: 24px`, `--space-6: 32px`, `--space-7: 40px`, `--space-8: 48px` |
| Radius | `--radius-control: 3px`, `--radius-panel: 4px`, `--radius-popout: 6px`, `--radius-round: 50%` |
| Borders | 1px ordinary divider; 2px selected-tab indicator |
| Popout shadow | `0 8px 28px rgb(9 30 66 / 25%)` |
| Focus | 2–3px outline with 2px offset; inverse focus color on the rail |
| Layers | Content 0, sticky navigation 10, popouts 20, drawer backdrop 30, drawer/dialog 40, notices 50 |

Use flat surfaces for ordinary lists and forms. Reserve elevation for floating surfaces. Keep table cells around 12px vertical and 12–16px horizontal padding; allow rows to grow for multiline information.

## Icons and avatars

Use one local outline icon family with consistent 1.5–2px stroke weight. Default icons are 20px; inline table icons may be 16px. Use a compass for Explore, history/clock for Recents, magnifier for Search, plus for Create, and a branching-request icon for Pull requests.

Icon-only controls need accessible names and hover/focus tooltips. Decorative icons use `aria-hidden="true"`. The icon is not the hit target: utility buttons have a common 40px square target, expanded to at least 44px for coarse pointers where practical.

The account image or fallback initials must share the same centered geometry. Use a circular 28px image inside the square button, with grid/flex centering, zero internal offset, and `object-fit: cover`. Hover/focus background belongs to the button, not the image. A fallback initial must not shift when replaced by an image. Test both axes, image loading/failure, browser zoom, and focus/hover states.

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
| Diff viewer | Monospace lines, explicit old/new sides, binary/truncation notices, safe immutable anchors |

Prefer real navigation links for page transitions and buttons for actions. Menus/popouts do not require JavaScript to emulate ordinary links. Do not use tables for non-tabular page layout. Do not copy legacy global selectors into the shared stylesheet.

### Utility interactions

- **Search:** opens and focuses a repository/owner search field; show authorized matches, a no-results state, and a path to the full Explore results. Prevent stale responses replacing a newer search.
- **Explore:** navigates to the full accessible-repositories page; it is not a popout or a public-only catalog.
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
