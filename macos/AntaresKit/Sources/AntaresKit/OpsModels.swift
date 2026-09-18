import Foundation

public struct VPSListResponse: Decodable, Sendable {
    public var hosts: [VPSHost]

    enum CodingKeys: String, CodingKey {
        case hosts
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        hosts = try container.decodeIfPresent([VPSHost].self, forKey: .hosts) ?? []
    }
}

public struct VPSHost: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var label: String
    public var host: String
    public var port: Int
    public var username: String
    public var authMethod: String
    public var hasPassword: Bool
    public var hasKey: Bool
    public var createdAt: String?
    public var updatedAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case host
        case port
        case username
        case authMethod = "auth_method"
        case hasPassword = "has_password"
        case hasKey = "has_key"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        label = try container.decodeIfPresent(String.self, forKey: .label) ?? ""
        host = try container.decodeIfPresent(String.self, forKey: .host) ?? ""
        port = try container.decodeIfPresent(Int.self, forKey: .port) ?? 22
        username = try container.decodeIfPresent(String.self, forKey: .username) ?? ""
        authMethod = try container.decodeIfPresent(String.self, forKey: .authMethod) ?? "password"
        hasPassword = try container.decodeIfPresent(Bool.self, forKey: .hasPassword) ?? false
        hasKey = try container.decodeIfPresent(Bool.self, forKey: .hasKey) ?? false
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt)
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt)
    }
}

public struct VPSSaveRequest: Encodable, Sendable {
    public var id: String?
    public var label: String
    public var host: String
    public var port: Int
    public var username: String
    public var authMethod: String
    public var password: String
    public var privateKey: String
    public var passphrase: String

    enum CodingKeys: String, CodingKey {
        case id
        case label
        case host
        case port
        case username
        case authMethod = "auth_method"
        case password
        case privateKey = "private_key"
        case passphrase
    }

    public init(
        id: String? = nil,
        label: String,
        host: String,
        port: Int,
        username: String,
        authMethod: String,
        password: String = "",
        privateKey: String = "",
        passphrase: String = ""
    ) {
        self.id = id
        self.label = label
        self.host = host
        self.port = port
        self.username = username
        self.authMethod = authMethod
        self.password = password
        self.privateKey = privateKey
        self.passphrase = passphrase
    }
}

public struct VPSSaveResponse: Decodable, Sendable {
    public var ok: Bool
    public var id: String?
}

public struct VPSTestResponse: Decodable, Sendable {
    public var ok: Bool
    public var asUser: String?
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case ok
        case asUser = "as"
        case error
    }

    public init(ok: Bool, asUser: String? = nil, error: String? = nil) {
        self.ok = ok
        self.asUser = asUser
        self.error = error
    }
}

public struct VPSMetrics: Decodable, Sendable, Hashable {
    public var reachable: Bool
    public var error: String?
    public var hostname: String
    public var os: String
    public var kernel: String
    public var uptime: String
    public var cpuCores: Int
    public var cpuPercent: Double
    public var load1: Double
    public var load5: Double
    public var load15: Double
    public var memTotalMB: Int
    public var memUsedMB: Int
    public var memPercent: Double
    public var swapTotalMB: Int
    public var swapUsedMB: Int
    public var swapPercent: Double
    public var diskTotalGB: Double
    public var diskUsedGB: Double
    public var diskPercent: Double
    public var processes: Int
    public var topProc: [String]

