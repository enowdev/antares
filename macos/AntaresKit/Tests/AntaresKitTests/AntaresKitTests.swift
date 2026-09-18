import XCTest
@testable import AntaresKit

final class AntaresKitTests: XCTestCase {
    func testDefaultBaseURLIsLoopback8787() {
        XCTAssertEqual(AntaresKit.defaultBaseURL.absoluteString, "http://127.0.0.1:8787")
    }

    func testResolveBaseURLUsesANTARES_URL() {
        XCTAssertEqual(
            AntaresKit.resolveBaseURL(environment: ["ANTARES_URL": "http://127.0.0.1:9999"]).absoluteString,
            "http://127.0.0.1:9999"
        )
    }

    func testResolveBaseURLFallsBackToDefault() {
        XCTAssertEqual(AntaresKit.resolveBaseURL(environment: [:]), AntaresKit.defaultBaseURL)
    }
}
