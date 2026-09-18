import Foundation

public struct InterceptStatus: Decodable, Sendable {
    public var running: Bool
    public var addr: String
    public var exchanges: Int
    public var rules: Int

    enum CodingKeys: String, CodingKey {
        case running, addr, exchanges, rules
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        running = try container.decodeIfPresent(Bool.self, forKey: .running) ?? false
        addr = try container.decodeIfPresent(String.self, forKey: .addr) ?? ""
        exchanges = try container.decodeIfPresent(Int.self, forKey: .exchanges) ?? 0
        rules = try container.decodeIfPresent(Int.self, forKey: .rules) ?? 0
    }
}

public struct InterceptStartRequest: Encodable, Sendable {
    public var port: Int

    public init(port: Int) {
        self.port = port
    }
}

public struct InterceptExchangesResponse: Decodable, Sendable {
    public var exchanges: [InterceptExchange]

    enum CodingKeys: String, CodingKey {
        case exchanges
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        exchanges = try container.decodeIfPresent([InterceptExchange].self, forKey: .exchanges) ?? []
    }
}

public struct InterceptExchange: Decodable, Identifiable, Sendable, Hashable {
    public var id: Int
    public var time: String?
    public var method: String
    public var url: String
    public var host: String?
    public var secure: Bool
    public var reqHeaders: [String: [String]]
    public var reqBody: String
    public var status: Int
    public var respHeaders: [String: [String]]
    public var respBody: String
    public var durationMS: Int
    public var mocked: Bool
    public var blocked: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case time
        case method
        case url
        case host
        case secure
        case reqHeaders = "req_headers"
        case reqBody = "req_body"
        case status
        case respHeaders = "resp_headers"
        case respBody = "resp_body"
        case durationMS = "duration_ms"
        case mocked
        case blocked
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(Int.self, forKey: .id)
        time = try container.decodeIfPresent(String.self, forKey: .time)
        method = try container.decodeIfPresent(String.self, forKey: .method) ?? ""
        url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
        host = try container.decodeIfPresent(String.self, forKey: .host)
        secure = try container.decodeIfPresent(Bool.self, forKey: .secure) ?? false
        reqHeaders = try container.decodeIfPresent([String: [String]].self, forKey: .reqHeaders) ?? [:]
        reqBody = try container.decodeIfPresent(String.self, forKey: .reqBody) ?? ""
        status = try container.decodeIfPresent(Int.self, forKey: .status) ?? 0
        respHeaders = try container.decodeIfPresent([String: [String]].self, forKey: .respHeaders) ?? [:]
        respBody = try container.decodeIfPresent(String.self, forKey: .respBody) ?? ""
        durationMS = try container.decodeIfPresent(Int.self, forKey: .durationMS) ?? 0
        mocked = try container.decodeIfPresent(Bool.self, forKey: .mocked) ?? false
        blocked = try container.decodeIfPresent(Bool.self, forKey: .blocked) ?? false
    }
}

public struct InterceptRulesResponse: Decodable, Sendable {
    public var rules: [InterceptRule]

    enum CodingKeys: String, CodingKey {
        case rules
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        rules = try container.decodeIfPresent([InterceptRule].self, forKey: .rules) ?? []
    }
}

public struct InterceptRule: Decodable, Identifiable, Sendable {
    public var id: Int
    public var match: String
    public var block: Bool
    public var mockStatus: Int
    public var mockBody: String
    public var breakpoint: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case match
        case block
        case mockStatus = "mock_status"
        case mockBody = "mock_body"
        case breakpoint
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(Int.self, forKey: .id)
        match = try container.decodeIfPresent(String.self, forKey: .match) ?? ""
        block = try container.decodeIfPresent(Bool.self, forKey: .block) ?? false
        mockStatus = try container.decodeIfPresent(Int.self, forKey: .mockStatus) ?? 0
        mockBody = try container.decodeIfPresent(String.self, forKey: .mockBody) ?? ""
        breakpoint = try container.decodeIfPresent(Bool.self, forKey: .breakpoint)
    }
}

public struct InterceptRuleRequest: Encodable, Sendable {
    public var match: String
    public var block: Bool
    public var mockStatus: Int
    public var mockBody: String

