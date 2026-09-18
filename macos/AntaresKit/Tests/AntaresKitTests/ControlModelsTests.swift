import XCTest
@testable import AntaresKit

final class ControlModelsTests: XCTestCase {
    func testDecodesRolesListFixtureFromHandler() throws {
        let data = #"""
        {
          "roles": [
            {
              "name": "assistant",
              "title": "Orchestrator",
              "summary": "Default agent",
              "category": "general",
              "toolset": "",
              "model": "",
              "source": "builtin"
            },
            {
              "name": "security",
              "title": "Security",
              "summary": "Authorized testing",
              "category": "security",
              "toolset": "web",
              "model": "gpt-4o",
              "danger": true,
              "subrole": false,
              "parent": "",
              "effort": "high",
              "max_turns": 12,
              "tags": ["sec"],
              "source": "local"
            }
          ],
          "scope": ["web"],
          "active": [
            {
              "id": "sub-1",
              "role": "reviewer",
              "task": "Review the diff",
              "parent": "sess_1",
              "started_at": "2026-09-18T00:00:00Z"
            }
          ],
          "performance": [
            {
              "role": "reviewer",
              "missions": 4,
              "successes": 3,
              "failures": 1,
              "kept": 2,
              "total_turns": 10,
              "score": 75.5,
              "updated_at": "2026-09-18T00:00:01Z"
            }
          ],
          "toolsets": ["coding", "web"],
          "categories": ["general", "engineering", "research", "writing", "security"]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(RolesResponse.self, from: data)

        XCTAssertEqual(response.roles.count, 2)
        XCTAssertEqual(response.roles[0].name, "assistant")
        XCTAssertEqual(response.roles[0].title, "Orchestrator")
        XCTAssertEqual(response.roles[0].source, "builtin")
        XCTAssertFalse(response.roles[0].isDanger)
        XCTAssertFalse(response.roles[0].isSubrole)
        XCTAssertEqual(response.roles[1].name, "security")
        XCTAssertTrue(response.roles[1].isDanger)
        XCTAssertEqual(response.roles[1].effort, "high")
        XCTAssertEqual(response.roles[1].maxTurns, 12)
        XCTAssertEqual(response.scope, ["web"])
        XCTAssertEqual(response.active?.count, 1)
        XCTAssertEqual(response.active?[0].id, "sub-1")
        XCTAssertEqual(response.active?[0].role, "reviewer")
        XCTAssertEqual(response.active?[0].startedAt, "2026-09-18T00:00:00Z")
        XCTAssertEqual(response.performance?.count, 1)
        XCTAssertEqual(response.performance?[0].role, "reviewer")
        XCTAssertEqual(response.performance?[0].score, 75.5)
        XCTAssertEqual(response.toolsets, ["coding", "web"])
        XCTAssertEqual(response.categories?.count, 5)
    }

    func testDecodesSessionRoleFixture() throws {
        let data = #"{"role":"reviewer"}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(SessionRoleResponse.self, from: data)

        XCTAssertEqual(response.role, "reviewer")
    }

    func testDecodesSoulFixtureFromHandler() throws {
        let data = #"{"soul":"I am Antares.","unset":false}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(SoulResponse.self, from: data)

        XCTAssertEqual(response.soul, "I am Antares.")
        XCTAssertFalse(response.unset)
    }

    func testEncodesSoulBodyMatchingHandler() throws {
        let body = SoulRequest(soul: "Be concise.")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["soul": "Be concise."])
    }

    func testDecodesSoulSaveFixture() throws {
        let data = #"{"ok":true,"unset":true}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(SoulSaveResponse.self, from: data)

        XCTAssertTrue(response.ok)
        XCTAssertTrue(response.unset)
    }

