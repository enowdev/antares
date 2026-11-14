# VPS over Cloudflare Tunnel (cloudflared): design

Date: 2026-11-14 · Size: small change (short design)

## Intent

Some servers expose SSH only through a Cloudflare Tunnel: port 22 is closed to
the internet, and people reach the box with an ssh config like

```
Host vps-example
    HostName ssh.example.com
    User root
    ProxyCommand cloudflared access ssh --hostname %h
```

Antares dials `host:port` over raw TCP today, so such a server cannot be added
on the VPS page. The goal: a saved VPS can be marked "via Cloudflare Tunnel",
and every VPS feature (Test button, metrics, process list, agent `vps_run`,
file upload/download) works against it unchanged.

## Goals / non-goals / anti-goals

- Goal: connect to a tunnel hostname such as `ssh.example.com` with no extra
  software on the machine running Antares (Docker, another VPS, Windows,
  desktop app).
- Goal: optional Cloudflare Access **service token** (Client ID + Secret) for
  hostnames protected by Access. Empty for hosts with no Access policy.
- Non-goal: interactive Access login (browser). A background agent cannot do
  it; a service token is the supported path.
- Non-goal: HTTP(S) proxy support for the WebSocket dial (`HTTPS_PROXY`).
- Non-goal: a free-form ProxyCommand field.
- Anti-goal: weakening host-key pinning (TOFU) or storing the service-token
  secret in the clear.

## Approaches

1. **Native WebSocket bridge in Go (picked).** Antares opens
   `wss://<hostname>/` itself and runs SSH over binary frames, exactly what
   `cloudflared access ssh` does internally (verified in cloudflared's
   `carrier/websocket.go` and `cmd/cloudflared/access/carrier.go`: GET upgrade
   to the hostname, binary frames, `User-Agent`, optional
   `Cf-Access-Client-Id` / `Cf-Access-Client-Secret`). Uses
   `golang.org/x/net/websocket`, already in `go.mod`: no new dependency.
   Trade-off: if Cloudflare changes the carrier protocol, our code must follow.
2. **Spawn `cloudflared access ssh --hostname …` and pipe its stdio.** Identical
   to the user's laptop, but requires cloudflared wherever Antares runs, and
   the metrics poll would spawn many short-lived processes.
3. **Free-form ProxyCommand field.** Dropped: a dashboard field that runs any
   local command, and it still needs cloudflared installed.

The user picked approach 1.

## Design

**Data.** Migration v4 adds three columns to `vps_hosts`:

- `tunnel TEXT NOT NULL DEFAULT ''`: `''` = direct SSH (today's behavior),
  `'cloudflared'` = Cloudflare Tunnel.
- `cf_client_id TEXT NOT NULL DEFAULT ''`: the service token's Client ID, which
  is an identifier, not a secret.
- `cf_client_secret TEXT NOT NULL DEFAULT ''`: encrypted at rest with the same
  secret box as password/key.

`store.VPSHost` and `vps.Target` gain `Tunnel`, `CFClientID`, `CFClientSecret`.
Existing rows default to direct, so nothing changes for current users.

**Dial (`internal/vps`).** `dial` stops calling `net.Dialer` directly and asks
the target for its transport:

- direct: unchanged TCP dial to `host:port`.
- cloudflared: WebSocket dial to `wss://<host>/` (port 443; the saved Port is
  ignored), with a 20s dial timeout and ctx cancellation,
  `PayloadType = BinaryFrame`, `User-Agent: antares`, plus the two
  `Cf-Access-*` headers when set. The resulting `*websocket.Conn` is a
  `net.Conn`, so the SSH handshake, TOFU host-key check, keepalive, Run,
  transfers and metrics run on it unchanged.

A rejected upgrade surfaces as a readable error that wraps `ErrTunnelRefused`
and names the HTTP status. The wording follows the status class:
401/403 or a redirect to an Access login says the hostname is protected by
Cloudflare Access (add or check the service token); 502/503/504/530 says the
tunnel is offline (check that cloudflared is running on the server); any other
status says the hostname is not routed to SSH (check the ingress for
`ssh://localhost:22`). x/net discards the status, so the upgrade is performed
over a connection that records the start of the response.

Hostname input is normalized in cloudflared mode: a leading `https://`,
`wss://` or `ssh://` and any trailing `/` or path are stripped.

**API (`internal/server/handlers_vps.go`).** `vpsBody` accepts `tunnel`,
`cf_client_id`, `cf_client_secret`. Save keeps a blank secret on edit (same
rule as password/key). `vpsView` returns `tunnel`, `cf_client_id` and
`has_cf_secret`, never the secret. `/vps/test` passes the new fields through,
including a just-typed secret override. `tools/vps.go` `targetFromHost` maps
the new fields; the agent's server list shows "via cloudflared".

**Dashboard (`web/src/pages/VPSPage.tsx`).** The add/edit dialog gets a
"Connection" select: "Direct SSH" / "Cloudflare Tunnel (cloudflared)". In
tunnel mode:

```
Connection   [ Cloudflare Tunnel (cloudflared) v ]
Hostname     [ ssh.example.com                    ]   (no Port field)
             Same hostname as --hostname in your cloudflared ProxyCommand.
Username     [ root ]      Auth method [ Key v ]
...
Cloudflare Access service token (optional)
Client ID     [                ]
Client Secret [ ••••••••       ]   (blank on edit = keep)
```

The host card shows a small "cloudflared" tag. New strings go into the English
defaults (`i18n.tsx`) and the id/ja/ru/zh locale files.

## Verification

- Go unit test: an `httptest` WebSocket server bridging to the existing
  in-process test SSH server; `Run` and `Ping` succeed over the tunnel
  transport, and the test asserts the `Cf-Access-*` headers arrive when set
  and are absent when empty.
- Go unit test: a non-101 upgrade response yields the readable error.
- Store test: round-trip of the new fields with the secret encrypted at rest.
- Migration tests stay green with v4 in the registry.
- Live smoke check against the user's real tunnel hostname: dial over the new
  transport and read only the server's SSH banner (no credentials, no login).
- Web: typecheck/build, then a look at the dialog in both modes.

## Left to the builder

Exact component layout inside the dialog, wording of hints in each locale, and
test helper structure.
