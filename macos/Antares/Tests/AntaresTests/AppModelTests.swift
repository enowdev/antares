import XCTest
@testable import Antares

@MainActor
final class AppModelTests: XCTestCase {
    func testStopDelegatesToRuntimeSupervisor() async {
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime)

        await model.stop()

        XCTAssertTrue(runtime.didStop)
    }
}

private final class SpyRuntimeSupervisor: RuntimeSupervising {
    private(set) var didStop = false

    func start(binary: URL?) async throws {}

    func stop() async {
        didStop = true
    }
}
