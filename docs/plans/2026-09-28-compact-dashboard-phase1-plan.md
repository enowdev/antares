# Compact Dashboard — Phase 1 Implementation Plan (Hubs and Redirects)

> Design: [2026-09-28-compact-dashboard-design.md](2026-09-28-compact-dashboard-design.md)
> Date: 2026-09-28

## Outcome

The sidebar shows 8 hub entries instead of 25 routes. Pages that share a hub
render under one header with a tab strip. Every old URL redirects to its new
nested path. All modules remain visible; module hiding is phase 2.

Frontend only. No Go changes: the SPA fallback in
`internal/server/routes.go` (`handleDashboard`) already serves `index.html` for
paths of any depth, and built assets use absolute `/assets/…` URLs, so nested
routes load correctly (verified against a running server).

## Current state (what the plan builds on)

- `web/src/lib/routeManifest.ts` — pure-data list of 25 routes. `aliases`
  render the same page at another path (`/c/:sessionId`, `/models`).
  `smokeTargets()` feeds the smoke walker.
- `web/src/lib/routes.ts` — attaches icon + lazy component per manifest id;
  exports `ROUTES`, `NAV_LABELS`, `PRIMARY_ROUTES`, `routeFor()`.
- `web/src/App.tsx` — registers `path` and every alias inside `AppShell`.
- `web/src/components/layout/AppShell.tsx` — `NavItems` renders `ROUTES`;
  `PageFrame` owns the page header (title, description, actions from
  `usePageActions`); mobile bottom bar renders `PRIMARY_ROUTES`.
- `scripts/smoke.mjs` — visits every `smokeTargets()` path at two viewports
  and fails with `REDIRECT` when the final path differs from the requested one.
- Locales: `en` inline in `web/src/lib/i18n.tsx` (source of truth,
  `MessageKey = keyof typeof en`); `id`, `ja`, `zh`, `ru` in
  `web/src/lib/locales/*.ts`.

## Tasks

### 1. Hub definitions in the manifest

`web/src/lib/routeManifest.ts`:

- Add `HubId = 'chat' | 'sessions' | 'agent' | 'capabilities' | 'automation' |
  'security' | 'studio' | 'system'`.
- Add `HUB_MANIFEST: { id: HubId; path: string; titleKey: MessageKey; tier:
  'core' | 'module' | 'system'; module?: ModuleId }[]` in sidebar order.
  `ModuleId = 'automation' | 'security' | 'studio'` is declared now so phase 2
  only adds filtering.
- Each `RouteManifestEntry` gains `hub: HubId`, `tabKey?: MessageKey` (short
  tab label), and `legacyPaths?: string[]`.
- Move paths to the nested scheme from the design (`/agent/models`,
  `/automation/schedules`, `/system/settings`, …). Tab order within a hub is
  manifest order.
- `/models` moves from `aliases` to `legacyPaths` of `/agent/models`, together
  with `/providers`. `/c/:sessionId` stays an alias (it renders the chat, it is
  not a redirect).
- Add pure helpers (no React): `hubFor(pathname)`, `tabsOf(hubId)`,
  `legacyRedirects(): { from: string; to: string }[]`.
- `smokeTargets()` keeps returning canonical paths and aliases. Add
  `smokeRedirects()` returning `legacyRedirects()` plus each multi-tab hub root
  (`/agent` → `/agent/models`).
- Mark `primary` on chat, sessions, and the Agent models tab for the mobile
  bar (see task 5).

### 2. Manifest tests

New `web/src/lib/routeManifest.test.mjs` (bun, matching the existing
`*.test.mjs` style):

- Every entry's `path` starts with its hub's `path` (except the single-route
  hubs chat and sessions, whose path equals the hub path).
- Every hub has at least one route; no route references an unknown hub.
- Paths, aliases, and legacy paths are globally unique — no legacy path
  shadows a live route.
- Every one of the 25 pre-change paths appears as a canonical path, alias, or
  legacy path. Hardcode that list in the test so a future edit that drops one
  fails loudly.
- `smokeRedirects()` destinations are all canonical paths.

### 3. Runtime routes and redirects

`web/src/lib/routes.ts`:

- Export `HUBS` (hub manifest + icon + resolved tabs) built the same way as
  `ROUTES`, so a missing icon throws at startup like a missing runtime does
  today.
- Hub icons: Agent `Robot`, Capabilities `Toolbox`, Automation `Kanban`,
  Security `ShieldCheck`, Studio `FilmStrip`, System `Gear` (all already
  imported).
