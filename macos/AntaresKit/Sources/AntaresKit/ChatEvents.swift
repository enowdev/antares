import Foundation

public struct ChatToolCall: Equatable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var args: String
    public var progress: String
    public var result: String?
    public var isError: Bool
    public var running: Bool

    public init(
        id: String = "",
        name: String = "",
        args: String = "",
        progress: String = "",
        result: String? = nil,
        isError: Bool = false,
        running: Bool = false
    ) {
        self.id = id
        self.name = name
        self.args = args
        self.progress = progress
        self.result = result
        self.isError = isError
        self.running = running
    }
}

public struct ChatUsage: Equatable, Sendable {
    public var inputTokens: Int?
    public var outputTokens: Int?
    public var contextTokens: Int?
    public var contextWindow: Int?
    public var cost: Double?

    public init(
        inputTokens: Int? = nil,
        outputTokens: Int? = nil,
        contextTokens: Int? = nil,
        contextWindow: Int? = nil,
        cost: Double? = nil
    ) {
        self.inputTokens = inputTokens
        self.outputTokens = outputTokens
        self.contextTokens = contextTokens
        self.contextWindow = contextWindow
        self.cost = cost
    }
}

public struct ChatTranscript: Equatable, Sendable {
    public var sessionID: String?
    public var title: String?
    public var assistantText: String
    public var reasoning: String
    public var toolCalls: [ChatToolCall]
    public var notice: String?
    public var askID: String?
    public var usage: ChatUsage?
    public var turn: Int?
    public var done: Bool
    public var error: String?

    public init(
        sessionID: String? = nil,
        title: String? = nil,
        assistantText: String = "",
        reasoning: String = "",
        toolCalls: [ChatToolCall] = [],
        notice: String? = nil,
        askID: String? = nil,
        usage: ChatUsage? = nil,
        turn: Int? = nil,
        done: Bool = false,
        error: String? = nil
    ) {
        self.sessionID = sessionID
        self.title = title
        self.assistantText = assistantText
        self.reasoning = reasoning
        self.toolCalls = toolCalls
        self.notice = notice
        self.askID = askID
        self.usage = usage
        self.turn = turn
        self.done = done
        self.error = error
    }

    public mutating func apply(_ event: [String: String]) {
        guard let type = event["type"] else { return }

        switch type {
        case "session":
            if let id = event["id"] { sessionID = id }
            if let title = event["title"] { self.title = title }

        case "turn":
            if let turnStr = event["turn"], let n = Int(turnStr) {
                turn = n
            }

        case "text":
            assistantText += event["delta"] ?? ""

        case "reasoning":
            reasoning += event["delta"] ?? ""

        case "tool_call":
            toolCalls.append(ChatToolCall(
                id: event["id"] ?? "",
                name: event["name"] ?? "",
                args: event["arguments"] ?? "",
                running: true
            ))

        case "tool_progress":
            let id = event["id"] ?? ""
            updateTool(id: id) { tool in
                if let chunk = event["chunk"] {
                    tool.progress += chunk
                } else {
                    tool.progress = event["message"] ?? tool.progress
                }
            }

        case "tool_result":
            let id = event["id"] ?? ""
            updateTool(id: id) { tool in
                tool.result = event["content"] ?? ""
                tool.isError = event["is_error"] == "true"
                tool.running = false
            }

        case "usage":
            var u = usage ?? ChatUsage()
            if let v = event["input_tokens"], let n = Int(v) { u.inputTokens = n }
            if let v = event["output_tokens"], let n = Int(v) { u.outputTokens = n }
            if let v = event["context_tokens"], let n = Int(v) { u.contextTokens = n }
            if let v = event["context_window"], let n = Int(v) { u.contextWindow = n }
            if let v = event["cost"], let n = Double(v) { u.cost = n }
            usage = u

        case "notice":
            let raw = event["message"] ?? event["content"] ?? ""
            let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            notice = trimmed.isEmpty ? nil : trimmed

        case "ask":
            askID = event["id"] ?? ""

        case "reset":
            assistantText = ""
            reasoning = ""
            toolCalls = []
            error = nil

        case "error":
            error = event["error"]

        case "done":
            done = true

        case "approval":
            break

        default:
            break
        }
    }

    private mutating func updateTool(id: String, update: (inout ChatToolCall) -> Void) {
        guard let idx = toolCalls.firstIndex(where: { $0.id == id }) else { return }
        update(&toolCalls[idx])
    }
}

public struct ChatHistoryMessage: Equatable, Identifiable, Sendable {
    public var id: String
    public var role: String
    public var content: String
    public var reasoning: String
    public var toolCalls: [ChatToolCall]
    public var error: String?

    public init(
        id: String,
        role: String,
        content: String = "",
        reasoning: String = "",
        toolCalls: [ChatToolCall] = [],
        error: String? = nil
    ) {
        self.id = id
        self.role = role
        self.content = content
        self.reasoning = reasoning
        self.toolCalls = toolCalls
        self.error = error
    }
}

public func hydrateChat(_ detail: SessionDetail) -> [ChatHistoryMessage] {
    var rows: [ChatHistoryMessage] = []
    var toolIndex: [String: Int] = [:]

    for message in detail.messages {
        if message.hidden == true { continue }

        if message.role == "tool" {
            if let callID = message.toolCallID, let idx = toolIndex[callID], rows.indices.contains(idx) {
                rows[idx].toolCalls = rows[idx].toolCalls.map { call in
                    guard call.id == callID else { return call }
                    var next = call
                    next.result = message.content
                    next.running = false
                    next.isError = message.meta?["is_error"]?.boolValue == true
                    return next
                }
            }
            continue
        }

        var row = ChatHistoryMessage(
            id: message.id,
            role: message.role,
            content: message.content,
            reasoning: message.reasoning ?? ""
        )

        if message.role == "assistant", message.meta?["is_error"]?.boolValue == true {
            row.error = parseHydratedError(message.content)
            row.content = ""
        }

        if let raw = message.toolCalls, let parsed = parseHydratedToolCalls(raw) {
            row.toolCalls = parsed
            let index = rows.count
            for call in parsed {
                toolIndex[call.id] = index
            }
        }

        rows.append(row)
    }

    return rows
}

private func parseHydratedError(_ content: String) -> String {
    guard let data = content.data(using: .utf8),
          let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
          let error = obj["error"] as? String
    else {
        return content
    }
    return error
}

private struct EncodedHydratedToolCall: Decodable {
    var id: String
    var name: String
    var arguments: String
}

private func parseHydratedToolCalls(_ raw: String) -> [ChatToolCall]? {
    guard let data = raw.data(using: .utf8),
          let parsed = try? JSONDecoder().decode([EncodedHydratedToolCall].self, from: data)
    else {
        return nil
    }
    return parsed.map {
        ChatToolCall(id: $0.id, name: $0.name, args: $0.arguments, running: true)
    }
}
