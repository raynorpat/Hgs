# Hgs landing and identity browser evidence

Actual Cua review on Windows, 2026-10-28, against source-frozen, disposable production-router fixtures. All repositories, accounts, addresses, preview tokens and mail content are synthetic. No SMTP worker or external mail delivery was used. This is scoped visual evidence, not release acceptance or a mail-client compatibility matrix.

## Fixture and capture provenance

| Fixture | Configuration and observation |
| --- | --- |
| 127.0.0.1:64972/code/ | Public discovery, registration and four configured provider links; one public project and a private repository named private-landing-secret. Only alice/project appears in the preview. The clone command uses this actual fixture's native Mercurial URL. |
| 127.0.0.1:52943/code/ | REQUIRE_SIGNIN_VIEW enabled: public identity landing renders, with zero Explore links, repository links, preview or clone command. The separate production-route tests establish canonical anonymous Explore/search404 behavior. |
| 127.0.0.1:49431/code/ | Both repositories private: public-empty message and Explore links, with no clone command or private names. |
| 127.0.0.1:53465/code/ | Registration disabled and no providers: zero signup/provider links; public discovery remains available. |
| 127.0.0.1:53492/code/ | Configured name The operator's laboratory: visible mobile identity, escaped apostrophe, retained title and email identity. |
| 127.0.0.1:58078/code/install | Actual unlocked installer GET, Hgs/beaker identity and existing fields. No installation was submitted. |

The landing gate exception exposes only the approved public identity under blocked discovery; it does not relax repository/account permissions. No repository query or preview is allowed there. Signed-in Home/dashboard behavior and direct policy boundaries have separate actual handler tests. Browser visits here were anonymous direct navigation, not proof of signed-in interactions.

| Capture | View |
| --- | --- |
| landing-public-desktop.jpg | 1440×1000 public hero, approved Gopher and feature content. |
| landing-public-mobile.jpg | 390×844 public header/actions/provider links and responsive hero. |
| landing-restricted-desktop.jpg | 1440×1000 restricted discovery variant. |
| landing-restricted-mobile.jpg | 390×844 restricted variant, no Explore action. |
| landing-custom-mobile.jpg | Long configured instance name remains visible and wraps. |
| identity-assets-desktop.jpg | Native16/32px favicon PNGs and master/inverse SVG at16/24/32px on white/blue, plus transparent mascot at150px. |
| mail-collaborator-mobile.jpg | Existing collaboration email with beaker, identity, escaped sample text and readable action. |
| mail-collaborator-text-only-mobile.jpg | Same preview with image markup omitted; identity/action/legal links remain readable. |
| mail-custom-mobile.jpg | Configured instance name and long action label wrap in the existing mail family. |
| signup-identity-mobile.jpg | Actual signup GET, final beaker identity and complete local CAPTCHA image. No CAPTCHA was solved or submitted. |
| installer-identity-mobile.jpg | Narrow actual installer form; lower scroll position than its desktop capture. |
| installer-identity-desktop.jpg | Actual installer header/beaker and form at1440×1000. |

## Geometry and asset observations

Public/restricted landing at1440×1000 measured client/scroll widths1425/1425, including the vertical scrollbar. At390×844 both measured375/375. Empty, registration-disabled and custom-name variants also measured375/375. Signup and installer measured375/375 at390px; installer desktop measured1425/1425. Collaboration/custom mail previews measured390/390. No observed document overflow was hidden to obtain these readings.

All26 images in the identity sheet completed. Native favicon PNG widths were16 and32; SVG display widths were16/24/32. The matching pouring lip, graduated glass and blue liquid remain recognizable; inverse outlines are legible on blue. The approved mascot at150px has no opaque rectangle against blue. Enlarged16px PNG samples are intentionally less sharp than native-size PNG/SVG samples.

Independent local SHA-256 comparison of the approved prototype and public mascot matched exactly: D82630AF7EF9F05089FA6DC1D6F28B76B14B4478E6F6E1E4CD851C1B6945B810. No mascot regeneration or bitmap editing occurred. The local license records original Renee French/Go Gopher CC BY4.0 credit and the AI-generated lab-coat/beaker adaptation without inventing a separate human author/licensor. The shared master and palette derivative are code-native vector assets; PNG/ICO export receipts are in the implementation evidence.

## Mail and interaction limits

Mail previews use actual escaped templates and local absolute asset/action URLs. The images=blocked variant removes image markup to inspect text-only readability; it is a simulation, not actual Outlook/Gmail image blocking. Activation preview lifetime values are synthetic fixture defaults (including0 hours); configured lifetime/token preservation is established by the separate fake-sink tests, not these screenshots. All seven existing delivery helpers, including direct issue-watcher HTML outside the six templates, have separate fake-sink/recipient/trigger/escaping receipts. No action/token/provider links were activated and no email was sent externally.

The old Task15 owned Settings tab remains stuck after a native-confirm interaction. User assistance was requested; no successful cancel/close is claimed. Consequently these captures do not establish full keyboard traversal, clipboard Copy activation, OAuth/login/signup/install submissions, signed-in final rail geometry or true browser zoom. Earlier task-specific evidence retains its own source provenance; viewport resizing is not zoom. Current HTTP/script tests must not be described as completing those browser interactions.

Capture tab7 was closed and its absence confirmed; temporary viewport override reset and browser visibility remained false. Only the previously stuck tab4 remains unconfirmed. Fixture shutdown receipts will be recorded by the implementer after root's safe-to-stop handoff. Task15 full tagged/race/platform/database/fault gates remain failed/open.

## Owned fixture shutdown

After root's explicit safe-to-stop handoff, the implementer requested GET /fixture/stop on exactly64972,52943,49431,53465,53492,58078. All returned200 and all six test/server processes exited naturally with PASS (public979.643s, restricted967.861s, empty919.822s, closed907.779s, custom895.253s, installer882.077s, including manual QA time). No process kill was used. Original task16-browser-<variant>.log files remain in the worktree root. Only the old stuck Task15 browser tab remains unconfirmed, as above.

After capture cleanup, browser visibility was deliberately set true solely to bring the previously stuck Task15 tab into view for the pending user-cancellation request. No further interaction was attempted, and its cancellation/cleanup remains unconfirmed. Capture-time visibility and geometry above are unchanged.

After the final separately named PR UI regression completed, an owned-path process audit found0active processes. Exact Task16 temp target/containment and zero-reparse checks preceded removal of its remaining12owned scratch entries; final temp existence=false. No process kill or unrelated temp removal occurred.

## Direct watcher URL amendment provenance

These twelve captures precede Task16 review fix round1, which changes only direct watcher-email content URL resolution and its tests. They do not establish the amended mention/issue/changeset hrefs. No shared mail template, image, CSS, landing layout or browser interaction changed; the exact generated root/sub-URL hrefs and unchanged fake-mail delivery/escaping have separate focused test receipts. No new screenshot or actual email-client compatibility evidence is claimed.
