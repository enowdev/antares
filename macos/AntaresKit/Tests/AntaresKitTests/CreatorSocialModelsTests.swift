import XCTest
@testable import AntaresKit

final class CreatorSocialModelsTests: XCTestCase {
    func testDecodesCreatorProjectsListTreatingNullAsEmpty() throws {
        let empty = try JSONDecoder().decode(
            CreatorProjectsResponse.self,
            from: #"{"projects":null}"#.data(using: .utf8)!
        )
        let data = #"""
        {
          "projects": [
            {
              "id": "video_1",
              "title": "Launch clip",
              "brief": "Show the dashboard",
              "platform": "youtube",
              "account_id": "soc_1",
              "style": "clean",
              "size": "720x1280",
              "target_seconds": 24,
              "caption": "Ship it",
              "revision": 2,
              "publish_mode": "draft",
              "status": "draft",
              "error": "",
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z",
              "references": null,
              "shots": [],
              "research": [],
              "ideas": [],
              "final_path": "",
              "publication": {
                "status": "not_started",
                "post_url": "",
                "error": "",
                "account_id": "",
                "artifact_hash": ""
              }
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(CreatorProjectsResponse.self, from: data)

        XCTAssertEqual(empty.projects.count, 0)
        XCTAssertEqual(list.projects.count, 1)
        XCTAssertEqual(list.projects[0].id, "video_1")
        XCTAssertEqual(list.projects[0].title, "Launch clip")
        XCTAssertEqual(list.projects[0].brief, "Show the dashboard")
        XCTAssertEqual(list.projects[0].platform, "youtube")
        XCTAssertEqual(list.projects[0].accountID, "soc_1")
        XCTAssertEqual(list.projects[0].targetSeconds, 24)
        XCTAssertEqual(list.projects[0].revision, 2)
        XCTAssertEqual(list.projects[0].publishMode, "draft")
        XCTAssertEqual(list.projects[0].references.count, 0)
        XCTAssertEqual(list.projects[0].publication.status, "not_started")
    }

    func testDecodesCreatorProjectNestedMediaAndRunStatus() throws {
        let data = #"""
        {
          "id": "video_2",
          "title": "Trend recap",
          "brief": "What shipped this week",
          "platform": "tiktok",
          "account_id": "",
          "style": "bold",
          "size": "720x1280",
          "target_seconds": 16,
          "caption": "",
          "revision": 1,
          "publish_mode": "auto",
          "status": "producing",
          "error": "",
          "created_at": "2026-09-18T00:00:00Z",
          "updated_at": "2026-09-18T00:01:00Z",
          "run_session_id": "ses_creator_abc",
          "run_stage": "produce",
          "run_status": "running",
          "last_run_error": "",
          "references": [
            {
              "id": "ref_hero",
              "kind": "character",
              "name": "Hero",
              "prompt": "A founder at a desk",
              "path": "refs/hero.png",
              "status": "ready",
              "error": ""
            }
          ],
          "shots": [
            {
              "id": "shot_1",
              "prompt": "Wide of the desk",
              "reference_ids": ["ref_hero"],
              "duration_seconds": 8,
              "continuity": "cut",
              "notes": "",
              "keyframe_path": "shots/1.png",
              "video_job_id": "job_9",
              "video_path": "",
              "last_frame_path": "",
              "status": "rendering",
              "error": "",
              "progress": 40
            }
          ],
          "research": [
            {
              "url": "https://example.test/trend",
              "title": "Short form recaps",
              "platform": "tiktok",
              "observed_at": "2026-09-18T00:00:00Z",
              "notes": "High completion",
              "metrics": {"views": 1200}
            }
          ],
          "ideas": [
            {
              "id": "idea_1",
              "title": "Desk to dashboard",
              "hook": "One screen, every agent",
              "rationale": "Matches the brief",
              "selected": true
            }
          ],
          "final_path": "",
          "publication": {
            "status": "blocked",
            "post_url": "",
            "error": "login required",
            "account_id": "soc_1",
            "artifact_hash": "abc"
          }
        }
        """#.data(using: .utf8)!

        let project = try JSONDecoder().decode(CreatorProject.self, from: data)

        XCTAssertEqual(project.id, "video_2")
        XCTAssertEqual(project.runSessionID, "ses_creator_abc")
        XCTAssertEqual(project.runStage, "produce")
        XCTAssertEqual(project.runStatus, "running")
        XCTAssertEqual(project.references[0].id, "ref_hero")
        XCTAssertEqual(project.references[0].kind, "character")
        XCTAssertEqual(project.references[0].path, "refs/hero.png")
        XCTAssertEqual(project.shots[0].id, "shot_1")
        XCTAssertEqual(project.shots[0].referenceIDs, ["ref_hero"])
        XCTAssertEqual(project.shots[0].durationSeconds, 8)
        XCTAssertEqual(project.shots[0].progress, 40)
        XCTAssertEqual(project.research[0].title, "Short form recaps")
        XCTAssertEqual(project.research[0].metrics?["views"], 1200)
        XCTAssertEqual(project.ideas[0].id, "idea_1")
        XCTAssertTrue(project.ideas[0].selected)
        XCTAssertEqual(project.publication.status, "blocked")
        XCTAssertEqual(project.publication.error, "login required")
    }

    func testEncodesCreatorCreateAndUpdateBodiesMatchingHandlers() throws {
        let create = CreatorProjectDraft(
            title: "Launch clip",
            brief: "Show the dashboard",
            platform: "youtube",
            size: "720x1280",
            targetSeconds: 24,
            style: "clean",
            publishMode: "draft"
        )
        var project = try JSONDecoder().decode(
            CreatorProject.self,
            from: #"""
            {
              "id": "video_1",
              "title": "Launch clip",
              "brief": "Show the dashboard",
              "platform": "youtube",
              "account_id": "soc_1",
              "style": "clean",
              "size": "720x1280",
              "target_seconds": 24,
              "caption": "Ship it",
              "revision": 2,
              "publish_mode": "draft",
              "status": "draft",
              "error": "",
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z",
              "references": [],
              "shots": [],
              "research": [],
              "ideas": [],
              "final_path": "",
              "publication": {
                "status": "not_started",
                "post_url": "",
                "error": "",
                "account_id": "",
                "artifact_hash": ""
              }
            }
            """#.data(using: .utf8)!
        )
        project.caption = "Updated caption"

        let createJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(create)) as? [String: Any]
        let updateJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(project)) as? [String: Any]

        XCTAssertEqual(createJSON?["title"] as? String, "Launch clip")
        XCTAssertEqual(createJSON?["brief"] as? String, "Show the dashboard")
        XCTAssertEqual(createJSON?["platform"] as? String, "youtube")
        XCTAssertEqual(createJSON?["size"] as? String, "720x1280")
        XCTAssertEqual(createJSON?["target_seconds"] as? Int, 24)
        XCTAssertEqual(createJSON?["style"] as? String, "clean")
        XCTAssertEqual(createJSON?["publish_mode"] as? String, "draft")
        XCTAssertEqual(updateJSON?["id"] as? String, "video_1")
        XCTAssertEqual(updateJSON?["revision"] as? Int, 2)
        XCTAssertEqual(updateJSON?["account_id"] as? String, "soc_1")
        XCTAssertEqual(updateJSON?["caption"] as? String, "Updated caption")
        XCTAssertEqual(updateJSON?["target_seconds"] as? Int, 24)
    }

    func testEncodesCreatorActionRunAndReconcileBodies() throws {
        let action = CreatorActionRequest(action: "generate_video", targetID: "shot_1")
        let reset = CreatorActionRequest(action: "reset_shot", targetID: "shot_1", confirmed: true)
        let run = CreatorRunRequest(stage: "produce", publishMode: "draft")
        let reconcile = CreatorReconcileRequest(proof: "Checked the account", notPublished: true)

        let actionJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(action)) as? [String: Any]
        let resetJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(reset)) as? [String: Any]
        let runJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(run)) as? [String: String]
        let reconcileJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(reconcile)) as? [String: Any]

        XCTAssertEqual(actionJSON?["action"] as? String, "generate_video")
        XCTAssertEqual(actionJSON?["target_id"] as? String, "shot_1")
        XCTAssertNil(actionJSON?["confirmed"])
        XCTAssertEqual(resetJSON?["action"] as? String, "reset_shot")
        XCTAssertEqual(resetJSON?["confirmed"] as? Bool, true)
        XCTAssertEqual(runJSON, ["stage": "produce", "publish_mode": "draft"])
        XCTAssertEqual(reconcileJSON?["proof"] as? String, "Checked the account")
        XCTAssertEqual(reconcileJSON?["not_published"] as? Bool, true)
    }

    func testDecodesCreatorRunAndSettingsFixtures() throws {
        let runData = #"{"session_id":"ses_creator_abc"}"#.data(using: .utf8)!
        let settingsData = #"""
        {
          "image": {
            "enabled": true,
            "provider": "openai",
            "model": "gpt-image-1",
            "base_url": "https://api.openai.com/v1",
            "size": "1024x1024",
            "has_key": true
          },
          "video": {
            "enabled": false,
            "provider": "",
            "model": "sora-2",
            "base_url": "",
            "size": "720x1280",
            "seconds": 8,
            "has_key": false
          },
          "ffmpeg": true,
          "ffprobe": false
        }
        """#.data(using: .utf8)!

        let run = try JSONDecoder().decode(CreatorRunResponse.self, from: runData)
        let settings = try JSONDecoder().decode(CreatorSettings.self, from: settingsData)
        let patch = CreatorSettingsPatch(
            image: CreatorProviderPatch(enabled: true, model: "gpt-image-1", apiKey: "sk-test"),
            video: CreatorProviderPatch(seconds: 8)
        )
        let patchJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(patch)) as? [String: Any]
        let image = patchJSON?["image"] as? [String: Any]

        XCTAssertEqual(run.sessionID, "ses_creator_abc")
        XCTAssertTrue(settings.image.enabled)
        XCTAssertEqual(settings.image.provider, "openai")
        XCTAssertEqual(settings.image.model, "gpt-image-1")
        XCTAssertTrue(settings.image.hasKey)
        XCTAssertFalse(settings.video.enabled)
        XCTAssertEqual(settings.video.seconds, 8)
        XCTAssertTrue(settings.ffmpeg)
        XCTAssertFalse(settings.ffprobe)
        XCTAssertEqual(image?["enabled"] as? Bool, true)
        XCTAssertEqual(image?["model"] as? String, "gpt-image-1")
        XCTAssertEqual(image?["api_key"] as? String, "sk-test")
        XCTAssertEqual((patchJSON?["video"] as? [String: Any])?["seconds"] as? Int, 8)
    }

    func testEncodesCreatorCronJobBodyMatchingHandler() throws {
        let body = CreatorCronJobRequest(
            name: "Morning research",
            schedule: "0 8 * * *",
            role: "content-creator",
            contentProjectID: "video_1",
            contentStage: "research",
            publishMode: "draft"
        )

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json?["name"], "Morning research")
        XCTAssertEqual(json?["schedule"], "0 8 * * *")
        XCTAssertEqual(json?["role"], "content-creator")
        XCTAssertEqual(json?["content_project_id"], "video_1")
        XCTAssertEqual(json?["content_stage"], "research")
        XCTAssertEqual(json?["publish_mode"], "draft")
        XCTAssertNil(json?["prompt"])
    }

    func testDecodesSocialStatusAndAccountsFixtures() throws {
        let statusData = #"""
        {
          "enabled": true,
          "encryption_ready": true,
          "imap_configured": true,
          "imap_host": "imap.gmail.com",
          "imap_port": 993,
          "imap_username": "ops@example.test",
          "browser": {"enabled": true, "state": "running", "error": ""},
          "autopilot_enabled": false,
          "accounts": [
            {
              "id": "soc_1",
              "platform": "instagram",
              "display_name": "Ops",
              "username": "antares",
              "profile_url": "https://instagram.com/antares",
              "status": "connected",
              "rag_namespace": "social/instagram",
              "skill_name": "social-instagram",
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z",
              "has_password": true,
              "has_recovery": false
            }
          ]
        }
        """#.data(using: .utf8)!
        let emptyAccounts = try JSONDecoder().decode(
            SocialStatus.self,
            from: #"""
            {
              "enabled": false,
              "encryption_ready": false,
              "imap_configured": false,
              "imap_host": "",
              "imap_port": 0,
              "imap_username": "",
              "browser": {"enabled": false, "state": "disabled", "error": ""},
              "autopilot_enabled": false,
              "accounts": null
            }
            """#.data(using: .utf8)!
        )
        let listData = #"""
        [
          {
            "id": "soc_2",
            "platform": "youtube",
            "display_name": "",
            "username": "studio",
            "profile_url": "",
            "status": "connected",
            "rag_namespace": "social/youtube",
            "skill_name": "social-youtube",
            "created_at": "2026-09-18T00:00:00Z",
            "updated_at": "2026-09-18T00:00:00Z",
            "has_password": false,
            "has_recovery": true
          }
        ]
        """#.data(using: .utf8)!

        let status = try JSONDecoder().decode(SocialStatus.self, from: statusData)
        let accounts = try JSONDecoder().decode([SocialAccount].self, from: listData)

        XCTAssertTrue(status.enabled)
        XCTAssertTrue(status.encryptionReady)
        XCTAssertTrue(status.imapConfigured)
        XCTAssertEqual(status.imapHost, "imap.gmail.com")
        XCTAssertEqual(status.imapPort, 993)
        XCTAssertEqual(status.imapUsername, "ops@example.test")
        XCTAssertEqual(status.browser.state, "running")
        XCTAssertFalse(status.autopilotEnabled)
        XCTAssertEqual(status.accounts.count, 1)
        XCTAssertEqual(status.accounts[0].id, "soc_1")
        XCTAssertEqual(status.accounts[0].platform, "instagram")
        XCTAssertEqual(status.accounts[0].displayName, "Ops")
        XCTAssertTrue(status.accounts[0].hasPassword)
        XCTAssertEqual(emptyAccounts.accounts.count, 0)
        XCTAssertEqual(accounts.count, 1)
        XCTAssertEqual(accounts[0].id, "soc_2")
        XCTAssertEqual(accounts[0].username, "studio")
        XCTAssertTrue(accounts[0].hasRecovery)
    }

    func testEncodesSocialRequestBodiesMatchingHandlers() throws {
        let imap = SocialIMAPRequest(
            host: "imap.gmail.com",
            port: 993,
            username: "ops@example.test",
            password: "app-pass"
        )
        let account = SocialAccountRequest(
            platform: "instagram",
            username: "antares",
            password: "secret",
            recoveryCodes: "abc",
            profileURL: "https://instagram.com/antares",
            status: "connected"
        )
        let autopilot = SocialAutopilotRequest(enabled: true)

        let imapJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(imap)) as? [String: Any]
        let accountJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(account)) as? [String: String]
        let autopilotJSON = try JSONSerialization.jsonObject(with: JSONEncoder().encode(autopilot)) as? [String: Bool]

        XCTAssertEqual(imapJSON?["host"] as? String, "imap.gmail.com")
        XCTAssertEqual(imapJSON?["port"] as? Int, 993)
        XCTAssertEqual(imapJSON?["username"] as? String, "ops@example.test")
        XCTAssertEqual(imapJSON?["password"] as? String, "app-pass")
        XCTAssertEqual(accountJSON?["platform"], "instagram")
        XCTAssertEqual(accountJSON?["username"], "antares")
        XCTAssertEqual(accountJSON?["password"], "secret")
        XCTAssertEqual(accountJSON?["recovery_codes"], "abc")
        XCTAssertEqual(accountJSON?["profile_url"], "https://instagram.com/antares")
        XCTAssertEqual(accountJSON?["status"], "connected")
        XCTAssertEqual(autopilotJSON, ["enabled": true])
    }

    func testDecodesSocialEncryptionAndIMAPTestFixtures() throws {
        let keyData = #"{"ok":true,"recovery_key":"abc123","message":"Save this recovery key"}"#.data(using: .utf8)!
        let okData = #"{"ok":true,"inbox_count":4}"#.data(using: .utf8)!
        let failData = #"{"ok":false,"error":"auth failed"}"#.data(using: .utf8)!

        let key = try JSONDecoder().decode(SocialEncryptionSetupResponse.self, from: keyData)
        let ok = try JSONDecoder().decode(SocialIMAPTestResponse.self, from: okData)
        let fail = try JSONDecoder().decode(SocialIMAPTestResponse.self, from: failData)

        XCTAssertTrue(key.ok)
        XCTAssertEqual(key.recoveryKey, "abc123")
        XCTAssertTrue(ok.ok)
        XCTAssertEqual(ok.inboxCount, 4)
        XCTAssertFalse(fail.ok)
        XCTAssertEqual(fail.error, "auth failed")
    }
}
