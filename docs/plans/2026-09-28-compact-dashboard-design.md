# Compact Dashboard Design

> Status: Approved design
> Date: 2026-09-28

## Problem

The dashboard sidebar lists 25 routes in one flat, scrolling column
(`web/src/lib/routeManifest.ts`). A daily surface like Chat has the same weight
as VPS or Proxies. Settings adds a second sidebar of roughly 25 sections, eight
of which duplicate pages that already have their own nav entry (Tools, Memory,
RAG, Skills, Plugins, Roles, Cron, Autopilot). Related features are scattered
across separate pages, and the sidebar footer spends space on language and
theme controls that are rarely touched.

The dashboard is used by people other than its author. A new user should see a
small surface first and opt into advanced areas when they need them.

## Goals

- Sidebar of 5–8 entries instead of 25, with no scrolling.
- One place to configure each feature; no Settings/page duplication.
- Progressive disclosure: new installs start small, chosen by use-case preset.
- Nothing disappears for existing users on upgrade.
- Every existing URL keeps working.

## Non-goals

- Redesigning the internals of individual pages (ChannelsPage and
  ContentCreatorPage density stays as is in this effort).
- Sidebar drag-to-reorder, favourites, or pins.
- Backend feature changes. Hiding a module only hides navigation.

## Information architecture

Three tiers of navigation. Each hub is one sidebar entry whose pages become tabs.

| Tier | Hub | Tabs |
|---|---|---|
| Core (always) | Chat | — |
| | Sessions | — |
| | Agent | Models · Roles · Soul · Memory & RAG |
| | Capabilities | Tools · Skills · MCP · Plugins |
| Module (preset) | Automation | Schedules · Autopilot · Board · Channels |
| | Security | Engagement · Intercept · Proxies · VPS |
| | Studio | Content Creator · Social Media |
| System (bottom, small) | System | Status · Files · Logs · Analytics · Settings |

### Presets

Chosen in the Setup `extras` step, General pre-selected. Changeable any time in
Settings.

| Preset | Modules | Sidebar entries |
|---|---|---|
| General (default) | — | 5 |
| Coding | automation | 6 |
| Security | automation, security | 7 |
| Creator | automation, studio | 7 |
| Full | automation, security, studio | 8 |

Toggling an individual module after choosing a preset shows the preset as
**Custom**.

## Configuration

Stored in the existing `display` block of `config.yaml`:

```yaml
display:
  modules: [automation]   # active modules — the source of truth
  preset: coding          # label only, for the UI
```

- `modules` decides visibility; `preset` is display-only.
- **Absent `modules` means Full.** Existing installs have no key, so upgrading
  hides nothing and needs no migration. Only a new install completing Setup
  writes an explicit list.
- Module ids are `automation`, `security`, `studio`. Go validates them on
  config save and rejects unknown ids. A parity test keeps the Go and
  TypeScript lists identical.

## Routing

- Each manifest entry gains `hub` and optional `module` fields. The sidebar is
  built from hubs, filtered by active modules.
- Tabs are nested paths so they can be bookmarked and deep-linked:

  | Old | New |
  |---|---|
  | `/providers`, `/models` | `/agent/models` |
  | `/roles`, `/soul`, `/memory` | `/agent/roles`, `/agent/soul`, `/agent/memory` |
  | `/tools`, `/skills`, `/mcp`, `/plugins` | `/capabilities/…` |
  | `/cron` | `/automation/schedules` |
  | `/autopilot`, `/board`, `/channels` | `/automation/…` |
  | `/engagement`, `/intercept`, `/proxies`, `/vps` | `/security/…` |
  | `/content-creator`, `/social-media` | `/studio/creator`, `/studio/social` |
  | `/system`, `/files`, `/logs`, `/analytics` | `/system/status`, `/system/…` |
  | `/config` | `/system/settings` |

- **Every old path redirects** to its new path, preserving query string and
  hash. A bare hub path (`/agent`) redirects to its first tab.
- A disabled module's routes still resolve. They are hidden from the sidebar
  only, stay reachable through bookmarks and the command palette, and show a
  small "module off · turn on" notice.

## Page UI

### Hub header

The shell already owns the page header (`PageFrame` in `AppShell.tsx`); pages
contribute buttons through `usePageActions`. The hub header is therefore built
in `PageFrame`, not in each page:

```
┌──────────────────────────────────────────────────────┐
│ Automation  [Schedules] Autopilot Board Channels  [actions] │
├──────────────────────────────────────────────────────┤
│ tab content                                             │
```

- One row: hub title, tab strip, then page actions.
- Page descriptions leave the header. They move to the tab tooltip and to the
  page's empty state.
- Tabs are `NavLink`s (they are routes), scroll horizontally on mobile, and
  keep lazy loading per page so the bundle does not grow.

### Sidebar

- Collapsible to an icon rail (about 56px); the collapsed state is a
  per-browser preference in `localStorage`.
- Footer keeps only the status pill and update banner. Language and theme move
  to Settings.

### Mobile

Bottom bar becomes Chat · Sessions · Agent · More. More opens the drawer with
the full hub list.

### Command palette

`Cmd+K` / `Ctrl+K`, built on the existing `dialog.tsx` with a simple fuzzy
filter; no new dependency. First version covers:

1. Every hub and tab, including disabled modules (marked "off").
2. Recent sessions, opening straight into the chat.
3. A few actions: new chat, switch model, toggle theme.

### Settings

- Keeps global settings only: Server, Database, Password, Language, Theme,
  Modules, Logging.
- A new **Modules** section under Essentials: preset dropdown plus one toggle
  per module.
- Search still finds settings that moved, and shows "Moved to Automation →
  Schedules" with a link.

## Rollout

Four phases, each mergeable on its own.

1. **Hubs and redirects.** Manifest `hub` field, nested paths, legacy
   redirects, hub header with tabs, grouped sidebar, mobile bottom bar. All
   modules visible. The sidebar drops to eight entries with no feature lost.
2. **Modules and presets.** `display.modules` with Go validation, Settings
   Modules section, Setup preset step, sidebar filtering.
3. **Command palette.**
4. **Slimmer Settings and sidebar polish.** Move per-feature settings into
   their hubs, "moved to" search results, language and theme out of the
   sidebar footer, collapsible icon rail.

## Testing

- **Go:** unknown module ids rejected; absent `modules` behaves as Full;
  `POST /api/setup/complete` writes the chosen list.
- **Frontend (`bun test`):**
  - Every legacy path redirects to a valid hub tab. Generated from the
    manifest, so new routes are covered automatically.
  - Preset → modules mapping.
  - Sidebar hides disabled modules; palette still lists them.
- **Parity:** Go and TypeScript module lists match.
- **Smoke:** the existing route walker (every route × two viewports) moves to
  the new paths and additionally asserts each legacy path lands on its new
  destination.
- **Manual:** browser checks at 1440×900 and 390×844.

## Risks

- **Hardcoded links.** Six navigation links in `web/src` point at old paths
  (`/config` ×4, `/cron`, `/social-media`). Redirects keep them working; they
  are updated anyway to avoid the extra hop.
- **Page layout inside tabs.** `staticHeight` (Soul) and `fullBleed` pages must
  still size correctly under the hub header.
- **i18n.** New hub, preset, and "More" labels in all five locales (en, id, ja,
  zh, ru).
- **"Where did VPS go?"** after choosing General. Mitigated by the palette and
  by Settings search finding disabled modules.
