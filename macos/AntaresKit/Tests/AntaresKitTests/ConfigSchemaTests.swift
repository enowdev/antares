import XCTest
@testable import AntaresKit

final class ConfigSchemaTests: XCTestCase {
    func testUnknownFieldTypeFallsBackToStringField() throws {
        let data = #"""
        {
          "path": "display.palette",
          "label": "Palette",
          "group": "display",
          "type": "color-palette",
          "tier": "advanced",
          "default": "default",
          "secret": false
        }
        """#.data(using: .utf8)!

        let field = try JSONDecoder().decode(ConfigField.self, from: data)

        XCTAssertEqual(field.type, .string)
        XCTAssertEqual(field.path, "display.palette")
        XCTAssertEqual(field.label, "Palette")
        XCTAssertEqual(field.group, "display")
    }

    func testDecodesKnownFieldTypesWithoutCrashing() throws {
        let data = #"""
        [
          {
            "path": "model.default",
            "label": "Default",
            "group": "model",
            "type": "string",
            "tier": "essential",
            "default": "gpt-4o",
            "secret": false,
            "help": "Model id",
            "reload": "restart_required"
          },
          {
            "path": "server.port",
            "label": "Port",
            "group": "server",
            "type": "number",
            "tier": "essential",
            "default": 8787,
            "secret": false
          },
          {
            "path": "rag.enabled",
            "label": "Enabled",
            "group": "rag",
            "type": "boolean",
            "tier": "essential",
            "default": true,
            "secret": false
          },
          {
            "path": "tools.toolset",
            "label": "Toolset",
            "group": "tools",
            "type": "string[]",
            "tier": "common",
            "default": ["web", "files"],
            "secret": false,
            "enum": ["web", "files", "shell"]
          },
          {
            "path": "providers",
            "label": "Providers",
            "group": "providers",
            "type": "object",
            "tier": "advanced",
            "default": {},
            "secret": true
          }
        ]
        """#.data(using: .utf8)!

        let fields = try JSONDecoder().decode([ConfigField].self, from: data)

        XCTAssertEqual(fields.map(\.type), [.string, .number, .boolean, .stringArray, .object])
        XCTAssertEqual(fields[0].help, "Model id")
        XCTAssertEqual(fields[0].reload, "restart_required")
        XCTAssertEqual(fields[1].defaultValue?.numberValue, 8787)
        XCTAssertEqual(fields[2].defaultValue?.boolValue, true)
        XCTAssertEqual(fields[3].enumValues, ["web", "files", "shell"])
        XCTAssertTrue(fields[4].secret)
    }

    func testDecodesConfigResponseValuesAndSchemaTogether() throws {
        let data = #"""
        {
          "values": {
            "model": { "default": "gpt-4o", "provider": "openai" },
            "server": { "port": 8787 }
          },
          "schema": [
            {
              "path": "model.default",
              "label": "Default",
              "group": "model",
              "type": "string",
              "tier": "essential",
              "default": "",
              "secret": false
            },
            {
              "path": "weird.flag",
              "label": "Flag",
              "group": "weird",
              "type": "mystery",
              "tier": "advanced",
              "default": "nope",
              "secret": false
            }
          ],
          "restart_fields": ["server.port"],
          "profile": "default",
          "path": "/tmp/config.yaml"
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(ConfigResponse.self, from: data)

        XCTAssertEqual(response.schema.count, 2)
        XCTAssertEqual(response.schema[1].type, .string)
        XCTAssertEqual(response.restartFields, ["server.port"])
        XCTAssertEqual(response.profile, "default")
        XCTAssertEqual(response.path, "/tmp/config.yaml")
        XCTAssertEqual(response.value(at: "model.default")?.stringValue, "gpt-4o")
        XCTAssertEqual(response.value(at: "server.port")?.numberValue, 8787)
        XCTAssertNil(response.value(at: "missing.key"))
    }

    func testUpdateRequestEncodesDottedPathUpdates() throws {
        let request = ConfigUpdateRequest(updates: [
            "model.default": .string("claude"),
            "server.port": .int(9090),
            "rag.enabled": .bool(true),
            "tools.toolset": .strings(["web", "files"]),
        ])

        let data = try JSONEncoder().encode(request)
        let object = try XCTUnwrap(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let updates = try XCTUnwrap(object["updates"] as? [String: Any])

        XCTAssertEqual(updates["model.default"] as? String, "claude")
        XCTAssertEqual(updates["server.port"] as? Int, 9090)
        XCTAssertEqual(updates["rag.enabled"] as? Bool, true)
        XCTAssertEqual(updates["tools.toolset"] as? [String], ["web", "files"])
    }

    func testRawConfigBodiesMatchGoHandlers() throws {
        let raw = try JSONDecoder().decode(
            ConfigRawResponse.self,
            from: #"{"yaml":"model:\n  default: gpt-4o\n","path":"/tmp/config.yaml"}"#.data(using: .utf8)!
        )
        XCTAssertTrue(raw.yaml.contains("gpt-4o"))
        XCTAssertEqual(raw.path, "/tmp/config.yaml")

        let save = try JSONEncoder().encode(ConfigRawRequest(yaml: "model: {}\n"))
        let saveObject = try XCTUnwrap(JSONSerialization.jsonObject(with: save) as? [String: Any])
        XCTAssertEqual(saveObject["yaml"] as? String, "model: {}\n")

        let saved = try JSONDecoder().decode(
            ConfigRawSaveResponse.self,
            from: #"{"saved":true}"#.data(using: .utf8)!
        )
        XCTAssertEqual(saved.saved, true)
    }
}
