import AntaresKit
import XCTest
@testable import Antares

@MainActor
final class AppModelTests: XCTestCase {
    override func tearDown() {
        StubURLProtocol.reset()
        super.tearDown()
    }

    func testStopDelegatesToRuntimeSupervisor() async {
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime)

        await model.stop()

        XCTAssertTrue(runtime.didStop)
    }

    func testClientUsesTheInjectedBaseURL() {
        let url = URL(string: "http://127.0.0.1:9999")!
        let model = AppModel(baseURL: url, runtime: SpyRuntimeSupervisor())

        XCTAssertEqual(model.client.baseURL, url)
    }

    func testStartCallsRuntimeOnceThenReadsSetupAndAuth() async {
        StubURLProtocol.readyAuthenticated()
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime, session: StubURLProtocol.session())

        await model.start()

        XCTAssertEqual(runtime.startCount, 1)
        guard case .ready = model.phase else {
            return XCTFail("expected ready, got \(model.phase)")
        }
    }

    func testLoginDoesNotStartRuntimeAgain() async throws {
        StubURLProtocol.readyAuthenticated()
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime, session: StubURLProtocol.session())

        await model.start()
        XCTAssertEqual(runtime.startCount, 1)

        try await model.login(password: "secret")

        XCTAssertEqual(runtime.startCount, 1)
        guard case .ready = model.phase else {
            return XCTFail("expected ready, got \(model.phase)")
        }
    }

    func testCompleteSetupDoesNotStartRuntimeAgain() async throws {
        StubURLProtocol.readyAuthenticated()
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime, session: StubURLProtocol.session())

        await model.start()
        XCTAssertEqual(runtime.startCount, 1)

        try await model.completeSetup(Self.completeSetupRequest)

        XCTAssertEqual(runtime.startCount, 1)
        guard case .ready = model.phase else {
            return XCTFail("expected ready, got \(model.phase)")
        }
    }

    func testUnauthorizedRefreshGoesToLogin() async {
        StubURLProtocol.statusCodeByPath = [
            "/api/setup/status": 401,
        ]
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime, session: StubURLProtocol.session())

        await model.start()

        XCTAssertEqual(runtime.startCount, 1)
        guard case .login = model.phase else {
            return XCTFail("expected login, got \(model.phase)")
        }
    }

    func testTerminateSpawnedChildIsCallableFromAppModel() {
        let runtime = SpyRuntimeSupervisor()
        let model = AppModel(runtime: runtime)

        model.terminateSpawnedChild()

        XCTAssertTrue(runtime.didTerminate)
    }

    private static let completeSetupRequest = CompleteSetupRequest(
        provider: "openrouter",
        baseURL: "https://openrouter.ai/api/v1",
        apiKey: "sk-test",
        model: "anthropic/claude-sonnet-4.5",
        workspace: "/tmp/antares",
        database: SetupDatabaseRequest(),
        rag: SetupRAGRequest()
    )
}

private final class SpyRuntimeSupervisor: RuntimeSupervising {
    private(set) var startCount = 0
    private(set) var didStop = false
    private(set) var didTerminate = false

    func start(binary: URL?) async throws {
        startCount += 1
    }

    func stop() async {
        didStop = true
    }

    func terminateSpawnedChild() {
        didTerminate = true
    }
}

private final class StubURLProtocol: URLProtocol {
    private static let state = StubState()

    private final class StubState: @unchecked Sendable {
        let lock = NSLock()
        var statusCodeByPath: [String: Int] = [:]
    }

    static var statusCodeByPath: [String: Int] {
        get {
            state.lock.lock()
            defer { state.lock.unlock() }
            return state.statusCodeByPath
        }
        set {
            state.lock.lock()
            state.statusCodeByPath = newValue
            state.lock.unlock()
        }
    }

    static func reset() {
        statusCodeByPath = [:]
    }

    static func readyAuthenticated() {
        statusCodeByPath = [:]
    }

    static func session() -> URLSession {
        let config = URLSessionConfiguration.ephemeral
        config.protocolClasses = [StubURLProtocol.self]
        return URLSession(configuration: config)
    }

    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }

    override func startLoading() {
        let path = request.url?.path ?? ""
        let status = Self.statusCodeByPath[path] ?? 200

        let body: Data
        if status >= 400 {
            body = Data(#"{"error":"unauthorized"}"#.utf8)
        } else if path.hasSuffix("/api/setup/status") {
            body = Data(Self.setupStatusJSON.utf8)
        } else if path.hasSuffix("/api/auth/status") {
            body = Data(#"{"password_required":true,"authenticated":true}"#.utf8)
        } else {
            body = Data("{}".utf8)
        }

        let response = HTTPURLResponse(
            url: request.url!,
            statusCode: status,
            httpVersion: nil,
            headerFields: ["Content-Type": "application/json"]
        )!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: body)
        client?.urlProtocolDidFinishLoading(self)
    }

    override func stopLoading() {}

    private static let setupStatusJSON = """
    {
      "needs_setup": false,
      "model": "anthropic/claude-sonnet-4.5",
      "provider": "openrouter",
      "workspace": "/tmp/antares",
      "home": "/tmp",
      "config_path": "/tmp/.antares/config.json",
      "database": "sqlite",
      "providers": []
    }
    """
}
