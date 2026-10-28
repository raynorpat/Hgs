# Shared UI cutover browser evidence

Actual Cua walkthrough on Windows, 2026-10-28, against the disposable production-router fixture at `http://127.0.0.1:62766/code/`. The fixture uses SQLite and Mercurial 7.2.4. All identities, repositories, comments and addresses are synthetic. Notification mail is disabled, with no SMTP initialization. These captures establish the observations below, not full release acceptance.

## Captures and source provenance

The first fixture included the Task15 runtime cutover and carried fixes, before the later en/de private-visibility guidance copy amendment, Admin heading corrections and mobile Admin table correction. Those later corrections require separate evidence. The fixture also initially seeded a malformed issue label value and an individual account named laboratory instead of an organization; these are fixture errors, not successful organization or label-workflow proof.

| Capture | Actual observation |
| --- | --- |
| issue-desktop.jpg | Open issue at 1440×1000; shared repository navigation, PR above Issues, 28px account avatar centered in its 40px control. Initial label seed was malformed. |
| issue-closed-desktop.jpg | Actual writer assignment and Close-with-comment submission; one persisted comment, neutral Closed badge and 100% milestone. The label seed had not yet been repaired. |
| issue-mobile.jpg | 390×844, after canonical owned-fixture label reset, actual rendered Add label and Reopen submission. Needs review label, writer assignment and comment persist, milestone returns to 0%. Long issue title and long comment URL wrap. |
| issue-metadata-mobile.jpg | Same reopened issue, metadata controls reachable with keyboard focus on Save. Label/Remove label, writer and milestone fit the content column. |
| admin-desktop.jpg | Actual Admin Overview at 1440×1000; maintenance operations were not executed. |
| admin-users-desktop.jpg | Actual Admin Users at 1440×1000, one shared shell and no duplicate desktop top navigation. |
| admin-repositories-mobile.jpg | **Pre-fix failure**, not a passing capture: at 390×844 the document is 525px wide with a 375px client and no local table scroller. Admin Users and Monitoring have the same failure. |

## Verified interactions and geometry

Actual alice login, issue writer assignment, close-with-one-comment, rendered label attachment after fixture repair, and reopening were observed in the browser. The corrected label form also has a separate actual rendered-form HTTP regression receipt; the runtime issue-label implementation was unchanged.

At 1440×1000, initial issue client/scroll widths were 1440/1440; after the page acquired a vertical scrollbar they were 1425/1425. Account image bounds were 28×28 at (14,958), with its 40×40 control at (8,952): both center differences were zero. Reopened issue and metadata views at 390×844 measured client/scroll widths 375/375. Metadata bounds fit from x72 to x359 and the Save focus outline was visible. Closed badge computed background was rgb(235,236,240), text rgb(66,82,110).

Direct navigation inspected Admin Overview, Users, Organizations, Repositories, Authentication, Configuration, Notices and Monitoring at desktop width: each had one main region, one Global tools navigation and no document overflow. Organizations was empty because the original laboratory seed was an individual account. Admin Configuration and repository Settings measured 375/375 at the narrow width; this does not waive the dense-table failure above.

## Limits and interrupted checks

Testing cancellation of a repository rename stalled one owned Settings tab during a native-confirm interaction. The click timed out, the documented JavaScript-dialog API returned no dialog, and supported close/focus attempts timed out. No confirmation was accepted, no successful cancel is claimed, and a fresh Settings GET confirmed the actual name remains project. A separate recovered tab supported direct navigation, DOM reads and screenshots, while click/key activation did not establish a completed action. User help was requested to cancel/close that isolated test tab. No private browser API, native Codex UI automation or unrelated Chrome tab was used as a workaround.

Consequently this walkthrough does not claim completed all-role, all-popout, full keyboard, signup/installer interaction, two-locale visual or true 200% browser-zoom acceptance. Earlier task-specific HTTP/browser receipts retain their own narrower source provenance. Viewport resizing is not browser zoom. Additional current-source observations will be appended separately; unexecuted checks remain open in shared-ui-evidence.md.

## Second fixture: amended source and blocked authenticated interaction

The second owned fixture was `http://127.0.0.1:60969/code/`, with the visibility guidance, Admin headings/table CSS and locale adapter amendments. The first fixture was stopped by the implementer. Final reduced auth views were directly observed; a new login attempt by click and keyboard did not establish success because the old stuck tab remained. There was no fixture auto-login/authentication shortcut.

- login-final-mobile.jpg: current painted login, 390×844, client/scroll390/390; fields cleared before capture.
- login-final-desktop.jpg: current login, 1440×1000, client/scroll1440/1440.
- signup-before-captcha-fix-mobile.jpg and signup-before-captcha-fix-de-mobile.jpg: current signup at390×844, client/scroll375/375, English/German form labels and buttons. These show a **broken CAPTCHA image**, not complete signup acceptance. Actual generated image GET was subsequently found to return404 under the sub-URL; its focused fix and replacement capture must be recorded separately. No CAPTCHA was solved or signup submitted.

Actual browser GET `.../code/user/sign_up?lang=de-DE` landed at `.../code/user/sign_up`, retained the German form and did not duplicate `/code`. Returning via login?lang=en-US restored the English form. This closes that narrow visual redirect observation; the broader root/subURL/encoded-path/cookie/restoration proof is in focused HTTP tests.

A documented Cua connection reset restored fast reads and screenshots but did not resolve the stuck Settings tab's focus timeout. The completed guest capture tab5 was closed and absence confirmed in tab inventory. Viewport override reset; browser visibility false. Only the stuck old tab4 remains, with cleanup unconfirmed and user assistance pending. This does not establish passing final authenticated Admin geometry, all-role keyboard/popout behavior or rename cancellation. First fixture Admin screenshots and final rendered-route/CSS tests have their separate provenance.

## Final CAPTCHA amendment

Third fixture `http://127.0.0.1:58204/code/` includes the separate pinned-Captchaer RequestURI compatibility amendment. Actual rendered signup at390×844 measured375/375 in English and German. The image element was complete, with natural dimensions240×80 and a local `/code/captcha/<random-id>.png` URL. No CAPTCHA text was transcribed, solved, submitted or bypassed.

Final passing image/layout captures are signup-final-mobile.jpg, signup-final-de-mobile.jpg and signup-final-de-desktop.jpg. Desktop1440×1000 measured1440/1440. The earlier `signup-before-captcha-fix-*` files retain the real failure evidence. German form labels/buttons are translated; new headings use the configured English fallback where no translation exists.

Actual language-query navigation again retained `/code/user/sign_up` without double prefix. This is an image-render/layout proof, not account-creation or all-keyboard acceptance. Final capture tab6 was closed and absence confirmed; viewport reset. Only previously stuck tab4 remains unconfirmed, with no additional close/input attempts. Root has completed the available browser work for this task; the missing authenticated post-fix Admin geometry and other interrupted checks remain explicitly open.
