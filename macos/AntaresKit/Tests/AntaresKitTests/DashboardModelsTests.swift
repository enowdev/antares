import XCTest
@testable import AntaresKit

final class DashboardModelsTests: XCTestCase {
    func testDecodesSessionListFixtureFromListHandler() throws {
        let data = #"""
        {
          "sessions": [
            {
              "id": "sess_1",
              "title": "Hello",
              "platform": "web",
              "channel_id": "",
              "user_id": "",
              "model": "gpt-4",
              "provider": "openai",
              "workspace": "/tmp/proj",
              "message_count": 4,
              "tokens_in": 10,
              "tokens_out": 20,
              "cost": 0.01,
              "archived": false,
              "pinned": false,
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z",
              "meta": {"project_dir": "/tmp/proj"}
            }
          ],
          "total": 1
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(SessionListResponse.self, from: data)

        XCTAssertEqual(list.total, 1)
        XCTAssertEqual(list.sessions.count, 1)
        XCTAssertEqual(list.sessions[0].id, "sess_1")
        XCTAssertEqual(list.sessions[0].title, "Hello")
        XCTAssertEqual(list.sessions[0].platform, "web")
        XCTAssertEqual(list.sessions[0].model, "gpt-4")
        XCTAssertEqual(list.sessions[0].messageCount, 4)
        XCTAssertEqual(list.sessions[0].tokensIn, 10)
        XCTAssertEqual(list.sessions[0].updatedAt, "2026-09-18T00:00:01Z")
        XCTAssertEqual(list.sessions[0].meta?["project_dir"]?.stringValue, "/tmp/proj")
    }

    func testDecodesSessionSearchHitsFixture() throws {
        let data = #"""
        {
          "hits": [
            {
              "session_id": "sess_1",
              "session_title": "Hello",
              "message_id": "msg_9",
              "role": "user",
              "snippet": "deploy the app",
              "created_at": "2026-09-18T00:00:02Z",
              "score": 1.5
            }
          ]
        }
        """#.data(using: .utf8)!

        let search = try JSONDecoder().decode(SessionSearchResponse.self, from: data)

        XCTAssertEqual(search.hits.count, 1)
        XCTAssertEqual(search.hits[0].sessionID, "sess_1")
        XCTAssertEqual(search.hits[0].sessionTitle, "Hello")
        XCTAssertEqual(search.hits[0].messageID, "msg_9")
        XCTAssertEqual(search.hits[0].role, "user")
        XCTAssertEqual(search.hits[0].snippet, "deploy the app")
        XCTAssertEqual(search.hits[0].createdAt, "2026-09-18T00:00:02Z")
        XCTAssertEqual(search.hits[0].score, 1.5)
    }

    func testDecodesDeletedFlagFixture() throws {
        let data = #"{"deleted":true}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(DeletedFlagResponse.self, from: data)

        XCTAssertTrue(response.deleted)
    }

    func testDecodesModelOptionsFixtureFromHandler() throws {
        let data = #"""
        {
          "active": {"model": "gpt-4o", "provider": "openai"},
          "providers": [
            {
              "id": "openai",
              "label": "OpenAI",
              "kind": "openai",
              "enabled": true,
              "has_key": true,
              "local": false,
              "base_url": "https://api.openai.com/v1",
              "active": true,
              "hint": "Official API",
              "key_hint": "sk-...",
              "key_url": "https://platform.openai.com/api-keys",
              "timeout_seconds": 60
            },
            {
              "id": "ollama",
              "label": "Ollama",
              "kind": "openai-compatible",
              "enabled": true,
              "has_key": false,
              "local": true,
              "base_url": "http://127.0.0.1:11434/v1",
              "active": false,
              "custom": false
            }
          ]
        }
        """#.data(using: .utf8)!

        let options = try JSONDecoder().decode(ModelOptionsResponse.self, from: data)

        XCTAssertEqual(options.active.model, "gpt-4o")
        XCTAssertEqual(options.active.provider, "openai")
        XCTAssertEqual(options.providers.count, 2)
        XCTAssertEqual(options.providers[0].id, "openai")
        XCTAssertTrue(options.providers[0].hasKey)
        XCTAssertTrue(options.providers[0].active)
        XCTAssertEqual(options.providers[0].baseURL, "https://api.openai.com/v1")
        XCTAssertEqual(options.providers[0].timeoutSeconds, 60)
        XCTAssertTrue(options.providers[1].local)
        XCTAssertFalse(options.providers[1].hasKey)
    }

    func testDecodesModelListAllFixtureFromHandler() throws {
        let data = #"""
        {
          "active": {"model": "gpt-4o", "provider": "openai"},
          "models": [
            {
              "id": "gpt-4o",
              "name": "GPT-4o",
              "provider": "openai",
              "provider_label": "OpenAI",
              "context_window": 128000,
              "max_output": 16384,
              "input_cost": 2.5,
              "output_cost": 10,
              "vision": true,
              "tools": true,
              "reasoning": false
            }
          ],
          "errors": [
            {
              "provider": "ollama",
              "label": "Ollama",
              "error": "connection refused"
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(ModelListAllResponse.self, from: data)

        XCTAssertEqual(list.active.model, "gpt-4o")
        XCTAssertEqual(list.models.count, 1)
        XCTAssertEqual(list.models[0].id, "gpt-4o")
        XCTAssertEqual(list.models[0].name, "GPT-4o")
        XCTAssertEqual(list.models[0].provider, "openai")
        XCTAssertEqual(list.models[0].providerLabel, "OpenAI")
        XCTAssertEqual(list.models[0].contextWindow, 128000)
        XCTAssertTrue(list.models[0].vision)
        XCTAssertEqual(list.errors.count, 1)
        XCTAssertEqual(list.errors[0].provider, "ollama")
        XCTAssertEqual(list.errors[0].error, "connection refused")
    }

    func testEncodesModelSetBodyWithSnakeCaseProvider() throws {
        let body = ModelSetRequest(model: "gpt-4o", provider: "openai")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["model": "gpt-4o", "provider": "openai"])
    }

    func testDecodesModelSetResponseFixture() throws {
        let data = #"{"model":"gpt-4o","provider":"openai"}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(ModelSetResponse.self, from: data)

        XCTAssertEqual(response.model, "gpt-4o")
        XCTAssertEqual(response.provider, "openai")
    }

    func testDecodesToolsFixtureFromListHandler() throws {
        let data = #"""
        {
          "toolset": "coding",
          "toolsets": ["coding", "web"],
          "tools": [
            {
              "name": "read",
              "description": "Read a file",
              "enabled": true,
              "requires_approval": false,
              "toolsets": ["coding"]
            },
            {
              "name": "bash",
              "description": "Run a shell command",
              "enabled": false,
              "requires_approval": true,
              "toolsets": ["coding", "web"]
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(ToolsResponse.self, from: data)

        XCTAssertEqual(response.toolset, "coding")
        XCTAssertEqual(response.toolsets, ["coding", "web"])
        XCTAssertEqual(response.tools.count, 2)
        XCTAssertEqual(response.tools[0].name, "read")
        XCTAssertTrue(response.tools[0].enabled)
        XCTAssertFalse(response.tools[0].requiresApproval)
        XCTAssertEqual(response.tools[1].name, "bash")
        XCTAssertTrue(response.tools[1].requiresApproval)
        XCTAssertEqual(response.tools[1].toolsets, ["coding", "web"])
    }

    func testEncodesToggleToolBody() throws {
        let body = ToggleToolRequest(name: "bash", enabled: false)

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(json?["name"] as? String, "bash")
        XCTAssertEqual(json?["enabled"] as? Bool, false)
        XCTAssertEqual(json?.count, 2)
    }

    func testDecodesOkResponseFixture() throws {
        let data = #"{"ok":true}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(OKResponse.self, from: data)

        XCTAssertTrue(response.ok)
    }

    func testDecodesMemoryListFixtureFromHandler() throws {
        let data = #"""
        {
          "memories": [
            {
              "id": "mem_1",
              "scope": "global",
              "scope_key": "",
              "key": "prefs",
              "content": "Prefers concise answers",
              "tags": "[]",
              "source": "dashboard",
              "pinned": true,
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z"
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(MemoryListResponse.self, from: data)

        XCTAssertEqual(list.memories.count, 1)
        XCTAssertEqual(list.memories[0].id, "mem_1")
        XCTAssertEqual(list.memories[0].scope, "global")
        XCTAssertEqual(list.memories[0].scopeKey, "")
        XCTAssertEqual(list.memories[0].key, "prefs")
        XCTAssertEqual(list.memories[0].content, "Prefers concise answers")
        XCTAssertEqual(list.memories[0].source, "dashboard")
        XCTAssertEqual(list.memories[0].pinned, true)
        XCTAssertEqual(list.memories[0].updatedAt, "2026-09-18T00:00:01Z")
    }

    func testEncodesPutMemoryBodyMatchingHandler() throws {
        let body = PutMemoryRequest(
            id: "",
            key: "prefs",
            content: "Prefers concise answers",
            scope: "global"
        )

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(json?["id"] as? String, "")
        XCTAssertEqual(json?["key"] as? String, "prefs")
        XCTAssertEqual(json?["content"] as? String, "Prefers concise answers")
        XCTAssertEqual(json?["scope"] as? String, "global")
        XCTAssertNil(json?["pinned"])
    }
}
