import XCTest
@testable import Antares

final class SidebarItemTests: XCTestCase {
    func testAllContainsRequiredIdsInRouteManifestOrder() {
        XCTAssertEqual(
            SidebarItem.all.map(\.id),
            [
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
        )
    }
}
