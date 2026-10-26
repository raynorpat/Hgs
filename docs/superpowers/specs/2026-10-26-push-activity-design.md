# Push Activity, Repository Feeds, and Changeset Issue Closing — Design

Date: 2026-10-26. Status: draft for approval.

First of seven follow-up projects restoring features deferred by the Mercurial conversion (`2026-10-25-mercurial-hosting-design.md`). Agreed order: (1) push events, (2) activity feeds and issue closing — this document covers both; then forks, mirrors/imports/migration, pull requests, releases/archives, push webhooks. Web editing and editable hooks are dropped permanently.

## Intent

After the Git removal, a push to a Gogs repository leaves no trace outside the repository itself: the dashboard shows no code activity, nothing can be subscribed to, and "fixes #12" in a changeset message does nothing. This project makes pushes visible and lets changesets close issues, and lays down the push record that later projects (webhooks, release feeds) build on.

## Goals

1. **Push record.** After every write session that changes a repository, Gogs records who pushed and what changed (new changesets per branch). Informational only: it never blocks, delays, or rolls back a push.
2. **Activity stream.** Dashboard and profile feeds (`models.GetFeeds`, `templates/user/dashboard/feeds.tmpl`) gain push entries and "closed issue via changeset" entries.
3. **Repository feed.** Every repository has an Atom feed URL that a feed reader can subscribe to without signing in. Private repositories use a per-user secret token in the URL.
4. **Changeset issue closing.** A closing keyword plus `#N` in a changeset message closes issue N of the same repository once that changeset is public and on mainline.

## Non-goals

- Closing issues in other repositories (`owner/repo#12`).
- Reopening an issue if the closing changeset is later stripped or obsoleted.
- "Mentioned in changeset" comments for references without a closing keyword.
- Showing bookmark-only moves in the activity stream.
- Release entries in feeds — added by the releases project.
- Webhooks — a later project consumes the push record.
- Per-user (dashboard) Atom feeds — only repository feeds are subscribable.

## Anti-goals (would be a failure even if it "works")

- A push that fails, slows noticeably, or is rejected because recording or issue closing failed.
- A feed URL, token, error, or response timing that reveals a private repository's existence or content to someone without read access.
- An issue closed twice, or closed by a draft changeset or one that only lives on a feature branch that was never merged.
- A new Python helper, wire-protocol change, or Git-era compatibility layer (forbidden by the parent design).

## Constraints

- Everything in the parent design's CLAUDE.md "key decisions" still holds: Go only, Mercurial as child processes, portable across Windows/macOS/Linux, `modules/hg` imports no models/web code, models never import hosting/routers, `modules/hosting` composes.
- Server Mercurial ≥ 5.0: revsets used must work on 5.0.2 and 7.2.4.
- Copy goes in `conf/locale/locale_en-US.ini`.
- No new third-party dependency; Atom is generated with `encoding/xml`.

## Decided behavior

### Push record

- Recorded per write session (HTTP push request or SSH `hg serve --stdio` session) that changed the repository.
- Contents: repository, pusher (the authenticated user of the session), time, and for each branch that received new changesets, the list of new changesets (node, author, first line of description, date) in topological order.

### Initial sync is a baseline (user decision)

Gogs acts only on pushes after the initial sync. The first write session that brings changesets into an empty repository, and any repository Gogs has no stored state for yet, is a baseline: Gogs stores the heads and marks every existing mainline changeset as processed. It closes no issues and adds no push entries to the stream or feed. Old "fixes #3" messages from a previous tracker therefore never touch Gogs issues. Every later session is processed normally.

### Activity stream entry (option "A", chosen from mockups)

One entry per branch that received changesets:

```
alice pushed to default at alice/widgets
  a1b2c3d Fix crash on empty manifest (fixes #12)
  e4f5a6b Add tests for manifest parser
  9c8d7e6 Tidy imports
  View comparison for these 3 changesets »
  5 minutes ago
```

- At most 5 changesets listed, then "and N more".
- The comparison link goes to the existing history/changeset views (single changeset view when there is one changeset).
- Short node hashes link to the changeset page. Issue references in descriptions render as links (existing `RenderIssueIndexPattern`).
- A changeset that closes an issue also produces an entry: "alice closed #12 in alice/widgets via `a1b2c3d`".
- Visibility follows the existing rule: entries for private repositories are shown only to users who can read them (existing `IsPrivate` handling in `Action`/`GetFeeds`, fan-out via `NotifyWatchers`).

### Repository Atom feed

