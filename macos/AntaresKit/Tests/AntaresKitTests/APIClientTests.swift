import XCTest
@testable import AntaresKit

final class APIClientTests: XCTestCase {
    func testDecodeErrorMessageFromJSONBody() {
        let body = #"{"error":"nope"}"#.data(using: .utf8)!
        let err = APIError(status: 400, body: body)
        XCTAssertEqual(err.message, "nope")
    }

    func test428IsDashboardPasswordRequired() {
        XCTAssertTrue(APIError(status: 428, body: Data()).isDashboardPasswordRequired)
        XCTAssertFalse(APIError(status: 401, body: Data()).isDashboardPasswordRequired)
    }

    func testJoinPathOntoBase() {
        let c = APIClient(baseURL: URL(string: "http://127.0.0.1:8787")!)
        XCTAssertEqual(c.url("/api/health").absoluteString, "http://127.0.0.1:8787/api/health")
    }

    func test401IsUnauthorized() {
        XCTAssertTrue(APIError(status: 401, body: Data()).isUnauthorized)
        XCTAssertFalse(APIError(status: 403, body: Data()).isUnauthorized)
    }
}
