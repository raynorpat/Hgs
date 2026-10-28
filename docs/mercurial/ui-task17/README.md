# Local Help and repository-action browser evidence

Actual Cua guest review on Windows, 2026-10-28, of committed Task 17 source `3fbd16a`. The existing production-router fixture was reused; no new harness or authentication shortcut was added. All data is synthetic. No preference mutation, login, native command, email or install submission was performed in this browser pass.

## Setup failure and recovery

The original fixture at 127.0.0.1:65259 was alive but isolated by the execution sandbox. Cua could not connect. Process/listener inspection confirmed its owned cmd.test process and loopback listener; an outside health request timed out and a sandbox request reported socket permission denial, with no request reaching Macaron. Its inaccessible stop URL required stopping only the positively identified owned test PID. The original fixture ended with forced-stop FAIL 318.299s; this is a setup receipt, not a successful normal shutdown or a Help-code failure.

The same fixture was restarted with scoped execution permission at `http://127.0.0.1:65194/code/`. Outside health requests and actual Cua visits reached Help and the repository page. No production code, firewall, global network configuration or authentication policy changed to fix the reachability issue.

The failed connection created tab8 with a browser-generated internal data error page. Browser URL policy prevented rebinding and a documented close attempt; no alternative content-access method was used. Its cleanup remains unconfirmed. The working capture tab9 was closed; old Task15 tab4 also remains unconfirmed. Temporary viewport override reset; browser visibility was restored for the pending manual-cancellation handoff. There is no confirmed rename cancellation or complete browser-input recovery.

## Captures and actual observations

| Capture | Source and observation |
| --- | --- |
| help-desktop.jpg | Guest Help at1440×1000, selected German heading with existing English body fallback. Client/scroll widths1425/1425. |
| help-mobile.jpg | Guest Help at390×844, client/scroll375/375. Beaker identity and readable wrapping; language persisted on a subsequent visit without a lang query. |
| repository-guest-desktop.jpg | Empty public repository at1440×1000, client/scroll1440/1440. One shared Global tools region, context navigation, PR above Issues, and Help in rail/footer. |
| repository-guest-mobile.jpg | Same repository at390×844, client/scroll375/375. Guest sign-in affordance retains Watch/Star intent; long quickstart code scrolls locally instead of widening the document. |

Actual Help navigation to `/code/help?page=2&q=mercurial&token=synthetic-context-only&lang=de-DE` retained that URL and rendered HTML language de-DE/heading Hilfe. Footer language links preserved page2/q=mercurial and omitted the synthetic token. A later `/code/help?page=2&q=mercurial` still selected de-DE, demonstrating cookie continuation without reading cookie contents. Footer links were inspected in the DOM; no keyboard activation is claimed.

The repository's local code scroller measured285px client/357px content while document widths remained375/375. The actual rail beaker image completed at32×32 within a40×40 control; horizontal and vertical center differences were both zero. This verifies the navigation mark, not a signed-in account avatar. Desktop had one Global tools navigation and two intended Help entry points (rail/footer).

The empty repository quickstart shows example.test because the reused fixture's hosting configuration was initialized with that synthetic PublicURL before the temporary listener was bound. It is a configured fixture value, not a runtime hardcoded host or a native clone/push receipt. No command from that block was executed.

## Limits

Actual owner/private-reader POST, sequential duplicate, CSRF/GET denial, failure flash/retry, and add/remove rollback/count behavior are covered by the separate production HTTP/model/integration receipts. They are not browser-click proof. This pass did not establish signed-in owner/reader keyboard usability, menu activation, preference clicks, clipboard or true browser zoom because the earlier input interruption remains unresolved. HTTP/DOM assertions and viewport resizing do not substitute for those interactions.

Current fixture Help correctly omits SSH instructions when no effective SSH address is configured; the enabled/disabled configuration matrix is separately tested. Source/license attribution and configured identity remain. Existing Task 15 full tagged/race/platform/database/fault requirements remain failed/open.

## Normal replacement shutdown and retained receipts

After the controller completed this pass, the existing replacement fixture stop URL returned HTTP 200 with its unchanged `stopping owned Task16 fixture` body. Session 62912 exited naturally: TestLandingBrowserFixture PASS 855.15s, cmd PASS 857.378s. An outside-sandbox OS check then found no listener on port 65194 and no remaining owned test PID 54736. The original forced-stop failure remains separately recorded above.

Original `.log` files remain under the owned outside-checkout `C:/Users/raynorpat/.codex/visualizations/2026/10/26/01a177c6-a836-7dd1-a55e-6a2aca024544/task17-temp` directory for independent review. No temp cleanup or receipt deletion was performed.