    enum CodingKeys: String, CodingKey {
        case reachable
        case error
        case hostname
        case os
        case kernel
        case uptime
        case cpuCores = "cpu_cores"
        case cpuPercent = "cpu_percent"
        case load1
        case load5
        case load15
        case memTotalMB = "mem_total_mb"
        case memUsedMB = "mem_used_mb"
        case memPercent = "mem_percent"
        case swapTotalMB = "swap_total_mb"
        case swapUsedMB = "swap_used_mb"
        case swapPercent = "swap_percent"
        case diskTotalGB = "disk_total_gb"
        case diskUsedGB = "disk_used_gb"
        case diskPercent = "disk_percent"
        case processes
        case topProc = "top_proc"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        reachable = try container.decodeIfPresent(Bool.self, forKey: .reachable) ?? false
        error = try container.decodeIfPresent(String.self, forKey: .error)
        hostname = try container.decodeIfPresent(String.self, forKey: .hostname) ?? ""
        os = try container.decodeIfPresent(String.self, forKey: .os) ?? ""
        kernel = try container.decodeIfPresent(String.self, forKey: .kernel) ?? ""
        uptime = try container.decodeIfPresent(String.self, forKey: .uptime) ?? ""
        cpuCores = try container.decodeIfPresent(Int.self, forKey: .cpuCores) ?? 0
        cpuPercent = try container.decodeIfPresent(Double.self, forKey: .cpuPercent) ?? 0
        load1 = try container.decodeIfPresent(Double.self, forKey: .load1) ?? 0
        load5 = try container.decodeIfPresent(Double.self, forKey: .load5) ?? 0
        load15 = try container.decodeIfPresent(Double.self, forKey: .load15) ?? 0
        memTotalMB = try container.decodeIfPresent(Int.self, forKey: .memTotalMB) ?? 0
        memUsedMB = try container.decodeIfPresent(Int.self, forKey: .memUsedMB) ?? 0
        memPercent = try container.decodeIfPresent(Double.self, forKey: .memPercent) ?? 0
        swapTotalMB = try container.decodeIfPresent(Int.self, forKey: .swapTotalMB) ?? 0
        swapUsedMB = try container.decodeIfPresent(Int.self, forKey: .swapUsedMB) ?? 0
        swapPercent = try container.decodeIfPresent(Double.self, forKey: .swapPercent) ?? 0
        diskTotalGB = try container.decodeIfPresent(Double.self, forKey: .diskTotalGB) ?? 0
        diskUsedGB = try container.decodeIfPresent(Double.self, forKey: .diskUsedGB) ?? 0
        diskPercent = try container.decodeIfPresent(Double.self, forKey: .diskPercent) ?? 0
        processes = try container.decodeIfPresent(Int.self, forKey: .processes) ?? 0
        topProc = try container.decodeIfPresent([String].self, forKey: .topProc) ?? []
    }

    public init(reachable: Bool, error: String? = nil) {
        self.reachable = reachable
        self.error = error
        hostname = ""
        os = ""
        kernel = ""
        uptime = ""
        cpuCores = 0
        cpuPercent = 0
        load1 = 0
        load5 = 0
        load15 = 0
        memTotalMB = 0
        memUsedMB = 0
        memPercent = 0
        swapTotalMB = 0
        swapUsedMB = 0
        swapPercent = 0
        diskTotalGB = 0
        diskUsedGB = 0
        diskPercent = 0
        processes = 0
        topProc = []
    }
}

public struct VPSProcessList: Decodable, Sendable {
    public var processes: [VPSProcess]
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case processes
        case error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        processes = try container.decodeIfPresent([VPSProcess].self, forKey: .processes) ?? []
        error = try container.decodeIfPresent(String.self, forKey: .error)
    }
}

public struct VPSProcess: Decodable, Identifiable, Sendable, Hashable {
    public var pid: Int
    public var user: String
    public var cpu: Double
    public var mem: Double
    public var command: String

    public var id: Int { pid }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        pid = try container.decodeIfPresent(Int.self, forKey: .pid) ?? 0
        user = try container.decodeIfPresent(String.self, forKey: .user) ?? ""
        cpu = try container.decodeIfPresent(Double.self, forKey: .cpu) ?? 0
        mem = try container.decodeIfPresent(Double.self, forKey: .mem) ?? 0
        command = try container.decodeIfPresent(String.self, forKey: .command) ?? ""
    }

    enum CodingKeys: String, CodingKey {
        case pid, user, cpu, mem, command
    }
}

public struct AnalyticsResponse: Decodable, Sendable {
    public var series: [AnalyticsUsagePoint]
    public var byModel: [AnalyticsModelUsage]
    public var totals: AnalyticsTotals

    enum CodingKeys: String, CodingKey {
        case series
        case byModel = "by_model"
        case totals
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        series = try container.decodeIfPresent([AnalyticsUsagePoint].self, forKey: .series) ?? []
        byModel = try container.decodeIfPresent([AnalyticsModelUsage].self, forKey: .byModel) ?? []
        totals = try container.decodeIfPresent(AnalyticsTotals.self, forKey: .totals) ?? AnalyticsTotals()
    }
}

public struct AnalyticsUsagePoint: Decodable, Identifiable, Sendable, Hashable {
    public var bucket: String
    public var tokensIn: Int
    public var tokensOut: Int
    public var cost: Double
    public var calls: Int