    public init(match: String, block: Bool, mockStatus: Int, mockBody: String) {
        self.match = match
        self.block = block
        self.mockStatus = mockStatus
        self.mockBody = mockBody
    }

    enum CodingKeys: String, CodingKey {
        case match
        case block
        case mockStatus = "mock_status"
        case mockBody = "mock_body"
    }
}

public struct InterceptorsResponse: Decodable, Sendable {
    public var interceptors: [InterceptorInfo]
    public var sessions: [InterceptorSession]

    enum CodingKeys: String, CodingKey {
        case interceptors, sessions
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        interceptors = try container.decodeIfPresent([InterceptorInfo].self, forKey: .interceptors) ?? []
        sessions = try container.decodeIfPresent([InterceptorSession].self, forKey: .sessions) ?? []
    }
}

public struct InterceptorInfo: Decodable, Identifiable, Sendable {
    public var id: String
    public var label: String
    public var category: String
    public var available: Bool
    public var reason: String?
}

public struct InterceptorSession: Decodable, Identifiable, Sendable {
    public var id: String
    public var interceptor: String
    public var info: [String: FlexibleJSON]?

    public var envText: String? {
        info?["env"]?.stringValue
    }
}

public struct InterceptActivateRequest: Encodable, Sendable {
    public var interceptor: String

    public init(interceptor: String) {
        self.interceptor = interceptor
    }
}

public struct InterceptPausedList: Decodable, Sendable {
    public var paused: [InterceptPaused]

    enum CodingKeys: String, CodingKey {
        case paused
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        paused = try container.decodeIfPresent([InterceptPaused].self, forKey: .paused) ?? []
    }
}

public struct InterceptPaused: Decodable, Identifiable, Sendable, Hashable {
    public var id: Int
    public var method: String
    public var url: String
    public var headers: [String: String]
    public var body: String

    enum CodingKeys: String, CodingKey {
        case id, method, url, headers, body
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(Int.self, forKey: .id)
        method = try container.decodeIfPresent(String.self, forKey: .method) ?? ""
        url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
        headers = try container.decodeIfPresent([String: String].self, forKey: .headers) ?? [:]
        body = try container.decodeIfPresent(String.self, forKey: .body) ?? ""
    }
}

public struct MCPStatusResponse: Decodable, Sendable {
    public var enabled: Bool
    public var servers: [MCPServerStatus]

    enum CodingKeys: String, CodingKey {
        case enabled, servers
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        servers = try container.decodeIfPresent([MCPServerStatus].self, forKey: .servers) ?? []
    }
}

public struct MCPServerStatus: Decodable, Identifiable, Sendable {
    public var name: String
    public var started: Bool
    public var connected: Bool
    public var error: String?
    public var tools: [MCPTool]

    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, started, connected, error, tools
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        started = try container.decodeIfPresent(Bool.self, forKey: .started) ?? false
        connected = try container.decodeIfPresent(Bool.self, forKey: .connected) ?? false
        error = try container.decodeIfPresent(String.self, forKey: .error)
        tools = try container.decodeIfPresent([MCPTool].self, forKey: .tools) ?? []
    }
}

public struct MCPTool: Decodable, Identifiable, Sendable {
    public var name: String
    public var description: String

    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, description
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        description = try container.decodeIfPresent(String.self, forKey: .description) ?? ""
    }
}

public struct AddMCPServerRequest: Encodable, Sendable {
    public var name: String
    public var transport: String
    public var command: String?
    public var args: [String]?
    public var env: [String: String]?
    public var url: String?

    public init(
        name: String,
        transport: String,
        command: String? = nil,
        args: [String]? = nil,
        env: [String: String]? = nil,
        url: String? = nil
    ) {
        self.name = name
        self.transport = transport
        self.command = command
        self.args = args
        self.env = env
        self.url = url
    }

    enum CodingKeys: String, CodingKey {
        case name, transport, command, args, env, url
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(name, forKey: .name)
        try container.encode(transport, forKey: .transport)
        try container.encodeIfPresent(command, forKey: .command)
        try container.encodeIfPresent(args, forKey: .args)
        try container.encodeIfPresent(env, forKey: .env)
        try container.encodeIfPresent(url, forKey: .url)
    }
}

public enum MCPEnvParser {
    public enum Result: Equatable, Sendable {
        case ok([String: String])
        case invalid
    }

