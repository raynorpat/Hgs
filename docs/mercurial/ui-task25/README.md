# Maintenance and resilient status-page evidence

Implementation `acd7caa` adds standalone maintenance503, safe500 and404 pages; `050d8ca` rejects incomplete image payloads during preload. Independent task review and scoped fix review accepted the implementation with no remaining blocking findings. These captures come from actual production handlers and an actual application binary in disposable loopback fixtures, rather than the approved static prototypes. Whole-branch review remains pending at this evidence checkpoint.

| State | Desktop English | Narrow German |
| --- | --- | --- |
| Maintenance503 | [1440px](503-desktop-en.jpg) | [390px](503-mobile-de.jpg) |
| Missing page404 | [1440px](404-desktop-en.jpg) | [390px](404-mobile-de.jpg) |
| Closed-database500 | [1440px](500-desktop-en.jpg) | [390px](500-mobile-de.jpg) |

All three local illustrations and the beaker loaded. The document stayed within its client width at1440 and390 viewport widths (the narrow scrollbar leaves375px). Unknown-route/query markers and database diagnostics did not appear. The configured maintenance message rendered as literal escaped text. The pages have no authenticated sidebar, ordinary database footer, automatic retry or POST resubmission. Normal disabled-mode Home and sign-in still reached the healthy landing page and workspace.

The500 fixture closed its owned SQLite connection before serving the normal Explore route. The real route returned safe500, while standalone Help remained200. A separate fixture removed only an owned copied error image before renderer preload: the same route returned a [safe plain-text500](500-missing-image-fallback.jpg). No repository image or live application configuration was changed.

Independent source review found that metadata-only PNG validation could accept a valid header with missing pixels. The fix fully decodes images after dimension/allocation bounds. Focused regressions first reproduced header-only, missing-end and oversized valid-header files incorrectly producing HTML across404/500/503, then passed with original status, no-store and plain-text fallback. An intact-image baseline prevents unrelated fixture failure from masking the defect. Full owning renderer and maintenance suites pass. This correction changes no approved image bytes or page composition.

The first composition captures precede the final shared-footer amendment. Its affected browser recheck verifies keyboard language selection, German retention through manual Check again, German Help and Home navigation, fixed language-only links and current-language semantics. [Desktop footer/focus](503-locale-focus-desktop-de.jpg) and [mobile footer/focus](503-locale-focus-mobile-de.jpg) show the actual saved final composition; the mobile view intentionally scrolls to the footer. Deutsch keyboard focus has a3px solid blue outline with2px offset. Every accepted image was saved from one screenshot buffer and the actual saved file was directly viewed.

HTTP receipts are retained as [maintenance](maintenance-headers.json), [404](404-headers.json), and [dependency/fallback](failure-headers.json). They confirm true codes, no-store, maintenance Retry-After300, bodyless404 HEAD, suitable plain API/Hg failures, denied image POST and reachable standalone Help. These receipts precede only the footer amendment; fresh focused status/startup tests cover the final production source.

All seven owned fixture invocations stopped normally and joined with exit0; their listeners were absent. The controller reset its viewport, closed only its test tabs and preserved the user's existing tabs and browser visibility. No authentication bypass or production failure-control endpoint was added.

Complete amended ordinary and tagged cmd/middleware/router suites passed, as did focused final status/startup, packaging, build and vet checks. Original broader runs were compiled before later corrections and retain their failures. An avatar-test/profile mismatch and a one-time tree-call bound failure remain for whole-branch triage. Final full-suite confirmation is required after all source fixes settle. Race lacks a local gcc compiler; other native platforms/versions, live network databases, broader fault acceptance and true browser text zoom remain unverified. The browser API did not support true text zoom; CSS scaling was not substituted.