    public var id: String { bucket }

    enum CodingKeys: String, CodingKey {
        case bucket
        case tokensIn = "tokens_in"
        case tokensOut = "tokens_out"
        case cost
        case calls
    }
}

public struct AnalyticsModelUsage: Decodable, Identifiable, Sendable, Hashable {
    public var model: String
    public var provider: String
    public var calls: Int
    public var tokensIn: Int
    public var tokensOut: Int
    public var cost: Double

    public var id: String { "\(provider)/\(model)" }

    enum CodingKeys: String, CodingKey {
        case model
        case provider
        case calls
        case tokensIn = "tokens_in"
        case tokensOut = "tokens_out"
        case cost
    }
}

public struct AnalyticsTotals: Decodable, Sendable, Hashable {
    public var tokensIn: Int
    public var tokensOut: Int
    public var cost: Double
    public var calls: Int

    enum CodingKeys: String, CodingKey {
        case tokensIn = "tokens_in"
        case tokensOut = "tokens_out"
        case cost
        case calls
    }

    public init(
        tokensIn: Int = 0,
        tokensOut: Int = 0,
        cost: Double = 0,
        calls: Int = 0
    ) {
        self.tokensIn = tokensIn
        self.tokensOut = tokensOut
        self.cost = cost
        self.calls = calls
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        tokensIn = try container.decodeIfPresent(Int.self, forKey: .tokensIn) ?? 0
        tokensOut = try container.decodeIfPresent(Int.self, forKey: .tokensOut) ?? 0
        cost = try container.decodeIfPresent(Double.self, forKey: .cost) ?? 0
        calls = try container.decodeIfPresent(Int.self, forKey: .calls) ?? 0
    }
}

public struct FileListResponse: Decodable, Sendable {
    public var path: String
    public var parent: String
    public var entries: [FileEntry]

    enum CodingKeys: String, CodingKey {
        case path
        case parent
        case entries
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? "."
        parent = try container.decodeIfPresent(String.self, forKey: .parent) ?? ""
        entries = try container.decodeIfPresent([FileEntry].self, forKey: .entries) ?? []
    }
}

public struct FileEntry: Decodable, Identifiable, Sendable, Hashable {
    public var name: String
    public var path: String
    public var isDir: Bool
    public var size: Int
    public var modified: String

    public var id: String { path }

    enum CodingKeys: String, CodingKey {
        case name
        case path
        case isDir = "is_dir"
        case size
        case modified
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
        isDir = try container.decodeIfPresent(Bool.self, forKey: .isDir) ?? false
        size = try container.decodeIfPresent(Int.self, forKey: .size) ?? 0
        modified = try container.decodeIfPresent(String.self, forKey: .modified) ?? ""
    }
}

public struct FileReadResponse: Decodable, Sendable {
    public var content: String
    public var binary: Bool
    public var size: Int

    enum CodingKeys: String, CodingKey {
        case content
        case binary
        case size
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        content = try container.decodeIfPresent(String.self, forKey: .content) ?? ""
        binary = try container.decodeIfPresent(Bool.self, forKey: .binary) ?? false
        size = try container.decodeIfPresent(Int.self, forKey: .size) ?? 0
    }
}

public struct LogListResponse: Decodable, Sendable {
    public var entries: [LogEntry]

    enum CodingKeys: String, CodingKey {
        case entries
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        entries = try container.decodeIfPresent([LogEntry].self, forKey: .entries) ?? []
    }
}

public struct LogEntry: Decodable, Sendable, Hashable {
    public var time: String
    public var level: String
    public var message: String
    public var source: String?
    public var attrs: [String: FlexibleJSON]?

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        time = try container.decodeIfPresent(String.self, forKey: .time) ?? ""
        level = try container.decodeIfPresent(String.self, forKey: .level) ?? ""
        message = try container.decodeIfPresent(String.self, forKey: .message) ?? ""
        source = try container.decodeIfPresent(String.self, forKey: .source)
        attrs = try container.decodeIfPresent([String: FlexibleJSON].self, forKey: .attrs)
    }

    enum CodingKeys: String, CodingKey {
        case time, level, message, source, attrs
    }
}

