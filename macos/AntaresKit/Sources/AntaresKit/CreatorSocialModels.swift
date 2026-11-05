import Foundation

public struct CreatorProjectsResponse: Decodable, Sendable {
    public var projects: [CreatorProject]

    enum CodingKeys: String, CodingKey {
        case projects
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        projects = try container.decodeIfPresent([CreatorProject].self, forKey: .projects) ?? []
    }
}

public struct CreatorProject: Codable, Identifiable, Sendable {
    public var id: String
    public var title: String
    public var brief: String
    public var platform: String
    public var accountID: String
    public var style: String
    public var size: String
    public var targetSeconds: Int
    public var caption: String
    public var revision: Int
    public var publishMode: String
    public var status: String
    public var error: String
    public var createdAt: String
    public var updatedAt: String
    public var runSessionID: String
    public var runStage: String
    public var runPublishMode: String
    public var runStatus: String
    public var lastRunError: String
    public var runStartedAt: String
    public var runFinishedAt: String
    public var references: [CreatorReference]
    public var shots: [CreatorShot]
    public var research: [CreatorTrend]
    public var ideas: [CreatorIdea]
    public var finalPath: String
    public var finalHash: String
    public var publication: CreatorPublication

    public var isRunning: Bool { runStatus == "running" }

    enum CodingKeys: String, CodingKey {
        case id
        case title
        case brief
        case platform
        case accountID = "account_id"
        case style
        case size
        case targetSeconds = "target_seconds"
        case caption
        case revision
        case publishMode = "publish_mode"
        case status
        case error
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case runSessionID = "run_session_id"
        case runStage = "run_stage"
        case runPublishMode = "run_publish_mode"
        case runStatus = "run_status"
        case lastRunError = "last_run_error"
        case runStartedAt = "run_started_at"
        case runFinishedAt = "run_finished_at"
        case references
        case shots
        case research
        case ideas
        case finalPath = "final_path"
        case finalHash = "final_hash"
        case publication
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        brief = try container.decodeIfPresent(String.self, forKey: .brief) ?? ""
        platform = try container.decodeIfPresent(String.self, forKey: .platform) ?? ""
        accountID = try container.decodeIfPresent(String.self, forKey: .accountID) ?? ""
        style = try container.decodeIfPresent(String.self, forKey: .style) ?? ""
        size = try container.decodeIfPresent(String.self, forKey: .size) ?? ""
        targetSeconds = try container.decodeIfPresent(Int.self, forKey: .targetSeconds) ?? 0
        caption = try container.decodeIfPresent(String.self, forKey: .caption) ?? ""
        revision = try container.decodeIfPresent(Int.self, forKey: .revision) ?? 0
        publishMode = try container.decodeIfPresent(String.self, forKey: .publishMode) ?? "draft"
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        runSessionID = try container.decodeIfPresent(String.self, forKey: .runSessionID) ?? ""
        runStage = try container.decodeIfPresent(String.self, forKey: .runStage) ?? ""
        runPublishMode = try container.decodeIfPresent(String.self, forKey: .runPublishMode) ?? ""
        runStatus = try container.decodeIfPresent(String.self, forKey: .runStatus) ?? ""
        lastRunError = try container.decodeIfPresent(String.self, forKey: .lastRunError) ?? ""
        runStartedAt = try container.decodeIfPresent(String.self, forKey: .runStartedAt) ?? ""
        runFinishedAt = try container.decodeIfPresent(String.self, forKey: .runFinishedAt) ?? ""
        references = try container.decodeIfPresent([CreatorReference].self, forKey: .references) ?? []
        shots = try container.decodeIfPresent([CreatorShot].self, forKey: .shots) ?? []
        research = try container.decodeIfPresent([CreatorTrend].self, forKey: .research) ?? []
        ideas = try container.decodeIfPresent([CreatorIdea].self, forKey: .ideas) ?? []
        finalPath = try container.decodeIfPresent(String.self, forKey: .finalPath) ?? ""
        finalHash = try container.decodeIfPresent(String.self, forKey: .finalHash) ?? ""
        publication = try container.decodeIfPresent(CreatorPublication.self, forKey: .publication) ?? CreatorPublication()
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(id, forKey: .id)
        try container.encode(title, forKey: .title)
        try container.encode(brief, forKey: .brief)
        try container.encode(platform, forKey: .platform)
        try container.encode(accountID, forKey: .accountID)
        try container.encode(style, forKey: .style)
        try container.encode(size, forKey: .size)
        try container.encode(targetSeconds, forKey: .targetSeconds)
        try container.encode(caption, forKey: .caption)
        try container.encode(revision, forKey: .revision)
        try container.encode(publishMode, forKey: .publishMode)
        try container.encode(status, forKey: .status)
        try container.encode(error, forKey: .error)
        try container.encode(createdAt, forKey: .createdAt)
        try container.encode(updatedAt, forKey: .updatedAt)
        try container.encode(runSessionID, forKey: .runSessionID)
        try container.encode(runStage, forKey: .runStage)
        try container.encode(runPublishMode, forKey: .runPublishMode)
        try container.encode(runStatus, forKey: .runStatus)
        try container.encode(lastRunError, forKey: .lastRunError)
        try container.encode(runStartedAt, forKey: .runStartedAt)
        try container.encode(runFinishedAt, forKey: .runFinishedAt)
        try container.encode(references, forKey: .references)
        try container.encode(shots, forKey: .shots)
        try container.encode(research, forKey: .research)
        try container.encode(ideas, forKey: .ideas)
        try container.encode(finalPath, forKey: .finalPath)
        try container.encode(finalHash, forKey: .finalHash)
        try container.encode(publication, forKey: .publication)
    }
}