    public static func parse(_ text: String) -> Result {
        var env: [String: String] = [:]
        for rawLine in text.split(separator: "\n", omittingEmptySubsequences: false) {
            let trimmed = rawLine.trimmingCharacters(in: .whitespacesAndNewlines)
            if trimmed.isEmpty || trimmed.hasPrefix("#") { continue }
            guard let eq = trimmed.firstIndex(of: "=") else { return .invalid }
            let key = String(trimmed[..<eq]).trimmingCharacters(in: .whitespaces)
            guard key.range(of: "^[A-Za-z_][A-Za-z0-9_]*$", options: .regularExpression) != nil else {
                return .invalid
            }
            env[key] = String(trimmed[trimmed.index(after: eq)...]).trimmingCharacters(in: .whitespaces)
        }
        return .ok(env)
    }
}

public struct HubMCPResponse: Decodable, Sendable {
    public var servers: [HubMCPServer]
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case servers, error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        servers = try container.decodeIfPresent([HubMCPServer].self, forKey: .servers) ?? []
        error = try container.decodeIfPresent(String.self, forKey: .error)
    }
}

public struct HubMCPServer: Decodable, Identifiable, Sendable {
    public var id: String
    public var kind: String?
    public var name: String
    public var summary: String
    public var source: String
    public var tags: [String]?
    public var homepage: String?
    public var author: String?
    public var command: String?
    public var args: [String]?
    public var url: String?
    public var needsKeys: [String]?
    public var setup: String?
    public var installed: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case kind
        case name
        case summary
        case source
        case tags
        case homepage
        case author
        case command
        case args
        case url
        case needsKeys = "needs_keys"
        case setup
        case installed
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        kind = try container.decodeIfPresent(String.self, forKey: .kind)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        summary = try container.decodeIfPresent(String.self, forKey: .summary) ?? ""
        source = try container.decodeIfPresent(String.self, forKey: .source) ?? ""
        tags = try container.decodeIfPresent([String].self, forKey: .tags)
        homepage = try container.decodeIfPresent(String.self, forKey: .homepage)
        author = try container.decodeIfPresent(String.self, forKey: .author)
        command = try container.decodeIfPresent(String.self, forKey: .command)
        args = try container.decodeIfPresent([String].self, forKey: .args)
        url = try container.decodeIfPresent(String.self, forKey: .url)
        needsKeys = try container.decodeIfPresent([String].self, forKey: .needsKeys)
        setup = try container.decodeIfPresent(String.self, forKey: .setup)
        installed = try container.decodeIfPresent(Bool.self, forKey: .installed) ?? false
    }
}

public struct NamedOKResponse: Decodable, Sendable {
    public var ok: Bool
    public var error: String?
    public var name: String?
    public var dir: String?
}

public struct PluginsResponse: Decodable, Sendable {
    public var enabled: Bool
    public var plugins: [PluginInfo]
    public var dirs: [String]

    enum CodingKeys: String, CodingKey {
        case enabled, plugins, dirs
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        plugins = try container.decodeIfPresent([PluginInfo].self, forKey: .plugins) ?? []
        dirs = try container.decodeIfPresent([String].self, forKey: .dirs) ?? []
    }
}

public struct PluginInfo: Decodable, Identifiable, Sendable {
    public var name: String
    public var description: String
    public var version: String
    public var command: String
    public var hooks: [String]
    public var dir: String
    public var enabled: Bool
    public var error: String?

    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name, description, version, command, hooks, dir, enabled, error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        description = try container.decodeIfPresent(String.self, forKey: .description) ?? ""
        version = try container.decodeIfPresent(String.self, forKey: .version) ?? ""
        command = try container.decodeIfPresent(String.self, forKey: .command) ?? ""
        hooks = try container.decodeIfPresent([String].self, forKey: .hooks) ?? []
        dir = try container.decodeIfPresent(String.self, forKey: .dir) ?? ""
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        error = try container.decodeIfPresent(String.self, forKey: .error)
    }
}

public struct AddPluginRequest: Encodable, Sendable {
    public var name: String
    public var description: String
    public var command: String
    public var args: [String]
    public var hooks: [String]

