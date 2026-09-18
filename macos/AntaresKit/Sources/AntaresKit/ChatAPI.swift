import Foundation

public struct ChatSendRequest: Encodable, Sendable {
    public var sessionID: String?
    public var message: String
    public var images: [String]
    public var role: String?
    public var model: String?
    public var toolset: String?
    public var reasoningEffort: String?

    public init(
        sessionID: String? = nil,
        message: String,
        images: [String] = [],
        role: String? = nil,
        model: String? = nil,
        toolset: String? = nil,
        reasoningEffort: String? = nil
    ) {
        self.sessionID = sessionID
        self.message = message
        self.images = images
        self.role = role
        self.model = model
        self.toolset = toolset
        self.reasoningEffort = reasoningEffort
    }

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case message
        case images
        case role
        case model
        case toolset
        case reasoningEffort = "reasoning_effort"
    }
}

public struct ChatInterruptRequest: Encodable, Sendable {
    public var sessionID: String

    public init(sessionID: String) {
        self.sessionID = sessionID
    }

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
    }
}

public struct ChatEditRequest: Encodable, Sendable {
    public var messageID: String
    public var revert: Bool

    public init(messageID: String, revert: Bool) {
        self.messageID = messageID
        self.revert = revert
    }

    enum CodingKeys: String, CodingKey {
        case messageID = "message_id"
        case revert
    }
}

public struct ChatUploadRequest: Encodable, Sendable {
    public var sessionID: String?
    public var name: String
    public var data: String

    public init(sessionID: String? = nil, name: String, data: String) {
        self.sessionID = sessionID
        self.name = name
        self.data = data
    }

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
        case name
        case data
    }
}

public struct ChatUploadResponse: Decodable, Sendable {
    public var ok: Bool
    public var path: String
    public var name: String
    public var size: Int
}

public struct ChatInterruptResponse: Decodable, Sendable {
    public var interrupted: Bool
}

public struct ChatEditResponse: Decodable, Sendable {
    public var ok: Bool
    public var reverted: [String]
    public var skipped: [String]
}

public struct SessionDetail: Decodable, Sendable {
    public var session: SessionSummary
    public var messages: [SessionMessage]
}

public struct SessionSummary: Decodable, Sendable {
    public var id: String
    public var title: String
    public var model: String?
    public var provider: String?
    public var meta: [String: FlexibleJSON]?
}

public struct SessionMessage: Decodable, Identifiable, Sendable {
    public var id: String
    public var role: String
    public var content: String
    public var reasoning: String?
    public var toolCalls: String?
    public var toolCallID: String?
    public var toolName: String?
    public var attachments: String?
    public var createdAt: String
    public var tokensIn: Int?
    public var tokensOut: Int?
    public var hidden: Bool?
    public var meta: [String: FlexibleJSON]?

    enum CodingKeys: String, CodingKey {
        case id
        case role
        case content
        case reasoning
        case toolCalls = "tool_calls"
        case toolCallID = "tool_call_id"
        case toolName = "tool_name"
        case attachments
        case createdAt = "created_at"
        case tokensIn = "tokens_in"
        case tokensOut = "tokens_out"
        case hidden
        case meta
    }
}

public enum FlexibleJSON: Decodable, Sendable, Equatable {
    case string(String)
    case bool(Bool)
    case number(Double)
    case object([String: FlexibleJSON])
    case array([FlexibleJSON])
    case null

    public init(from decoder: Decoder) throws {
        let container = try decoder.singleValueContainer()
        if container.decodeNil() {
            self = .null
        } else if let value = try? container.decode(Bool.self) {
            self = .bool(value)
        } else if let value = try? container.decode(Double.self) {
            self = .number(value)
        } else if let value = try? container.decode(String.self) {
            self = .string(value)
        } else if let value = try? container.decode([FlexibleJSON].self) {
            self = .array(value)
        } else {
            self = .object(try container.decode([String: FlexibleJSON].self))
        }
    }

