import XCTest
@testable import AntaresKit

final class SSEClientTests: XCTestCase {
    func testDispatchesJSONOnBlankLineAndStripsLeadingSpace() throws {
        let raw = "data: {\"delta\":\"Hel\"}\n\ndata: {\"delta\":\"lo\"}\r\n\r\n"
        var events: [[String: String]] = []
        try SSEParser.parse(Data(raw.utf8)) { json in
            events.append(json)
        }
        XCTAssertEqual(events, [["delta": "Hel"], ["delta": "lo"]])
    }

    func testIgnoresCommentsAndDoneSentinel() throws {
        let raw = ": keep-alive\n\ndata: [DONE]\n\ndata: {\"ok\":true}\n\n"
        var count = 0
        try SSEParser.parse(Data(raw.utf8)) { _ in count += 1 }
        XCTAssertEqual(count, 1)
    }

    func testDropsUnfinishedTrailingRecord() throws {
        let raw = "data: {\"a\":1}\n\ndata: {\"b\":2}"
        var count = 0
        try SSEParser.parse(Data(raw.utf8)) { _ in count += 1 }
        XCTAssertEqual(count, 1)
    }

    func testAccumulatorDispatchesRecordSplitAcrossChunks() throws {
        var accumulator = SSEAccumulator()
        var events: [[String: String]] = []

        try accumulator.append(Data("data: {\"type\":\"text\"".utf8)) { event in
            events.append(event)
        }
        try accumulator.append(Data(",\"delta\":\"x\"}\n\n".utf8)) { event in
            events.append(event)
        }

        XCTAssertEqual(events, [["type": "text", "delta": "x"]])
    }

    func testMalformedJSONKeepsStreamAlive() throws {
        let raw = "data: not-json\n\ndata: {\"ok\":true}\n\n"
        var events: [[String: String]] = []
        var errors: [String] = []
        try SSEParser.parse(Data(raw.utf8), onEvent: { json in
            events.append(json)
        }, onParseError: { raw, _ in
            errors.append(raw)
        })
        XCTAssertEqual(events, [["ok": "true"]])
        XCTAssertEqual(errors, ["not-json"])
    }

    func testStripsSingleLeadingSpaceAfterDataColon() throws {
        let raw = "data: \"spaced\"\n\ndata:\"tight\"\n\n"
        var events: [[String: String]] = []
        try SSEParser.parse(Data(raw.utf8)) { json in
            events.append(json)
        }
        XCTAssertEqual(events, [["value": "spaced"], ["value": "tight"]])
    }

    func testConsumeAssemblesEventsFromAsyncByteStreamInChunks() async throws {
        let raw = "data: {\"type\":\"text\",\"delta\":\"hello\"}\n\n"
        let bytes = AsyncStream<UInt8> { continuation in
            for byte in raw.utf8 {
                continuation.yield(byte)
            }
            continuation.finish()
        }

        let collected = EventCollector()
        try await SSEStream.consume(bytes, chunkSize: 8) { event in
            collected.append(event)
        }

        XCTAssertEqual(collected.events, [["type": "text", "delta": "hello"]])
    }
}

private final class EventCollector: @unchecked Sendable {
    private(set) var events: [[String: String]] = []

    func append(_ event: [String: String]) {
        events.append(event)
    }
}
