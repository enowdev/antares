import Foundation

public struct SSEAccumulator {
    private var buffer = Data()
    private var dataLines: [String] = []

    public init() {}

    public mutating func append(
        _ data: Data,
        onEvent: ([String: String]) throws -> Void,
        onParseError: ((String, Error) -> Void)? = nil
    ) throws {
        buffer.append(data)
        try consumeCompleteLines(
            final: false,
            onEvent: onEvent,
            onParseError: onParseError
        )
    }

    public mutating func finish(
        onEvent: ([String: String]) throws -> Void,
        onParseError: ((String, Error) -> Void)? = nil
    ) throws {
        try consumeCompleteLines(
            final: true,
            onEvent: onEvent,
            onParseError: onParseError
        )
        buffer.removeAll(keepingCapacity: false)
        dataLines.removeAll(keepingCapacity: false)
    }

    private mutating func consumeCompleteLines(
        final: Bool,
        onEvent: ([String: String]) throws -> Void,
        onParseError: ((String, Error) -> Void)?
    ) throws {
        var lineStart = 0
        var i = 0

        while i < buffer.count {
            if buffer[i] == 0x0A {
                try flushLine(lineStart..<i, onEvent: onEvent, onParseError: onParseError)
                i += 1
                lineStart = i
                continue
            }

            if buffer[i] == 0x0D {
                if i == buffer.count - 1 && !final {
                    break
                }

                try flushLine(lineStart..<i, onEvent: onEvent, onParseError: onParseError)
                if i + 1 < buffer.count, buffer[i + 1] == 0x0A {
                    i += 2
                } else {
                    i += 1
                }
                lineStart = i
                continue
            }

            i += 1
        }

        if final, lineStart < buffer.count {
            try flushLine(lineStart..<buffer.count, onEvent: onEvent, onParseError: onParseError)
            lineStart = buffer.count
        }

        if lineStart > 0 {
            buffer.removeSubrange(0..<lineStart)
        }
    }

    private mutating func flushLine(
        _ range: Range<Int>,
        onEvent: ([String: String]) throws -> Void,
        onParseError: ((String, Error) -> Void)?
    ) throws {
        let line = String(decoding: buffer[range], as: UTF8.self)
        if line.hasPrefix(":") { return }
        if line.isEmpty {
            let payload = dataLines.joined(separator: "\n")
            dataLines = []
            try Self.dispatch(payload, onEvent: onEvent, onParseError: onParseError)
            return
        }
        if line.hasPrefix("data:") {
            var value = String(line.dropFirst(5))
            if value.first == " " { value.removeFirst() }
            dataLines.append(value)
        }
    }

    private static func stringifyValue(_ value: Any) -> String {
        if let string = value as? String { return string }
        if let boolean = value as? Bool { return boolean ? "true" : "false" }
        return "\(value)"
    }

    private static func dispatch(
        _ payload: String,
        onEvent: ([String: String]) throws -> Void,
        onParseError: ((String, Error) -> Void)?
    ) throws {
        guard !payload.isEmpty, payload != "[DONE]" else { return }
        let obj: Any
        do {
            obj = try JSONSerialization.jsonObject(
                with: Data(payload.utf8),
                options: .fragmentsAllowed
            )
        } catch {
            onParseError?(payload, error)
            return
        }
        if let string = obj as? String {
            try onEvent(["value": string])
            return
        }
        guard let dict = obj as? [String: Any] else { return }
        var strings: [String: String] = [:]
        for (key, value) in dict {
            strings[key] = stringifyValue(value)
        }
        try onEvent(strings)
    }
}

public enum SSEStream {
    public static let defaultChunkSize = 4096

    public static func consume<S: AsyncSequence>(
        _ bytes: S,
        chunkSize: Int = defaultChunkSize,
        onEvent: @escaping @Sendable ([String: String]) async -> Void,
        onParseError: (@Sendable (String, Error) async -> Void)? = nil
    ) async throws where S.Element == UInt8 {
        let flushAt = max(chunkSize, 1)
        var accumulator = SSEAccumulator()
        var chunk = Data()
        chunk.reserveCapacity(flushAt)

        func flush() async throws {
            guard !chunk.isEmpty else { return }
            var emitted: [[String: String]] = []
            var parseErrors: [(String, Error)] = []
            try accumulator.append(chunk, onEvent: { event in
                emitted.append(event)
            }, onParseError: { raw, error in
                parseErrors.append((raw, error))
            })
            chunk.removeAll(keepingCapacity: true)
            for event in emitted {
                await onEvent(event)
            }
            if let onParseError {
                for (raw, error) in parseErrors {
                    await onParseError(raw, error)
                }
            }
        }

        for try await byte in bytes {
            chunk.append(byte)
            if chunk.count >= flushAt || byte == 0x0A {
                try await flush()
            }
        }
        try await flush()

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

public enum SSEParser {
    public static func parse(
        _ data: Data,
        onEvent: ([String: String]) throws -> Void,
        onParseError: ((String, Error) -> Void)? = nil
    ) throws {
        var accumulator = SSEAccumulator()
        try accumulator.append(data, onEvent: onEvent, onParseError: onParseError)
        try accumulator.finish(onEvent: onEvent, onParseError: onParseError)
    }
}