    func testDecodesSkillsListFixtureFromHandler() throws {
        let data = #"""
        {
          "skills": [
            {
              "name": "deploy",
              "description": "Ship the app",
              "path": "/tmp/deploy.md",
              "enabled": true,
              "source": "local",
              "tags": ["ops"],
              "triggers": ["deploy"],
              "updated_at": "2026-09-18T00:00:00Z",
              "usage_count": 3
            }
          ],
          "library": 12
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(SkillsResponse.self, from: data)

        XCTAssertEqual(response.library, 12)
        XCTAssertEqual(response.skills.count, 1)
        XCTAssertEqual(response.skills[0].name, "deploy")
        XCTAssertEqual(response.skills[0].description, "Ship the app")
        XCTAssertTrue(response.skills[0].enabled)
        XCTAssertEqual(response.skills[0].source, "local")
        XCTAssertEqual(response.skills[0].usageCount, 3)
        XCTAssertEqual(response.skills[0].updatedAt, "2026-09-18T00:00:00Z")
    }

    func testEncodesToggleSkillBody() throws {
        let body = ToggleToolRequest(name: "deploy", enabled: false)

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(json?["name"] as? String, "deploy")
        XCTAssertEqual(json?["enabled"] as? Bool, false)
        XCTAssertEqual(json?.count, 2)
    }

    func testDecodesHubSkillsFixtureFromHandler() throws {
        let data = #"""
        {
          "skills": [
            {
              "id": "github:acme/deploy",
              "kind": "skill",
              "name": "deploy",
              "summary": "Ship it",
              "source": "github",
              "tags": ["ops"],
              "homepage": "https://example.com",
              "author": "acme",
              "installed": false
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(HubSkillsResponse.self, from: data)

        XCTAssertEqual(response.skills.count, 1)
        XCTAssertEqual(response.skills[0].id, "github:acme/deploy")
        XCTAssertEqual(response.skills[0].name, "deploy")
        XCTAssertEqual(response.skills[0].summary, "Ship it")
        XCTAssertEqual(response.skills[0].source, "github")
        XCTAssertFalse(response.skills[0].installed)
        XCTAssertNil(response.error)
    }

    func testEncodesHubInstallBody() throws {
        let body = HubInstallRequest(id: "github:acme/deploy")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["id": "github:acme/deploy"])
    }

    func testDecodesHubInstallResultFixture() throws {
        let ok = #"{"ok":true,"name":"deploy","path":"/tmp/deploy.md","summary":"Ship it"}"#.data(using: .utf8)!
        let failed = #"{"ok":false,"error":"not found"}"#.data(using: .utf8)!

        let success = try JSONDecoder().decode(HubInstallResponse.self, from: ok)
        let failure = try JSONDecoder().decode(HubInstallResponse.self, from: failed)

        XCTAssertTrue(success.ok)
        XCTAssertEqual(success.name, "deploy")
        XCTAssertEqual(success.path, "/tmp/deploy.md")
        XCTAssertFalse(failure.ok)
        XCTAssertEqual(failure.error, "not found")
    }

    func testDecodesCronJobsFixtureFromHandler() throws {
        let data = #"""
        {
          "jobs": [
            {
              "id": "job_1",
              "name": "Morning brief",
              "schedule": "0 8 * * *",
              "prompt": "Summarize overnight mail",
              "enabled": true,
              "target": "",
              "timezone": "",
              "last_run": "2026-09-18T01:00:00Z",
              "next_run": null,
              "last_state": "ok",
              "created_at": "2026-09-17T00:00:00Z",
              "updated_at": "2026-09-18T01:00:00Z",
              "meta": {}
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(CronJobsResponse.self, from: data)

        XCTAssertEqual(response.jobs.count, 1)
        XCTAssertEqual(response.jobs[0].id, "job_1")
        XCTAssertEqual(response.jobs[0].name, "Morning brief")
        XCTAssertEqual(response.jobs[0].schedule, "0 8 * * *")
        XCTAssertEqual(response.jobs[0].prompt, "Summarize overnight mail")
        XCTAssertTrue(response.jobs[0].enabled)
        XCTAssertEqual(response.jobs[0].lastRun, "2026-09-18T01:00:00Z")
        XCTAssertNil(response.jobs[0].nextRun)
        XCTAssertEqual(response.jobs[0].lastState, "ok")
    }

    func testDecodesNullCronJobsAsEmptyList() throws {
        let data = #"{"jobs":null}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(CronJobsResponse.self, from: data)

        XCTAssertEqual(response.jobs.count, 0)
    }

    func testEncodesCreateCronBodyMatchingHandler() throws {
        let body = CreateCronRequest(name: "Morning brief", schedule: "0 8 * * *", prompt: "Summarize")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, [
            "name": "Morning brief",
            "schedule": "0 8 * * *",
            "prompt": "Summarize",
        ])
    }

    func testEncodesToggleCronBody() throws {
        let body = ToggleEnabledRequest(enabled: false)

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Bool]

        XCTAssertEqual(json, ["enabled": false])
    }

    func testDecodesCronStartedFixture() throws {
        let data = #"{"started":true}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(StartedFlagResponse.self, from: data)

        XCTAssertTrue(response.started)
    }

    func testDecodesChannelsAndPairingsFixtureFromHandler() throws {
        let data = #"""
        {
          "channels": [
            {
              "id": "telegram",
              "label": "Telegram",
              "enabled": true,
              "connected": false,
              "configured": true,
              "has_token": true,
              "bot_name": "AntaresBot",
              "detail": "Telegram bot — long polling",
              "docs": "https://t.me/BotFather",
              "fields": [
                {
                  "key": "bot_token",
                  "label": "Bot token",
                  "secret": true,
                  "set": true
                }
              ]
            }
          ],
          "pairings": [
            {
              "id": "pair_1",
              "platform": "telegram",
              "external_id": "123",
              "display_name": "Ada",
              "status": "pending",
              "code": "ABCD",
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z"
            }
          ]
        }
        """#.data(using: .utf8)!

        let response = try JSONDecoder().decode(ChannelsResponse.self, from: data)

        XCTAssertEqual(response.channels.count, 1)
        XCTAssertEqual(response.channels[0].id, "telegram")
        XCTAssertEqual(response.channels[0].label, "Telegram")
        XCTAssertTrue(response.channels[0].enabled)
        XCTAssertFalse(response.channels[0].connected)
        XCTAssertTrue(response.channels[0].configured)
        XCTAssertEqual(response.channels[0].botName, "AntaresBot")
        XCTAssertEqual(response.channels[0].fields.count, 1)
        XCTAssertEqual(response.channels[0].fields[0].key, "bot_token")
        XCTAssertTrue(response.channels[0].fields[0].secret)
        XCTAssertTrue(response.channels[0].fields[0].set)
        XCTAssertEqual(response.pairings.count, 1)
        XCTAssertEqual(response.pairings[0].id, "pair_1")
        XCTAssertEqual(response.pairings[0].platform, "telegram")
        XCTAssertEqual(response.pairings[0].displayName, "Ada")
        XCTAssertEqual(response.pairings[0].status, "pending")
        XCTAssertEqual(response.pairings[0].externalID, "123")
    }

    func testEncodesChannelTokenBody() throws {
        let body = ChannelTokenRequest(token: "123:abc")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["token": "123:abc"])
    }

    func testDecodesChannelTokenResultFixture() throws {
        let ok = #"{"ok":true,"restart_required":true}"#.data(using: .utf8)!
        let failed = #"{"ok":false,"error":"invalid token"}"#.data(using: .utf8)!

        let success = try JSONDecoder().decode(ChannelMutationResponse.self, from: ok)
        let failure = try JSONDecoder().decode(ChannelMutationResponse.self, from: failed)

        XCTAssertTrue(success.ok)
        XCTAssertEqual(success.restartRequired, true)
        XCTAssertFalse(failure.ok)
        XCTAssertEqual(failure.error, "invalid token")
    }

    func testEncodesPairingIDBody() throws {
        let body = PairingIDRequest(id: "pair_1")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["id": "pair_1"])
    }

    func testEncodesChannelConfigBody() throws {
        let body = ChannelConfigRequest(fields: ["bot_token": "123:abc"], enabled: true)

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual((json?["fields"] as? [String: String])?["bot_token"], "123:abc")
        XCTAssertEqual(json?["enabled"] as? Bool, true)
    }

    func testSignalConfigPayloadPostsApiURLAndNumber() throws {
        let payload = ChannelConfigForm.collectedFields([
            "api_url": " http://localhost:8080 ",
            "number": "+15551234567",
        ])

        XCTAssertEqual(payload, [
            "api_url": "http://localhost:8080",
            "number": "+15551234567",
        ])
    }

    func testBlankConfigValuesAreOmittedSoExistingSecretsAreKept() {
        let payload = ChannelConfigForm.collectedFields([
            "app_token": "xapp-1",
            "bot_token": "   ",
        ])

        XCTAssertEqual(payload, ["app_token": "xapp-1"])
    }

    func testWhatsAppRequiredFieldsIgnoreListenAddrAndPath() {
        let fields = [
            ChannelField(key: "token", label: "Graph API token", secret: true, set: false),
            ChannelField(key: "phone_number_id", label: "Phone number id", secret: false, set: false),
            ChannelField(key: "verify_token", label: "Webhook verify token", secret: true, set: false),
            ChannelField(key: "listen_addr", label: "Listen address", secret: false, placeholder: ":8090", set: false),
            ChannelField(key: "path", label: "Webhook path", secret: false, placeholder: "/webhook", set: false),
        ]

        XCTAssertTrue(ChannelConfigForm.hasMissingRequired(fields, values: [:]))
        XCTAssertTrue(ChannelConfigForm.hasMissingRequired(fields, values: [
            "token": "t",
            "listen_addr": ":8090",
        ]))
        XCTAssertFalse(ChannelConfigForm.hasMissingRequired(fields, values: [
            "token": "t",
            "phone_number_id": "1",
            "verify_token": "v",
        ]))
    }

    func testAlreadySetRequiredFieldIsNotMissingWhenLeftBlank() {
        let fields = [
            ChannelField(key: "homeserver", label: "Homeserver URL", secret: false, set: true),
            ChannelField(key: "user_id", label: "Bot user id", secret: false, placeholder: "@bot:matrix.org", set: false),
            ChannelField(key: "access_token", label: "Access token", secret: true, set: true),
        ]

        XCTAssertFalse(ChannelConfigForm.hasMissingRequired(fields, values: [:]))
    }

    func testSlackConfigPayloadPostsBothTokens() {
        let payload = ChannelConfigForm.collectedFields([
            "app_token": "xapp-1",
            "bot_token": "xoxb-2",
        ])

        XCTAssertEqual(payload["app_token"], "xapp-1")
        XCTAssertEqual(payload["bot_token"], "xoxb-2")
        XCTAssertEqual(payload.count, 2)
    }
}
