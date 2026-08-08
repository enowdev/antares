package config

import "path/filepath"

// Default returns a fully populated configuration. Every field the dashboard
// can edit has a sensible value here so the UI never renders a blank form.
func Default() *Config {
	return &Config{
		Model: Model{
			Default:          "anthropic/claude-sonnet-4.5",
			Provider:         "openrouter",
			Temperature:      0.7,
			TopP:             1.0,
			MaxTokens:        8192,
			ContextWindow:    200000,
			ReasoningEffort:  "medium",
			ParallelToolCall: true,
		},
		Providers: map[string]Provider{
			"openrouter": {
				Kind: "openai-compatible", Label: "OpenRouter", Enabled: true,
				BaseURL: "https://openrouter.ai/api/v1", APIKeyEnv: "OPENROUTER_API_KEY", TimeoutSecs: 300,
			},
			"openai": {
				Kind: "openai", Label: "OpenAI", Enabled: true,
				BaseURL: "https://api.openai.com/v1", APIKeyEnv: "OPENAI_API_KEY", TimeoutSecs: 300,
			},
			"anthropic": {
				Kind: "anthropic", Label: "Anthropic", Enabled: true,
				BaseURL: "https://api.anthropic.com/v1", APIKeyEnv: "ANTHROPIC_API_KEY", TimeoutSecs: 300,
			},
			"gemini": {
				Kind: "gemini", Label: "Google Gemini", Enabled: true,
				BaseURL: "https://generativelanguage.googleapis.com/v1beta", APIKeyEnv: "GEMINI_API_KEY", TimeoutSecs: 300,
			},
			"ollama": {
				Kind: "openai-compatible", Label: "Ollama", Enabled: false,
				BaseURL: "http://127.0.0.1:11434/v1", TimeoutSecs: 600,
			},
			"lmstudio": {
				Kind: "openai-compatible", Label: "LM Studio", Enabled: false,
				BaseURL: "http://127.0.0.1:1234/v1", TimeoutSecs: 600,
			},
			"custom": {
				Kind: "custom", Label: "Custom endpoint", Enabled: false, TimeoutSecs: 300,
			},
		},
		Database: Database{
			Driver: "sqlite", DSN: filepath.Join(Home(), "antares.db"),
			MaxConns: 8, Busy: 5000, WAL: true,
		},
		Server: Server{
			Host: "0.0.0.0", Port: 8787,
			// Same-origin is the safe default. Cross-origin access must be
			// explicitly configured by the operator.
			CORSOrigins: []string{},
		},
		Agent: Agent{
			MaxTurns: 200, MaxToolCalls: 32, ReasoningEffort: "medium",
			Personality: "default", Workspace: "~/antares-workspace",
			Timezone: "Local", Language: "auto", IdleTimeoutSecs: 900,
			RepeatLimit: 3, VerifyReplies: false, VerifyMax: 2, GoalMaxIterations: 10,
			WrapUntrustedOutput: true, SmartTitles: true,
		},
		Tools: Tools{
			Toolset: "default", ApprovalMode: "auto", MaxOutputChars: 60000,
			Timeouts:  map[string]int{"terminal": 300, "web_fetch": 60, "web_search": 30},
			WebSearch: WebSearch{Provider: "browser", MaxResults: 8},
			Browser: Browser{
				Enabled: true, Width: 1280, Height: 800,
				UserDataDir: "~/.antares/browser",
				// Stealth on by default: the patched Chromium passes
				// bot-detection challenges, and it is fetched and verified once
				// on first browser use.
				Stealth: true,
			},
			HTTP: HTTP{Preset: "chrome-131", WrapTerminal: true},
			Platform: map[string]string{
				"cli": "default", "web": "default", "telegram": "default", "discord": "default",
			},
		},
		Terminal: Terminal{
			Backend: "local", CWD: "~/antares-workspace", Timeout: 300,
			HomeMode: "workspace", LifetimeSeconds: 3600, Shell: "",
			Sandbox:         "none",
			BlockedCommands: []string{"rm -rf /", "mkfs", ":(){:|:&};:", "shutdown", "reboot"},
			AllowNetwork:    true, DockerImage: "debian:bookworm-slim",
		},
		Compression: Compression{
			Enabled: true, ProgressNotices: true, Threshold: 0.8, TargetRatio: 0.5,
			ProtectLastN: 6, ProtectFirstN: 2, MinTailUserMessages: 2, MaxAttempts: 3,
			IdleCompactAfterSecs: 1800, ProactivePruneTokens: 4000, ProactivePruneMinChars: 4000,
		},
		PromptCaching: PromptCaching{Enabled: true, CacheTTL: "5m"},
		Memory: Memory{
			Enabled: true, UserProfileEnabled: true,
			MemoryCharLimit: 12000, UserCharLimit: 6000,
			NudgeInterval: 20, FlushMinTurns: 4, SearchLimit: 20,
		},
		RAG: RAG{
			Enabled:       false,
			EmbedProvider: "openai", EmbedModel: "text-embedding-3-small",
			ChunkSize: 1200, ChunkOverlap: 200, TopK: 8, Recall: 40, Hybrid: true,
			RerankMode: "llm", Compress: true, AutoContext: true,
		},
		Plugins: Plugins{
			Enabled: true, Dirs: []string{"~/.antares/plugins"},
		},
		Roles:    Roles{Dirs: []string{"~/.antares/roles"}},
		ImageGen: ImageGen{Enabled: false, Model: "gpt-image-1", Size: "1024x1024"},
		Skills: Skills{
			Enabled: true, Dirs: []string{"~/.antares/skills"},
			CreationNudgeInterval: 25, AutoCreate: true,
		},
		Cron: Cron{Enabled: true, Timezone: "Local", MaxConcurrent: 3, HistoryLimit: 200},
		// Discord.Intents left 0 so the adapter applies its own DefaultIntents
		// (guild messages + message content + direct messages). A hardcoded value
		// here once dropped DIRECT_MESSAGES and broke DMs.
		Gateway:       Gateway{Enabled: false, Telegram: Telegram{RequirePairing: true, StreamEdits: true}, Discord: Discord{RequirePairing: true}},
		Delegation:    Delegation{Enabled: true, MaxIterations: 40, MaxDepth: 2, MaxParallel: 4},
		CodeExecution: CodeExecution{Enabled: true, Timeout: 120, MaxToolCalls: 50},
		Guardrails:    Guardrails{WarningsEnabled: true, HardStopEnabled: true, WarnAfter: 25, HardStopAfter: 60},
		SessionReset:  SessionReset{Mode: "never", IdleMinutes: 180, AtHour: 4},
		Streaming:     Streaming{Enabled: true},
		Display: Display{
			ToolProgress: true, ShowReasoning: true,
			MaxLiveReasoningChars: 48_000,
			Theme: "system", Skin: "antares", Language: "auto", InterimAssistant: true,
		},
		Logging: Logging{Level: "info", File: filepath.Join(Home(), "logs", "antares.log")},
		MCP:     MCP{Enabled: true, Servers: map[string]MCPServer{}},

		MaxConcurrentSessions: 0,
		GroupSessionsPerUser:  true,
		Social: Social{
			Enabled:  false,
			IMAPHost: "imap.gmail.com", IMAPPort: 993,
		},
	}
}