public struct CreatorReference: Codable, Identifiable, Sendable, Hashable {
    public var id: String
    public var kind: String
    public var name: String
    public var prompt: String
    public var path: String
    public var status: String
    public var error: String

    public init(
        id: String,
        kind: String = "character",
        name: String = "",
        prompt: String = "",
        path: String = "",
        status: String = "draft",
        error: String = ""
    ) {
        self.id = id
        self.kind = kind
        self.name = name
        self.prompt = prompt
        self.path = path
        self.status = status
        self.error = error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        kind = try container.decodeIfPresent(String.self, forKey: .kind) ?? "character"
        name = try container.decodeIfPresent(String.self, forKey: .name) ?? ""
        prompt = try container.decodeIfPresent(String.self, forKey: .prompt) ?? ""
        path = try container.decodeIfPresent(String.self, forKey: .path) ?? ""
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
    }
}

public struct CreatorShot: Codable, Identifiable, Sendable, Hashable {
    public var id: String
    public var prompt: String
    public var referenceIDs: [String]
    public var durationSeconds: Int
    public var continuity: String
    public var notes: String
    public var keyframePath: String
    public var videoJobID: String
    public var videoPath: String
    public var lastFramePath: String
    public var status: String
    public var error: String
    public var progress: Int

    enum CodingKeys: String, CodingKey {
        case id
        case prompt
        case referenceIDs = "reference_ids"
        case durationSeconds = "duration_seconds"
        case continuity
        case notes
        case keyframePath = "keyframe_path"
        case videoJobID = "video_job_id"
        case videoPath = "video_path"
        case lastFramePath = "last_frame_path"
        case status
        case error
        case progress
    }

    public init(
        id: String,
        prompt: String = "",
        referenceIDs: [String] = [],
        durationSeconds: Int = 8,
        continuity: String = "cut",
        notes: String = "",
        keyframePath: String = "",
        videoJobID: String = "",
        videoPath: String = "",
        lastFramePath: String = "",
        status: String = "draft",
        error: String = "",
        progress: Int = 0
    ) {
        self.id = id
        self.prompt = prompt
        self.referenceIDs = referenceIDs
        self.durationSeconds = durationSeconds
        self.continuity = continuity
        self.notes = notes
        self.keyframePath = keyframePath
        self.videoJobID = videoJobID
        self.videoPath = videoPath
        self.lastFramePath = lastFramePath
        self.status = status
        self.error = error
        self.progress = progress
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        prompt = try container.decodeIfPresent(String.self, forKey: .prompt) ?? ""
        referenceIDs = try container.decodeIfPresent([String].self, forKey: .referenceIDs) ?? []
        durationSeconds = try container.decodeIfPresent(Int.self, forKey: .durationSeconds) ?? 0
        continuity = try container.decodeIfPresent(String.self, forKey: .continuity) ?? "cut"
        notes = try container.decodeIfPresent(String.self, forKey: .notes) ?? ""
        keyframePath = try container.decodeIfPresent(String.self, forKey: .keyframePath) ?? ""
        videoJobID = try container.decodeIfPresent(String.self, forKey: .videoJobID) ?? ""
        videoPath = try container.decodeIfPresent(String.self, forKey: .videoPath) ?? ""
        lastFramePath = try container.decodeIfPresent(String.self, forKey: .lastFramePath) ?? ""
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
        progress = try container.decodeIfPresent(Int.self, forKey: .progress) ?? 0
    }
}

