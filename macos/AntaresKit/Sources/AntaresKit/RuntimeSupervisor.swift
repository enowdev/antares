import Darwin
import Foundation

public enum RuntimePlan {
    public enum Mode: Equatable { case attached, spawned, conflict }

    public static func decide(healthOK: Bool, portBusy: Bool, childExists: Bool = false) -> Mode {
        if childExists { return .spawned }
        if healthOK { return .attached }
        if portBusy { return .conflict }
        return .spawned
    }

    public static func shouldStopChild(mode: Mode) -> Bool { mode == .spawned }
}

public struct HealthResponse: Decodable, Sendable {
    public let ok: Bool
    public let version: String

    /// Any 2xx /api/health decode means Antares is present; `ok` may be false when db ping fails.
    public static func meansAntaresPresent(decodeSucceeded: Bool, ok: Bool = true) -> Bool {
        _ = ok
        return decodeSucceeded
    }
}

public enum RuntimeSupervisorError: Error, LocalizedError {
    case missingBinary
    case portConflict
    case healthTimeout
    case spawnFailed(String)

    public var errorDescription: String? {
        switch self {
        case .missingBinary: "Bundled antares binary not found"
        case .portConflict: "Port in use by a process that is not Antares"
        case .healthTimeout: "Backend did not become healthy within 30 seconds"
        case .spawnFailed(let detail): detail
        }
    }
}

/// Captures stderr lines from a spawned child for LaunchView.
public actor StderrLog {
    private var lines: [String] = []

    public var lastLog: String { lines.last ?? "" }

    func append(_ data: Data) {
        let chunk = String(decoding: data, as: UTF8.self)
        for line in chunk.split(whereSeparator: \.isNewline) {
            let text = String(line)
            if !text.isEmpty { lines.append(text) }
        }
    }
}

public final class RuntimeSupervisor: @unchecked Sendable {
    public private(set) var mode: RuntimePlan.Mode?
    public let stderrLog = StderrLog()
    public let baseURL: URL

    private let client: APIClient
    private var process: Process?
    private var stderrTask: Task<Void, Never>?

    public init(baseURL: URL? = nil, session: URLSession = .shared) {
        let resolved = baseURL ?? AntaresKit.resolveBaseURL()
        self.baseURL = resolved
        self.client = APIClient(baseURL: resolved, session: session)
    }

    /// Probes health, attaches or spawns the bundled backend, then waits until healthy.
    /// If a child already exists, keep `.spawned` and return without re-probing —
    /// a healthy child must not flip the mode to `.attached` (quit would leak it).
    public func start(binary: URL?) async throws {
        if process != nil {
            mode = .spawned
            return
        }

        let healthOK = await probeHealth()
        let portBusy = !healthOK && isPortBusy(baseURL: baseURL)
        let plan = RuntimePlan.decide(healthOK: healthOK, portBusy: portBusy, childExists: false)
        mode = plan

        switch plan {
        case .attached:
            return
        case .conflict:
            throw RuntimeSupervisorError.portConflict
        case .spawned:
            guard let binary else { throw RuntimeSupervisorError.missingBinary }
            try spawn(binary: binary)
            do {
                try await waitForHealth()
            } catch {
                await stop()
                throw error
            }
        }
    }

    /// Sends SIGTERM immediately to a spawned child. Call from
    /// `NSApplication.willTerminateNotification` on the main actor so the
    /// child is signaled even if the async wait in `stop()` never runs.
    ///
    /// Remaining race: the app can still exit before the 5s wait / SIGKILL
    /// in `stop()` finishes. Immediate `process.terminate()` is the part
    /// that is guaranteed once this method is invoked.
    public func terminateSpawnedChild() {
        guard let mode, RuntimePlan.shouldStopChild(mode: mode) else { return }
        process?.terminate()
    }

    /// Stops a spawned child with SIGTERM, then SIGKILL after 5s. Attached mode is left running.
    public func stop() async {
        terminateSpawnedChild()
        stderrTask?.cancel()
        stderrTask = nil
        guard let process else { return }
        if process.isRunning {
            let pid = process.processIdentifier
            try? await Task.sleep(for: .seconds(5))
            if process.isRunning {
                kill(pid, SIGKILL)
            }
        }
        self.process = nil
    }

    private func probeHealth() async -> Bool {
        // Attach on any 2xx decode; ok:false is degraded Antares, not a port conflict.
        HealthResponse.meansAntaresPresent(decodeSucceeded: (try? await fetchHealth()) != nil)
    }

    private func fetchHealth() async throws -> HealthResponse {
        try await client.get("/api/health")
    }

    private func waitForHealth() async throws {
        let deadline = Date().addingTimeInterval(30)
        while Date() < deadline {
            if process?.isRunning == false {
                let detail = await stderrLog.lastLog
                throw RuntimeSupervisorError.spawnFailed(
                    detail.isEmpty ? "Backend process exited before becoming healthy" : detail
                )
            }
            if await probeHealth() { return }
            try await Task.sleep(for: .milliseconds(200))
        }
        let detail = await stderrLog.lastLog
        throw RuntimeSupervisorError.spawnFailed(
            detail.isEmpty ? RuntimeSupervisorError.healthTimeout.localizedDescription : detail
        )
    }

    private func spawn(binary: URL) throws {
        let child = Process()
        child.executableURL = binary
        child.arguments = ["--foreground"]
        var env = ProcessInfo.processInfo.environment
        env["HOME"] = NSHomeDirectory()
        child.environment = env

        let pipe = Pipe()
        child.standardOutput = FileHandle.nullDevice
        child.standardError = pipe

        try child.run()
        process = child
        stderrTask = Task { [stderrLog] in
            let handle = pipe.fileHandleForReading
            while !Task.isCancelled {
                let data = handle.availableData
                if data.isEmpty { break }
                await stderrLog.append(data)
            }
        }
    }

    private func isPortBusy(baseURL: URL) -> Bool {
        guard let host = baseURL.host else { return false }
        let port = baseURL.port ?? (baseURL.scheme == "https" ? 443 : 80)
        var hints = addrinfo(
            ai_flags: AI_NUMERICHOST,
            ai_family: AF_UNSPEC,
            ai_socktype: SOCK_STREAM,
            ai_protocol: IPPROTO_TCP,
            ai_addrlen: 0,
            ai_canonname: nil,
            ai_addr: nil,
            ai_next: nil
        )
        var result: UnsafeMutablePointer<addrinfo>?
        defer { if result != nil { freeaddrinfo(result) } }
        guard getaddrinfo(host, String(port), &hints, &result) == 0, let info = result else {
            return false
        }
        let fd = socket(info.pointee.ai_family, info.pointee.ai_socktype, info.pointee.ai_protocol)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        var addr = info.pointee.ai_addr.withMemoryRebound(to: sockaddr.self, capacity: 1) { $0.pointee }
        let len = info.pointee.ai_addrlen
        return withUnsafePointer(to: &addr) { ptr in
            connect(fd, UnsafeRawPointer(ptr).assumingMemoryBound(to: sockaddr.self), len) == 0
        }
    }
}
