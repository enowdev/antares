# VPS over Cloudflare Tunnel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A saved VPS can be marked "via Cloudflare Tunnel" and every VPS feature (Test, metrics, processes, `vps_run`, upload/download) works over it.

**Architecture:** `internal/vps.dial` gets its `net.Conn` from a transport switch: direct TCP (today) or a WebSocket to `wss://<host>/` carrying SSH in binary frames, the same protocol `cloudflared access ssh` speaks. Three new `vps_hosts` columns (migration v4) hold the mode and an optional Access service token (secret encrypted at rest). The API, agent tool and dashboard dialog pass the fields through.

**Tech Stack:** Go (`golang.org/x/crypto/ssh`, `golang.org/x/net/websocket`, already in `go.mod`), SQLite/Postgres migrations, React + TypeScript (Vite, bun).

**Spec:** `docs/superpowers/specs/2026-11-14-vps-cloudflared-design.md`

## Global Constraints

- No new Go or npm dependencies.
- Tunnel mode value is exactly `"cloudflared"`; empty string means direct SSH. Anything else is stored as `""`.
- Tunnel dial: `wss://<host>/`, origin `https://<host>`, header `User-Agent: antares`, `PayloadType = websocket.BinaryFrame`, 20s dial timeout, ctx cancellation honoured. Port is ignored in tunnel mode.
- Headers `Cf-Access-Client-Id` / `Cf-Access-Client-Secret` are sent only when non-empty.
- `cf_client_secret` is encrypted at rest with the store's secret box and is never returned by the API (only `has_cf_secret`).
- Refused upgrade error text: `cloudflare refused the tunnel connection to <host>: if the hostname is protected by Cloudflare Access add a service token; otherwise check that the tunnel routes it to ssh://localhost:22`.
- Host-key TOFU behaviour is unchanged in both modes.
- Tests must not touch the real `~/.antares/.secretkey`: inject a box built with `secret.NewBox`.

## Review Focus

1. Editing a tunnel host with the Client Secret left blank keeps the stored secret → `TestKeepStoredSecretsKeepsBlankCFSecret` (Task 3).
2. Pasting `https://ssh.example.com/` as the hostname saves `ssh.example.com` → `TestVPSFromBodyNormalizesTunnelHost` (Task 3).
3. A tunnel endpoint that accepts TCP but never answers must time out, not hang → `TestTunnelDialHonoursContext` (Task 2).
4. TOFU still works over the tunnel: Ping returns the presented host key → asserted in `TestPingOverCloudflaredTunnel` (Task 2).
5. Rows saved without a tunnel stay direct after the migration → `TestVPSHostDefaultsToDirect` (Task 1).

---

### Task 1: Store columns (migration v4)

**Files:**
- Modify: `internal/store/migrations.go` (add `vpsTunnelV4` next to `devicesV3`)
- Modify: `internal/store/migrate.go:22-40` (register v4, extend doc comment)
- Modify: `internal/store/types.go:111-131` (`VPSHost`)
- Modify: `internal/store/vps.go` (cols, put, scan, decrypt)
- Test: `internal/store/vps_test.go` (new)

**Interfaces:**
- Produces: `store.VPSHost` fields `Tunnel string \`json:"tunnel"\``, `CFClientID string \`json:"cf_client_id,omitempty"\``, `CFClientSecret string \`json:"cf_client_secret,omitempty"\``.

- [ ] **Step 1: Write the failing tests** in `internal/store/vps_test.go`. Helper `newVPSTestStore(t) *sqlStore`: `newTestStore(t).(*sqlStore)`, then `ss.boxOnce.Do(func(){ ss.box, ss.boxErr = secret.NewBox(bytes.Repeat([]byte{7}, 32)) })`.
  - `TestVPSHostTunnelFieldsRoundTrip`: Put `{ID:"vps_t", Host:"ssh.example.com", Tunnel:"cloudflared", CFClientID:"abc.access", CFClientSecret:"s3cret", AuthMethod:"password", Password:"pw"}`; Get returns the same three values; `SELECT cf_client_secret FROM vps_hosts WHERE id='vps_t'` is non-empty and `!= "s3cret"`. Put again with `CFClientSecret:"new"` → Get returns `"new"` (conflict-update set includes the new columns).
  - `TestVPSHostDefaultsToDirect`: Put `{ID:"vps_d", Host:"1.2.3.4", Password:"pw"}` → Get `.Tunnel == ""`, `.CFClientID == ""`, `.CFClientSecret == ""`.
