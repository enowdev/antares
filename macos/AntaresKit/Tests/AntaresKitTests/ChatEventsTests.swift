import XCTest
@testable import AntaresKit

final class ChatEventsTests: XCTestCase {
    func testTextDeltasAppendAndDoneMarksComplete() {
        var s = ChatTranscript()
        s.apply(["type": "session", "id": "s1", "title": "T"])
        s.apply(["type": "text", "delta": "Hel"])
        s.apply(["type": "text", "delta": "lo"])
        s.apply(["type": "done"])
        XCTAssertEqual(s.sessionID, "s1")
        XCTAssertEqual(s.title, "T")
        XCTAssertEqual(s.assistantText, "Hello")
        XCTAssertTrue(s.done)
    }

    func testReasoningDeltasAppend() {
        var s = ChatTranscript()
        s.apply(["type": "reasoning", "delta": "think"])
        s.apply(["type": "reasoning", "delta": "ing"])
        XCTAssertEqual(s.reasoning, "thinking")
    }

    func testToolCallRecordsRunningAndResultCompletes() {
        var s = ChatTranscript()
        s.apply(["type": "tool_call", "id": "t1", "name": "read", "arguments": "{}"])
        XCTAssertEqual(s.toolCalls.count, 1)
        XCTAssertEqual(s.toolCalls[0].id, "t1")
        XCTAssertEqual(s.toolCalls[0].name, "read")
        XCTAssertEqual(s.toolCalls[0].args, "{}")
        XCTAssertTrue(s.toolCalls[0].running)

        s.apply(["type": "tool_result", "id": "t1", "content": "ok", "is_error": "false"])
        XCTAssertEqual(s.toolCalls[0].result, "ok")
        XCTAssertFalse(s.toolCalls[0].isError)
        XCTAssertFalse(s.toolCalls[0].running)
    }

    func testToolResultRecordsError() {
        var s = ChatTranscript()
        s.apply(["type": "tool_call", "id": "t1", "name": "read", "arguments": "{}"])
        s.apply(["type": "tool_result", "id": "t1", "content": "nope", "is_error": "true"])
        XCTAssertEqual(s.toolCalls[0].result, "nope")
        XCTAssertTrue(s.toolCalls[0].isError)
        XCTAssertFalse(s.toolCalls[0].running)
    }

    func testToolProgressChunkAppendsMessageReplaces() {
        var s = ChatTranscript()
        s.apply(["type": "tool_call", "id": "t1", "name": "run", "arguments": ""])
        s.apply(["type": "tool_progress", "id": "t1", "chunk": "a"])
        s.apply(["type": "tool_progress", "id": "t1", "chunk": "b"])
        XCTAssertEqual(s.toolCalls[0].progress, "ab")
        s.apply(["type": "tool_progress", "id": "t1", "message": "done"])
        XCTAssertEqual(s.toolCalls[0].progress, "done")
    }

    func testErrorSetsErrorString() {
        var s = ChatTranscript()
        s.apply(["type": "error", "error": "boom"])
        XCTAssertEqual(s.error, "boom")
    }

    func testResetClearsPartialState() {
        var s = ChatTranscript()
        s.apply(["type": "text", "delta": "partial"])
        s.apply(["type": "reasoning", "delta": "hmm"])
        s.apply(["type": "tool_call", "id": "t1", "name": "read", "arguments": "{}"])
        s.apply(["type": "error", "error": "fail"])
        s.apply(["type": "reset"])
        XCTAssertEqual(s.assistantText, "")
        XCTAssertEqual(s.reasoning, "")
        XCTAssertTrue(s.toolCalls.isEmpty)
        XCTAssertNil(s.error)
    }

    func testUnknownTypesAreIgnored() {
        var s = ChatTranscript()
        s.apply(["type": "unknown_type", "foo": "bar"])
        s.apply([:])
        XCTAssertEqual(s.assistantText, "")
        XCTAssertFalse(s.done)
    }

    func testHasDisplayableContentIncludesReasoningOnly() {
        let empty = ChatTranscript()
        XCTAssertFalse(empty.hasDisplayableContent)

        var reasoningOnly = ChatTranscript()
        reasoningOnly.apply(["type": "reasoning", "delta": "hmm"])
        XCTAssertTrue(reasoningOnly.hasDisplayableContent)
    }

    func testUsageParsesTokenFields() {
        var s = ChatTranscript()
        s.apply([
            "type": "usage",
            "input_tokens": "100",
            "output_tokens": "50",
            "context_tokens": "512",
            "context_window": "8000",
        ])
        XCTAssertEqual(s.usage?.inputTokens, 100)
        XCTAssertEqual(s.usage?.outputTokens, 50)
        XCTAssertEqual(s.usage?.contextTokens, 512)
        XCTAssertEqual(s.usage?.contextWindow, 8000)
    }

    func testNoticeAndAsk() {
        var s = ChatTranscript()
        s.apply(["type": "notice", "message": "  hello  "])
        XCTAssertEqual(s.notice, "hello")
        s.apply(["type": "ask", "id": "ask_1"])
        XCTAssertEqual(s.askID, "ask_1")
    }
}
