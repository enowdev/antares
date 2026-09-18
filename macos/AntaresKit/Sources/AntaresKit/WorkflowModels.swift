import Foundation

public struct AutopilotCardsResponse: Decodable, Sendable {
    public var cards: [AutopilotCard]

    enum CodingKeys: String, CodingKey {
        case cards
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        cards = try container.decodeIfPresent([AutopilotCard].self, forKey: .cards) ?? []
    }
}

public struct AutopilotCard: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var title: String
    public var prompt: String
    public var status: String
    public var result: String?
    public var verifyOutput: String?
    public var branch: String?
    public var pr: String?
    public var error: String?
    public var createdAt: String
    public var updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case prompt
        case status
        case result
        case verifyOutput = "verify_output"
        case branch
        case pr
        case error
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

public struct AutopilotAddRequest: Encodable, Sendable {
    public var title: String
    public var prompt: String

    public init(title: String, prompt: String) {
        self.title = title
        self.prompt = prompt
    }
}

public struct AutopilotRunResponse: Decodable, Sendable {
    public var started: Int
    public var alreadyRunning: Bool?

    enum CodingKeys: String, CodingKey {
        case started
        case alreadyRunning = "already_running"
    }
}

public struct EngagementSessionsResponse: Decodable, Sendable {
    public var sessions: [EngagementSession]

    enum CodingKeys: String, CodingKey {
        case sessions
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        sessions = try container.decodeIfPresent([EngagementSession].self, forKey: .sessions) ?? []
    }
}

public struct EngagementSession: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var title: String
    public var findings: Int
    public var intel: Int
}

public struct EngagementSnapshot: Decodable, Sendable {
    public var phases: [EngagementPhase]
    public var coverage: [EngagementArea]
    public var coveragePercent: Int
    public var chains: [EngagementChain]
    public var findings: [EngagementFinding]
    public var intel: [EngagementIntel]
    public var next: String?
    public var scope: [String]?

    enum CodingKeys: String, CodingKey {
        case phases
        case coverage
        case coveragePercent = "coverage_percent"
        case chains
        case findings
        case intel
        case next
        case scope
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        phases = try container.decodeIfPresent([EngagementPhase].self, forKey: .phases) ?? []
        coverage = try container.decodeIfPresent([EngagementArea].self, forKey: .coverage) ?? []
        coveragePercent = try container.decodeIfPresent(Int.self, forKey: .coveragePercent) ?? 0
        chains = try container.decodeIfPresent([EngagementChain].self, forKey: .chains) ?? []
        findings = try container.decodeIfPresent([EngagementFinding].self, forKey: .findings) ?? []
        intel = try container.decodeIfPresent([EngagementIntel].self, forKey: .intel) ?? []
        next = try container.decodeIfPresent(String.self, forKey: .next)
        scope = try container.decodeIfPresent([String].self, forKey: .scope)
    }
}

public struct EngagementPhase: Decodable, Identifiable, Sendable {
    public var name: String
    public var title: String
    public var summary: String
    public var status: String
    public var evidence: Int
    public var roles: [String]?
    public var requires: [String]?

    public var id: String { name }
}

public struct EngagementArea: Decodable, Identifiable, Sendable {
    public var name: String
    public var title: String
    public var covered: Bool

    public var id: String { name }
}

public struct EngagementChain: Decodable, Identifiable, Sendable {
    public var name: String
    public var impact: String

    public var id: String { name }
}

public struct EngagementFinding: Decodable, Identifiable, Sendable {
    public var id: String
    public var title: String
    public var severity: String
    public var target: String?
    public var description: String
    public var cwe: String?
    public var status: String?
    public var createdAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case severity
        case target
        case description
        case cwe
        case status
        case createdAt = "created_at"
    }
}

public struct EngagementIntel: Decodable, Identifiable, Sendable {
    public var id: String
    public var type: String
    public var value: String
    public var detail: String?
    public var createdAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case type
        case value
        case detail
        case createdAt = "created_at"
    }
}

public struct BoardSessionsResponse: Decodable, Sendable {
    public var sessions: [BoardSession]

    enum CodingKeys: String, CodingKey {
        case sessions
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        sessions = try container.decodeIfPresent([BoardSession].self, forKey: .sessions) ?? []
    }
}

public struct BoardSession: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var title: String
}

public struct BoardSnapshot: Decodable, Sendable {
    public var columns: [BoardColumn]

    enum CodingKeys: String, CodingKey {
        case columns
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        columns = try container.decodeIfPresent([BoardColumn].self, forKey: .columns) ?? []
    }
}

public struct BoardColumn: Decodable, Identifiable, Sendable {
    public var name: String
    public var cards: [BoardCard]

    public var id: String { name }

    enum CodingKeys: String, CodingKey {
        case name
        case cards
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decode(String.self, forKey: .name)
        cards = try container.decodeIfPresent([BoardCard].self, forKey: .cards) ?? []
    }
}

public struct BoardCard: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var title: String
    public var note: String?
    public var column: String
    public var createdAt: String?
    public var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case note
        case column
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

extension APIClient {
    public func listAutopilotCards() async throws -> AutopilotCardsResponse {
        try await get("/api/autopilot")
    }

    public func addAutopilotCard(title: String, prompt: String) async throws -> AutopilotCard {
        try await post("/api/autopilot", body: AutopilotAddRequest(title: title, prompt: prompt))
    }

    public func runAutopilot() async throws -> AutopilotRunResponse {
        try await post("/api/autopilot/run", body: EmptyBody())
    }

    public func removeAutopilotCard(_ id: String) async throws -> OKResponse {
        try await delete("/api/autopilot/\(id)")
    }

    public func listEngagementSessions() async throws -> EngagementSessionsResponse {
        try await get("/api/engagement/sessions")
    }

    public func engagement(sessionID: String) async throws -> EngagementSnapshot {
        try await get("/api/engagement", query: [URLQueryItem(name: "session", value: sessionID)])
    }

    public func engagementReport(sessionID: String, title: String) async throws -> String {
        let data = try await getData(
            "/api/engagement/report",
            query: [
                URLQueryItem(name: "session", value: sessionID),
                URLQueryItem(name: "title", value: title),
            ]
        )
        return String(decoding: data, as: UTF8.self)
    }

    public func deleteEngagement(sessionID: String) async throws -> OKResponse {
        try await delete("/api/engagement/\(sessionID)")
    }

    public func listBoardSessions() async throws -> BoardSessionsResponse {
        try await get("/api/board/sessions")
    }

    public func board(sessionID: String) async throws -> BoardSnapshot {
        try await get("/api/board", query: [URLQueryItem(name: "session", value: sessionID)])
    }

    public func deleteBoardCard(sessionID: String, id: String) async throws -> OKResponse {
        try await delete(
            "/api/board/card",
            query: [
                URLQueryItem(name: "session", value: sessionID),
                URLQueryItem(name: "id", value: id),
            ]
        )
    }

    public func clearBoard(sessionID: String) async throws -> OKResponse {
        try await delete("/api/board", query: [URLQueryItem(name: "session", value: sessionID)])
    }
}
