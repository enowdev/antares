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

## Cursor Cloud Agents

Cursor is the one integration where the last mile **costs money**, so it is
verified by metadata only. Nothing in this repository's automated verification
may create, run, follow up on, or cancel a Cursor agent.

This metadata-only smoke test calls Cursor's `/v1/me` and `/v1/models`
endpoints. It does not create an agent or run. Enter the key interactively so
it is not stored in shell history:

```bash
read -rsp 'Cursor API key: ' CURSOR_API_KEY
export CURSOR_API_KEY
go test ./internal/cursor -run TestLiveCursorMetadata -count=1 -v
unset CURSOR_API_KEY
```

To check direct Cursor mode in the dashboard without spending anything, stop at
the approval card:

1. Enable Cursor on the Providers page (it ships disabled) and confirm the page
   reports a resolved key. The key itself is never returned by the API — only a
   `has_key` flag.
2. Open the composer's model search and confirm the live catalogue loads under
   **Cursor Cloud Agents**.
3. Select a model and open its options. The reasoning/effort axis, Context,
   Fast, and any other dimensions must come from that model's real variants.
4. Press Send **once**. The turn must stop at the approval card, and the card
   must show the exact model, every parameter, repository and starting ref, and
   any dirty-worktree or local-only-commit warning.
5. **Deny** it. A denied operation sends no request to Cursor, so this whole
   pass is free. Leaving the card pending is not a substitute for denying it.

Approving at step 5 starts a billable run. Do that only when you actually want
the work done.

## Running the suite hermetically

The credential-gated tests above skip when their variables are absent, so a
machine that happens to export `OPENAI_API_KEY` or `CURSOR_API_KEY` will run
live calls during an ordinary `make check`. Strip them for a hermetic pass:

```bash
env -u CURSOR_API_KEY -u OPENAI_API_KEY -u ANTHROPIC_API_KEY -u GEMINI_API_KEY \
    -u OPENROUTER_API_KEY -u ANTARES_API_KEY -u ANTARES_LIVE_HTTP \
    -u ANTARES_LIVE_EMAILOSINT -u ANTARES_LIVE_PROXY -u ANTARES_STRESS \
    -u AZURE_OPENAI_ENDPOINT -u AZURE_OPENAI_KEY -u AZURE_OPENAI_DEPLOYMENT \
    -u AWS_ACCESS_KEY_ID -u AWS_SECRET_ACCESS_KEY \
    -u GOOGLE_APPLICATION_CREDENTIALS -u VERTEX_SA_JSON \
    -u COPILOT_GITHUB_TOKEN \
  make check
```

## Chat gateways

Gateways need a running bot and, for the webhook ones, a reachable URL. Verify
each by configuring it, starting `antares serve`, and sending the bot a message —
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
