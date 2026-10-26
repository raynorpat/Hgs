# Hgs landing-page prototype

Open `index.html` in a browser. This is a throwaway design concept, not the application home route. It has no backend and collects no credentials. Repository rows are samples; the clone URL uses the reserved `.example` domain.

## Identity and artwork

Product: Hgs. Tagline: **A painless, self-hosted Mercurial service.**

`gopher-lab.png` is an AI-generated adaptation concept of the Go Gopher wearing a lab coat and holding a beaker, generated on 2026-10-26. Original Go Gopher by Renee French, [CC BY 4.0](https://creativecommons.org/licenses/by/4.0/); [source and attribution guidance](https://go.dev/blog/gopher). The adaptation adds the lab coat, beaker and pose. It does not imply endorsement by the Go project. Retain this attribution with reused artwork.

## Preview checks

Visually inspected at 390px and 1440px viewport widths, plus the default desktop viewport. No page-level horizontal overflow was observed, and the local mascot loaded. Sign-in and repository preview dialogs opened; Escape dismissed the sign-in dialog and restored focus. Copy displayed its success status. These checks do not establish backend readiness.

Production home templates, routes and public logo assets are unchanged. The design direction and later integration requirements are in `../../DESIGN.md`.