public struct SystemStats: Decodable, Sendable {
    public var version: String
    public var goVersion: String
    public var os: String
    public var arch: String
    public var uptimeSeconds: Int
    public var goroutines: Int
    public var memoryAlloc: Int
    public var memorySys: Int
    public var cpuCount: Int
    public var workspace: String
    public var configPath: String
    public var database: SystemDatabaseStats
    public var store: SystemStoreStats
    public var activeShells: Int
    public var model: String
    public var provider: String
    public var ragProvider: String

    enum CodingKeys: String, CodingKey {
        case version
        case goVersion = "go_version"
        case os
        case arch
        case uptimeSeconds = "uptime_seconds"
        case goroutines
        case memoryAlloc = "memory_alloc"
        case memorySys = "memory_sys"
        case cpuCount = "cpu_count"
        case workspace
        case configPath = "config_path"
        case database
        case store
        case activeShells = "active_shells"
        case model
        case provider
        case ragProvider = "rag_provider"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        version = try container.decodeIfPresent(String.self, forKey: .version) ?? ""
        goVersion = try container.decodeIfPresent(String.self, forKey: .goVersion) ?? ""
        os = try container.decodeIfPresent(String.self, forKey: .os) ?? ""
        arch = try container.decodeIfPresent(String.self, forKey: .arch) ?? ""
        uptimeSeconds = try container.decodeIfPresent(Int.self, forKey: .uptimeSeconds) ?? 0
        goroutines = try container.decodeIfPresent(Int.self, forKey: .goroutines) ?? 0
        memoryAlloc = try container.decodeIfPresent(Int.self, forKey: .memoryAlloc) ?? 0
        memorySys = try container.decodeIfPresent(Int.self, forKey: .memorySys) ?? 0
        cpuCount = try container.decodeIfPresent(Int.self, forKey: .cpuCount) ?? 0
        workspace = try container.decodeIfPresent(String.self, forKey: .workspace) ?? ""
        configPath = try container.decodeIfPresent(String.self, forKey: .configPath) ?? ""
        database = try container.decodeIfPresent(SystemDatabaseStats.self, forKey: .database) ?? SystemDatabaseStats()
        store = try container.decodeIfPresent(SystemStoreStats.self, forKey: .store) ?? SystemStoreStats()
        activeShells = try container.decodeIfPresent(Int.self, forKey: .activeShells) ?? 0
        model = try container.decodeIfPresent(String.self, forKey: .model) ?? ""
        provider = try container.decodeIfPresent(String.self, forKey: .provider) ?? ""
        ragProvider = try container.decodeIfPresent(String.self, forKey: .ragProvider) ?? ""
    }
}

public struct SystemDatabaseStats: Decodable, Sendable, Hashable {
    public var driver: String
    public var sizeBytes: Int

    enum CodingKeys: String, CodingKey {
        case driver
        case sizeBytes = "size_bytes"
    }

    public init(driver: String = "", sizeBytes: Int = 0) {
        self.driver = driver
        self.sizeBytes = sizeBytes
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        driver = try container.decodeIfPresent(String.self, forKey: .driver) ?? ""
        sizeBytes = try container.decodeIfPresent(Int.self, forKey: .sizeBytes) ?? 0
    }
}

public struct SystemStoreStats: Decodable, Sendable, Hashable {
    public var sessions: Int
    public var messages: Int
    public var memories: Int
    public var cronJobs: Int
    public var tokensIn: Int
    public var tokensOut: Int
    public var cost: Double
    public var emptySessions: Int
    public var sizeBytes: Int

    enum CodingKeys: String, CodingKey {
        case sessions
        case messages
        case memories
        case cronJobs = "cron_jobs"
        case tokensIn = "tokens_in"
        case tokensOut = "tokens_out"
        case cost
        case emptySessions = "empty_sessions"
        case sizeBytes = "size_bytes"
    }