    public init(name: String, description: String, command: String, args: [String], hooks: [String]) {
        self.name = name
        self.description = description
        self.command = command
        self.args = args
        self.hooks = hooks
    }
}

public struct HubPluginsResponse: Decodable, Sendable {
    public var plugins: [HubPlugin]
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case plugins, error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        plugins = try container.decodeIfPresent([HubPlugin].self, forKey: .plugins) ?? []
        error = try container.decodeIfPresent(String.self, forKey: .error)
    }
}

public struct HubPlugin: Decodable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var summary: String
    public var command: String?
    public var hooks: [String]
    public var installed: Bool
    public var author: String?
    public var homepage: String?
    public var tags: [String]?

    enum CodingKeys: String, CodingKey {
        case id, name, summary, command, hooks, installed, author, homepage, tags
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decode(String.self, forKey: .id)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        summary = try container.decodeIfPresent(String.self, forKey: .summary) ?? ""
        command = try container.decodeIfPresent(String.self, forKey: .command)
        hooks = try container.decodeIfPresent([String].self, forKey: .hooks) ?? []
        installed = try container.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        author = try container.decodeIfPresent(String.self, forKey: .author)
        homepage = try container.decodeIfPresent(String.self, forKey: .homepage)
        tags = try container.decodeIfPresent([String].self, forKey: .tags)
    }
}

public struct ProxyListResponse: Decodable, Sendable {
    public var entries: [ProxyEntry]

    enum CodingKeys: String, CodingKey {
        case entries
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        entries = try container.decodeIfPresent([ProxyEntry].self, forKey: .entries) ?? []
    }
}

public struct ProxyEntry: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var label: String
    public var scheme: String
    public var host: String
    public var port: Int
    public var username: String
    public var password: String
    public var url: String

    enum CodingKeys: String, CodingKey {
        case id, label, scheme, host, port, username, password, url
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        label = try container.decodeIfPresent(String.self, forKey: .label) ?? ""
        scheme = try container.decodeIfPresent(String.self, forKey: .scheme) ?? ""
        host = try container.decodeIfPresent(String.self, forKey: .host) ?? ""
        port = try container.decodeIfPresent(Int.self, forKey: .port) ?? 0
        username = try container.decodeIfPresent(String.self, forKey: .username) ?? ""
        password = try container.decodeIfPresent(String.self, forKey: .password) ?? ""
        url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
    }
}

public struct ProxyBatchRequest: Encodable, Sendable {
    public var text: String

    public init(text: String) {
        self.text = text
    }
}

public struct ProxyTestRequest: Encodable, Sendable {
    public var id: String

    public init(id: String) {
        self.id = id
    }
}

public struct ProxyBatchResponse: Decodable, Sendable {
    public var ok: Bool
    public var added: Int
    public var failed: [ProxyBatchFailure]

    enum CodingKeys: String, CodingKey {
        case ok, added, failed
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        ok = try container.decodeIfPresent(Bool.self, forKey: .ok) ?? false
        added = try container.decodeIfPresent(Int.self, forKey: .added) ?? 0
        failed = try container.decodeIfPresent([ProxyBatchFailure].self, forKey: .failed) ?? []
    }
}

public struct ProxyBatchFailure: Decodable, Sendable {
    public var line: Int
    public var text: String
    public var error: String
}

public struct ProxyTestResponse: Decodable, Sendable {
    public var ok: Bool
    public var ip: String?
    public var error: String?

    public init(ok: Bool, ip: String? = nil, error: String? = nil) {
        self.ok = ok
        self.ip = ip
        self.error = error
    }
}

public struct ClearedFlagResponse: Decodable, Sendable {
    public var cleared: Bool?
    public var stopped: Bool?
    public var deleted: Bool?
    public var ok: Bool?
}

func encodedPath(_ value: String) -> String {
    value.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? value
}

extension APIClient {
    public func interceptStatus() async throws -> InterceptStatus {
        try await get("/api/intercept/status")
    }

    public func startIntercept(port: Int) async throws -> InterceptStatus {
        try await post("/api/intercept/start", body: InterceptStartRequest(port: port))
    }

    public func stopIntercept() async throws -> ClearedFlagResponse {
        try await post("/api/intercept/stop", body: EmptyBody())
    }

    public func interceptExchanges() async throws -> InterceptExchangesResponse {
        try await get("/api/intercept/exchanges")
    }

