import Foundation

public enum ConfigFieldKind: String, Sendable, Equatable, Hashable {
    case string
    case number
    case boolean
    case stringArray = "string[]"
    case object
}

public struct ConfigField: Decodable, Sendable, Identifiable, Hashable {
    public var path: String
    public var label: String
    public var group: String
    public var type: ConfigFieldKind
    public var tier: String
    public var defaultValue: FlexibleJSON?
    public var secret: Bool
    public var enumValues: [String]?
    public var help: String?
    public var reload: String?

    public var id: String { path }

    enum CodingKeys: String, CodingKey {
        case path
        case label
        case group
        case type
        case tier
        case defaultValue = "default"
        case secret
        case enumValues = "enum"
        case help
        case reload
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
        label = try container.decodeIfPresent(String.self, forKey: .label) ?? ""
        group = try container.decodeIfPresent(String.self, forKey: .group) ?? ""
        let rawType = try container.decodeIfPresent(String.self, forKey: .type) ?? ""
        type = ConfigFieldKind(rawValue: rawType) ?? .string
        tier = try container.decodeIfPresent(String.self, forKey: .tier) ?? ""
        defaultValue = try container.decodeIfPresent(FlexibleJSON.self, forKey: .defaultValue)
        secret = try container.decodeIfPresent(Bool.self, forKey: .secret) ?? false
        enumValues = try container.decodeIfPresent([String].self, forKey: .enumValues)
        help = try container.decodeIfPresent(String.self, forKey: .help)
        reload = try container.decodeIfPresent(String.self, forKey: .reload)
    }
}

public struct ConfigResponse: Decodable, Sendable {
    public var values: [String: FlexibleJSON]
    public var schema: [ConfigField]
    public var restartFields: [String]
    public var profile: String
    public var path: String

    public var formFields: [ConfigField] {
        schema.filter { $0.type != .object }
    }

    enum CodingKeys: String, CodingKey {
        case values
        case schema
        case restartFields = "restart_fields"
        case profile
        case path
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        values = try container.decodeIfPresent([String: FlexibleJSON].self, forKey: .values) ?? [:]
        schema = try container.decodeIfPresent([ConfigField].self, forKey: .schema) ?? []
        restartFields = try container.decodeIfPresent([String].self, forKey: .restartFields) ?? []
        profile = try container.decodeIfPresent(String.self, forKey: .profile) ?? ""
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
    }

    public func value(at path: String) -> FlexibleJSON? {
        Self.value(at: path, in: values)
    }

    public static func value(at path: String, in values: [String: FlexibleJSON]) -> FlexibleJSON? {
        let parts = path.split(separator: ".").map(String.init)
        guard !parts.isEmpty else { return nil }
        var current: FlexibleJSON = .object(values)
        for part in parts {
            guard case .object(let object) = current, let next = object[part] else {
                return nil
            }
            current = next
        }
        return current
    }
}

public struct ConfigSchemaResponse: Decodable, Sendable {
    public var schema: [ConfigField]

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        schema = try container.decodeIfPresent([ConfigField].self, forKey: .schema) ?? []
    }

    enum CodingKeys: String, CodingKey {
        case schema
    }
}

