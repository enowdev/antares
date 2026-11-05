import Foundation

public struct SessionListResponse: Decodable, Sendable {
    public var sessions: [SessionSummary]
    public var total: Int
}

public struct SessionSearchResponse: Decodable, Sendable {
    public var hits: [SessionSearchHit]
}

public struct SessionSearchHit: Decodable, Identifiable, Sendable {
    public var sessionID: String
    public var sessionTitle: String
    public var messageID: String
    public var role: String
    public var snippet: String
    public var createdAt: String
    public var score: Double?

    public var id: String { "\(sessionID):\(messageID)" }

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case sessionTitle = "session_title"
        case messageID = "message_id"
        case role
        case snippet
        case createdAt = "created_at"
        case score
    }
}

public struct DeletedFlagResponse: Decodable, Sendable {
    public var deleted: Bool
}

public struct ActiveModel: Decodable, Sendable {
    public var model: String
    public var provider: String

    public init(model: String, provider: String) {
        self.model = model
        self.provider = provider
    }
}

public struct ModelOptionsResponse: Decodable, Sendable {
    public var active: ActiveModel
    public var providers: [ModelProvider]

    public init(active: ActiveModel, providers: [ModelProvider]) {
        self.active = active
        self.providers = providers
    }
}

public struct ModelProvider: Decodable, Identifiable, Sendable {
    public var id: String
    public var label: String
    public var kind: String
    public var enabled: Bool
    public var hasKey: Bool
    public var local: Bool
    public var baseURL: String
    public var active: Bool
    public var hint: String?
    public var keyHint: String?
    public var keyURL: String?
    public var keyLabel: String?
    public var note: String?
    public var needsRegion: Bool?
    public var needsAPIVersion: Bool?
    public var needsBaseURL: Bool?
    public var timeoutSeconds: Int?
    public var headers: [String: String]?
    public var custom: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case kind
        case enabled
        case hasKey = "has_key"
        case local
        case baseURL = "base_url"
        case active
        case hint
        case keyHint = "key_hint"
        case keyURL = "key_url"
        case keyLabel = "key_label"
        case note
        case needsRegion = "needs_region"
        case needsAPIVersion = "needs_api_version"
        case needsBaseURL = "needs_base_url"
        case timeoutSeconds = "timeout_seconds"
        case headers
        case custom
    }
}

public struct ModelListAllResponse: Decodable, Sendable {
    public var active: ActiveModel
    public var models: [ProviderModel]
    public var errors: [ProviderModelError]
}

public struct ProviderModel: Decodable, Sendable {
    public var id: String
    public var name: String
    public var provider: String
    public var providerLabel: String
    public var contextWindow: Int
    public var maxOutput: Int
    public var inputCost: Double
    public var outputCost: Double
    public var vision: Bool
    public var tools: Bool
    public var reasoning: Bool

    public var stableID: String { "\(provider)/\(id)" }

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case provider
        case providerLabel = "provider_label"
        case contextWindow = "context_window"
        case maxOutput = "max_output"
        case inputCost = "input_cost"
        case outputCost = "output_cost"
        case vision
        case tools
        case reasoning
    }
}

public struct ProviderModelError: Decodable, Sendable {
    public var provider: String
    public var label: String
    public var error: String
}

public struct ModelSetRequest: Encodable, Sendable {
    public var model: String
    public var provider: String

    public init(model: String, provider: String) {
        self.model = model
        self.provider = provider
    }
}

public struct ModelSetResponse: Decodable, Sendable {
    public var model: String
    public var provider: String
}

public struct ToolsResponse: Decodable, Sendable {
    public var toolset: String
    public var toolsets: [String]
    public var tools: [ToolInfo]
}

public struct ToolInfo: Decodable, Identifiable, Sendable {
    public var name: String
    public var description: String
    public var enabled: Bool
    public var requiresApproval: Bool
    public var toolsets: [String]

    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case description
        case enabled
        case requiresApproval = "requires_approval"
        case toolsets
    }
}

public struct ToggleToolRequest: Encodable, Sendable {
    public var name: String
    public var enabled: Bool

    public init(name: String, enabled: Bool) {
        self.name = name
        self.enabled = enabled
    }
}

public struct OKResponse: Decodable, Sendable {
    public var ok: Bool
}

public struct MemoryListResponse: Decodable, Sendable {
    public var memories: [MemoryItem]
}

public struct MemoryItem: Decodable, Identifiable, Sendable {
    public var id: String
    public var scope: String
    public var scopeKey: String
    public var key: String
    public var content: String
    public var tags: String
    public var source: String
    public var pinned: Bool
    public var createdAt: String
    public var updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case scope
        case scopeKey = "scope_key"
        case key
        case content
        case tags
        case source
        case pinned
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

public struct PutMemoryRequest: Encodable, Sendable {
    public var id: String
    public var key: String
    public var content: String
    public var scope: String
    public var pinned: Bool?

    public init(id: String = "", key: String, content: String, scope: String, pinned: Bool? = nil) {
        self.id = id
        self.key = key
        self.content = content
        self.scope = scope
        self.pinned = pinned
    }
}

extension APIClient {
    public func listSessions(limit: Int = 100, query: String = "") async throws -> SessionListResponse {
        var items = [URLQueryItem(name: "limit", value: String(limit))]
        if !query.isEmpty {
            items.append(URLQueryItem(name: "q", value: query))
        }
        return try await get("/api/sessions", query: items)
    }

    public func searchSessions(query: String, limit: Int = 30) async throws -> SessionSearchResponse {
        try await get("/api/sessions/search", query: [
            URLQueryItem(name: "q", value: query),
            URLQueryItem(name: "limit", value: String(limit)),
        ])
    }

    public func deleteSession(_ id: String) async throws -> DeletedFlagResponse {
        try await delete("/api/sessions/\(id)")
    }

    public func modelOptions() async throws -> ModelOptionsResponse {
        try await get("/api/model/options")
    }

    public func modelListAll() async throws -> ModelListAllResponse {
        try await get("/api/model/list-all")
    }

    public func setModel(_ model: String, provider: String) async throws -> ModelSetResponse {
        try await post("/api/model/set", body: ModelSetRequest(model: model, provider: provider))
    }

    public func listTools() async throws -> ToolsResponse {
        try await get("/api/tools")
    }

    public func toggleTool(name: String, enabled: Bool) async throws -> OKResponse {
        try await post("/api/tools/toggle", body: ToggleToolRequest(name: name, enabled: enabled))
    }

    public func listMemory() async throws -> MemoryListResponse {
        try await get("/api/memory")
    }

    public func searchMemory(query: String, limit: Int = 30) async throws -> MemoryListResponse {
        try await get("/api/memory/search", query: [
            URLQueryItem(name: "q", value: query),
            URLQueryItem(name: "limit", value: String(limit)),
        ])
    }

    public func putMemory(_ request: PutMemoryRequest) async throws -> MemoryItem {
        try await post("/api/memory", body: request)
    }

    public func deleteMemory(_ id: String) async throws -> DeletedFlagResponse {
        try await delete("/api/memory/\(id)")
    }
}