- One feed per repository, linked from the repository page (and advertised with `<link rel="alternate" type="application/atom+xml">`).
- Items: pushes (same content as the stream entry), issue opened, issue commented, issue closed (by changeset or manually). Releases later.
- Latest 50 items, newest first.
- **Public repository:** plain URL, no authentication.
- **Private repository:** each user who can read it sees a URL containing their personal feed token. One token per user, valid for every repository that user can read. Each request re-checks that the token's owner currently has read access, so revoking access cuts off the feed immediately. Users can regenerate their token from their account settings, invalidating all old URLs.
- An unknown repository, a private repository with no/invalid/revoked token, and a token whose owner lacks access all return the same 404.

### Changeset issue closing

- Keywords, case-insensitive, followed by whitespace then `#N`: close, closes, closed, fix, fixes, fixed, resolve, resolves, resolved. Multiple references per message are all honored.
- Same repository only; a nonexistent or already-closed issue is silently skipped.
- **Trigger condition:** the changeset is in the public phase **and** is an ancestor of (or is) a head of the `default` branch. "Mainline" is ancestry, not branch name, so feature-branch changesets merged into `default` count.
- Evaluated whenever the set of public mainline changesets may have grown: after any write session (covers direct push, a later merge into `default`, and a later draft→public phase change).
- Each changeset is processed at most once per repository, ever. Re-pushes, repeated phase changes, or restarts never re-close or re-comment.
- On close: a `COMMENT_TYPE_CLOSE` comment referencing the changeset (short hash linking to the changeset page), the issue marked closed through the same model path the manual close uses (so counters, `IssueUser` pairs and notifications behave identically), and an activity entry.
- Credited to the active Gogs user whose primary email matches the changeset author email **and** who can currently read the repository; otherwise to the pusher. (Claude's call: author emails are client-supplied, so an unchecked match would let a pusher put actions in a private repository on anyone's profile.)

### Phases on a publishing server (Claude's call)

The server is publishing (`phases.publish` default, `evolution.exchange = false` in `modules/hg/config.go`; parent design: "normal publishing-server phase behavior"), so every pushed changeset is public on arrival and nothing becomes hidden. This project does not change that. In practice the trigger is therefore "is an ancestor of a `default` head", and the feature-branch half of the rule is what matters. The revsets still include `public()` so the rule stays correct if non-publishing repositories are ever introduced, but there is no draft-push test and no obsolescence handling beyond not erroring on an unknown stored head.

## How it gets made (Claude's calls — reviewable)

These are implementation choices, not things the user decided:

- **Detection by before/after comparison in Go, not a new Mercurial hook.** Earlier in the conversation Claude proposed an advisory `txnclose`/`changegroup` hook. On reading `modules/hgbridge`, comparing repository state when a write session ends is simpler and sturdier: no new hook surface, no capability lifetime issues at close time, and it naturally catches phase changes and merges that a changegroup hook would miss. Per repository, Gogs persists (a) the heads it last saw and (b) a marker of the public-mainline set it has already processed for issue closing. When a write session ends, Gogs runs `hg log` revsets against the repository: new changesets = ancestors of current heads minus ancestors of stored heads; newly closable = public ancestors of `default` heads minus already processed. Then it updates the stored state.
- **Persisted state, not in-memory**, so a Gogs crash between push and processing is caught up on the next session end for that repository (or at startup — builder's choice).
- Session-end processing happens after the session's response is complete and outside the session's lifetime budget, serialized per repository, errors logged and never surfaced to the client.
- Token in the feed URL is a query parameter or path segment (builder's choice), and must be kept out of access logs.
- Push changesets stored as JSON in `Action.Content` (as old Gogs did), new `ActionType` values for push and changeset-close, `ActionIcon` extended.

## Left to the builder

- Exact revsets, provided the goals and anti-goals hold.
- Where the persisted per-repository state lives (columns on `Repository` or a new table) and the schema of the feed token (column on `User` or a table).
- Attribution when two write sessions to one repository overlap (rare; either session's pusher is acceptable as long as nothing is lost or duplicated).
- Route paths for the feed and the settings control for regenerating the token.
- Handling of very large pushes (e.g. initial push of a big history): the stream entry is capped at 5 listed changesets, but processing and issue-closing scans must stay bounded in time and memory.
- Test layout: unit tests for keyword parsing and Atom output; `integration`-tagged tests with real Mercurial for detection (push to default, push to feature branch then merge, initial push into an empty repository is a silent baseline, re-push idempotence, author email matching a user without access, private feed access/revocation).