public struct CreatorTrend: Codable, Identifiable, Sendable {
    public var url: String
    public var title: String
    public var platform: String
    public var observedAt: String
    public var notes: String
    public var metrics: [String: Double]?

    public var id: String { "\(url)|\(observedAt)|\(title)" }

    enum CodingKeys: String, CodingKey {
        case url
        case title
        case platform
        case observedAt = "observed_at"
        case notes
        case metrics
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        url = try container.decodeIfPresent(String.self, forKey: .url) ?? ""
        title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        platform = try container.decodeIfPresent(String.self, forKey: .platform) ?? ""
        observedAt = try container.decodeIfPresent(String.self, forKey: .observedAt) ?? ""
        notes = try container.decodeIfPresent(String.self, forKey: .notes) ?? ""
        metrics = try container.decodeIfPresent([String: Double].self, forKey: .metrics)
    }
}

public struct CreatorIdea: Codable, Identifiable, Sendable, Hashable {
    public var id: String
    public var title: String
    public var hook: String
    public var rationale: String
    public var selected: Bool

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        title = try container.decodeIfPresent(String.self, forKey: .title) ?? ""
        hook = try container.decodeIfPresent(String.self, forKey: .hook) ?? ""
        rationale = try container.decodeIfPresent(String.self, forKey: .rationale) ?? ""
        selected = try container.decodeIfPresent(Bool.self, forKey: .selected) ?? false
    }
}

public struct CreatorPublication: Codable, Sendable, Hashable {
    public var status: String
    public var postURL: String
    public var error: String
    public var accountID: String
    public var artifactHash: String
    public var proof: String
    public var preparedAt: String
    public var publishedAt: String

    enum CodingKeys: String, CodingKey {
        case status
        case postURL = "post_url"
        case error
        case accountID = "account_id"
        case artifactHash = "artifact_hash"
        case proof
        case preparedAt = "prepared_at"
        case publishedAt = "published_at"
    }

    public init(
        status: String = "not_started",
        postURL: String = "",
        error: String = "",
        accountID: String = "",
        artifactHash: String = "",
        proof: String = "",
        preparedAt: String = "",
        publishedAt: String = ""
    ) {
        self.status = status
        self.postURL = postURL
        self.error = error
        self.accountID = accountID
        self.artifactHash = artifactHash
        self.proof = proof
        self.preparedAt = preparedAt
        self.publishedAt = publishedAt
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? "not_started"
        postURL = try container.decodeIfPresent(String.self, forKey: .postURL) ?? ""
        error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
        accountID = try container.decodeIfPresent(String.self, forKey: .accountID) ?? ""
        artifactHash = try container.decodeIfPresent(String.self, forKey: .artifactHash) ?? ""
        proof = try container.decodeIfPresent(String.self, forKey: .proof) ?? ""
        preparedAt = try container.decodeIfPresent(String.self, forKey: .preparedAt) ?? ""
        publishedAt = try container.decodeIfPresent(String.self, forKey: .publishedAt) ?? ""
    }
}

public struct CreatorProjectDraft: Encodable, Sendable {
    public var title: String
    public var brief: String
    public var platform: String
    public var size: String
    public var targetSeconds: Int
    public var style: String
    public var publishMode: String

    enum CodingKeys: String, CodingKey {
        case title
        case brief
        case platform
        case size
        case targetSeconds = "target_seconds"
        case style
        case publishMode = "publish_mode"
    }

    public init(
        title: String,
        brief: String,
        platform: String,
        size: String,
        targetSeconds: Int,
        style: String,
        publishMode: String
    ) {
        self.title = title
        self.brief = brief
        self.platform = platform
        self.size = size
        self.targetSeconds = targetSeconds
        self.style = style
        self.publishMode = publishMode
    }
}

