import Foundation

public struct RolesResponse: Decodable, Sendable {
    public var roles: [RoleInfo]
    public var scope: [String]?
    public var active: [ActiveAgentInfo]?
    public var performance: [RolePerformance]?
    public var toolsets: [String]?
    public var categories: [String]?

    enum CodingKeys: String, CodingKey {
        case roles, scope, active, performance, toolsets, categories
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        roles = try container.decodeIfPresent([RoleInfo].self, forKey: .roles) ?? []
        scope = try container.decodeIfPresent([String].self, forKey: .scope)
        active = try container.decodeIfPresent([ActiveAgentInfo].self, forKey: .active)
        performance = try container.decodeIfPresent([RolePerformance].self, forKey: .performance)
        toolsets = try container.decodeIfPresent([String].self, forKey: .toolsets)
        categories = try container.decodeIfPresent([String].self, forKey: .categories)
    }
}

public struct RoleInfo: Decodable, Identifiable, Sendable, Hashable {
    public var name: String
    public var title: String
    public var summary: String
    public var category: String
    public var toolset: String
    public var model: String
    public var effort: String?
    public var maxTurns: Int?
    public var tags: [String]?
    public var danger: Bool?
    public var subrole: Bool?
    public var parent: String?
    public var source: String

    public var id: String { name }
    public var isDanger: Bool { danger ?? false }
    public var isSubrole: Bool { subrole ?? false }

    enum CodingKeys: String, CodingKey {
        case name
        case title
        case summary
        case category
        case toolset
        case model
        case effort
        case maxTurns = "max_turns"
        case tags
        case danger
        case subrole
        case parent
        case source
    }
}

public struct ActiveAgentInfo: Decodable, Identifiable, Sendable {
    public var id: String
    public var role: String
    public var task: String
    public var parent: String?
    public var startedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case role
        case task
        case parent
        case startedAt = "started_at"
    }
}

public struct RolePerformance: Decodable, Identifiable, Sendable {
    public var role: String
    public var missions: Int
    public var successes: Int
    public var failures: Int?
    public var kept: Int
    public var totalTurns: Int?
    public var score: Double
    public var updatedAt: String?

    public var id: String { role }

    enum CodingKeys: String, CodingKey {
        case role
        case missions
        case successes
        case failures
        case kept
        case totalTurns = "total_turns"
        case score
        case updatedAt = "updated_at"
    }
}

public struct SessionRoleResponse: Decodable, Sendable {
    public var role: String
}

public struct SoulResponse: Decodable, Sendable {
    public var soul: String
    public var unset: Bool
}

public struct SoulRequest: Encodable, Sendable {
    public var soul: String

    public init(soul: String) {
        self.soul = soul
    }
}

public struct SoulSaveResponse: Decodable, Sendable {
    public var ok: Bool
    public var unset: Bool
}

public struct SkillsResponse: Decodable, Sendable {
    public var skills: [SkillInfo]
    public var library: Int?
    public var searching: Bool?

    enum CodingKeys: String, CodingKey {
        case skills, library, searching
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        skills = try container.decodeIfPresent([SkillInfo].self, forKey: .skills) ?? []
        library = try container.decodeIfPresent(Int.self, forKey: .library)
        searching = try container.decodeIfPresent(Bool.self, forKey: .searching)
    }
}

public struct SkillInfo: Decodable, Identifiable, Sendable {
    public var name: String
    public var description: String
    public var path: String
    public var enabled: Bool
    public var source: String
    public var tags: [String]?
    public var triggers: [String]?
    public var updatedAt: String
    public var usageCount: Int

    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case description
        case path
        case enabled
        case source
        case tags
        case triggers
        case updatedAt = "updated_at"
        case usageCount = "usage_count"
    }
}

public struct HubSkillsResponse: Decodable, Sendable {
    public var skills: [HubSkill]
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case skills, error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        skills = try container.decodeIfPresent([HubSkill].self, forKey: .skills) ?? []
        error = try container.decodeIfPresent(String.self, forKey: .error)
    }
}

public struct HubSkill: Decodable, Identifiable, Sendable {
    public var id: String
    public var kind: String
    public var name: String
    public var summary: String
    public var source: String
    public var tags: [String]?
    public var homepage: String?
    public var author: String?
    public var installed: Bool
}

public struct HubInstallRequest: Encodable, Sendable {
    public var id: String

    public init(id: String) {
        self.id = id
    }
}

public struct HubInstallResponse: Decodable, Sendable {
    public var ok: Bool
    public var error: String?
    public var name: String?
    public var path: String?
    public var summary: String?
}

public struct CronJobsResponse: Decodable, Sendable {
    public var jobs: [CronJob]

    enum CodingKeys: String, CodingKey {
        case jobs
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        jobs = try container.decodeIfPresent([CronJob].self, forKey: .jobs) ?? []
    }
}

public struct CronJob: Decodable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var schedule: String
    public var prompt: String
    public var enabled: Bool
    public var target: String
    public var timezone: String?
    public var lastRun: String?
    public var nextRun: String?
    public var lastState: String
    public var createdAt: String?
    public var updatedAt: String?
    public var meta: [String: FlexibleJSON]?

    enum CodingKeys: String, CodingKey {
        case id
        case name
        case schedule
        case prompt
        case enabled
        case target
        case timezone
        case lastRun = "last_run"
        case nextRun = "next_run"
        case lastState = "last_state"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case meta
    }
}

public struct CreateCronRequest: Encodable, Sendable {
    public var name: String
    public var schedule: String
    public var prompt: String

