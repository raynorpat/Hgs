# Hgs maintenance and error-page prototype

Open index.html locally, or use the active visual-companion preview. Switch between Maintenance, 500 error, 404 not found and Compare all. Hide illustrations checks that the messages/actions remain useful without artwork.

This is a throwaway design preview, not application routing or real maintenance mode. The preview server returns ordinary successful HTML; its displayed 503/500/404 labels describe the intended production states. Check again is simulated, Home shows a destination preview, Help opens a local dialog, and Copy copies only a fictional reference ID. No account/server data is loaded and no failed request is replayed.

## Illustrations

All three PNGs were generated with the built-in image_gen tool on 2026-10-26 using the approved landing-page Gopher as the reference. They retain RGBA transparency and are copied into this prototype so it works without generated-image storage paths or external requests.

- hgs-maintenance.png: yellow hardhat, wrench and white laboratory coat. The lab coat was explicitly requested after the first hardhat image.
- hgs-error.png: lab-coat Gopher with soot, ruffled fur and a cracked smoking beaker after a harmless experiment mishap.
- hgs-not-found.png: lab-coat Gopher searching through a magnifying glass.

Exact generation/edit prompts are in [PROMPTS.md](PROMPTS.md). The user approved all three page compositions and these exact Gopher illustrations on 2026-10-26, including the white lab coat on the hardhat Gopher. Reuse these PNGs when implementing Tasks 24–25; do not regenerate or replace the artwork unless the user requests a change. Prototype controls, simulated actions and the fictional reference ID are excluded from production. The header beaker is a code-drawn prototype mark; the shared production mark remains governed by Task 16.

## Attribution

Original Go Gopher by Renee French: [source and attribution guidance](https://go.dev/blog/gopher), licensed [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/). These AI-generated adaptations add the hardhat/wrench/lab coat, lab-mishap details, and searching pose/magnifying glass. They do not imply endorsement by the Go project. Retain these attribution and adaptation notices with redistributed artwork; existing Gogs copyright/license notices remain applicable to the application.

## Preview checks

- JavaScript syntax checked using node --check on the extracted inline script.
- All three local PNGs load and retain PNG color type 6 (RGBA).
- Desktop comparison and individual pages inspected; checked at 1440px and 390px with no page-level horizontal overflow.
- Check again displays its simulated-maintenance message; Help opens and closes with focus returned to its trigger; Home explains the prototype destination.
- 500 shows a fictional reference ID; hiding illustrations preserves headings and recovery actions.
- No browser console errors observed during the checks.

Production status codes, Retry-After, admission control and dependency-free server rendering remain the responsibility of implementation Task 25, behind the existing hosting prerequisite gate.