public struct CreatorActionRequest: Encodable, Sendable {
    public var action: String
    public var targetID: String?
    public var confirmed: Bool?

    enum CodingKeys: String, CodingKey {
        case action
        case targetID = "target_id"
        case confirmed
    }

    public init(action: String, targetID: String? = nil, confirmed: Bool? = nil) {
        self.action = action
        self.targetID = targetID
        self.confirmed = confirmed
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encode(action, forKey: .action)
        try container.encodeIfPresent(targetID, forKey: .targetID)
        try container.encodeIfPresent(confirmed, forKey: .confirmed)
    }
}

public struct CreatorRunRequest: Encodable, Sendable {
    public var stage: String
    public var publishMode: String

    enum CodingKeys: String, CodingKey {
        case stage
        case publishMode = "publish_mode"
    }

    public init(stage: String, publishMode: String) {
        self.stage = stage
        self.publishMode = publishMode
    }
}

public struct CreatorRunResponse: Decodable, Sendable {
    public var sessionID: String

    enum CodingKeys: String, CodingKey {
        case sessionID = "session_id"
    }
}

public struct CreatorReconcileRequest: Encodable, Sendable {
    public var proof: String
    public var notPublished: Bool

    enum CodingKeys: String, CodingKey {
        case proof
        case notPublished = "not_published"
    }

    public init(proof: String, notPublished: Bool) {
        self.proof = proof
        self.notPublished = notPublished
    }
}

public struct CreatorSettings: Decodable, Sendable {
    public var image: CreatorProviderSettings
    public var video: CreatorProviderSettings
    public var ffmpeg: Bool
    public var ffprobe: Bool

    enum CodingKeys: String, CodingKey {
        case image, video, ffmpeg, ffprobe
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        image = try container.decodeIfPresent(CreatorProviderSettings.self, forKey: .image) ?? CreatorProviderSettings()
        video = try container.decodeIfPresent(CreatorProviderSettings.self, forKey: .video) ?? CreatorProviderSettings()
        ffmpeg = try container.decodeIfPresent(Bool.self, forKey: .ffmpeg) ?? false
        ffprobe = try container.decodeIfPresent(Bool.self, forKey: .ffprobe) ?? false
    }
}

public struct CreatorProviderSettings: Decodable, Sendable {
    public var enabled: Bool
    public var provider: String
    public var model: String
    public var baseURL: String
    public var size: String
    public var seconds: Int?
    public var hasKey: Bool

    enum CodingKeys: String, CodingKey {
        case enabled
        case provider
        case model
        case baseURL = "base_url"
        case size
        case seconds
        case hasKey = "has_key"
    }

    public init(
        enabled: Bool = false,
        provider: String = "",
        model: String = "",
        baseURL: String = "",
        size: String = "",
        seconds: Int? = nil,
        hasKey: Bool = false
    ) {
        self.enabled = enabled
        self.provider = provider
        self.model = model
        self.baseURL = baseURL
        self.size = size
        self.seconds = seconds
        self.hasKey = hasKey
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        provider = try container.decodeIfPresent(String.self, forKey: .provider) ?? ""
        model = try container.decodeIfPresent(String.self, forKey: .model) ?? ""
        baseURL = try container.decodeIfPresent(String.self, forKey: .baseURL) ?? ""
        size = try container.decodeIfPresent(String.self, forKey: .size) ?? ""
        seconds = try container.decodeIfPresent(Int.self, forKey: .seconds)
        hasKey = try container.decodeIfPresent(Bool.self, forKey: .hasKey) ?? false
    }

    public var patch: CreatorProviderPatch {
        CreatorProviderPatch(
            enabled: enabled,
            provider: provider,
            model: model,
            baseURL: baseURL,
            size: size,
            seconds: seconds
        )
    }
}

public struct CreatorSettingsPatch: Encodable, Sendable {
    public var image: CreatorProviderPatch?
    public var video: CreatorProviderPatch?

    enum CodingKeys: String, CodingKey {
        case image, video
    }

    public init(image: CreatorProviderPatch? = nil, video: CreatorProviderPatch? = nil) {
        self.image = image
        self.video = video
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encodeIfPresent(image, forKey: .image)
        try container.encodeIfPresent(video, forKey: .video)
    }
}