- Replace `NAV_LABELS` (keyed by path) with hub `titleKey` for the sidebar and
  entry `tabKey` for tabs. Delete `PRIMARY_ROUTES` in favour of task 5.
- `routeFor()` unchanged in behaviour; it now also sees nested paths.

`web/src/App.tsx`:

- Register a `<LegacyRedirect to=… />` route for every `legacyRedirects()`
  entry and every multi-tab hub root. `LegacyRedirect` is a small component
  using `useLocation` that navigates with `replace` and carries over `search`
  and `hash`.

### 4. Hub header and grouped sidebar

`web/src/components/layout/AppShell.tsx`:

- `NavItems` renders `HUBS`. A hub is active when `hubFor(pathname)` matches
  (keep the existing `/c/` special case for Chat). The link target is the
  hub's first tab.
- Tier `system` renders below a separator at the bottom of the nav list.
- `PageFrame` header, for routes whose hub has more than one tab:
  - One row: hub title, then a `HubTabs` strip, then page actions.
  - Description is no longer rendered in the header. It becomes the tab's
    tooltip (existing `Tooltip` primitives).
  - Single-tab hubs and non-hub pages keep today's header, including the
    description.
- New `web/src/components/layout/HubTabs.tsx`: `NavLink` per tab with
  `aria-current`, horizontal scroll and no wrap on mobile, active style matching
  the sidebar (`bg-primary/12 text-primary`).
- Check `staticHeight` (Soul) and `fill` still size correctly with the new
  header. Chat stays `fullBleed` and is unaffected.

### 5. Mobile bottom bar

- Bottom bar renders Chat, Sessions, Agent, and a **More** button that opens
  the existing drawer (`setDrawerOpen(true)`). The drawer already renders
  `NavItems`, so it shows every hub.
- Add `nav.more` to the locales (task 7).

### 6. Update hardcoded links

Redirects cover these, but update them to skip the hop:

| File | Old | New |
|---|---|---|
| `components/ui/SensitiveGate.tsx:44` | `/config` | `/system/settings` |
| `components/chat/ModelPicker.tsx:175` | `/config` | `/system/settings` |
| `pages/SetupPage.tsx:624` | `/config` | `/system/settings` |
| `pages/ChatPage.tsx:602` | `/config` | `/system/settings` |
| `pages/ContentCreatorPage.tsx:1178` | `/social-media` | `/studio/social` |
| `pages/ContentCreatorPage.tsx:1405` | `/cron` | `/automation/schedules` |

Re-run the grep from the design's risk section afterwards to confirm no
navigation link still targets a legacy path.

### 7. i18n

Add to `en` and mirror in `id`, `ja`, `zh`, `ru`:

- `hub.agent`, `hub.capabilities`, `hub.automation`, `hub.security`,
  `hub.studio`, `hub.system`
- `tab.*` short labels where the existing page title is too long for a tab
  (for example `tab.schedules`, `tab.creator`, `tab.social`, `tab.status`,
  `tab.settings`); reuse existing `nav.*` keys otherwise.
- `nav.more`

`bun run typecheck` fails on a missing `en` key; the other locales are
checked by reviewing that their key count matches `en`.

### 8. Smoke walker

`scripts/smoke.mjs`:

- Import `smokeRedirects` alongside `smokeTargets`.
- For each `{ from, to }`: navigate to `from`, and pass when the final path is
  `to`. Fail with `REDIRECT: expected ${to} got ${finalPath}` otherwise. Reuse
  the existing console-error, blank-page, and overflow checks.

## Verification

- `cd web && bun test` — existing 86 tests plus the new manifest tests.
- `cd web && bun run typecheck && bun run build`
- `go build ./...` (embedded dist) and `make smoke` — every canonical route and
  every legacy redirect at both viewports.
- Manual browser pass at 1440×900 and 390×844:
  - Sidebar shows 8 entries without scrolling.
  - Each hub's tabs switch without a full reload, and page actions still
    appear in the header.
  - Old bookmarks (`/cron`, `/vps?x=1#y`, `/config`) land on the new path with
    query and hash intact.
  - Mobile: More opens the drawer; tabs scroll horizontally without page
    overflow.

## Out of scope for phase 1

Module config and filtering, the Setup preset step, the command palette, the
collapsible icon rail, and moving settings out of the Settings page. These are
phases 2–4 in the design.
