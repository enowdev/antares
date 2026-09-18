import XCTest
@testable import AntaresKit

final class ChatRequestTests: XCTestCase {
    func testInterruptBodyEncodesSessionID() throws {
        let body = ChatInterruptRequest(sessionID: "session-123")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["session_id": "session-123"])
    }

    func testEditBodyMatchesSessionEditHandler() throws {
        let body = ChatEditRequest(messageID: "msg-456", revert: true)

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(json?["message_id"] as? String, "msg-456")
        XCTAssertEqual(json?["revert"] as? Bool, true)
        XCTAssertEqual(json?.count, 2)
    }

    func testSendBodyEncodesSessionIDAndMessage() throws {
        let body = ChatSendRequest(sessionID: "sess_1", message: "hello")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(json?["session_id"] as? String, "sess_1")
        XCTAssertEqual(json?["message"] as? String, "hello")
        XCTAssertNil(json?["platform"])
    }

    func testHydrateSkipsHiddenMergesToolResultsAndSurfacesAssistantError() throws {
        let data = #"""
        {
          "session": {
            "id": "s1",
            "title": "Hello",
            "platform": "web",
            "channel_id": "",
            "user_id": "",
            "model": "gpt-4",
            "provider": "openai",
            "workspace": "",
            "message_count": 4,
            "tokens_in": 10,
            "tokens_out": 20,
            "cost": 0.01,
            "archived": false,
            "pinned": false,
            "created_at": "2026-09-18T00:00:00Z",
            "updated_at": "2026-09-18T00:00:01Z",
            "meta": {"project_dir": "/tmp/proj", "index_rag": true}
          },
          "messages": [
            {
              "id": "m-hidden",
              "session_id": "s1",
              "seq": 1,
              "role": "user",
              "content": "secret",
              "created_at": "2026-09-18T00:00:00Z",
              "tokens_in": 0,
              "tokens_out": 0,
              "hidden": true,
              "compacted": false,
              "meta": null
            },
            {
              "id": "m-user",
              "session_id": "s1",
              "seq": 2,
              "role": "user",
              "content": "hello",
              "created_at": "2026-09-18T00:00:01Z",
              "tokens_in": 0,
              "tokens_out": 0,
              "hidden": false,
              "compacted": false,
              "meta": null
            },
            {
              "id": "m-asst",
              "session_id": "s1",
              "seq": 3,
              "role": "assistant",
              "content": "world",
              "reasoning": "think",
              "tool_calls": "[{\"id\":\"t1\",\"name\":\"read\",\"arguments\":\"{}\"}]",
              "created_at": "2026-09-18T00:00:02Z",
              "tokens_in": 5,
              "tokens_out": 3,
              "hidden": false,
              "compacted": false,
              "meta": {}
            },
            {
              "id": "m-tool",
              "session_id": "s1",
              "seq": 4,
              "role": "tool",
              "content": "file contents",
              "tool_call_id": "t1",
              "tool_name": "read",
              "created_at": "2026-09-18T00:00:03Z",
              "tokens_in": 0,
              "tokens_out": 0,
              "hidden": false,
              "compacted": false,
              "meta": {"is_error": true}
            },
            {
              "id": "m-err",
              "session_id": "s1",
              "seq": 5,
              "role": "assistant",
              "content": "{\"error\":\"boom\"}",
              "created_at": "2026-09-18T00:00:04Z",
              "tokens_in": 0,
              "tokens_out": 0,
              "hidden": false,
              "compacted": false,
              "meta": {"is_error": true}
            }
          ]
        }
        """#.data(using: .utf8)!

        let detail = try JSONDecoder().decode(SessionDetail.self, from: data)
        let rows = hydrateChat(detail)

        XCTAssertEqual(detail.session.id, "s1")
        XCTAssertEqual(detail.session.title, "Hello")
        XCTAssertEqual(detail.session.meta?["project_dir"]?.stringValue, "/tmp/proj")
        XCTAssertEqual(detail.session.meta?["index_rag"]?.boolValue, true)

        XCTAssertEqual(rows.map(\.id), ["m-user", "m-asst", "m-err"])
        XCTAssertEqual(rows[0].role, "user")
        XCTAssertEqual(rows[0].content, "hello")

        XCTAssertEqual(rows[1].role, "assistant")
        XCTAssertEqual(rows[1].content, "world")
        XCTAssertEqual(rows[1].reasoning, "think")
        XCTAssertEqual(rows[1].toolCalls.count, 1)
        XCTAssertEqual(rows[1].toolCalls[0].id, "t1")
        XCTAssertEqual(rows[1].toolCalls[0].name, "read")
        XCTAssertEqual(rows[1].toolCalls[0].args, "{}")
        XCTAssertEqual(rows[1].toolCalls[0].result, "file contents")
        XCTAssertTrue(rows[1].toolCalls[0].isError)
        XCTAssertFalse(rows[1].toolCalls[0].running)

        XCTAssertEqual(rows[2].role, "assistant")
        XCTAssertEqual(rows[2].content, "")
        XCTAssertEqual(rows[2].error, "boom")
    }
}