public enum ConfigJSON: Codable, Sendable, Equatable {
    case string(String)
    case int(Int)
    case double(Double)
    case bool(Bool)
    case strings([String])
    case null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Int.self) {
            self = .int(value)
        } else if let value = try? container.decode(Double.self) {
            self = .double(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([String].self) {
            self = .strings(value)
        } else {
            throw DecodingError.dataCorruptedError(
                in: container,
                debugDescription: "Unsupported config JSON value"
            )
        }
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.singleValueContainer()
        switch self {
        case .string(let value):
            try container.encode(value)
        case .int(let value):
            try container.encode(value)
        case .double(let value):
            try container.encode(value)
        case .bool(let value):
            try container.encode(value)
        case .strings(let value):
            try container.encode(value)
        case .null:
            try container.encodeNil()
        }
    }

    public var boolValue: Bool {
        if case .bool(let value) = self { return value }
        return false
    }

    public var stringValue: String {
        switch self {
        case .string(let value):
            return value
        case .int(let value):
            return String(value)
        case .double(let value):
            if value.rounded() == value { return String(Int(value)) }
            return String(value)
        case .bool(let value):
            return value ? "true" : "false"
        case .strings(let value):
            return value.joined(separator: ", ")
        case .null:
            return ""
        }
    }

    public var stringArray: [String] {
        if case .strings(let value) = self { return value }
        if stringValue.isEmpty { return [] }
        return stringValue.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
    }

    public var isMaskedSecret: Bool {
        if case .string(let value) = self {
            return value.contains("••••")
        }
        return false
    }
}

public struct ConfigUpdateRequest: Encodable, Sendable {
    public var updates: [String: ConfigJSON]

    public init(updates: [String: ConfigJSON]) {
        self.updates = updates
    }
}

public struct ConfigUpdateResponse: Decodable, Sendable {
    public var saved: Int?
    public var values: [String: FlexibleJSON]?
    public var restartFields: [String]

    enum CodingKeys: String, CodingKey {
        case saved
        case values
        case restartFields = "restart_fields"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        saved = try container.decodeIfPresent(Int.self, forKey: .saved)
        values = try container.decodeIfPresent([String: FlexibleJSON].self, forKey: .values)
        restartFields = try container.decodeIfPresent([String].self, forKey: .restartFields) ?? []
    }
}

public struct ConfigRawResponse: Decodable, Sendable {
    public var yaml: String
    public var path: String

    enum CodingKeys: String, CodingKey {
        case yaml
        case path
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        yaml = try container.decodeIfPresent(String.self, forKey: .yaml) ?? ""
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
    }
}

public struct ConfigRawRequest: Encodable, Sendable {
    public var yaml: String

    public init(yaml: String) {
        self.yaml = yaml
    }
}

public struct ConfigRawSaveResponse: Decodable, Sendable {
    public var saved: Bool?
    public var restartFields: [String]

    enum CodingKeys: String, CodingKey {
        case saved
        case restartFields = "restart_fields"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        saved = try container.decodeIfPresent(Bool.self, forKey: .saved)
        restartFields = try container.decodeIfPresent([String].self, forKey: .restartFields) ?? []
    }
}

extension FlexibleJSON {
    public var configJSON: ConfigJSON {
        switch self {
        case .string(let value):
            return .string(value)
        case .bool(let value):
            return .bool(value)
        case .number(let value):
            if value.rounded() == value, let intValue = Int(exactly: value) {
                return .int(intValue)
            }
            return .double(value)
        case .array(let items):
            let strings = items.compactMap(\.stringValue)
            if strings.count == items.count {
                return .strings(strings)
            }
            return .string(displayString)
        case .object, .null:
            return .string(displayString)
        }
    }

    public var stringArray: [String] {
        if case .array(let items) = self {
            return items.compactMap(\.stringValue)
        }
        if let value = stringValue, !value.isEmpty {
            return [value]
        }
        return []
    }
}

extension APIClient {
    public func getConfig() async throws -> ConfigResponse {
        try await get("/api/config")
    }

    public func getConfigSchema() async throws -> ConfigSchemaResponse {
        try await get("/api/config/schema")
    }

    public func updateConfig(_ updates: [String: ConfigJSON]) async throws -> ConfigUpdateResponse {
        try await post("/api/config", body: ConfigUpdateRequest(updates: updates))
    }

    public func getRawConfig() async throws -> ConfigRawResponse {
        try await get("/api/config/raw")
    }

    public func saveRawConfig(_ yaml: String) async throws -> ConfigRawSaveResponse {
        try await post("/api/config/raw", body: ConfigRawRequest(yaml: yaml))
    }
}