public struct CreatorProviderPatch: Encodable, Sendable {
    public var enabled: Bool?
    public var provider: String?
    public var model: String?
    public var baseURL: String?
    public var size: String?
    public var seconds: Int?
    public var apiKey: String?

    enum CodingKeys: String, CodingKey {
        case enabled
        case provider
        case model
        case baseURL = "base_url"
        case size
        case seconds
        case apiKey = "api_key"
    }

    public init(
        enabled: Bool? = nil,
        provider: String? = nil,
        model: String? = nil,
        baseURL: String? = nil,
        size: String? = nil,
        seconds: Int? = nil,
        apiKey: String? = nil
    ) {
        self.enabled = enabled
        self.provider = provider
        self.model = model
        self.baseURL = baseURL
        self.size = size
        self.seconds = seconds
        self.apiKey = apiKey
    }

    public func encode(to encoder: Encoder) throws {
        var container = encoder.container(keyedBy: CodingKeys.self)
        try container.encodeIfPresent(enabled, forKey: .enabled)
        try container.encodeIfPresent(provider, forKey: .provider)
        try container.encodeIfPresent(model, forKey: .model)
        try container.encodeIfPresent(baseURL, forKey: .baseURL)
        try container.encodeIfPresent(size, forKey: .size)
        try container.encodeIfPresent(seconds, forKey: .seconds)
        try container.encodeIfPresent(apiKey, forKey: .apiKey)
    }
}

public struct CreatorCronJobRequest: Encodable, Sendable {
    public var name: String
    public var schedule: String
    public var role: String
    public var contentProjectID: String
    public var contentStage: String
    public var publishMode: String

    enum CodingKeys: String, CodingKey {
        case name
        case schedule
        case role
        case contentProjectID = "content_project_id"
        case contentStage = "content_stage"
        case publishMode = "publish_mode"
    }

    public init(
        name: String,
        schedule: String,
        role: String,
        contentProjectID: String,
        contentStage: String,
        publishMode: String
    ) {
        self.name = name
        self.schedule = schedule
        self.role = role
        self.contentProjectID = contentProjectID
        self.contentStage = contentStage
        self.publishMode = publishMode
    }
}

public struct SocialStatus: Decodable, Sendable {
    public var enabled: Bool
    public var encryptionReady: Bool
    public var imapConfigured: Bool
    public var imapHost: String
    public var imapPort: Int
    public var imapUsername: String
    public var browser: SocialBrowserStatus
    public var autopilotEnabled: Bool
    public var accounts: [SocialAccount]

    enum CodingKeys: String, CodingKey {
        case enabled
        case encryptionReady = "encryption_ready"
        case imapConfigured = "imap_configured"
        case imapHost = "imap_host"
        case imapPort = "imap_port"
        case imapUsername = "imap_username"
        case browser
        case autopilotEnabled = "autopilot_enabled"
        case accounts
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        encryptionReady = try container.decodeIfPresent(Bool.self, forKey: .encryptionReady) ?? false
        imapConfigured = try container.decodeIfPresent(Bool.self, forKey: .imapConfigured) ?? false
        imapHost = try container.decodeIfPresent(String.self, forKey: .imapHost) ?? ""
        imapPort = try container.decodeIfPresent(Int.self, forKey: .imapPort) ?? 0
        imapUsername = try container.decodeIfPresent(String.self, forKey: .imapUsername) ?? ""
        browser = try container.decodeIfPresent(SocialBrowserStatus.self, forKey: .browser) ?? SocialBrowserStatus()
        autopilotEnabled = try container.decodeIfPresent(Bool.self, forKey: .autopilotEnabled) ?? false
        accounts = try container.decodeIfPresent([SocialAccount].self, forKey: .accounts) ?? []
    }
}

public struct SocialBrowserStatus: Decodable, Sendable {
    public var enabled: Bool
    public var state: String
    public var error: String

    enum CodingKeys: String, CodingKey {
        case enabled, state, error
    }

    public init(enabled: Bool = false, state: String = "disabled", error: String = "") {
        self.enabled = enabled
        self.state = state
        self.error = error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try container.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        state = try container.decodeIfPresent(String.self, forKey: .state) ?? "disabled"
        error = try container.decodeIfPresent(String.self, forKey: .error) ?? ""
    }
}