    public func clearIntercept() async throws -> ClearedFlagResponse {
        try await post("/api/intercept/clear", body: EmptyBody())
    }

    public func interceptCA() async throws -> Data {
        try await getData("/api/intercept/ca")
    }

    public func interceptRules() async throws -> InterceptRulesResponse {
        try await get("/api/intercept/rules")
    }

    public func addInterceptRule(_ request: InterceptRuleRequest) async throws -> InterceptRule {
        try await post("/api/intercept/rules", body: request)
    }

    public func deleteInterceptRule(id: Int) async throws -> ClearedFlagResponse {
        try await delete("/api/intercept/rules/\(id)")
    }

    public func interceptors() async throws -> InterceptorsResponse {
        try await get("/api/intercept/interceptors")
    }

    public func activateInterceptor(_ id: String) async throws -> InterceptorSession {
        try await post("/api/intercept/activate", body: InterceptActivateRequest(interceptor: id))
    }

    public func deactivateInterceptorSession(_ id: String) async throws -> ClearedFlagResponse {
        try await delete("/api/intercept/sessions/\(encodedPath(id))")
    }

    public func interceptBreakpoints() async throws -> InterceptPausedList {
        try await get("/api/intercept/breakpoints")
    }

    public func streamInterceptBreakpoints(
        onEvent: @escaping @Sendable (InterceptPausedList) async -> Void
    ) async throws {
        try await streamGetJSON("/api/intercept/breakpoints/stream", onEvent: onEvent)
    }

    public func resumeBreakpoint(id: Int, abort: Bool) async throws -> OKResponse {
        let path = abort
            ? "/api/intercept/breakpoints/\(id)?abort=1"
            : "/api/intercept/breakpoints/\(id)"
        return try await post(path, body: EmptyBody())
    }

    public func mcpStatus() async throws -> MCPStatusResponse {
        try await get("/api/mcp")
    }

    public func refreshMCP() async throws -> MCPStatusResponse {
        try await post("/api/mcp/refresh", body: EmptyBody())
    }

    public func addMCPServer(_ request: AddMCPServerRequest) async throws -> NamedOKResponse {
        try await post("/api/mcp/servers", body: request)
    }

    public func deleteMCPServer(_ name: String) async throws -> NamedOKResponse {
        try await delete("/api/mcp/servers/\(encodedPath(name))")
    }

    public func hubMCP(query: String = "") async throws -> HubMCPResponse {
        try await get("/api/hub/mcp", query: [URLQueryItem(name: "q", value: query)])
    }

    public func installHubMCP(id: String) async throws -> HubInstallResponse {
        try await post("/api/hub/mcp/install", body: HubInstallRequest(id: id))
    }

    public func plugins() async throws -> PluginsResponse {
        try await get("/api/plugins")
    }

    public func addPlugin(_ request: AddPluginRequest) async throws -> NamedOKResponse {
        try await post("/api/plugins", body: request)
    }

    public func togglePlugin(name: String, enabled: Bool) async throws -> OKResponse {
        try await post(
            "/api/plugins/\(encodedPath(name))/toggle",
            body: ToggleEnabledRequest(enabled: enabled)
        )
    }

    public func reloadPlugins() async throws -> NamedOKResponse {
        try await post("/api/plugins/reload", body: EmptyBody())
    }

    public func hubPlugins(query: String = "") async throws -> HubPluginsResponse {
        try await get("/api/hub/plugins", query: [URLQueryItem(name: "q", value: query)])
    }

    public func installHubPlugin(id: String) async throws -> HubInstallResponse {
        try await post("/api/hub/plugins/install", body: HubInstallRequest(id: id))
    }

    public func listProxies() async throws -> ProxyListResponse {
        try await get("/api/proxies")
    }

    public func batchAddProxies(text: String) async throws -> ProxyBatchResponse {
        try await post("/api/proxies/batch", body: ProxyBatchRequest(text: text))
    }

    public func testProxy(id: String) async throws -> ProxyTestResponse {
        try await post("/api/proxies/test", body: ProxyTestRequest(id: id))
    }

    public func deleteProxy(_ id: String) async throws -> NamedOKResponse {
        try await delete("/api/proxies/\(encodedPath(id))")
    }
}
