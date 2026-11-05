# Deployment

One binary with the dashboard inside it. Copy it and run it.

## Build

```bash
make build          # → bin/antares
```

Cross-compile the usual way:

```bash
GOOS=linux GOARCH=amd64 make build
GOOS=darwin GOARCH=arm64 make build
```

The dashboard is embedded, so the binary is the whole deployment.

## systemd

```ini
# /etc/systemd/system/antares.service
[Unit]
Description=Antares
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=antares
Environment=ANTARES_HOME=/var/lib/antares
ExecStart=/opt/antares/antares --foreground
Restart=on-failure
RestartSec=5

NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/var/lib/antares

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now antares
journalctl -u antares -f
```

`ProtectSystem=strict` with a single `ReadWritePaths` is worth keeping. The
agent has a shell; the hardening is what bounds what that shell can reach.

## Docker

```dockerfile
FROM golang:1.26 AS build
WORKDIR /src
COPY . .
RUN make build

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates chromium && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/bin/antares /usr/local/bin/antares
ENV ANTARES_HOME=/data \
    TOOLS_BROWSER_EXECUTABLE=/usr/bin/chromium
VOLUME /data
EXPOSE 8787
CMD ["antares", "--foreground"]
```

Chromium is only needed for the browser tool. Leave it out and everything else
works.

```bash
docker run -d --name antares \
  -p 8787:8787 \
  -v antares-data:/data \
  -e ANTHROPIC_API_KEY=… \
  antares
```

## Behind a reverse proxy

```nginx
location / {
    proxy_pass http://127.0.0.1:8787;
    proxy_http_version 1.1;
    proxy_set_header Upgrade $http_upgrade;
    proxy_set_header Connection "upgrade";
    proxy_set_header Host $host;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;

    # Streaming replies must not be buffered.
    proxy_buffering off;
    proxy_read_timeout 3600s;
}
```

```yaml
server:
  host: 127.0.0.1
  trust_proxy: true
  public_url: https://antares.example.com
```

`proxy_buffering off` matters: with it on, a streamed reply arrives in one lump
at the end.

## Securing it

**Set a token.** Anything reachable beyond a private network needs one.

```bash
antares config set server.auth_token "$(openssl rand -hex 24)"
```

**Bind to loopback** when a proxy is in front.

**Keep the workspace small.** `agent.workspace` bounds the file tools. Point it
at what the agent should reach, not at `/`.

**Consider the toolset.** `tools.toolset: research` removes the shell entirely.
`tools.approval_mode: prompt` makes mutating tools ask first.

**Remember the browser holds logins.** The profile in `~/.antares/browser`
persists sessions. Treat that directory as credentials.

## Postgres

```yaml
database:
  driver: postgres
  dsn: postgres://antares:pass@db:5432/antares?sslmode=require
  max_conns: 10
```

The schema is created on first run. Back up the database and `config.yaml` and
you have everything — sessions, memory, vectors, and schedules all live there.

## Upgrading

```bash
systemctl stop antares
cp antares /opt/antares/antares
systemctl start antares
```

Migrations run at startup and are additive. Take a database backup first
regardless.

## Watching it

`GET /api/health` needs no credential and is the right liveness check.

`GET /api/status` needs one and reports version, uptime, model, and counts.

Logs go to stdout by default:

```yaml
logging:
  level: info
  json: true      # for a log shipper
  file: ""        # or a path
```
