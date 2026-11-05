import Foundation

public struct AuthStatus: Decodable, Equatable, Sendable {
    public var passwordRequired: Bool
    public var authenticated: Bool

    enum CodingKeys: String, CodingKey {
        case passwordRequired = "password_required"
        case authenticated
    }
}

public struct LoginRequest: Encodable, Equatable, Sendable {
    public var password: String

    public init(password: String) {
        self.password = password
    }
}

public struct SetPasswordRequest: Encodable, Equatable, Sendable {
    public var password: String
    public var current: String?

    public init(password: String, current: String? = nil) {
        self.password = password
        self.current = current
    }
}

public struct SetupStatus: Decodable, Equatable, Sendable {
    public var needsSetup: Bool
    public var model: String
    public var provider: String
    public var workspace: String
    public var home: String
    public var configPath: String
    public var providers: [SetupProvider]
    public var database: String

    enum CodingKeys: String, CodingKey {
        case needsSetup = "needs_setup"
        case model
        case provider
        case workspace
        case home
        case configPath = "config_path"
        case providers
        case database
    }
}

public struct SetupProvider: Decodable, Identifiable, Equatable, Sendable {
    public var id: String
    public var label: String
    public var kind: String
    public var hint: String
    public var keyHint: String?
    public var keyURL: String?
    public var baseURL: String?
    public var local: Bool
    public var models: [String]?
    public var hasKey: Bool
    public var keyLabel: String?
    public var note: String?
    public var needsRegion: Bool?
    public var needsAPIVersion: Bool?
    public var needsBaseURL: Bool?
    public var custom: Bool?

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case kind
        case hint
        case keyHint = "key_hint"
        case keyURL = "key_url"
        case baseURL = "base_url"
        case local
        case models
        case hasKey = "has_key"
        case keyLabel = "key_label"
        case note
        case needsRegion = "needs_region"
        case needsAPIVersion = "needs_api_version"
        case needsBaseURL = "needs_base_url"
        case custom
    }
}

public struct SetupTestRequest: Encodable, Equatable, Sendable {
    public var provider: String
    public var baseURL: String
    public var apiKey: String
    public var headers: [String: String]?

    public init(provider: String, baseURL: String, apiKey: String, headers: [String: String]? = nil) {
        self.provider = provider
        self.baseURL = baseURL
        self.apiKey = apiKey
        self.headers = headers
    }

    enum CodingKeys: String, CodingKey {
        case provider
        case baseURL = "base_url"
        case apiKey = "api_key"
        case headers
    }
}

public struct SetupTestResponse: Decodable, Equatable, Sendable {
    public var ok: Bool
    public var error: String?
    public var note: String?
    public var models: [String]?
    public var suggested: [String]?
}

public struct CompleteSetupRequest: Encodable, Equatable, Sendable {
    public var provider: String
    public var name: String
    public var baseURL: String
    public var apiKey: String
    public var headers: [String: String]?
    public var model: String
    public var workspace: String
    public var database: SetupDatabaseRequest
    public var rag: SetupRAGRequest
    public var telegramToken: String
    public var dashboardPassword: String

    public init(
        provider: String,
        name: String = "",
        baseURL: String,
        apiKey: String,
        headers: [String: String]? = nil,
        model: String,
        workspace: String,
        database: SetupDatabaseRequest,
        rag: SetupRAGRequest,
        telegramToken: String = "",
        dashboardPassword: String = ""
    ) {
        self.provider = provider
        self.name = name
        self.baseURL = baseURL
        self.apiKey = apiKey
        self.headers = headers
        self.model = model
        self.workspace = workspace
        self.database = database
        self.rag = rag
        self.telegramToken = telegramToken
        self.dashboardPassword = dashboardPassword
    }

    enum CodingKeys: String, CodingKey {
        case provider
        case name
        case baseURL = "base_url"
        case apiKey = "api_key"
        case headers
        case model
        case workspace
        case database
        case rag
        case telegramToken = "telegram_token"
        case dashboardPassword = "dashboard_password"
    }
}

public struct SetupDatabaseRequest: Encodable, Equatable, Sendable {
    public var driver: String
    public var dsn: String

    public init(driver: String = "sqlite", dsn: String = "") {
        self.driver = driver
        self.dsn = dsn
    }
}

public struct SetupRAGRequest: Encodable, Equatable, Sendable {
    public var enabled: Bool
    public var embedProvider: String
    public var embedModel: String
    public var embedAPIKey: String

    public init(
        enabled: Bool = false,
        embedProvider: String = "voyage",
        embedModel: String = "voyage-4",
        embedAPIKey: String = ""
    ) {
        self.enabled = enabled
        self.embedProvider = embedProvider
        self.embedModel = embedModel
        self.embedAPIKey = embedAPIKey
    }

    enum CodingKeys: String, CodingKey {
        case enabled
        case embedProvider = "embed_provider"
        case embedModel = "embed_model"
        case embedAPIKey = "embed_api_key"
    }
}