- [ ] **Step 2: Run** `go test ./internal/store/ -run 'VPSHost' -v` → FAIL (unknown fields).
- [ ] **Step 3: Implement.** `vpsTunnelV4` = three statements: `ALTER TABLE vps_hosts ADD COLUMN tunnel TEXT NOT NULL DEFAULT ''`, `... cf_client_id TEXT NOT NULL DEFAULT ''`, `... cf_client_secret TEXT NOT NULL DEFAULT ''`. Register `{version: 4, statements: append([]string(nil), vpsTunnelV4...)}`. Append `tunnel,cf_client_id,cf_client_secret` to `vpsCols` (keep `created_at,updated_at` last or update placeholders consistently), encrypt `CFClientSecret` in `PutVPSHost`, add the three columns to the `onConflict` update set, scan and decrypt them.
- [ ] **Step 4: Run** `go test ./internal/store/ -v` → PASS, including every `Migrat*` test (ledger counts derive from `baselineMigrations`, so they absorb v4).
- [ ] **Step 5: Commit** `git add internal/store && git commit -m "store: vps_hosts tunnel and Access service token columns (migration v4)"`

### Task 2: Tunnel transport in `internal/vps`

**Files:**
- Create: `internal/vps/tunnel.go`
- Modify: `internal/vps/vps.go:19-42` (Target, endpoint), `:87-111` (dial uses transport)
- Test: `internal/vps/tunnel_test.go` (new)

**Interfaces:**
- Produces: `const TunnelCloudflared = "cloudflared"`; `Target` fields `Tunnel, CFClientID, CFClientSecret string`; `func NormalizeTunnelHost(s string) string`; `var ErrTunnelRefused error`; unexported `func dialTransport(ctx context.Context, t Target) (net.Conn, error)`, `func (t Target) endpoint() string` (`wss://host` in tunnel mode, else `addr()`), and test seam `var tunnelEndpoint = func(host string) (location, origin string)` defaulting to `("wss://"+host+"/", "https://"+host)`.

- [ ] **Step 1: Write the failing tests** in `internal/vps/tunnel_test.go`. Helper `newTunnelBridge(t, sshAddr string) (host string, got *http.Header)`: `httptest.NewServer(websocket.Server{Handshake: record r.Header into *got; return nil, Handler: set PayloadType=BinaryFrame, net.Dial sshAddr, copy both ways until either side closes})`; override `tunnelEndpoint` to `ws://<srv host>/` + `http://<srv host>` and restore with `t.Cleanup`.
  - `TestRunOverCloudflaredTunnel`: `newTestSSHServer` with `execOutput "tunnel-ok\n"`; target = `srv.target()` with `Tunnel: TunnelCloudflared, Host: bridgeHost, Port: 1`; `Run(ctx, target, "whoami")` → out `"tunnel-ok\n"`, err nil.
  - `TestPingOverCloudflaredTunnel`: Ping over the bridge → err nil, `seenHostKey` starts with `"ssh-"`.
  - `TestTunnelSendsAccessHeaders`: with `CFClientID:"abc.access", CFClientSecret:"s3cret"` → recorded `Cf-Access-Client-Id == "abc.access"`, `Cf-Access-Client-Secret == "s3cret"`, `User-Agent` contains `"antares"`; a second Ping with both empty → both headers `== ""`.
  - `TestTunnelRefusedIsReadable`: tunnel endpoint at an `httptest` server answering every request `403` → Ping err; `errors.Is(err, ErrTunnelRefused)`; `strings.Contains(err.Error(), "service token")`.
  - `TestTunnelDialHonoursContext`: endpoint at a raw `net.Listen` that accepts and never writes; ctx 300ms → Ping returns within 2s; `errors.Is(err, ErrTimeout)`.
  - `TestNormalizeTunnelHost`: `"https://ssh.example.com/"`, `"ssh://ssh.example.com"`, `"wss://ssh.example.com/x/y"`, `" ssh.example.com "`, `"ssh.example.com"` → all `"ssh.example.com"`.
  - `TestLiveCloudflaredBanner`: `t.Skip` unless env `ANTARES_LIVE_TUNNEL_HOST` is set; `dialTransport` with that host in tunnel mode, read until `\n` within 15s, assert prefix `"SSH-2.0-"`. Sends no credentials.