    public init(name: String, schedule: String, prompt: String) {
        self.name = name
        self.schedule = schedule
        self.prompt = prompt
    }
}

public struct ToggleEnabledRequest: Encodable, Sendable {
    public var enabled: Bool

    public init(enabled: Bool) {
        self.enabled = enabled
    }
}

public struct StartedFlagResponse: Decodable, Sendable {
    public var started: Bool
}

public struct ChannelsResponse: Decodable, Sendable {
    public var channels: [ChannelInfo]
    public var pairings: [PairingInfo]

    enum CodingKeys: String, CodingKey {
        case channels, pairings
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        channels = try container.decodeIfPresent([ChannelInfo].self, forKey: .channels) ?? []
        pairings = try container.decodeIfPresent([PairingInfo].self, forKey: .pairings) ?? []
    }
}

public struct ChannelInfo: Decodable, Identifiable, Sendable {
    public var id: String
    public var label: String
    public var enabled: Bool
    public var connected: Bool
    public var configured: Bool
    public var hasToken: Bool?
    public var botName: String?
    public var replyStyle: String?
    public var detail: String
    public var docs: String?
    public var fields: [ChannelField]

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case enabled
        case connected
        case configured
        case hasToken = "has_token"
        case botName = "bot_name"
        case replyStyle = "reply_style"
        case detail
        case docs
        case fields
    }
}

public struct ChannelField: Decodable, Identifiable, Sendable {
    public var key: String
    public var label: String
    public var secret: Bool
    public var placeholder: String?
    public var set: Bool

    public var id: String { key }
}

public struct PairingInfo: Decodable, Identifiable, Sendable {
    public var id: String
    public var platform: String
    public var externalID: String
    public var displayName: String
    public var status: String
    public var code: String?
    public var createdAt: String
    public var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case platform
        case externalID = "external_id"
        case displayName = "display_name"
        case status
        case code
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

public struct ChannelTokenRequest: Encodable, Sendable {
    public var token: String

    public init(token: String) {
        self.token = token
    }
}

public struct ChannelConfigRequest: Encodable, Sendable {
    public var fields: [String: String]
    public var enabled: Bool?

    public init(fields: [String: String], enabled: Bool? = nil) {
        self.fields = fields
        self.enabled = enabled
    }

    enum CodingKeys: String, CodingKey {
        case fields
        case enabled
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(fields, forKey: .fields)
        try container.encodeIfPresent(enabled, forKey: .enabled)
    }
}

public struct ChannelMutationResponse: Decodable, Sendable {
    public var ok: Bool
    public var error: String?
    public var restartRequired: Bool?

    enum CodingKeys: String, CodingKey {
        case ok
        case error
        case restartRequired = "restart_required"
    }
}

public struct PairingIDRequest: Encodable, Sendable {
    public var id: String

    public init(id: String) {
        self.id = id
    }
}

extension APIClient {
    public func listRoles() async throws -> RolesResponse {
        try await get("/api/roles")
    }

    public func sessionRole(_ sessionID: String) async throws -> SessionRoleResponse {
        try await get("/api/sessions/\(sessionID)/role")
    }

    public func getSoul() async throws -> SoulResponse {
        try await get("/api/soul")
    }

    public func saveSoul(_ soul: String) async throws -> SoulSaveResponse {
        try await post("/api/soul", body: SoulRequest(soul: soul))
    }

    public func listSkills(query: String = "") async throws -> SkillsResponse {
        if query.isEmpty {
            return try await get("/api/skills")
        }
        return try await get("/api/skills", query: [URLQueryItem(name: "q", value: query)])
    }

    public func toggleSkill(name: String, enabled: Bool) async throws -> OKResponse {
        try await post("/api/skills/toggle", body: ToggleToolRequest(name: name, enabled: enabled))
    }

    public func hubSkills(query: String = "") async throws -> HubSkillsResponse {
        try await get("/api/hub/skills", query: [URLQueryItem(name: "q", value: query)])
    }

    public func installHubSkill(id: String) async throws -> HubInstallResponse {
        try await post("/api/hub/skills/install", body: HubInstallRequest(id: id))
    }

    public func listCronJobs() async throws -> CronJobsResponse {
        try await get("/api/cron/jobs")
    }

    public func createCronJob(_ request: CreateCronRequest) async throws -> CronJob {
        try await post("/api/cron/jobs", body: request)
    }

    public func toggleCronJob(id: String, enabled: Bool) async throws -> CronJob {
        try await post("/api/cron/jobs/\(id)/toggle", body: ToggleEnabledRequest(enabled: enabled))
    }

    public func runCronJob(id: String) async throws -> StartedFlagResponse {
        try await post("/api/cron/jobs/\(id)/run", body: EmptyBody())
    }

    public func listChannels() async throws -> ChannelsResponse {
        try await get("/api/channels")
    }

    public func setChannelToken(id: String, token: String) async throws -> ChannelMutationResponse {
        try await post("/api/channels/\(id)/token", body: ChannelTokenRequest(token: token))
    }

    public func setChannelConfig(
        id: String,
        fields: [String: String],
        enabled: Bool? = nil
    ) async throws -> ChannelMutationResponse {
        try await post("/api/channels/\(id)/config", body: ChannelConfigRequest(fields: fields, enabled: enabled))
    }

    public func approvePairing(id: String) async throws -> PairingInfo {
        try await post("/api/pairing/approve", body: PairingIDRequest(id: id))
    }

    public func revokePairing(id: String) async throws -> PairingInfo {
        try await post("/api/pairing/revoke", body: PairingIDRequest(id: id))
    }
}

public struct EmptyBody: Encodable, Sendable {
    public init() {}
}