    public var boolValue: Bool? {
        if case .bool(let value) = self { return value }
        return nil
    }

    public var stringValue: String? {
        if case .string(let value) = self { return value }
        return nil
    }
}

extension APIClient {
    public func getSession(_ id: String) async throws -> SessionDetail {
        try await get("/api/sessions/\(id)")
    }

    public func interruptChat(sessionID: String) async throws -> ChatInterruptResponse {
        try await post("/api/chat/interrupt", body: ChatInterruptRequest(sessionID: sessionID))
    }

    public func editSession(
        sessionID: String,
        messageID: String,
        revert: Bool = false
    ) async throws -> ChatEditResponse {
        try await post(
            "/api/sessions/\(sessionID)/edit",
            body: ChatEditRequest(messageID: messageID, revert: revert)
        )
    }

    public func upload(_ request: ChatUploadRequest) async throws -> ChatUploadResponse {
        try await post("/api/upload", body: request)
    }

    public func streamChat(
        _ request: ChatSendRequest,
        onEvent: @escaping @Sendable ([String: String]) async -> Void,
        onParseError: (@Sendable (String, Error) async -> Void)? = nil
    ) async throws {
        var urlRequest = try makeJSONStreamRequest(path: "/api/chat", method: "POST", body: request)
        urlRequest.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        try await stream(urlRequest, onEvent: onEvent, onParseError: onParseError)
    }

    public func attachChat(
        sessionID: String,
        cursor: Int = 0,
        onEvent: @escaping @Sendable ([String: String]) async -> Void,
        onParseError: (@Sendable (String, Error) async -> Void)? = nil
    ) async throws {
        var components = URLComponents(url: url("/api/chat/attach"), resolvingAgainstBaseURL: false)!
        components.queryItems = [
            URLQueryItem(name: "session_id", value: sessionID),
            URLQueryItem(name: "cursor", value: String(cursor)),
        ]
        var request = URLRequest(url: components.url!)
        request.httpMethod = "GET"
        request.setValue("text/event-stream", forHTTPHeaderField: "Accept")
        if let bearerToken, !bearerToken.isEmpty {
            request.setValue("Bearer \(bearerToken)", forHTTPHeaderField: "Authorization")
        }
        try await stream(request, onEvent: onEvent, onParseError: onParseError)
    }

    private func makeJSONStreamRequest<B: Encodable>(
        path: String,
        method: String,
        body: B
    ) throws -> URLRequest {
        var request = URLRequest(url: url(path))
        request.httpMethod = method
        request.httpBody = try JSONEncoder().encode(body)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        if let bearerToken, !bearerToken.isEmpty {
            request.setValue("Bearer \(bearerToken)", forHTTPHeaderField: "Authorization")
        }
        return request
    }

    private func stream(
        _ request: URLRequest,
        onEvent: @escaping @Sendable ([String: String]) async -> Void,
        onParseError: (@Sendable (String, Error) async -> Void)?
    ) async throws {
        let (bytes, response) = try await session.bytes(for: request)
        let status = (response as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else {
            throw APIError(status: status, body: Data())
        }

        var accumulator = SSEAccumulator()
        for try await byte in bytes {
            var emitted: [[String: String]] = []
            var parseErrors: [(String, Error)] = []
            try accumulator.append(Data([byte]), onEvent: { event in
                emitted.append(event)
            }, onParseError: { raw, error in
                parseErrors.append((raw, error))
            })
            for event in emitted {
                await onEvent(event)
            }
            if let onParseError {
                for (raw, error) in parseErrors {
                    await onParseError(raw, error)
                }
            }
        }
        var emitted: [[String: String]] = []
        var parseErrors: [(String, Error)] = []
        try accumulator.finish(onEvent: { event in
            emitted.append(event)
        }, onParseError: { raw, error in
            parseErrors.append((raw, error))
        })
        for event in emitted {
            await onEvent(event)
        }
        if let onParseError {
            for (raw, error) in parseErrors {
                await onParseError(raw, error)
            }
        }
    }
}
