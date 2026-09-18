import XCTest
@testable import AntaresKit

final class RuntimeSupervisorTests: XCTestCase {
    func testHealthyServerMeansAttach() {
        XCTAssertEqual(RuntimePlan.decide(healthOK: true, portBusy: false), .attached)
    }

    func testUnhealthyAndFreePortMeansSpawn() {
        XCTAssertEqual(RuntimePlan.decide(healthOK: false, portBusy: false), .spawned)
    }

    func testUnhealthyBusyPortIsError() {
        XCTAssertEqual(RuntimePlan.decide(healthOK: false, portBusy: true), .conflict)
    }

    func testSpawnedChildIsStoppedOnShutdownAttachedIsNot() {
        XCTAssertTrue(RuntimePlan.shouldStopChild(mode: .spawned))
        XCTAssertFalse(RuntimePlan.shouldStopChild(mode: .attached))
    }

    func testHTTP200HealthWithOkFalseStillAttaches() {
        XCTAssertTrue(HealthResponse.meansAntaresPresent(decodeSucceeded: true, ok: false))
        XCTAssertTrue(HealthResponse.meansAntaresPresent(decodeSucceeded: true, ok: true))
        XCTAssertFalse(HealthResponse.meansAntaresPresent(decodeSucceeded: false, ok: false))
    }

    func testExistingChildKeepsSpawnedEvenWhenHealthy() {
        XCTAssertEqual(
            RuntimePlan.decide(healthOK: true, portBusy: false, childExists: true),
            .spawned
        )
    }

    func testSupervisorUsesInjectedBaseURL() {
        let url = URL(string: "http://127.0.0.1:9999")!
        XCTAssertEqual(RuntimeSupervisor(baseURL: url).baseURL, url)
    }
}