    public init(
        sessions: Int = 0,
        messages: Int = 0,
        memories: Int = 0,
        cronJobs: Int = 0,
        tokensIn: Int = 0,
        tokensOut: Int = 0,
        cost: Double = 0,
        emptySessions: Int = 0,
        sizeBytes: Int = 0
    ) {
        self.sessions = sessions
        self.messages = messages
        self.memories = memories
        self.cronJobs = cronJobs
        self.tokensIn = tokensIn
        self.tokensOut = tokensOut
        self.cost = cost
        self.emptySessions = emptySessions
        self.sizeBytes = sizeBytes
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        sessions = try container.decodeIfPresent(Int.self, forKey: .sessions) ?? 0
        messages = try container.decodeIfPresent(Int.self, forKey: .messages) ?? 0
        memories = try container.decodeIfPresent(Int.self, forKey: .memories) ?? 0
        cronJobs = try container.decodeIfPresent(Int.self, forKey: .cronJobs) ?? 0
        tokensIn = try container.decodeIfPresent(Int.self, forKey: .tokensIn) ?? 0
        tokensOut = try container.decodeIfPresent(Int.self, forKey: .tokensOut) ?? 0
        cost = try container.decodeIfPresent(Double.self, forKey: .cost) ?? 0
        emptySessions = try container.decodeIfPresent(Int.self, forKey: .emptySessions) ?? 0
        sizeBytes = try container.decodeIfPresent(Int.self, forKey: .sizeBytes) ?? 0
    }
}

public struct RuntimeStatus: Decodable, Sendable {
    public var ok: Bool
    public var version: String
    public var model: String
    public var provider: String
    public var providerReady: Bool
    public var database: String
    public var uptimeSeconds: Int
    public var activeSessions: Int
    public var needsSetup: Bool

    enum CodingKeys: String, CodingKey {
        case ok
        case version
        case model
        case provider
        case providerReady = "provider_ready"
        case database
        case uptimeSeconds = "uptime_seconds"
        case activeSessions = "active_sessions"
        case needsSetup = "needs_setup"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        ok = try container.decodeIfPresent(Bool.self, forKey: .ok) ?? false
        version = try container.decodeIfPresent(String.self, forKey: .version) ?? ""
        model = try container.decodeIfPresent(String.self, forKey: .model) ?? ""
        provider = try container.decodeIfPresent(String.self, forKey: .provider) ?? ""
        providerReady = try container.decodeIfPresent(Bool.self, forKey: .providerReady) ?? false
        database = try container.decodeIfPresent(String.self, forKey: .database) ?? ""
        uptimeSeconds = try container.decodeIfPresent(Int.self, forKey: .uptimeSeconds) ?? 0
        activeSessions = try container.decodeIfPresent(Int.self, forKey: .activeSessions) ?? 0
        needsSetup = try container.decodeIfPresent(Bool.self, forKey: .needsSetup) ?? false
    }
}

extension APIClient {
    public func listVPS() async throws -> VPSListResponse {
        try await get("/api/vps")
    }

    public func saveVPS(_ request: VPSSaveRequest) async throws -> VPSSaveResponse {
        try await post("/api/vps", body: request)
    }

    public func testVPS(_ request: VPSSaveRequest) async throws -> VPSTestResponse {
        try await post("/api/vps/test", body: request)
    }

    public func vpsMetrics(id: String) async throws -> VPSMetrics {
        try await get("/api/vps/\(encodedPath(id))/metrics")
    }

    public func vpsProcesses(id: String) async throws -> VPSProcessList {
        try await get("/api/vps/\(encodedPath(id))/processes")
    }

    public func deleteVPS(_ id: String) async throws -> OKResponse {
        try await delete("/api/vps/\(encodedPath(id))")
    }

    public func analytics(range: String, bucket: String) async throws -> AnalyticsResponse {
        try await get("/api/analytics", query: [
            URLQueryItem(name: "range", value: range),
            URLQueryItem(name: "bucket", value: bucket),
        ])
    }

    public func listFiles(path: String = ".") async throws -> FileListResponse {
        try await get("/api/files", query: [URLQueryItem(name: "path", value: path)])
    }

    public func readFile(path: String) async throws -> FileReadResponse {
        try await get("/api/files/read", query: [URLQueryItem(name: "path", value: path)])
    }

    public func listLogs(limit: Int = 500, level: String = "ALL", query: String = "") async throws -> LogListResponse {
        var items = [
            URLQueryItem(name: "limit", value: String(limit)),
            URLQueryItem(name: "level", value: level),
        ]
        if !query.isEmpty {
            items.append(URLQueryItem(name: "q", value: query))
        }
        return try await get("/api/logs", query: items)
    }

    public func streamLogs(
        onEvent: @escaping @Sendable (LogEntry) async -> Void
    ) async throws {
        try await streamGetJSON("/api/logs/stream", onEvent: onEvent)
    }

    public func systemStats() async throws -> SystemStats {
        try await get("/api/system/stats")
    }

    public func runtimeStatus() async throws -> RuntimeStatus {
        try await get("/api/status")
    }
}
