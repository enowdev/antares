import XCTest
@testable import AntaresKit

final class WorkflowModelsTests: XCTestCase {
    func testDecodesAutopilotCardsFixtureFromListHandler() throws {
        let data = #"""
        {
          "cards": [
            {
              "id": "ap_1",
              "title": "Add board screen",
              "prompt": "Implement Task 12",
              "status": "pending",
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z"
            },
            {
              "id": "ap_2",
              "title": "Verify runner",
              "prompt": "Run tests",
              "status": "failed",
              "result": "partial",
              "verify_output": "exit 1",
              "branch": "autopilot/ap_2",
              "pr": "https://example.test/pr/2",
              "error": "verify failed",
              "created_at": "2026-09-18T00:00:02Z",
              "updated_at": "2026-09-18T00:00:03Z"
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(AutopilotCardsResponse.self, from: data)

        XCTAssertEqual(list.cards.count, 2)
        XCTAssertEqual(list.cards[0].id, "ap_1")
        XCTAssertEqual(list.cards[0].title, "Add board screen")
        XCTAssertEqual(list.cards[0].prompt, "Implement Task 12")
        XCTAssertEqual(list.cards[0].status, "pending")
        XCTAssertEqual(list.cards[0].updatedAt, "2026-09-18T00:00:01Z")
        XCTAssertEqual(list.cards[1].status, "failed")
        XCTAssertEqual(list.cards[1].result, "partial")
        XCTAssertEqual(list.cards[1].verifyOutput, "exit 1")
        XCTAssertEqual(list.cards[1].branch, "autopilot/ap_2")
        XCTAssertEqual(list.cards[1].pr, "https://example.test/pr/2")
        XCTAssertEqual(list.cards[1].error, "verify failed")
    }

    func testEncodesAutopilotAddBodyMatchingHandler() throws {
        let body = AutopilotAddRequest(title: "Add board screen", prompt: "Implement Task 12")

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: String]

        XCTAssertEqual(json, ["title": "Add board screen", "prompt": "Implement Task 12"])
    }

    func testDecodesAutopilotRunResponseFixture() throws {
        let data = #"{"started":2,"already_running":true}"#.data(using: .utf8)!

        let response = try JSONDecoder().decode(AutopilotRunResponse.self, from: data)

        XCTAssertEqual(response.started, 2)
        XCTAssertEqual(response.alreadyRunning, true)
    }

    func testDecodesEngagementSessionsFixtureFromHandler() throws {
        let data = #"""
        {
          "sessions": [
            {
              "id": "sess_1",
              "title": "Web audit",
              "findings": 3,
              "intel": 5
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(EngagementSessionsResponse.self, from: data)

        XCTAssertEqual(list.sessions.count, 1)
        XCTAssertEqual(list.sessions[0].id, "sess_1")
        XCTAssertEqual(list.sessions[0].title, "Web audit")
        XCTAssertEqual(list.sessions[0].findings, 3)
        XCTAssertEqual(list.sessions[0].intel, 5)
    }

    func testDecodesEngagementSnapshotTreatingNullFindingsAsEmpty() throws {
        let data = #"""
        {
          "phases": [
            {
              "name": "recon",
              "title": "Reconnaissance",
              "summary": "Mapped the surface",
              "status": "complete",
              "evidence": 2,
              "roles": ["scout"]
            }
          ],
          "coverage": [
            {"name": "auth", "title": "Authentication", "covered": true},
            {"name": "injection", "title": "Injection", "covered": false}
          ],
          "coverage_percent": 50,
          "chains": [
            {"name": "Auth bypass to RCE", "impact": "Full takeover"}
          ],
          "findings": null,
          "intel": [
            {
              "id": "i1",
              "type": "endpoint",
              "value": "/login",
              "detail": "POST",
              "created_at": "2026-09-18T00:00:00Z"
            }
          ],
          "next": "Probe authentication",
          "scope": ["https://example.test"]
        }
        """#.data(using: .utf8)!

        let snapshot = try JSONDecoder().decode(EngagementSnapshot.self, from: data)

        XCTAssertEqual(snapshot.phases.count, 1)
        XCTAssertEqual(snapshot.phases[0].name, "recon")
        XCTAssertEqual(snapshot.phases[0].status, "complete")
        XCTAssertEqual(snapshot.phases[0].evidence, 2)
        XCTAssertEqual(snapshot.coverage.count, 2)
        XCTAssertEqual(snapshot.coverage[0].name, "auth")
        XCTAssertTrue(snapshot.coverage[0].covered)
        XCTAssertEqual(snapshot.coveragePercent, 50)
        XCTAssertEqual(snapshot.chains.count, 1)
        XCTAssertEqual(snapshot.chains[0].name, "Auth bypass to RCE")
        XCTAssertEqual(snapshot.findings.count, 0)
        XCTAssertEqual(snapshot.intel.count, 1)
        XCTAssertEqual(snapshot.intel[0].type, "endpoint")
        XCTAssertEqual(snapshot.intel[0].value, "/login")
        XCTAssertEqual(snapshot.next, "Probe authentication")
        XCTAssertEqual(snapshot.scope, ["https://example.test"])
    }

    func testDecodesEngagementFindingsFixtureFromHandler() throws {
        let data = #"""
        {
          "findings": [
            {
              "id": "f1",
              "title": "Open redirect",
              "severity": "medium",
              "target": "https://example.test/out",
              "description": "Unvalidated next param",
              "cwe": "CWE-601",
              "status": "confirmed",
              "created_at": "2026-09-18T00:00:00Z"
            }
          ]
        }
        """#.data(using: .utf8)!

        let snapshot = try JSONDecoder().decode(EngagementSnapshot.self, from: data)

        XCTAssertEqual(snapshot.findings.count, 1)
        XCTAssertEqual(snapshot.findings[0].id, "f1")
        XCTAssertEqual(snapshot.findings[0].title, "Open redirect")
        XCTAssertEqual(snapshot.findings[0].severity, "medium")
        XCTAssertEqual(snapshot.findings[0].target, "https://example.test/out")
        XCTAssertEqual(snapshot.findings[0].cwe, "CWE-601")
        XCTAssertEqual(snapshot.findings[0].status, "confirmed")
    }

    func testDecodesBoardSessionsAndColumnsFixtureFromHandlers() throws {
        let sessionsData = #"""
        {
          "sessions": [
            {"id": "sess_1", "title": "Ship dashboard"}
          ]
        }
        """#.data(using: .utf8)!
        let boardData = #"""
        {
          "columns": [
            {
              "name": "todo",
              "cards": [
                {
                  "id": "c1",
                  "title": "Write tests",
                  "note": "DTO first",
                  "column": "todo",
                  "created_at": "2026-09-18T00:00:00Z",
                  "updated_at": "2026-09-18T00:00:01Z"
                }
              ]
            },
            {"name": "doing", "cards": []},
            {"name": "done", "cards": []}
          ]
        }
        """#.data(using: .utf8)!

        let sessions = try JSONDecoder().decode(BoardSessionsResponse.self, from: sessionsData)
        let board = try JSONDecoder().decode(BoardSnapshot.self, from: boardData)

        XCTAssertEqual(sessions.sessions.count, 1)
        XCTAssertEqual(sessions.sessions[0].id, "sess_1")
        XCTAssertEqual(sessions.sessions[0].title, "Ship dashboard")
        XCTAssertEqual(board.columns.map(\.name), ["todo", "doing", "done"])
        XCTAssertEqual(board.columns[0].cards.count, 1)
        XCTAssertEqual(board.columns[0].cards[0].id, "c1")
        XCTAssertEqual(board.columns[0].cards[0].title, "Write tests")
        XCTAssertEqual(board.columns[0].cards[0].note, "DTO first")
        XCTAssertEqual(board.columns[0].cards[0].column, "todo")
        XCTAssertEqual(board.columns[1].cards.count, 0)
    }
}
