package server

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// routes registers every API endpoint plus the dashboard fallback.
func (s *Server) routes() {
	m := s.mux

	// Health & status
	m.HandleFunc("GET /api/health", s.handleHealth)
	m.HandleFunc("GET /api/status", s.handleStatus)
	m.HandleFunc("GET /api/system/stats", s.handleSystemStats)

	// Chat
	m.HandleFunc("POST /api/chat", s.handleChat)
	m.HandleFunc("POST /api/chat/cursor", s.handleCursorChat)
	m.HandleFunc("POST /api/chat/cursor/cancel", s.handleCursorCancel)
	m.HandleFunc("POST /api/upload", s.handleUpload)
	m.HandleFunc("GET /api/chat/attach", s.handleChatAttach)
	m.HandleFunc("POST /api/chat/interrupt", s.handleInterrupt)

	// Intercept proxy
	m.HandleFunc("GET /api/intercept/status", s.handleInterceptStatus)
	m.HandleFunc("POST /api/intercept/start", s.handleInterceptStart)
	m.HandleFunc("POST /api/intercept/stop", s.handleInterceptStop)
	m.HandleFunc("GET /api/intercept/exchanges", s.handleInterceptExchanges)
	m.HandleFunc("POST /api/intercept/clear", s.handleInterceptClear)
	m.HandleFunc("GET /api/intercept/ca", s.handleInterceptCA)
	m.HandleFunc("GET /api/intercept/rules", s.handleInterceptRules)
	m.HandleFunc("POST /api/intercept/rules", s.handleInterceptRules)
	m.HandleFunc("DELETE /api/intercept/rules/{id}", s.handleInterceptRuleDelete)
	m.HandleFunc("GET /api/intercept/interceptors", s.handleInterceptInterceptors)
	m.HandleFunc("POST /api/intercept/activate", s.handleInterceptActivate)
	m.HandleFunc("DELETE /api/intercept/sessions/{id}", s.handleInterceptDeactivate)
	m.HandleFunc("GET /api/intercept/cert", s.handleInterceptCertInfo)
	m.HandleFunc("GET /api/intercept/breakpoints", s.handleInterceptBreakpoints)
	m.HandleFunc("GET /api/intercept/breakpoints/stream", s.handleInterceptBreakpointStream)
	m.HandleFunc("POST /api/intercept/breakpoints/{id}", s.handleInterceptBreakpointResume)

	// Sessions
	m.HandleFunc("GET /api/sessions", s.handleListSessions)
	m.HandleFunc("GET /api/sessions/search", s.handleSearchSessions)
	m.HandleFunc("GET /api/sessions/empty/count", s.handleEmptyCount)
	m.HandleFunc("POST /api/sessions/empty/delete", s.handleDeleteEmpty)
	m.HandleFunc("POST /api/sessions/delete-all", s.handleDeleteAllSessions)
	m.HandleFunc("POST /api/sessions/prune", s.handlePruneSessions)
	m.HandleFunc("GET /api/sessions/{id}", s.handleGetSession)
	m.HandleFunc("GET /api/sessions/{id}/role", s.handleSessionRole)
	m.HandleFunc("DELETE /api/sessions/{id}", s.handleDeleteSession)
	m.HandleFunc("POST /api/sessions/{id}/title", s.handleRenameSession)
	m.HandleFunc("GET /api/sessions/{id}/bg-activity", s.handleBackgroundActivity)
	m.HandleFunc("GET /api/sessions/{id}/edit-preview", s.handleEditPreview)
	m.HandleFunc("POST /api/sessions/{id}/edit", s.handleEditMessage)

	// Dashboard login (web-only; TUI and gateways are unaffected)
	m.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	m.HandleFunc("POST /api/auth/login", s.handleAuthLogin)
	m.HandleFunc("POST /api/auth/logout", s.handleAuthLogout)
	m.HandleFunc("POST /api/auth/password", s.handleAuthSetPassword)

	// Setup / onboarding
	m.HandleFunc("GET /api/setup/status", s.handleSetupStatus)
	m.HandleFunc("POST /api/setup/test", s.handleSetupTest)
	m.HandleFunc("POST /api/setup/complete", s.handleSetupComplete)

	// Soul — the agent's identity (SOUL.md)
	m.HandleFunc("GET /api/soul", s.handleGetSoul)
	m.HandleFunc("POST /api/soul", s.handleSaveSoul)

	// Update — check for a newer release and run the in-place upgrade.
	m.HandleFunc("GET /api/update/check", s.handleUpdateCheck)
	m.HandleFunc("POST /api/update/run", s.handleUpdateRun)

	// Config
	m.HandleFunc("GET /api/config", s.handleGetConfig)
	m.HandleFunc("POST /api/config", s.handleUpdateConfig)
	m.HandleFunc("GET /api/config/raw", s.handleGetRawConfig)
	m.HandleFunc("POST /api/config/raw", s.handleSaveRawConfig)
	m.HandleFunc("GET /api/config/schema", s.handleConfigSchema)

	// Models & providers
	m.HandleFunc("GET /api/model/options", s.handleModelOptions)
	m.HandleFunc("GET /api/context-window", s.handleContextWindow)
	m.HandleFunc("GET /api/model/list", s.handleModelList)
	m.HandleFunc("GET /api/model/list-all", s.handleModelListAll)
	m.HandleFunc("POST /api/model/set", s.handleModelSet)
	m.HandleFunc("POST /api/providers/{id}/key", s.handleSetProviderKey)
	m.HandleFunc("GET /api/providers/{id}/models", s.handleProviderModels)
	m.HandleFunc("GET /api/providers/{id}/model-info", s.handleProviderModelInfo)
	m.HandleFunc("POST /api/providers/{id}/model", s.handleAddProviderModel)
	m.HandleFunc("DELETE /api/providers/{id}/model/{model}", s.handleDeleteProviderModel)
	m.HandleFunc("POST /api/providers/{id}/settings", s.handleProviderSettings)

	// Tools
	m.HandleFunc("GET /api/tools", s.handleListTools)
	m.HandleFunc("POST /api/tools/toggle", s.handleToggleTool)
	m.HandleFunc("POST /api/tools/toolset", s.handleSetToolset)

	// Memory
	m.HandleFunc("GET /api/memory", s.handleListMemory)
	m.HandleFunc("POST /api/memory", s.handlePutMemory)
	m.HandleFunc("GET /api/memory/search", s.handleSearchMemory)
	m.HandleFunc("DELETE /api/memory/{id}", s.handleDeleteMemory)
	m.HandleFunc("POST /api/memory/reset", s.handleResetMemory)

	// RAG
	m.HandleFunc("GET /api/rag/status", s.handleRagStatus)
	m.HandleFunc("POST /api/rag/index", s.handleRagIndex)
	m.HandleFunc("POST /api/rag/search", s.handleRagSearch)
	m.HandleFunc("DELETE /api/rag/collections/{name}", s.handleRagDelete)

	// Analytics
	m.HandleFunc("GET /api/analytics", s.handleAnalytics)

	// Logs
	m.HandleFunc("GET /api/logs", s.handleLogs)
	m.HandleFunc("GET /api/logs/stream", s.handleLogStream)

	// Files
	m.HandleFunc("GET /api/files", s.handleListFiles)
	m.HandleFunc("GET /api/files/read", s.handleReadFile)
	m.HandleFunc("GET /api/files/raw", s.handleRawFile)
	m.HandleFunc("GET /api/social/image", s.handleSocialImage)

	// Project sessions: browse the machine's directories for the folder picker.
	m.HandleFunc("GET /api/project/browse", s.handleBrowseDirs)
	m.HandleFunc("GET /api/project/complete", s.handleCompleteDir)
	// Project sidebar: environment (.env) viewer/editor and plan viewer.
	m.HandleFunc("GET /api/project/env", s.handleProjectEnv)
	m.HandleFunc("POST /api/project/env", s.handleSaveProjectEnv)
	m.HandleFunc("GET /api/project/plan", s.handleProjectPlan)
	m.HandleFunc("GET /api/project/cursor-repository", s.handleCursorRepository)
	// Project sidebar: git status, runnable scripts, file tree.
	m.HandleFunc("POST /api/project/index-rag", s.handleIndexProject)
	m.HandleFunc("GET /api/project/git", s.handleProjectGit)
	m.HandleFunc("GET /api/project/scripts", s.handleProjectScripts)
	m.HandleFunc("GET /api/project/tree", s.handleProjectTree)

	// Skills
	m.HandleFunc("GET /api/skills", s.handleListSkills)
	m.HandleFunc("POST /api/skills", s.handleSaveSkill)
	m.HandleFunc("POST /api/skills/toggle", s.handleToggleSkill)
	m.HandleFunc("GET /api/skills/library", s.handleSkillLibrary)
	m.HandleFunc("GET /api/skills/{name}", s.handleGetSkill)
	m.HandleFunc("DELETE /api/skills/{name}", s.handleDeleteSkill)

	// Cron
	m.HandleFunc("GET /api/cron/validate", s.handleValidateCron)
	m.HandleFunc("GET /api/cron/jobs", s.handleListCron)
	m.HandleFunc("POST /api/cron/jobs", s.handleCreateCron)
	m.HandleFunc("DELETE /api/cron/jobs/{id}", s.handleDeleteCron)
	m.HandleFunc("POST /api/cron/jobs/{id}/toggle", s.handleToggleCron)
	m.HandleFunc("POST /api/cron/jobs/{id}/run", s.handleRunCron)
	m.HandleFunc("GET /api/cron/jobs/{id}/runs", s.handleCronRuns)
	// OSINT
	m.HandleFunc("POST /api/osint/google/verify", s.handleGoogleVerify)
	m.HandleFunc("POST /api/osint/google/select", s.handleGoogleSelect)

	// Proxies — global proxy store
	m.HandleFunc("GET /api/proxies", s.handleListProxies)
	m.HandleFunc("POST /api/proxies", s.handleAddProxy)
	m.HandleFunc("POST /api/proxies/batch", s.handleBatchAddProxy)
	m.HandleFunc("POST /api/proxies/test", s.handleTestProxy)
	m.HandleFunc("DELETE /api/proxies/{id}", s.handleDeleteProxy)

	// VPS — connect, monitor, and manage servers over SSH
	m.HandleFunc("GET /api/vps", s.handleListVPS)
	m.HandleFunc("POST /api/vps", s.handleSaveVPS)
	m.HandleFunc("POST /api/vps/test", s.handleTestVPS)
	m.HandleFunc("GET /api/vps/{id}/metrics", s.handleVPSMetrics)
	m.HandleFunc("GET /api/vps/{id}/processes", s.handleVPSProcesses)
	m.HandleFunc("DELETE /api/vps/{id}", s.handleDeleteVPS)

	// MCP
	m.HandleFunc("GET /api/mcp", s.handleMCPStatus)
	m.HandleFunc("POST /api/mcp/refresh", s.handleMCPRefresh)
	m.HandleFunc("POST /api/mcp/servers", s.handleAddMCPServer)
	m.HandleFunc("DELETE /api/mcp/servers/{name}", s.handleDeleteMCPServer)

	// Roles and the swarm
	m.HandleFunc("GET /api/roles", s.handleRoles)
	m.HandleFunc("POST /api/roles", s.handleSaveRole)
	m.HandleFunc("GET /api/roles/{name}", s.handleGetRole)
	m.HandleFunc("DELETE /api/roles/{name}", s.handleDeleteRole)
	m.HandleFunc("GET /api/swarm", s.handleSwarm)
	m.HandleFunc("GET /api/swarm/stream", s.handleSwarmStream)
	m.HandleFunc("GET /api/subagent/{id}/attach", s.handleSubAgentAttach)

	// Plugins
	m.HandleFunc("GET /api/plugins", s.handlePlugins)
	m.HandleFunc("POST /api/plugins", s.handleAddPlugin)
	m.HandleFunc("POST /api/plugins/{name}/toggle", s.handleTogglePlugin)
	m.HandleFunc("POST /api/plugins/reload", s.handleReloadPlugins)

	// Approvals for tools that change something.
	m.HandleFunc("GET /api/approvals", s.handleApprovals)
	m.HandleFunc("POST /api/approvals/{id}", s.handleResolveApproval)

	// ask_user pause/resume
	m.HandleFunc("GET /api/asks", s.handleAsks)
	m.HandleFunc("POST /api/asks/{id}", s.handleResolveAsk)

	// Hub: browse and install skills and MCP servers.
	m.HandleFunc("GET /api/hub/skills", s.handleHubSkills)
	m.HandleFunc("POST /api/hub/skills/install", s.handleHubInstallSkill)
	m.HandleFunc("GET /api/hub/mcp", s.handleHubMCP)
	m.HandleFunc("POST /api/hub/mcp/install", s.handleHubInstallMCP)
	m.HandleFunc("GET /api/hub/plugins", s.handleHubPlugins)
	m.HandleFunc("POST /api/hub/plugins/install", s.handleHubInstallPlugin)

	// Slash commands, shared with the TUI and the messaging gateways.
	m.HandleFunc("GET /api/commands", s.handleCommandList)
	m.HandleFunc("POST /api/commands/run", s.handleCommandRun)

	// Channels & pairing
	m.HandleFunc("GET /api/channels", s.handleListChannels)
	m.HandleFunc("POST /api/channels/{id}/toggle", s.handleToggleChannel)
	m.HandleFunc("POST /api/channels/{id}/token", s.handleSetChannelToken)
	m.HandleFunc("POST /api/channels/{id}/config", s.handleSetChannelConfig)
	// Per-channel routing bindings + Discord discovery.
	m.HandleFunc("GET /api/channels/discord/guilds", s.handleDiscordGuilds)
	m.HandleFunc("GET /api/channels/discord/guilds/{id}/channels", s.handleDiscordChannels)
	m.HandleFunc("GET /api/channels/discord/guilds/{id}/roles", s.handleDiscordRoles)
	m.HandleFunc("GET /api/channels/bindings", s.handleListBindings)
	m.HandleFunc("POST /api/channels/bindings", s.handleSaveBinding)
	m.HandleFunc("DELETE /api/channels/bindings/{id}", s.handleDeleteBinding)
	m.HandleFunc("POST /api/channels/{id}/commands/{action}", s.handleChannelCommands)
	m.HandleFunc("POST /api/channels/{id}/style", s.handleSetChannelStyle)

	m.HandleFunc("GET /api/autopilot", s.handleAutopilotList)
	m.HandleFunc("POST /api/autopilot", s.handleAutopilotAdd)
	m.HandleFunc("POST /api/autopilot/run", s.handleAutopilotRun)
	m.HandleFunc("DELETE /api/autopilot/{id}", s.handleAutopilotRemove)

	m.HandleFunc("GET /api/engagement/sessions", s.handleEngagementSessions)
	m.HandleFunc("GET /api/engagement", s.handleEngagement)
	m.HandleFunc("GET /api/engagement/report", s.handleEngagementReport)
	m.HandleFunc("DELETE /api/engagement/{session}", s.handleEngagementDelete)
	m.HandleFunc("GET /api/board/sessions", s.handleBoardSessions)
	m.HandleFunc("GET /api/board", s.handleBoard)
	m.HandleFunc("GET /api/board/stream", s.handleBoardStream)
	m.HandleFunc("DELETE /api/board/card", s.handleBoardRemoveCard)
	m.HandleFunc("DELETE /api/board", s.handleBoardClear)
	m.HandleFunc("POST /api/pairing/approve", s.handleApprovePairing)
	m.HandleFunc("POST /api/pairing/revoke", s.handleRevokePairing)

	// Social Media
	m.HandleFunc("GET /api/social/status", s.handleSocialStatus)
	m.HandleFunc("POST /api/social/imap/test", s.handleSocialIMAPTest)
	m.HandleFunc("POST /api/social/imap/save", s.handleSocialIMAPSave)
	m.HandleFunc("POST /api/social/browser/start", s.handleSocialBrowserStart)
	m.HandleFunc("POST /api/social/browser/stop", s.handleSocialBrowserStop)
	m.HandleFunc("POST /api/social/browser/open", s.handleSocialBrowserOpen)
	m.HandleFunc("POST /api/social/autopilot", s.handleSocialAutopilot)
	m.HandleFunc("POST /api/social/encryption/setup", s.handleSocialEncryptionSetup)
	m.HandleFunc("GET /api/social/accounts", s.handleSocialListAccounts)
	m.HandleFunc("POST /api/social/accounts", s.handleSocialAddAccount)
	m.HandleFunc("DELETE /api/social/accounts/{id}", s.handleSocialDeleteAccount)

	// Dashboard (single-page app) — registered last so /api wins.
	m.HandleFunc("/", s.handleDashboard)
}

