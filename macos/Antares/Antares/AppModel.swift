import AntaresKit
import Combine
import Foundation

@MainActor
protocol RuntimeSupervising: AnyObject {
    func start(binary: URL?) async throws
    func stop() async
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

    let client = APIClient(baseURL: AntaresKit.defaultBaseURL)
    // RuntimeSupervisor is only touched from this @MainActor model to keep access serialized.
    private let runtime: any RuntimeSupervising

    init(runtime: any RuntimeSupervising = RuntimeSupervisor()) {
        self.runtime = runtime
    }

    func start() async {
        phase = .launching
        do {
            try await runtime.start(binary: Bundle.main.url(forAuxiliaryExecutable: "antares"))

            let setup: SetupStatus = try await client.get("/api/setup/status")
            let auth: AuthStatus = try await client.get("/api/auth/status")

            if setup.needsSetup {
                phase = .setup
            } else if auth.passwordRequired && !auth.authenticated {
                phase = .login
            } else {
                phase = .ready
            }
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }

    func stop() async {
        await runtime.stop()
    }
}

private struct SetupStatus: Decodable {
    var needsSetup: Bool

    enum CodingKeys: String, CodingKey {
        case needsSetup = "needs_setup"
    }
}

private struct AuthStatus: Decodable {
    var passwordRequired: Bool
    var authenticated: Bool

    enum CodingKeys: String, CodingKey {
        case passwordRequired = "password_required"
        case authenticated
    }
}
