import XCTest
@testable import AntaresKit

final class AuthSetupModelsTests: XCTestCase {
    func testDecodesAuthStatusFixture() throws {
        let data = #"{"password_required":true,"authenticated":false}"#.data(using: .utf8)!

        let status = try JSONDecoder().decode(AuthStatus.self, from: data)

        XCTAssertTrue(status.passwordRequired)
        XCTAssertFalse(status.authenticated)
    }

    func testDecodesSetupStatusWithProvidersFixture() throws {
        let data = #"""
        {
          "needs_setup": true,
          "model": "",
          "provider": "",
          "workspace": "/Users/example/antares",
          "home": "/Users/example",
          "config_path": "/Users/example/.antares/config.json",
          "database": "sqlite",
          "providers": [
            {
              "id": "openrouter",
              "label": "OpenRouter",
              "kind": "openai-compatible",
              "hint": "One key, most models",
              "key_hint": "sk-or-v1-...",
              "key_url": "https://openrouter.ai/keys",
              "base_url": "https://openrouter.ai/api/v1",
              "local": false,
              "models": ["anthropic/claude-sonnet-4.5"],
              "has_key": false
            }
          ]
        }
        """#.data(using: .utf8)!

        let status = try JSONDecoder().decode(SetupStatus.self, from: data)

        XCTAssertTrue(status.needsSetup)
        XCTAssertEqual(status.workspace, "/Users/example/antares")
        XCTAssertEqual(status.database, "sqlite")
        XCTAssertEqual(status.providers.first?.id, "openrouter")
        XCTAssertEqual(status.providers.first?.keyHint, "sk-or-v1-...")
        XCTAssertEqual(status.providers.first?.models, ["anthropic/claude-sonnet-4.5"])
        XCTAssertFalse(status.providers.first?.hasKey ?? true)
    }

    func testLoginRequestEncodesPasswordKey() throws {
        let data = try JSONEncoder().encode(LoginRequest(password: "secret"))
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])

        XCTAssertEqual(object["password"] as? String, "secret")
        XCTAssertNil(object["apiKey"])
    }

    func testCompleteSetupRequestEncodesHeadersWhenPresent() throws {
        let request = CompleteSetupRequest(
            provider: "custom",
            name: "Local Gateway",
            baseURL: "http://127.0.0.1:8080/v1",
            apiKey: "",
            headers: ["X-API-Key": "secret"],
            model: "local-model",
            workspace: "/tmp/antares",
            database: SetupDatabaseRequest(),
            rag: SetupRAGRequest()
        )

        let data = try JSONEncoder().encode(request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let headers = try XCTUnwrap(object["headers"] as? [String: String])

        XCTAssertEqual(headers["X-API-Key"], "secret")
        XCTAssertEqual(object["base_url"] as? String, "http://127.0.0.1:8080/v1")
        XCTAssertEqual(object["api_key"] as? String, "")
        XCTAssertNil(object["baseURL"])
        XCTAssertNil(object["apiKey"])
    }
}