public struct SocialAccount: Decodable, Identifiable, Sendable, Hashable {
    public var id: String
    public var platform: String
    public var displayName: String
    public var username: String
    public var profileURL: String
    public var status: String
    public var ragNamespace: String
    public var skillName: String
    public var lastCheckedAt: String?
    public var createdAt: String
    public var updatedAt: String
    public var hasPassword: Bool
    public var hasRecovery: Bool

    public var label: String {
        displayName.isEmpty ? username : displayName
    }

    enum CodingKeys: String, CodingKey {
        case id
        case platform
        case displayName = "display_name"
        case username
        case profileURL = "profile_url"
        case status
        case ragNamespace = "rag_namespace"
        case skillName = "skill_name"
        case lastCheckedAt = "last_checked_at"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case hasPassword = "has_password"
        case hasRecovery = "has_recovery"
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        id = try container.decodeIfPresent(String.self, forKey: .id) ?? ""
        platform = try container.decodeIfPresent(String.self, forKey: .platform) ?? ""
        displayName = try container.decodeIfPresent(String.self, forKey: .displayName) ?? ""
        username = try container.decodeIfPresent(String.self, forKey: .username) ?? ""
        profileURL = try container.decodeIfPresent(String.self, forKey: .profileURL) ?? ""
        status = try container.decodeIfPresent(String.self, forKey: .status) ?? ""
        ragNamespace = try container.decodeIfPresent(String.self, forKey: .ragNamespace) ?? ""
        skillName = try container.decodeIfPresent(String.self, forKey: .skillName) ?? ""
        lastCheckedAt = try container.decodeIfPresent(String.self, forKey: .lastCheckedAt)
        createdAt = try container.decodeIfPresent(String.self, forKey: .createdAt) ?? ""
        updatedAt = try container.decodeIfPresent(String.self, forKey: .updatedAt) ?? ""
        hasPassword = try container.decodeIfPresent(Bool.self, forKey: .hasPassword) ?? false
        hasRecovery = try container.decodeIfPresent(Bool.self, forKey: .hasRecovery) ?? false
    }
}

public struct SocialIMAPRequest: Encodable, Sendable {
    public var host: String
    public var port: Int
    public var username: String
    public var password: String

    public init(host: String, port: Int, username: String, password: String) {
        self.host = host
        self.port = port
        self.username = username
        self.password = password
    }
}

public struct SocialAccountRequest: Encodable, Sendable {
    public var platform: String
    public var username: String
    public var password: String
    public var recoveryCodes: String
    public var profileURL: String
    public var status: String

    enum CodingKeys: String, CodingKey {
        case platform
        case username
        case password
        case recoveryCodes = "recovery_codes"
        case profileURL = "profile_url"
        case status
    }

    public init(
        platform: String,
        username: String,
        password: String,
        recoveryCodes: String,
        profileURL: String,
        status: String
    ) {
        self.platform = platform
        self.username = username
        self.password = password
        self.recoveryCodes = recoveryCodes
        self.profileURL = profileURL
        self.status = status
    }
}

public struct SocialAutopilotRequest: Encodable, Sendable {
    public var enabled: Bool

    public init(enabled: Bool) {
        self.enabled = enabled
    }
}

public struct SocialEncryptionSetupResponse: Decodable, Sendable {
    public var ok: Bool
    public var recoveryKey: String
    public var message: String?

    enum CodingKeys: String, CodingKey {
        case ok
        case recoveryKey = "recovery_key"
        case message
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        ok = try container.decodeIfPresent(Bool.self, forKey: .ok) ?? false
        recoveryKey = try container.decodeIfPresent(String.self, forKey: .recoveryKey) ?? ""
        message = try container.decodeIfPresent(String.self, forKey: .message)
    }
}

public struct SocialIMAPTestResponse: Decodable, Sendable {
    public var ok: Bool
    public var inboxCount: Int?
    public var error: String?

    enum CodingKeys: String, CodingKey {
        case ok
        case inboxCount = "inbox_count"
        case error
    }

    public init(from decoder: Decoder) throws {
        let container = try decoder.container(keyedBy: CodingKeys.self)
        ok = try container.decodeIfPresent(Bool.self, forKey: .ok) ?? false
        inboxCount = try container.decodeIfPresent(Int.self, forKey: .inboxCount)
        error = try container.decodeIfPresent(String.self, forKey: .error)
    }
}