// handleDashboard serves the embedded SPA, falling back to index.html so client
// routes deep-link correctly.
func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if s.distFS == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(devPlaceholder))
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	// Client-side routes have no matching file; serve the SPA entry instead.
	info, err := fs.Stat(s.distFS, path)
	if err != nil || info.IsDir() {
		path = "index.html"
	}

	// Hashed assets are immutable; the entry document must never be cached, or
	// a deploy leaves clients pinned to a stale bundle.
	if strings.HasPrefix(path, "assets/") {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}

	// Serving the bytes directly avoids http.FileServer's index.html redirect,
	// which would bounce every SPA route back to "./".
	data, err := fs.ReadFile(s.distFS, path)
	if err != nil {
		writeError(w, http.StatusNotFound, errNotFound)
		return
	}
	if ctype := mime.TypeByExtension(filepath.Ext(path)); ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	modTime := time.Time{}
	if info != nil {
		modTime = info.ModTime()
	}
	http.ServeContent(w, r, path, modTime, bytes.NewReader(data))
}

const devPlaceholder = `<!doctype html>
<meta charset="utf-8">
<title>Antares API</title>
<style>
 body{font-family:ui-sans-serif,system-ui,sans-serif;background:#161011;color:#eee;
      display:grid;place-items:center;min-height:100vh;margin:0;text-align:center;padding:2rem}
 code{background:#242427;padding:.15rem .4rem;border-radius:.3rem;font-size:.9em}
 a{color:#e05a4e}
</style>
<div>
  <img src="/antares-192.png" alt="" width="72" height="72" style="opacity:.9">
  <h1>Antares API</h1>
  <p>The backend is running. No dashboard is embedded in this binary.</p>
  <p>Development: run <code>make dev-web</code> and open port <code>5173</code>.</p>
  <p>Production: <code>make build</code> embeds the dashboard into the binary.</p>
</div>`