- [ ] **Step 2: Run** `go test ./internal/vps/ -run 'Tunnel|Cloudflared' -v` → FAIL (undefined).
- [ ] **Step 3: Implement** `dialTransport`: direct → existing `net.Dialer{Timeout: 20s}.DialContext(ctx, "tcp", t.addr())`; tunnel → `websocket.NewConfig(tunnelEndpoint(host))`, headers per Global Constraints, `Dialer = &net.Dialer{Timeout: 20 * time.Second}`, `DialContext(ctx)`; map `errors.Is(err, websocket.ErrBadStatus)` to the refused error (wrapping `ErrTunnelRefused`), set `PayloadType = websocket.BinaryFrame`. In `dial`, call `dialTransport` and use `t.endpoint()` in every error message and as the `ssh.NewClientConn` address; the existing ctx-timeout wrapping with `ErrTimeout` stays.
- [ ] **Step 4: Run** `go test ./internal/vps/ -v` → PASS (old tests unchanged; live test SKIP).
- [ ] **Step 5: Commit** `git add internal/vps && git commit -m "vps: dial over Cloudflare Tunnel (cloudflared WebSocket carrier)"`

### Task 3: API and agent tool

**Files:**
- Modify: `internal/server/handlers_vps.go` (body, view, save, test, targetFor)
- Modify: `internal/tools/vps.go:42-48` (`targetFromHost`), `formatVPSList`
- Modify: `docs/tools.md:72-78` (one paragraph on tunnel hosts)
- Test: `internal/server/handlers_vps_test.go` (new), `internal/tools/vps_list_test.go` (new)

**Interfaces:**
- Consumes: Task 1 fields; Task 2 `vps.TunnelCloudflared`, `vps.NormalizeTunnelHost`, `Target` fields.
- Produces: `vpsBody` JSON fields `tunnel`, `cf_client_id`, `cf_client_secret`; view fields `tunnel`, `cf_client_id`, `has_cf_secret`; helpers `func vpsFromBody(body vpsBody) *store.VPSHost` and `func keepStoredSecrets(v, prev *store.VPSHost)`.

- [ ] **Step 1: Write the failing tests.**
  - `TestVPSFromBodyNormalizesTunnelHost`: body `{Host:"https://ssh.example.com/", Tunnel:"cloudflared"}` → `Host == "ssh.example.com"`, `Tunnel == "cloudflared"`, `Username == "root"`, `Label == "ssh.example.com"`.
  - `TestVPSFromBodyUnknownTunnelIsDirect`: `Tunnel:"bogus"` → `Tunnel == ""`; direct-mode host is only trimmed, not normalized.
  - `TestKeepStoredSecretsKeepsBlankCFSecret`: prev `{CFClientSecret:"old", Password:"pw", CreatedAt:t0}`, v with blanks → v gets `"old"`, `"pw"`, `t0`; v with `CFClientSecret:"new"` keeps `"new"`.
  - `TestVPSViewHidesCFSecret`: view of `{CFClientSecret:"s3cret", CFClientID:"abc", Tunnel:"cloudflared"}` has `has_cf_secret == true`, `cf_client_id == "abc"`, `tunnel == "cloudflared"`, and no value anywhere equals `"s3cret"`.
  - `TestTargetForMapsTunnel`: `targetFor` copies `Tunnel`, `CFClientID`, `CFClientSecret`.
  - `TestFormatVPSListShowsCloudflared` (tools): tunnel host line contains `via cloudflared` and no `:22`; direct host line still `user@host:port`.
