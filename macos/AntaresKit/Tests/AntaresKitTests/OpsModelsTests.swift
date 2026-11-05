import XCTest
@testable import AntaresKit

final class OpsModelsTests: XCTestCase {
    func testDecodesVPSHostListFixtureFromHandler() throws {
        let data = #"""
        {
          "hosts": [
            {
              "id": "vps_ab12",
              "label": "edge",
              "host": "203.0.113.9",
              "port": 22,
              "username": "root",
              "auth_method": "key",
              "has_password": false,
              "has_key": true,
              "created_at": "2026-09-18T00:00:00Z",
              "updated_at": "2026-09-18T00:00:01Z"
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(VPSListResponse.self, from: data)

        XCTAssertEqual(list.hosts.count, 1)
        XCTAssertEqual(list.hosts[0].id, "vps_ab12")
        XCTAssertEqual(list.hosts[0].label, "edge")
        XCTAssertEqual(list.hosts[0].host, "203.0.113.9")
        XCTAssertEqual(list.hosts[0].port, 22)
        XCTAssertEqual(list.hosts[0].username, "root")
        XCTAssertEqual(list.hosts[0].authMethod, "key")
        XCTAssertFalse(list.hosts[0].hasPassword)
        XCTAssertTrue(list.hosts[0].hasKey)
    }

    func testDecodesNullVPSHostsAsEmptyList() throws {
        let list = try JSONDecoder().decode(
            VPSListResponse.self,
            from: #"{"hosts":null}"#.data(using: .utf8)!
        )

        XCTAssertEqual(list.hosts.count, 0)
    }

    func testEncodesVPSSaveBodyMatchingHandler() throws {
        let body = VPSSaveRequest(
            id: "vps_ab12",
            label: "edge",
            host: "203.0.113.9",
            port: 22,
            username: "root",
            authMethod: "password",
            password: "secret",
            privateKey: "",
            passphrase: ""
        )

        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(body)) as? [String: Any]

        XCTAssertEqual(json?["id"] as? String, "vps_ab12")
        XCTAssertEqual(json?["label"] as? String, "edge")
        XCTAssertEqual(json?["host"] as? String, "203.0.113.9")
        XCTAssertEqual(json?["port"] as? Int, 22)
        XCTAssertEqual(json?["username"] as? String, "root")
        XCTAssertEqual(json?["auth_method"] as? String, "password")
        XCTAssertEqual(json?["password"] as? String, "secret")
        XCTAssertEqual(json?["private_key"] as? String, "")
        XCTAssertEqual(json?["passphrase"] as? String, "")
    }

    func testDecodesVPSMetricsFixtureFromHandler() throws {
        let data = #"""
        {
          "reachable": true,
          "hostname": "edge",
          "os": "Ubuntu 24.04",
          "kernel": "6.8.0",
          "uptime": "2 days",
          "cpu_cores": 4,
          "cpu_percent": 12.5,
          "load1": 0.4,
          "load5": 0.3,
          "load15": 0.2,
          "mem_total_mb": 2048,
          "mem_used_mb": 512,
          "mem_percent": 25.0,
          "swap_total_mb": 1024,
          "swap_used_mb": 0,
          "swap_percent": 0,
          "disk_total_gb": 40,
          "disk_used_gb": 12.5,
          "disk_percent": 31.2,
          "processes": 120,
          "top_proc": ["1 0.0 0.1 systemd"]
        }
        """#.data(using: .utf8)!

        let metrics = try JSONDecoder().decode(VPSMetrics.self, from: data)

        XCTAssertTrue(metrics.reachable)
        XCTAssertEqual(metrics.hostname, "edge")
        XCTAssertEqual(metrics.os, "Ubuntu 24.04")
        XCTAssertEqual(metrics.cpuCores, 4)
        XCTAssertEqual(metrics.cpuPercent, 12.5)
        XCTAssertEqual(metrics.load1, 0.4)
        XCTAssertEqual(metrics.memTotalMB, 2048)
        XCTAssertEqual(metrics.memPercent, 25.0)
        XCTAssertEqual(metrics.diskUsedGB, 12.5)
        XCTAssertEqual(metrics.processes, 120)
        XCTAssertEqual(metrics.topProc, ["1 0.0 0.1 systemd"])
    }

    func testDecodesUnreachableVPSMetricsWithError() throws {
        let data = #"{"reachable":false,"error":"dial timeout"}"#.data(using: .utf8)!

        let metrics = try JSONDecoder().decode(VPSMetrics.self, from: data)

        XCTAssertFalse(metrics.reachable)
        XCTAssertEqual(metrics.error, "dial timeout")
        XCTAssertEqual(metrics.cpuCores, 0)
        XCTAssertEqual(metrics.topProc, [])
    }

    func testDecodesVPSProcessListFixture() throws {
        let data = #"""
        {
          "processes": [
            {"pid": 42, "user": "root", "cpu": 12.3, "mem": 1.5, "command": "nginx"}
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(VPSProcessList.self, from: data)

        XCTAssertNil(list.error)
        XCTAssertEqual(list.processes.count, 1)
        XCTAssertEqual(list.processes[0].pid, 42)
        XCTAssertEqual(list.processes[0].user, "root")
        XCTAssertEqual(list.processes[0].cpu, 12.3)
        XCTAssertEqual(list.processes[0].mem, 1.5)
        XCTAssertEqual(list.processes[0].command, "nginx")
    }

    func testDecodesVPSProcessErrorAsEmptyList() throws {
        let list = try JSONDecoder().decode(
            VPSProcessList.self,
            from: #"{"error":"ssh failed","processes":[]}"#.data(using: .utf8)!
        )

        XCTAssertEqual(list.error, "ssh failed")
        XCTAssertEqual(list.processes.count, 0)
    }

    func testDecodesVPSTestResponseFixture() throws {
        let ok = try JSONDecoder().decode(
            VPSTestResponse.self,
            from: #"{"ok":true,"as":"root@edge"}"#.data(using: .utf8)!
        )
        let fail = try JSONDecoder().decode(
            VPSTestResponse.self,
            from: #"{"ok":false,"error":"host is required"}"#.data(using: .utf8)!
        )

        XCTAssertTrue(ok.ok)
        XCTAssertEqual(ok.asUser, "root@edge")
        XCTAssertFalse(fail.ok)
        XCTAssertEqual(fail.error, "host is required")
    }

    func testDecodesAnalyticsFixtureFromHandler() throws {
        let data = #"""
        {
          "series": [
            {"bucket": "2026-09-17", "tokens_in": 100, "tokens_out": 200, "cost": 0.0123, "calls": 4}
          ],
          "by_model": [
            {"model": "gpt-4o", "provider": "openai", "calls": 4, "tokens_in": 100, "tokens_out": 200, "cost": 0.0123}
          ],
          "totals": {"tokens_in": 100, "tokens_out": 200, "cost": 0.0123, "calls": 4}
        }
        """#.data(using: .utf8)!

        let analytics = try JSONDecoder().decode(AnalyticsResponse.self, from: data)

        XCTAssertEqual(analytics.series.count, 1)
        XCTAssertEqual(analytics.series[0].bucket, "2026-09-17")
        XCTAssertEqual(analytics.series[0].tokensIn, 100)
        XCTAssertEqual(analytics.series[0].tokensOut, 200)
        XCTAssertEqual(analytics.series[0].cost, 0.0123, accuracy: 0.00001)
        XCTAssertEqual(analytics.series[0].calls, 4)
        XCTAssertEqual(analytics.byModel.count, 1)
        XCTAssertEqual(analytics.byModel[0].model, "gpt-4o")
        XCTAssertEqual(analytics.byModel[0].provider, "openai")
        XCTAssertEqual(analytics.totals.tokensIn, 100)
        XCTAssertEqual(analytics.totals.calls, 4)
        XCTAssertEqual(analytics.totals.cost, 0.0123, accuracy: 0.00001)
    }

    func testDecodesNullAnalyticsSeriesAsEmpty() throws {
        let data = #"{"series":null,"by_model":null,"totals":{"tokens_in":0,"tokens_out":0,"cost":0,"calls":0}}"#
            .data(using: .utf8)!

        let analytics = try JSONDecoder().decode(AnalyticsResponse.self, from: data)

        XCTAssertEqual(analytics.series.count, 0)
        XCTAssertEqual(analytics.byModel.count, 0)
        XCTAssertEqual(analytics.totals.calls, 0)
    }

    func testDecodesFileListFixtureFromHandler() throws {
        let data = #"""
        {
          "path": "src",
          "parent": ".",
          "entries": [
            {
              "name": "main.go",
              "path": "src/main.go",
              "is_dir": false,
              "size": 128,
              "modified": "2026-09-18T00:00:00Z"
            },
            {
              "name": "pkg",
              "path": "src/pkg",
              "is_dir": true,
              "size": 64,
              "modified": "2026-09-18T00:00:01Z"
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(FileListResponse.self, from: data)

        XCTAssertEqual(list.path, "src")
        XCTAssertEqual(list.parent, ".")
        XCTAssertEqual(list.entries.count, 2)
        XCTAssertEqual(list.entries[0].name, "main.go")
        XCTAssertEqual(list.entries[0].path, "src/main.go")
        XCTAssertFalse(list.entries[0].isDir)
        XCTAssertEqual(list.entries[0].size, 128)
        XCTAssertEqual(list.entries[0].modified, "2026-09-18T00:00:00Z")
        XCTAssertTrue(list.entries[1].isDir)
    }

    func testDecodesNullFileEntriesAsEmpty() throws {
        let list = try JSONDecoder().decode(
            FileListResponse.self,
            from: #"{"path":".","parent":"","entries":null}"#.data(using: .utf8)!
        )

        XCTAssertEqual(list.path, ".")
        XCTAssertEqual(list.parent, "")
        XCTAssertEqual(list.entries.count, 0)
    }

    func testDecodesFileReadTextAndBinaryFixtures() throws {
        let text = try JSONDecoder().decode(
            FileReadResponse.self,
            from: #"{"content":"hello","size":5}"#.data(using: .utf8)!
        )
        let binary = try JSONDecoder().decode(
            FileReadResponse.self,
            from: #"{"binary":true,"size":2048}"#.data(using: .utf8)!
        )

        XCTAssertEqual(text.content, "hello")
        XCTAssertEqual(text.size, 5)
        XCTAssertFalse(text.binary)
        XCTAssertTrue(binary.binary)
        XCTAssertEqual(binary.size, 2048)
        XCTAssertEqual(binary.content, "")
    }

    func testDecodesLogListFixtureFromHandler() throws {
        let data = #"""
        {
          "entries": [
            {
              "time": "2026-09-18T00:00:00Z",
              "level": "INFO",
              "message": "listening",
              "source": "server",
              "attrs": {"port": 8787, "ok": true}
            }
          ]
        }
        """#.data(using: .utf8)!

        let list = try JSONDecoder().decode(LogListResponse.self, from: data)

        XCTAssertEqual(list.entries.count, 1)
        XCTAssertEqual(list.entries[0].time, "2026-09-18T00:00:00Z")
        XCTAssertEqual(list.entries[0].level, "INFO")
        XCTAssertEqual(list.entries[0].message, "listening")
        XCTAssertEqual(list.entries[0].source, "server")
        XCTAssertEqual(list.entries[0].attrs?["port"]?.stringValue, nil)
        XCTAssertEqual(list.entries[0].attrs?["port"]?.numberValue, 8787)
        XCTAssertEqual(list.entries[0].attrs?["ok"]?.boolValue, true)
    }

    func testParsesLogStreamSSEPayloadAsLogEntry() throws {
        let raw = "data: {\"time\":\"2026-09-18T00:00:02Z\",\"level\":\"WARN\",\"message\":\"slow query\",\"attrs\":{\"ms\":120}}\n\n"
        var entries: [LogEntry] = []
        try SSEParser.parseJSON(Data(raw.utf8), as: LogEntry.self) { entry in
            entries.append(entry)
        }

        XCTAssertEqual(entries.count, 1)
        XCTAssertEqual(entries.first?.time, "2026-09-18T00:00:02Z")
        XCTAssertEqual(entries.first?.level, "WARN")
        XCTAssertEqual(entries.first?.message, "slow query")
        XCTAssertEqual(entries.first?.attrs?["ms"]?.numberValue, 120)
    }

    func testDecodesSystemStatsFixtureFromHandler() throws {
        let data = #"""
        {
          "version": "0.4.0",
          "go_version": "go1.25.1",
          "os": "darwin",
          "arch": "arm64",
          "uptime_seconds": 90,
          "goroutines": 42,
          "memory_alloc": 1048576,
          "memory_sys": 2097152,
          "cpu_count": 10,
          "workspace": "/tmp/ws",
          "config_path": "/tmp/config.json",
          "database": {"driver": "sqlite", "size_bytes": 4096},
          "store": {
            "sessions": 3,
            "messages": 12,
            "memories": 2,
            "cron_jobs": 1,
            "tokens_in": 100,
            "tokens_out": 200,
            "cost": 0.01,
            "empty_sessions": 0,
            "size_bytes": 4096
          },
          "active_shells": 1,
          "model": "gpt-4o",
          "provider": "openai",
          "rag_provider": "openai"
        }
        """#.data(using: .utf8)!

        let stats = try JSONDecoder().decode(SystemStats.self, from: data)

        XCTAssertEqual(stats.version, "0.4.0")
        XCTAssertEqual(stats.goVersion, "go1.25.1")
        XCTAssertEqual(stats.os, "darwin")
        XCTAssertEqual(stats.arch, "arm64")
        XCTAssertEqual(stats.uptimeSeconds, 90)
        XCTAssertEqual(stats.goroutines, 42)
        XCTAssertEqual(stats.memoryAlloc, 1_048_576)
        XCTAssertEqual(stats.cpuCount, 10)
        XCTAssertEqual(stats.workspace, "/tmp/ws")
        XCTAssertEqual(stats.database.driver, "sqlite")
        XCTAssertEqual(stats.database.sizeBytes, 4096)
        XCTAssertEqual(stats.store.sessions, 3)
        XCTAssertEqual(stats.store.messages, 12)
        XCTAssertEqual(stats.store.cost, 0.01, accuracy: 0.00001)
        XCTAssertEqual(stats.activeShells, 1)
        XCTAssertEqual(stats.model, "gpt-4o")
        XCTAssertEqual(stats.provider, "openai")
        XCTAssertEqual(stats.ragProvider, "openai")
    }

    func testDecodesRuntimeStatusFixtureFromHandler() throws {
        let data = #"""
        {
          "ok": true,
          "version": "0.4.0",
          "model": "gpt-4o",
          "provider": "openai",
          "provider_ready": true,
          "database": "sqlite",
          "uptime_seconds": 90,
          "active_sessions": 2,
          "needs_setup": false
        }
        """#.data(using: .utf8)!

        let status = try JSONDecoder().decode(RuntimeStatus.self, from: data)

        XCTAssertTrue(status.ok)
        XCTAssertEqual(status.version, "0.4.0")
        XCTAssertEqual(status.model, "gpt-4o")
        XCTAssertEqual(status.provider, "openai")
        XCTAssertTrue(status.providerReady)
        XCTAssertEqual(status.database, "sqlite")
        XCTAssertEqual(status.uptimeSeconds, 90)
        XCTAssertEqual(status.activeSessions, 2)
        XCTAssertFalse(status.needsSetup)
    }
}
