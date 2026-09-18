import XCTest
@testable import Antares

final class SidebarItemTests: XCTestCase {
    private let expectedIDs = [
        "chat",
        "sessions",
        "providers",
        "tools",
        "memory",
        "roles",
        "soul",
        "skills",
        "cron",
        "channels",
        "autopilot",
        "engagement",
        "board",
        "intercept",
        "mcp",
        "plugins",
        "proxies",
        "vps",
        "analytics",
        "files",
        "logs",
        "config",
        "system",
        "content-creator",
        "social-media",
    ]

    func testAllContainsRequiredIdsInRouteManifestOrder() {
        XCTAssertEqual(SidebarItem.all.map(\.id), expectedIDs)
    }

    func testEverySidebarItemHasARouteAndEveryRouteIsListed() {
        let allIDs = Set(SidebarItem.all.map(\.id))
        let routeIDs = Set(SidebarRoute.allCases.map(\.rawValue))
        XCTAssertEqual(routeIDs, allIDs)
        XCTAssertEqual(routeIDs, Set(expectedIDs))
        for item in SidebarItem.all {
            XCTAssertEqual(item.route.rawValue, item.id)
        }
    }

    func testAppShellImplementsEverySidebarItemAndHasNoStub() throws {
        let source = try String(contentsOf: appShellURL, encoding: .utf8)
        XCTAssertFalse(source.contains("PageStub"), "AppShell still references PageStub")
        XCTAssertFalse(source.contains("default:"), "AppShell switch should be exhaustive without default")

        var implemented = Set<String>()
        for item in SidebarItem.all {
            let needle = "case .\(item.route):"
            XCTAssertTrue(source.contains(needle), "AppShell missing \(needle) for \(item.id)")
            implemented.insert(item.id)
        }
        XCTAssertEqual(implemented, Set(SidebarItem.all.map(\.id)))
        XCTAssertEqual(implemented, Set(expectedIDs))
    }

    private var appShellURL: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .deletingLastPathComponent()
            .appendingPathComponent("Antares/AppShell.swift")
    }
}