extension CronJob {
    public var contentProjectID: String? {
        meta?["content_project_id"]?.stringValue
    }

    public var contentStage: String? {
        meta?["content_stage"]?.stringValue
    }

    public var publishMode: String? {
        meta?["publish_mode"]?.stringValue
    }
}

extension APIClient {
    public func listCreatorProjects() async throws -> CreatorProjectsResponse {
        try await get("/api/content-creator/projects")
    }

    public func creatorProject(_ id: String) async throws -> CreatorProject {
        try await get("/api/content-creator/projects/\(encodedPath(id))")
    }

    public func createCreatorProject(_ draft: CreatorProjectDraft) async throws -> CreatorProject {
        try await post("/api/content-creator/projects", body: draft)
    }

    public func updateCreatorProject(_ project: CreatorProject) async throws -> CreatorProject {
        try await put("/api/content-creator/projects/\(encodedPath(project.id))", body: project)
    }

    public func creatorAction(_ id: String, _ request: CreatorActionRequest) async throws -> CreatorProject {
        try await post("/api/content-creator/projects/\(encodedPath(id))/action", body: request)
    }

    public func runCreatorProject(_ id: String, stage: String, publishMode: String) async throws -> CreatorRunResponse {
        try await post(
            "/api/content-creator/projects/\(encodedPath(id))/run",
            body: CreatorRunRequest(stage: stage, publishMode: publishMode)
        )
    }

    public func reconcileCreatorProject(_ id: String, proof: String) async throws -> CreatorProject {
        try await post(
            "/api/content-creator/projects/\(encodedPath(id))/reconcile",
            body: CreatorReconcileRequest(proof: proof, notPublished: true)
        )
    }

    public func creatorSettings() async throws -> CreatorSettings {
        try await get("/api/content-creator/settings")
    }

    public func saveCreatorSettings(_ patch: CreatorSettingsPatch) async throws -> CreatorSettings {
        try await post("/api/content-creator/settings", body: patch)
    }

    public func creatorArtifact(projectID: String, path: String) async throws -> Data {
        try await getData(
            "/api/content-creator/projects/\(encodedPath(projectID))/artifact",
            query: [URLQueryItem(name: "path", value: path)]
        )
    }

    public func creatorArtifactURL(projectID: String, path: String) -> URL {
        var components = URLComponents(
            url: url("/api/content-creator/projects/\(encodedPath(projectID))/artifact"),
            resolvingAgainstBaseURL: true
        )!
        var items = [URLQueryItem(name: "path", value: path)]
        if let bearerToken, !bearerToken.isEmpty {
            items.append(URLQueryItem(name: "token", value: bearerToken))
        }
        components.queryItems = items
        return components.url!
    }

    public func createCreatorCronJob(_ request: CreatorCronJobRequest) async throws -> CronJob {
        try await post("/api/cron/jobs", body: request)
    }

    public func socialStatus() async throws -> SocialStatus {
        try await get("/api/social/status")
    }

    public func listSocialAccounts() async throws -> [SocialAccount] {
        try await get("/api/social/accounts")
    }

    public func setupSocialEncryption() async throws -> SocialEncryptionSetupResponse {
        try await post("/api/social/encryption/setup", body: EmptyBody())
    }

    public func testSocialIMAP(_ request: SocialIMAPRequest) async throws -> SocialIMAPTestResponse {
        try await post("/api/social/imap/test", body: request)
    }

    public func saveSocialIMAP(_ request: SocialIMAPRequest) async throws -> OKResponse {
        try await post("/api/social/imap/save", body: request)
    }

    public func startSocialBrowser() async throws -> OKResponse {
        try await post("/api/social/browser/start", body: EmptyBody())
    }

    public func stopSocialBrowser() async throws -> OKResponse {
        try await post("/api/social/browser/stop", body: EmptyBody())
    }

    public func setSocialAutopilot(enabled: Bool) async throws -> OKResponse {
        try await post("/api/social/autopilot", body: SocialAutopilotRequest(enabled: enabled))
    }

    public func addSocialAccount(_ request: SocialAccountRequest) async throws -> SocialAccount {
        try await post("/api/social/accounts", body: request)
    }

    public func deleteSocialAccount(_ id: String) async throws -> OKResponse {
        try await delete("/api/social/accounts/\(encodedPath(id))")
    }
}