- [ ] **Step 2: Run** `go test -run 'VPS|TargetFor|FormatVPSList' -v ./internal/server/ ./internal/tools/` → FAIL.
- [ ] **Step 3: Implement.** `handleSaveVPS` = `vpsFromBody` + (on edit) `keepStoredSecrets`. `handleTestVPS`: build `v := vpsFromBody(body)`; with an id, load prev, `keepStoredSecrets(v, prev)`, `v.HostKey = prev.HostKey`, so Test checks what the form shows; pin onto prev as today. `targetFor`/`targetFromHost` map the three fields. `formatVPSList` prints `user@host via cloudflared` for tunnel hosts. Docs: tunnel hosts use the tunnel hostname, need no cloudflared install, optional service token.
- [ ] **Step 4: Run** `go test ./internal/server/ ./internal/tools/ -v` → PASS.
- [ ] **Step 5: Commit** `git add internal/server internal/tools docs/tools.md && git commit -m "server: VPS API and vps tools accept Cloudflare Tunnel hosts"`

### Task 4: Dashboard dialog

**Files:**
- Modify: `web/src/pages/VPSPage.tsx` (`VPSHost` type :35-44, card :188, `VPSDialog` :398-596)
- Modify: `web/src/lib/i18n.tsx` (en keys after `'vps.passphrasePh'`), `web/src/lib/locales/{id,ja,ru,zh}.ts`

**Interfaces:**
- Consumes: Task 3 JSON fields.

- [ ] **Step 1: Add i18n keys** (en / id): `vps.connection` Connection / Koneksi; `vps.connDirect` Direct SSH / SSH langsung; `vps.connCloudflared` Cloudflare Tunnel (cloudflared) / same; `vps.tunnelHost` Tunnel hostname / Hostname tunnel; `vps.tunnelHint` "Same hostname as --hostname in your cloudflared ProxyCommand." / "Hostname yang sama dengan --hostname di ProxyCommand cloudflared-mu."; `vps.cfToken` "Cloudflare Access service token (optional)" / "Service token Cloudflare Access (opsional)"; `vps.cfTokenHint` "Only needed if this hostname is protected by Cloudflare Access." / "Hanya perlu kalau hostname ini dilindungi Cloudflare Access."; `vps.cfClientId` Client ID; `vps.cfClientSecret` Client Secret; `vps.viaCloudflared` cloudflared. Translate the same keys into ja/ru/zh.
- [ ] **Step 2: Dialog.** `VPSHost` gains `tunnel?: string`, `cf_client_id?: string`, `has_cf_secret?: boolean`. State `tunnel`, `cfClientId`, `cfClientSecret`, reset on open like the others (secret always reset to `''`). A "Connection" select (styled like the auth select) right after Label. Tunnel mode: host label `vps.tunnelHost`, placeholder `ssh.example.com`, Port field hidden, hint `vps.tunnelHint` under it, and a service-token block (`vps.cfToken` heading, `vps.cfTokenHint`, Client ID input, Client Secret password input with `••••••••` placeholder when `has_cf_secret` on edit). `body()` sends `tunnel`, and the token fields only in tunnel mode (`''` otherwise).
- [ ] **Step 3: Card.** Tunnel hosts render `{username}@{host}` plus a small tag `vps.viaCloudflared` instead of `:{port}`.
- [ ] **Step 4: Verify** `cd web && bun run typecheck` → no errors; `bun test` → PASS.
- [ ] **Step 5: Look at it.** Run the dashboard, open Add server, switch Connection both ways: the Port field and the token block toggle, and the card shows the tag after saving a tunnel host.
- [ ] **Step 6: Commit** `git add web/src && git commit -m "web: Cloudflare Tunnel option in the VPS dialog"`

### Task 5: Whole-branch verification

- [ ] **Step 1:** `go vet ./... && go test ./...` → PASS.
- [ ] **Step 2:** Live smoke (no credentials): `ANTARES_LIVE_TUNNEL_HOST=<your-tunnel-hostname> go test ./internal/vps/ -run TestLiveCloudflaredBanner -v` → PASS with an `SSH-2.0-` banner.
- [ ] **Step 3:** `cd web && bun run build` → succeeds (do not commit build output unless it is already tracked).
