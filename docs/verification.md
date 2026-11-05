# Verifying the live integrations

Most of Antares is exercised by the normal test suite (`go test ./...`). A few
integrations reach third-party services that need your own credentials — the
cloud LLM providers, the voice endpoints, and the chat gateways. Their logic is
unit-tested (auth signing, URL routing, request/response mapping, payload
parsing), but the last mile — a real call to the real service — can only be run
with real keys. This is how.

## LLM providers

Each provider has a credential-gated smoke test that skips unless its keys are
set, then makes one real request and checks the reply. A pass means auth, URL
routing, and mapping all work end to end.

```bash
# Azure OpenAI
AZURE_OPENAI_ENDPOINT=https://<resource>.openai.azure.com \
AZURE_OPENAI_KEY=… AZURE_OPENAI_DEPLOYMENT=<deployment> \
  go test ./internal/llm -run TestLiveAzure -v

# AWS Bedrock (Claude)
AWS_ACCESS_KEY_ID=… AWS_SECRET_ACCESS_KEY=… AWS_REGION=us-east-1 \
  go test ./internal/llm -run TestLiveBedrock -v

# Google Vertex AI (Gemini)
GOOGLE_APPLICATION_CREDENTIALS=/path/sa.json GOOGLE_CLOUD_PROJECT=<project> \
  go test ./internal/llm -run TestLiveVertex -v

# GitHub Copilot  (token from `antares auth copilot`)
COPILOT_GITHUB_TOKEN=gho_… go test ./internal/llm -run TestLiveCopilot -v

# OpenAI Responses (codex)
OPENAI_API_KEY=sk-… go test ./internal/llm -run TestLiveCodex -v

# Voice: TTS → STT round trip
OPENAI_API_KEY=sk-… go test ./internal/llm -run TestLiveSpeakRoundTrip -v
```

## Chat gateways

Gateways need a running bot and, for the webhook ones, a reachable URL. Verify
each by configuring it, starting `antares`, and sending the bot a message —
it should reply.

| Gateway | What to provide | How it connects |
|---|---|---|
| Telegram | `gateway.telegram.bot_token` | long-poll (no URL) |
| Discord | `gateway.discord.bot_token` | gateway WebSocket |
| Slack | `gateway.slack.app_token` (xapp) + `bot_token` (xoxb) | Socket Mode (no URL) |
| Matrix | `gateway.matrix.homeserver` + `access_token` | /sync long-poll |
| Signal | `gateway.signal.api_url` (a running signal-cli REST daemon) + `number` | poll |
| WhatsApp | `gateway.whatsapp.token` + `phone_number_id` + `verify_token`; point Meta's webhook at the listener | webhook listener |
| Feishu | `gateway.feishu.app_id` + `app_secret`; point the event subscription at the listener | webhook listener |

The Socket-Mode, poll, and long-poll gateways (Slack, Signal, Matrix, Telegram,
Discord) need no public URL. The webhook gateways (WhatsApp, Feishu) run their
own listener — expose it directly or through a reverse proxy, and set the same
verify token on both sides.

## What is deliberately not shipped

The security roles and the skill library cover authorized penetration-testing
methodology, and the technique knowledge ships as searchable skill documents.
Antares does **not** ship runnable offensive tooling — exploit payloads,
credential-theft hooks, post-exploitation implants, or detection-evasion code.
That is a deliberate boundary, not an omission: the knowledge supports authorized
testing; the weapons are not distributed.
