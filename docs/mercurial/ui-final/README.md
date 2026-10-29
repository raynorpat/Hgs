# Final branch browser checks

This directory preserves checks at `1575677`, the initial fix commit `ba68c29`, and the recovery amendment `6b79e73`. The first six captures close the earlier interrupted post-fix Admin geometry check recorded in Task15. Later captures check affected UI, with source identities in the manifest. They do not replace the broader native/platform/database/race release requirements.

| Page | Actual narrow geometry | Capture |
| --- | --- | --- |
| Users |375px document/client;261px local table scroller/550px content |[Users](admin-users-mobile.jpg)|
| Repositories |375px document/client;261px scroller/552px content |[Repositories](admin-repositories-mobile.jpg)|
| Monitoring |375px document/client;261px scrollers/488px and440px content |[Monitoring](admin-monitor-mobile.jpg)|
| Organizations |375px document/client; correct Organizations heading and real laboratory row |[Organizations](admin-organizations-mobile.jpg)|
| Configuration, German |375px document/client and correct Konfiguration heading |[Narrow Configuration](admin-configuration-mobile-de.jpg)|

[Desktop German Configuration](admin-configuration-desktop-de.jpg) at1440×1000 has1425px document/client width, one main region and one context navigation. These new captures supersede only the corresponding interrupted visual checks; the original pre-fix overflow image remains historical failure evidence.

The narrow Menu opens from Enter and focuses Close. Escape returns focus to Menu with aria-expanded=false. Keyboard activation reaches the Admin table pages. The Users scroller has tabindex0; ArrowRight moves it40px, with a3px blue focus outline. Wide tables scroll locally rather than widening the page. No maintenance operations, account changes, repository rename or confirmation actions were performed.

Two visual observations were passed to the whole-branch reviewer: the narrow repository Name column wrapped into very short segments, and German Configuration showed English context labels. The review classified the first as M5 and accepted the configured English navigation fallback. The original repository image remains the before-fix evidence; the final check below verifies M5's correction.

Every image was saved from one screenshot buffer and the actual saved file was directly viewed. The fixture stopped normally with HTTP200, test249.40s/package251.623s/exit0 and no listener on64496. The viewport was reset, only owned tab38 closed, and the user's tabs and hidden-browser preference were preserved.

## Frozen fix `ba68c29`

The production-router fixture at54967 used normal alice/password sign-in. All accepted new captures were saved once and the actual saved file was directly viewed.

- [Desktop audit history](pr-history-desktop-en.jpg) and [narrow audit history](pr-history-mobile-en.jpg) show actor/time information for assignment, review, dismissal, decline, reopen and metadata edits. The seeded dismissal reason displays literal `<script>` text. Desktop document/client width was1425px at a1440px viewport; narrow width was375/375 at390px. The page had no inline scripts; its three script elements reference the existing local application files. History detail values currently display canonical strings such as `changes_requested`, `declined` and `open`; this was passed to scoped review as an observation.
- [PR selectors](pr-selectors-desktop-en.jpg) show the final form. Actual DOM options allow Named branch/Bookmark/Tag/Changeset sources and only Named branch/Bookmark destinations. Both1440px and390px layouts remain contained. No new PR was submitted for this form check.
- [Literal code example](pr-code-example-mobile-en.jpg) was submitted through the real comment editor once. After the browser action timed out, fresh state proved comment2 existed and its audit event was recorded; no retry was made. Its fenced `<script>example</script>` appears as code, with no inline script element and no narrow page overflow.
- [Readable Admin repository names](admin-repositories-mobile-fixed.jpg) retains the local table scroller while the long name occupies two lines instead of roughly thirteen. The name cell has about230px usable width; table content is772px within a261px local scroller. The375px document stays contained. Keyboard scrolling moved the scroller80px with a3px blue focus outline.
- [Final profile](profile-mobile-final-en.jpg) retains loaded28px rail/88px profile avatars, the owner Settings link, neutral Activity tab/heading and375px document containment. This is an affected-style check, not a repeat of the Task23 privacy/calendar matrix.

The separate account-handler fixture at59997 seeds an owned pending signup and captures outgoing test mail without a real provider. [Activation resend](activation-resend-mobile-de.jpg) shows the normal credential/CSRF form after a successful request: both credential fields are blank and no account/session control is granted. At390px the document/client width is390px. Existing German labels use the configured English fallback for new guidance; this is not a complete German-translation claim.

The actual captured loopback email link was followed; the fixture log records the user activation. Normal sign-in then accepted those credentials. Both actions redirect to `/code/`, which this minimal auth fixture intentionally does not compose, so its final root response is404. No workspace-rendering claim is made for that fixture; normal workspace sign-in was separately verified in the production-router fixture. The browser did not create the initial signup, test a real mail provider or certify every disabled-account/race case; those have their separately scoped Go receipts.

Activation stopped with HTTP200 and joined test271.25s/package278.389s/exit0; exact owned process39260 and listener59997 were absent. The main fixture also received HTTP200 stop and joined test771.30s/package773.726s/exit0; owned process30368 and listener54967 were absent. Owned tabs39/40 were closed, viewport reset, browser visibility remained false, and user tabs were preserved. Native rename-confirm cancellation remains unexecuted; no unsupported modal interaction was repeated.

## Recovery amendment `6b79e73`

A later integration regression established that normal password reset cleared the pending-signup flag and stranded activation. The minimal runtime amendment preserves pending eligibility across password/Rands rotation while retaining email/login guards and explicit administrator clearing. Its real reset/new-credential resend/activation and old-proof-denial checks are Go HTTP/model evidence; the browser did not change a password.

The new generic Sign In recovery link was checked in the owned account-handler fixture at51706. [English desktop](activation-recovery-entry-desktop-en.jpg) at1440px has1440px document/client width and the real `/code/user/activate` link. The actual footer language selector and keyboard Deutsch selection produced the [German narrow page](activation-recovery-entry-mobile-de.jpg), with390px document/client width. Tabbing from Forgot password focused the localized recovery link with a3px blue outline. Enter followed that observed link into the normal username/password resend form; this thin fixture returned its configured English fallback on that next request, so no cross-page language-persistence claim is made for it.

Both new screenshots were saved once and their actual files directly viewed. The fixture received HTTP200 stop and joined test372.12s/package397.123s/exit0; owned process50532 and listener51706 were absent. Owned tab41 closed, viewport reset and hidden browser/user tabs were preserved. The initial `ba68c29` full command failures and subsequent test amendments are recorded separately; screenshots do not certify a full-suite result.

## Completed command receipts

[The final handoff](../ui-implementation-handoff.md) records the completed ordinary/tagged/build/vet/race outcomes and the separate test-only Cron amendment. Selected structured receipts and skip/failure indexes are preserved under [validation](validation/ordinary.json); full original logs remain in the controller evidence. The [capture manifest](capture-manifest.json) binds all 15 images to their source and SHA-256. No broad command is claimed to have run at the later Cron-only commit.
