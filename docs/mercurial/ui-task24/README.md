# Status illustration integration evidence

Source `0ce2410` copies the three approved PNGs byte-for-byte into `public/img`. Independent source review and a fresh scoped evidence re-review accepted the implementation and corrected desktop proof, with no open blocking findings. No illustration was regenerated or edited.

The release-archive tests verify exact source hashes/bytes, decoded1218×1292 dimensions, transparent background and opaque artwork, retained attribution/provenance, and cache/prototype exclusion. The required package suite passed8 top-level tests;3 existing Windows symlink subcases skipped because the client lacks the required privilege. Those cases remain unverified.

Root inspected the actual public copies on white/light surfaces in an isolated inspection fixture at1440×1000 and390×844. All images loaded; the maintenance hardhat, white lab coat and wrench, harmless lab-mishap details, and searching Gopher's magnifying glass remain faithful to the approved source. Edges and palette remain consistent. Responsive sizing kept the document within its client width.

- [Verified desktop](illustrations-desktop-verified.jpg)
- [Narrow top](illustrations-mobile-top.jpg)
- [Narrow bottom](illustrations-mobile-bottom.jpg)

The initial claim that illustrations-desktop.jpg proved desktop acceptance was incorrect: reviewing the saved file revealed collapsed content without visible artwork. That file is retained as failed evidence and is not accepted. A fresh single-buffer capture was saved as illustrations-desktop-verified.jpg and the actual saved file was directly viewed, confirming all three illustrations at1440×1000. Its SHA256 is9867783dff008eebd50eee4305228114bcb759d7ee64f2807a22338c4863afce. The default-width capture is additional inspection, not the required1440px proof. The narrow bottom capture was refreshed after a stale frame. These capture corrections changed no product or image bytes.

Both loopback previews stopped normally (GET200, joined exit0, no listeners on59533 or49196). The viewport was reset and only owned tabs34/35 closed; existing tabs11/12 and hidden browser visibility were preserved. The inspection helper is ignored scratch and is not packaged. This verifies asset integration; Task25 must still verify real status codes, maintenance startup, recovery and production page composition.
