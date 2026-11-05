import XCTest
@testable import AntaresKit

final class IntegrationModelsTests: XCTestCase {
    func testDecodesInterceptStatusFixtureFromHandler() throws {
        let data = #"{"running":true,"addr":"127.0.0.1:8899","exchanges":3,"rules":1}"#.data(using: .utf8)!

        let status = try JSONDecoder().decode(InterceptStatus.self, from: data)

        XCTAssertTrue(status.running)
        XCTAssertEqual(status.addr, "127.0.0.1:8899")
        XCTAssertEqual(status.exchanges, 3)
        XCTAssertEqual(status.rules, 1)
    }

    func testEncodesInterceptStartBodyMatchingHandler() throws {
        let body = InterceptStartRequest(port: 8899)

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Int]

        XCTAssertEqual(json, ["port": 8899])
    }

    func testDecodesInterceptExchangesTreatingNullAsEmpty() throws {
        let empty = try JSONDecoder().decode(
            InterceptExchangesResponse.self,
            from: #"{"exchanges":null}"#.data(using: .utf8)!
        )
        let data = #"""
        {
          "exchanges": [
            {
              "id": 12,
              "time": "2026-09-18T00:00:00Z",
              "method": "GET",
              "url": "https://example.test/login",
              "host": "example.test",
              "secure": true,
              "req_headers": {"Accept": ["application/json"]},
              "req_body": "",
              "status": 200,
              "resp_headers": {"Content-Type": ["application/json"]},
              "resp_body": "{\"ok\":true}",
              "duration_ms": 42,
              "mocked": false,
              "blocked": false
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(InterceptExchangesResponse.self, from: data)

        XCTAssertEqual(empty.exchanges.count, 0)
        XCTAssertEqual(list.exchanges.count, 1)
        XCTAssertEqual(list.exchanges[0].id, 12)
        XCTAssertEqual(list.exchanges[0].method, "GET")
        XCTAssertEqual(list.exchanges[0].url, "https://example.test/login")
        XCTAssertEqual(list.exchanges[0].status, 200)
        XCTAssertEqual(list.exchanges[0].durationMS, 42)
        XCTAssertEqual(list.exchanges[0].reqHeaders["Accept"], ["application/json"])
        XCTAssertEqual(list.exchanges[0].respBody, "{\"ok\":true}")
        XCTAssertTrue(list.exchanges[0].secure)
        XCTAssertFalse(list.exchanges[0].mocked)
        XCTAssertFalse(list.exchanges[0].blocked)
    }

    func testDecodesInterceptRulesAndEncodesAddBody() throws {
        let data = #"""
        {
          "rules": [
            {
              "id": 3,
              "match": "/admin",
              "block": true,
              "mock_status": 403,
              "mock_body": "nope",
              "breakpoint": false
            }
          ]
        }
        """#.data(using: .utf8)!
        let body = InterceptRuleRequest(match: "/admin", block: true, mockStatus: 403, mockBody: "nope")

        let list = try JSONDecoder().decode(InterceptRulesResponse.self, from: data)
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(list.rules.count, 1)
        XCTAssertEqual(list.rules[0].id, 3)
        XCTAssertEqual(list.rules[0].match, "/admin")
        XCTAssertTrue(list.rules[0].block)
        XCTAssertEqual(list.rules[0].mockStatus, 403)
        XCTAssertEqual(list.rules[0].mockBody, "nope")
        XCTAssertEqual(json?["match"] as? String, "/admin")
        XCTAssertEqual(json?["block"] as? Bool, true)
        XCTAssertEqual(json?["mock_status"] as? Int, 403)
        XCTAssertEqual(json?["mock_body"] as? String, "nope")
    }

    func testDecodesNullInterceptRulesAsEmptyList() throws {
        let response = try JSONDecoder().decode(
            InterceptRulesResponse.self,
            from: #"{"rules":null}"#.data(using: .utf8)!
        )

        XCTAssertEqual(response.rules.count, 0)
    }

    func testDecodesInterceptorsAndActivateSessionEnv() throws {
        let data = #"""
        {
          "interceptors": [
            {
              "id": "terminal",
              "label": "Terminal",
              "category": "system",
              "available": true
            },
            {
              "id": "chrome",
              "label": "Chrome",
              "category": "browser",
              "available": false,
              "reason": "not installed"
            }
          ],
          "sessions": [
            {
              "id": "sess_term",
              "interceptor": "terminal",
              "info": {"env": "export HTTP_PROXY=http://127.0.0.1:8899"}
            }
          ]
        }
        """#.data(using: .utf8)!
        let activate = InterceptActivateRequest(interceptor: "terminal")

        let response = try JSONDecoder().decode(InterceptorsResponse.self, from: data)
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(activate)) as? [String: String]

        XCTAssertEqual(response.interceptors.count, 2)
        XCTAssertEqual(response.interceptors[0].id, "terminal")
        XCTAssertTrue(response.interceptors[0].available)
        XCTAssertEqual(response.interceptors[1].reason, "not installed")
        XCTAssertEqual(response.sessions.count, 1)
        XCTAssertEqual(response.sessions[0].id, "sess_term")
        XCTAssertEqual(response.sessions[0].envText, "export HTTP_PROXY=http://127.0.0.1:8899")
        XCTAssertEqual(json, ["interceptor": "terminal"])
    }

    func testDecodesPausedBreakpointsFromHandlerAndSSEPayload() throws {
        let data = #"""
        {
          "paused": [
            {
              "id": 7,
              "method": "POST",
              "url": "https://example.test/login",
              "headers": {"content-type": "application/json"},
              "body": "{\"user\":\"ada\"}"
            }
          ]
        }
        """#.data(using: .utf8)!
        let compact = #"{"paused":[{"id":7,"method":"POST","url":"https://example.test/login","headers":{"content-type":"application/json"},"body":"{\"user\":\"ada\"}"}]}"#
        let raw = "data: \(compact)\n\n"

        let list = try JSONDecoder().decode(InterceptPausedList.self, from: data)
        var snapshots: [InterceptPausedList] = []
        try SSEParser.parseJSON(Data(raw.utf8), as: InterceptPausedList.self) { event in
            snapshots.append(event)
        }

        XCTAssertEqual(list.paused.count, 1)
        XCTAssertEqual(list.paused[0].id, 7)
        XCTAssertEqual(list.paused[0].method, "POST")
        XCTAssertEqual(list.paused[0].url, "https://example.test/login")
        XCTAssertEqual(list.paused[0].headers["content-type"], "application/json")
        XCTAssertEqual(list.paused[0].body, "{\"user\":\"ada\"}")
        XCTAssertEqual(snapshots.count, 1)
        XCTAssertEqual(snapshots[0].paused[0].id, 7)
    }

    func testDecodesNullPausedBreakpointsAsEmptyList() throws {
        let list = try JSONDecoder().decode(
            InterceptPausedList.self,
            from: #"{"paused":null}"#.data(using: .utf8)!
        )

        XCTAssertEqual(list.paused.count, 0)
    }

    func testDecodesMCPStatusTreatingNullToolsAsEmpty() throws {
        let data = #"""
        {
          "enabled": true,
          "servers": [
            {
              "name": "filesystem",
              "started": true,
              "connected": true,
              "tools": [
                {"name": "read_file", "description": "Read a file"}
              ]
            },
            {
              "name": "broken",
              "started": false,
              "connected": false,
              "error": "spawn failed",
              "tools": null
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(MCPStatusResponse.self, from: data)

        XCTAssertTrue(response.enabled)
        XCTAssertEqual(response.servers.count, 2)
        XCTAssertEqual(response.servers[0].name, "filesystem")
        XCTAssertTrue(response.servers[0].connected)
        XCTAssertEqual(response.servers[0].tools.count, 1)
        XCTAssertEqual(response.servers[0].tools[0].name, "read_file")
        XCTAssertEqual(response.servers[1].error, "spawn failed")
        XCTAssertEqual(response.servers[1].tools.count, 0)
    }

    func testDecodesNullMCPServersAsEmptyList() throws {
        let response = try JSONDecoder().decode(
            MCPStatusResponse.self,
            from: #"{"enabled":false,"servers":null}"#.data(using: .utf8)!
        )

        XCTAssertFalse(response.enabled)
        XCTAssertEqual(response.servers.count, 0)
    }

    func testEncodesAddMCPStdioAndHTTPBodies() throws {
        let stdio = AddMCPServerRequest(
            name: "filesystem",
            transport: "stdio",
            command: "npx",
            args: ["-y", "@modelcontextprotocol/server-filesystem"],
            env: ["FOO": "bar"],
            url: nil
        )
        let http = AddMCPServerRequest(
            name: "remote",
            transport: "http",
            command: nil,
            args: nil,
            env: nil,
            url: "https://example.test/mcp"
        )

        let stdioJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(stdio)) as? [String: Any]
        let httpJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(http)) as? [String: Any]

        XCTAssertEqual(stdioJSON?["name"] as? String, "filesystem")
        XCTAssertEqual(stdioJSON?["transport"] as? String, "stdio")
        XCTAssertEqual(stdioJSON?["command"] as? String, "npx")
        XCTAssertEqual(stdioJSON?["args"] as? [String], ["-y", "@modelcontextprotocol/server-filesystem"])
        XCTAssertEqual((stdioJSON?["env"] as? [String: String])?["FOO"], "bar")
        XCTAssertNil(stdioJSON?["url"])
        XCTAssertEqual(httpJSON?["name"] as? String, "remote")
        XCTAssertEqual(httpJSON?["transport"] as? String, "http")
        XCTAssertEqual(httpJSON?["url"] as? String, "https://example.test/mcp")
        XCTAssertNil(httpJSON?["command"])
    }

    func testDecodesHubMCPServersFixture() throws {
        let data = #"""
        {
          "servers": [
            {
              "id": "builtin/github",
              "kind": "mcp",
              "name": "github",
              "summary": "GitHub tools",
              "source": "builtin",
              "tags": ["git"],
              "command": "npx",
              "args": ["-y", "@modelcontextprotocol/server-github"],
              "needs_keys": ["GITHUB_TOKEN"],
              "setup": "export GITHUB_TOKEN",
              "installed": false
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(HubMCPResponse.self, from: data)

        XCTAssertEqual(response.servers.count, 1)
        XCTAssertEqual(response.servers[0].id, "builtin/github")
        XCTAssertEqual(response.servers[0].name, "github")
        XCTAssertEqual(response.servers[0].command, "npx")
        XCTAssertEqual(response.servers[0].needsKeys, ["GITHUB_TOKEN"])
        XCTAssertFalse(response.servers[0].installed)
        XCTAssertNil(response.error)
    }

    func testDecodesHubInstallMCPMissingKeys() throws {
        let data = #"{"ok":true,"missing_keys":["GITHUB_TOKEN"]}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(HubInstallResponse.self, from: data)

        XCTAssertTrue(response.ok)
        XCTAssertEqual(response.missingKeys, ["GITHUB_TOKEN"])
    }

    func testDecodesPluginsListFixtureFromHandler() throws {
        let data = #"""
        {
          "enabled": true,
          "plugins": [
            {
              "name": "audit",
              "description": "Log every terminal command",
              "version": "1.0",
              "command": "./run.sh",
              "hooks": ["pre_tool_call"],
              "dir": "/tmp/plugins/audit",
              "enabled": true
            },
            {
              "name": "broken",
              "description": "",
              "version": "",
              "command": "./missing.sh",
              "dir": "/tmp/plugins/broken",
              "enabled": false,
              "error": "executable not found"
            }
          ],
          "dirs": ["/tmp/plugins"]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(PluginsResponse.self, from: data)

        XCTAssertTrue(response.enabled)
        XCTAssertEqual(response.dirs, ["/tmp/plugins"])
        XCTAssertEqual(response.plugins.count, 2)
        XCTAssertEqual(response.plugins[0].name, "audit")
        XCTAssertEqual(response.plugins[0].hooks, ["pre_tool_call"])
        XCTAssertTrue(response.plugins[0].enabled)
        XCTAssertEqual(response.plugins[1].error, "executable not found")
    }

    func testDecodesNullPluginsAsEmptyList() throws {
        let response = try JSONDecoder().decode(
            PluginsResponse.self,
            from: #"{"enabled":false,"plugins":null}"#.data(using: .utf8)!
        )

        XCTAssertFalse(response.enabled)
        XCTAssertEqual(response.plugins.count, 0)
        XCTAssertEqual(response.dirs, [])
    }

    func testEncodesAddPluginAndToggleBodies() throws {
        let add = AddPluginRequest(
            name: "audit",
            description: "Log commands",
            command: "./run.sh",
            args: ["--verbose"],
            hooks: ["pre_tool_call", "turn_end"]
        )
        let toggle = ToggleEnabledRequest(enabled: false)

        let addJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(add)) as? [String: Any]
        let toggleJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(toggle)) as? [String: Bool]

        XCTAssertEqual(addJSON?["name"] as? String, "audit")
        XCTAssertEqual(addJSON?["description"] as? String, "Log commands")
        XCTAssertEqual(addJSON?["command"] as? String, "./run.sh")
        XCTAssertEqual(addJSON?["args"] as? [String], ["--verbose"])
        XCTAssertEqual(addJSON?["hooks"] as? [String], ["pre_tool_call", "turn_end"])
        XCTAssertEqual(toggleJSON, ["enabled": false])
    }

    func testDecodesHubPluginsFixture() throws {
        let data = #"""
        {
          "plugins": [
            {
              "id": "builtin/audit",
              "name": "audit",
              "summary": "Log tool calls",
              "command": "./run.sh",
              "hooks": ["pre_tool_call"],
              "installed": true
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(HubPluginsResponse.self, from: data)

        XCTAssertEqual(response.plugins.count, 1)
        XCTAssertEqual(response.plugins[0].id, "builtin/audit")
        XCTAssertEqual(response.plugins[0].name, "audit")
        XCTAssertEqual(response.plugins[0].command, "./run.sh")
        XCTAssertEqual(response.plugins[0].hooks, ["pre_tool_call"])
        XCTAssertTrue(response.plugins[0].installed)
    }

    func testDecodesProxyListFixtureFromHandler() throws {
        let data = #"""
        {
          "entries": [
            {
              "id": "px_1",
              "label": "office",
              "scheme": "http",
              "host": "proxy.example.test",
              "port": 8080,
              "username": "ada",
              "password": "••••••••",
              "url": "http://ada:••••@proxy.example.test:8080"
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(ProxyListResponse.self, from: data)

        XCTAssertEqual(response.entries.count, 1)
        XCTAssertEqual(response.entries[0].id, "px_1")
        XCTAssertEqual(response.entries[0].label, "office")
        XCTAssertEqual(response.entries[0].scheme, "http")
        XCTAssertEqual(response.entries[0].host, "proxy.example.test")
        XCTAssertEqual(response.entries[0].port, 8080)
        XCTAssertEqual(response.entries[0].username, "ada")
        XCTAssertEqual(response.entries[0].url, "http://ada:••••@proxy.example.test:8080")
    }

    func testDecodesNullProxyEntriesAsEmptyList() throws {
        let response = try JSONDecoder().decode(
            ProxyListResponse.self,
            from: #"{"entries":null}"#.data(using: .utf8)!
        )

        XCTAssertEqual(response.entries.count, 0)
    }

    func testEncodesProxyBatchAndTestBodies() throws {
        let batch = ProxyBatchRequest(text: "host:8080\nsocks5://example.test:1080")
        let test = ProxyTestRequest(id: "px_1")

        let batchJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(batch)) as? [String: String]
        let testJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(test)) as? [String: String]

        XCTAssertEqual(batchJSON, ["text": "host:8080\nsocks5://example.test:1080"])
        XCTAssertEqual(testJSON, ["id": "px_1"])
    }

    func testDecodesProxyBatchAndTestResults() throws {
        let batchData = #"""
        {
          "ok": true,
          "added": 2,
          "failed": [
            {"line": 3, "text": "bad", "error": "missing host"}
          ]
        }
        """#.data(using: .utf8)!
        let testOK = #"{"ok":true,"ip":"203.0.113.9"}"#.data(using: .utf8)!
        let testFail = #"{"ok":false,"error":"dial timeout"}"#.data(using: .utf8)!

        let batch = try JSONDecoder().decode(ProxyBatchResponse.self, from: batchData)
        let ok = try JSONDecoder().decode(ProxyTestResponse.self, from: testOK)
        let fail = try JSONDecoder().decode(ProxyTestResponse.self, from: testFail)

        XCTAssertTrue(batch.ok)
        XCTAssertEqual(batch.added, 2)
        XCTAssertEqual(batch.failed.count, 1)
        XCTAssertEqual(batch.failed[0].line, 3)
        XCTAssertEqual(batch.failed[0].text, "bad")
        XCTAssertTrue(ok.ok)
        XCTAssertEqual(ok.ip, "203.0.113.9")
        XCTAssertFalse(fail.ok)
        XCTAssertEqual(fail.error, "dial timeout")
    }

    func testParsesMCPEnvLinesAndRejectsInvalidKeys() {
        XCTAssertEqual(
            MCPEnvParser.parse("FOO=bar\n# comment\nBAZ=qux=extra"),
            .ok(["FOO": "bar", "BAZ": "qux=extra"])
        )
        XCTAssertEqual(MCPEnvParser.parse("1BAD=nope"), .invalid)
        XCTAssertEqual(MCPEnvParser.parse(""), .ok([:]))
    }
}
