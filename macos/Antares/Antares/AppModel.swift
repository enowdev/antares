import AntaresKit
import Combine
import Foundation

@MainActor
protocol RuntimeSupervising: AnyObject {
    func start(binary: URL?) async throws
    func stop() async
    func terminateSpawnedChild()
}

extension RuntimeSupervising {
    func terminateSpawnedChild() {}
}

extension RuntimeSupervisor: RuntimeSupervising {}

@MainActor
final class AppModel: ObservableObject {
    @Published var phase: Phase = .launching

    enum Phase {
        case launching
        case login
        case setup
        case ready
        case failed(String)
    }

    let client: APIClient
    // RuntimeSupervisor is only touched from this @MainActor model to keep access serialized.
    private let runtime: any RuntimeSupervising

    init(
        baseURL: URL = AntaresKit.resolveBaseURL(),
        runtime: (any RuntimeSupervising)? = nil,
        session: URLSession = .shared
    ) {
        self.client = APIClient(baseURL: baseURL, session: session)
        self.runtime = runtime ?? RuntimeSupervisor(baseURL: baseURL, session: session)
    }

    func start() async {
        phase = .launching
        await ensureBackend()
        if case .failed = phase { return }
        await refreshPhase()
    }

    /// Spawns or attaches the backend once. Do not call again after login or setup.
    func ensureBackend() async {
        do {
            try await runtime.start(binary: Bundle.main.url(forAuxiliaryExecutable: "antares"))
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    /// Reads setup/auth status only. A 401 on a gated route means the session
    /// expired — go to login. `/api/auth/status` is public, so that 401 is not
    /// expected and must not restart the backend.
    func refreshPhase() async {
        do {
            let setup: SetupStatus = try await client.get("/api/setup/status")
            let auth: AuthStatus = try await client.get("/api/auth/status")

            if setup.needsSetup {
                phase = .setup
            } else if auth.passwordRequired && !auth.authenticated {
                phase = .login
            } else {
                phase = .ready
            }
        } catch let error as APIError where error.isUnauthorized {
            phase = .login
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    func stop() async {
        runtime.terminateSpawnedChild()
        await runtime.stop()
    }

    func terminateSpawnedChild() {
        runtime.terminateSpawnedChild()
    }

    func login(password: String) async throws {
        let _: EmptyResponse = try await client.post("/api/auth/login", body: LoginRequest(password: password))
        await refreshPhase()
    }

    func setDashboardPassword(_ password: String, current: String? = nil) async throws {
        let _: EmptyResponse = try await client.post(
            "/api/auth/password",
            body: SetPasswordRequest(password: password, current: current)
        )
    }

    func setupStatus() async throws -> SetupStatus {
        try await client.get("/api/setup/status")
    }

    func testSetup(_ request: SetupTestRequest) async throws -> SetupTestResponse {
        try await client.post("/api/setup/test", body: request)
    }

    func completeSetup(_ request: CompleteSetupRequest) async throws {
        let _: EmptyResponse = try await client.post("/api/setup/complete", body: request)
        await refreshPhase()
    }
}
