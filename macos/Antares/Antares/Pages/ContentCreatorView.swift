import AntaresKit
import AVKit
import AppKit
import SwiftUI

struct ContentCreatorView: View {
    private static let platforms = ["youtube", "tiktok", "instagram", "facebook", "x", "threads"]
    private static let sizes = ["720x1280", "1280x720", "1024x1792", "1792x1024"]
    private static let stages = ["research", "plan", "produce", "publish", "full"]
    private static let kinds = ["character", "setting", "style", "prop"]

    private enum Tab: String, CaseIterable, Identifiable {
        case brief, references, shots, research, publish, schedule, models
        var id: String { rawValue }
        var title: String { rawValue.capitalized }
    }

    @EnvironmentObject private var model: AppModel

    @State private var projects: [CreatorProject] = []
    @State private var project: CreatorProject?
    @State private var accounts: [SocialAccount] = []
    @State private var jobs: [CronJob] = []
    @State private var settings: CreatorSettings?
    @State private var imagePatch = CreatorProviderPatch()
    @State private var videoPatch = CreatorProviderPatch()
    @State private var tab: Tab = .brief
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busy = ""
    @State private var dirty = false
    @State private var showingCreate = false
    @State private var proof = ""
    @State private var scheduleName = ""
    @State private var scheduleCron = "0 8 * * *"
    @State private var scheduleStage = "research"
    @State private var schedulePublish = "draft"
    @State private var pendingReset: (kind: String, id: String)?
    @State private var pendingStage: String?
    @State private var pendingSwitchID: String?
    @State private var confirmPublishSchedule = false

    private var selectedID: String { project?.id ?? "" }
    private var locked: Bool { !busy.isEmpty || project?.isRunning == true }
    private var mediaLocked: Bool { locked || dirty }

    var body: some View {
        Group {
            if let errorMessage, projects.isEmpty, project == nil, !isLoading, tab != .models {
                ContentUnavailableView(
                    "Content Creator",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    Picker("Tab", selection: $tab) {
                        ForEach(Tab.allCases) { item in
                            Text(item.title).tag(item)
                        }
                    }
                    .pickerStyle(.segmented)
                    .padding(12)

                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                            .padding(.horizontal, 16)
                            .padding(.bottom, 8)
                    }

                    if tab == .models {
                        modelsForm
                    } else {
                        HSplitView {
                            projectList
                                .frame(minWidth: 200, idealWidth: 240)
                            detail
                                .frame(minWidth: 420)
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading video projects…") }
        }
        .navigationTitle("Content creator")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button("Refresh") {
                    Task { await refreshSelected() }
                }
                .disabled(project == nil || dirty || !busy.isEmpty)
            }
            ToolbarItem(placement: .automatic) {
                Button("Save plan") {
                    Task { await save() }
                }
                .disabled(project == nil || locked || !dirty)
            }
            ToolbarItem(placement: .primaryAction) {
                Button("New video") { showingCreate = true }
            }
        }
        .sheet(isPresented: $showingCreate) {
            CreateCreatorProjectSheet { created in
                accept(created)
            }
            .environmentObject(model)
        }
        .confirmationDialog(
            "Discard this generated asset?",
            isPresented: Binding(
                get: { pendingReset != nil },
                set: { if !$0 { pendingReset = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Reset", role: .destructive) {
                if let pendingReset {
                    Task { await reset(pendingReset.kind, pendingReset.id) }
                }
            }
            Button("Cancel", role: .cancel) { pendingReset = nil }
        } message: {
            Text("Regeneration may incur provider charges. For an uncertain submission, first verify the provider did not already accept a job.")
        }
        .confirmationDialog(
            "Allow this run to publish?",
            isPresented: Binding(
                get: { pendingStage != nil },
                set: { if !$0 { pendingStage = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Publish") {
                if let pendingStage {
                    Task { await run(pendingStage, confirmed: true) }
                }
            }
            Button("Cancel", role: .cancel) { pendingStage = nil }
        } message: {
            Text("Allow this run to publish the final video to the selected social account?")
        }
        .confirmationDialog(
            "Discard unsaved changes?",
            isPresented: Binding(
                get: { pendingSwitchID != nil },
                set: { if !$0 { pendingSwitchID = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Discard", role: .destructive) {
                if let pendingSwitchID {
                    Task { await choose(pendingSwitchID, force: true) }
                }
            }
            Button("Cancel", role: .cancel) { pendingSwitchID = nil }
        }
        .confirmationDialog(
            "Authorize publishing?",
            isPresented: $confirmPublishSchedule,
            titleVisibility: .visible
        ) {
            Button("Authorize") {
                Task { await createSchedule(confirmed: true) }
            }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("Authorize this recurring job to publish to the project account?")
        }
        .task { await load() }
        .task(id: selectedID) {
            guard !selectedID.isEmpty else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                if Task.isCancelled || dirty || !busy.isEmpty { continue }
                await refreshSelected(quiet: true)
            }
        }
        .refreshable { await load() }
    }

    private var projectList: some View {
        List(selection: Binding(
            get: { project?.id },
            set: { id in
                if let id { Task { await choose(id) } }
            }
        )) {
            Section("Video projects") {
                if projects.isEmpty {
                    Text("No projects yet. Create a video to start.")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(projects) { item in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(item.title)
                                .font(.headline)
                            Text("\(item.platform) · \(item.status)")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        .tag(item.id)
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var detail: some View {
        if let project {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    header(project)
                    switch tab {
                    case .brief: briefTab(project)
                    case .references: referencesTab(project)
                    case .shots: shotsTab(project)
                    case .research: researchTab(project)
                    case .publish: publishTab(project)
                    case .schedule: scheduleTab(project)
                    case .models: EmptyView()
                    }
                }
                .padding(16)
            }
        } else {
            ContentUnavailableView(
                "Create your first video",
                systemImage: "film",
                description: Text("Start with a brief, then plan references and shots. Configure image and video models before production.")
            )
        }
    }

    private func header(_ project: CreatorProject) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                VStack(alignment: .leading, spacing: 4) {
                    Text(project.title)
                        .font(.title2.weight(.semibold))
                    Text(statusLine(project))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
            }
            inlineError(project.error.isEmpty ? project.lastRunError : project.error)
            HStack {
                ForEach(Self.stages, id: \.self) { stage in
                    Button("Run \(stage)") {
                        Task { await run(stage) }
                    }
                    .disabled(
                        mediaLocked
                            || (stage == "publish" && project.publishMode != "auto")
                    )
                }
            }
            if !project.runSessionID.isEmpty {
                Text("Agent run \(project.runSessionID)")
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            if !busy.isEmpty {
                Text("Working: \(busy). Media generation may take several minutes.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
    }

    private func briefTab(_ project: CreatorProject) -> some View {
        Form {
            TextField("Title", text: stringBinding(\.title))
                .disabled(locked)
            TextField("Target duration (seconds)", value: intBinding(\.targetSeconds), format: .number)
                .disabled(locked)
            LabeledContent("Creative brief") {
                TextEditor(text: stringBinding(\.brief))
                    .font(.body)
                    .frame(minHeight: 90)
                    .disabled(locked)
            }
            LabeledContent("Visual style") {
                TextEditor(text: stringBinding(\.style))
                    .font(.body)
                    .frame(minHeight: 60)
                    .disabled(locked)
            }
            TextField("Resolution", text: stringBinding(\.size))
                .disabled(locked)
        }
        .formStyle(.grouped)
    }

    private func referencesTab(_ project: CreatorProject) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Generate each character, setting, prop, or style once. Shots reuse these IDs for continuity.")
                .foregroundStyle(.secondary)
            ForEach(project.references) { ref in
                referenceCard(ref)
            }
            Button("Add reference") {
                edit {
                    $0.references.append(
                        CreatorReference(id: uid("ref_"))
                    )
                }
            }
            .disabled(locked)
        }
    }

    private func referenceCard(_ ref: CreatorReference) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top, spacing: 12) {
                artifactImage(ref.path, label: ref.name.isEmpty ? ref.id : ref.name)
                VStack(alignment: .leading, spacing: 8) {
                    Text("\(ref.id) · \(ref.status)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    TextField("Name", text: refString(ref.id, \.name))
                        .disabled(locked)
                    Picker("Kind", selection: refString(ref.id, \.kind)) {
                        ForEach(Self.kinds, id: \.self) { kind in
                            Text(kind).tag(kind)
                        }
                    }
                    .disabled(locked)
                }
            }
            TextEditor(text: refString(ref.id, \.prompt))
                .font(.body)
                .frame(minHeight: 60)
                .disabled(locked)
            inlineError(ref.error)
            HStack {
                Button("Generate") {
                    Task { await media("generate_reference", ref.id) }
                }
                .disabled(mediaLocked || ref.status == "ready")
                Button("Reset") { pendingReset = ("reset_reference", ref.id) }
                    .disabled(mediaLocked)
                Button("Remove", role: .destructive) {
                    edit { $0.references.removeAll { $0.id == ref.id } }
                }
                .disabled(locked)
            }
        }
        .padding(12)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.2)))
    }

    private func shotsTab(_ project: CreatorProject) -> some View {
        let planned = project.shots.reduce(0) { $0 + $1.durationSeconds }
        return VStack(alignment: .leading, spacing: 12) {
            Text("\(planned) planned seconds / \(project.targetSeconds) target. A continuation starts from the previous clip’s last frame.")
                .foregroundStyle(.secondary)
            ForEach(Array(project.shots.enumerated()), id: \.element.id) { index, shot in
                shotCard(shot, index: index, total: project.shots.count)
            }
            HStack {
                Button("Add shot") {
                    edit {
                        $0.shots.append(
                            CreatorShot(
                                id: uid("shot_"),
                                referenceIDs: $0.references.map(\.id),
                                durationSeconds: settings?.video.seconds ?? 8,
                                continuity: $0.shots.isEmpty ? "cut" : "continue"
                            )
                        )
                    }
                }
                .disabled(locked)
                Button("Assemble final video") {
                    Task { await media("assemble", nil) }
                }
                .disabled(
                    mediaLocked
                        || project.shots.isEmpty
                        || project.shots.contains { $0.status != "ready" }
                )
            }
            if !project.finalPath.isEmpty {
                artifactVideo(project.finalPath)
            }
        }
    }

    private func shotCard(_ shot: CreatorShot, index: Int, total: Int) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                Text("Shot \(index + 1) · \(shot.status)\(shot.videoJobID.isEmpty ? "" : " (\(shot.progress)%)")")
                    .font(.headline)
                Spacer()
                Button("Move up") { moveShot(index, -1) }
                    .disabled(locked || index == 0)
                Button("Move down") { moveShot(index, 1) }
                    .disabled(locked || index == total - 1)
            }
            TextEditor(text: shotString(shot.id, \.prompt))
                .font(.body)
                .frame(minHeight: 60)
                .disabled(locked)
            HStack {
                TextField("Clip seconds", value: shotInt(shot.id, \.durationSeconds), format: .number)
                    .disabled(locked)
                Picker("Continuity", selection: shotString(shot.id, \.continuity)) {
                    Text("New composition").tag("cut")
                    Text("Continue previous clip").tag("continue")
                }
                .disabled(locked || index == 0)
                TextField("Notes", text: shotString(shot.id, \.notes))
                    .disabled(locked)
            }
            if let project, !project.references.isEmpty {
                Text("Visual references")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                ForEach(project.references) { ref in
                    Toggle(ref.name.isEmpty ? ref.id : ref.name, isOn: shotHasRef(shot.id, ref.id))
                        .disabled(locked)
                }
            }
            HStack(alignment: .top) {
                artifactImage(shot.keyframePath, label: "Keyframe")
                artifactVideo(shot.videoPath)
                artifactImage(shot.lastFramePath, label: "Last frame")
            }
            inlineError(shot.error)
            HStack {
                Button("Create keyframe") {
                    Task { await media("generate_keyframe", shot.id) }
                }
                .disabled(mediaLocked || !shot.keyframePath.isEmpty)
                Button("Generate clip") {
                    Task { await media("generate_video", shot.id) }
                }
                .disabled(mediaLocked || !shot.videoJobID.isEmpty || shot.status == "submission_unknown")
                Button("Check render") {
                    Task { await media("poll_video", shot.id) }
                }
                .disabled(mediaLocked || shot.videoJobID.isEmpty || shot.status == "ready")
                Button("Reset shot") { pendingReset = ("reset_shot", shot.id) }
                    .disabled(mediaLocked)
                Button("Remove", role: .destructive) {
                    edit { $0.shots.removeAll { $0.id == shot.id } }
                }
                .disabled(locked)
            }
        }
        .padding(12)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.2)))
    }

    private func researchTab(_ project: CreatorProject) -> some View {
        VStack(alignment: .leading, spacing: 16) {
            Text("Observed trends")
                .font(.headline)
            if project.research.isEmpty {
                Text("Run research to collect source URLs, observation times, and available metrics.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(project.research) { trend in
                    VStack(alignment: .leading, spacing: 4) {
                        if let url = URL(string: trend.url), !trend.url.isEmpty {
                            Button(trend.title.isEmpty ? trend.url : trend.title) {
                                NSWorkspace.shared.open(url)
                            }
                            .buttonStyle(.link)
                        } else {
                            Text(trend.title)
                        }
                        Text("Observed \(trend.observedAt)")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        if !trend.notes.isEmpty {
                            Text(trend.notes)
                        }
                        if let metrics = trend.metrics, !metrics.isEmpty {
                            Text(metrics.map { "\($0.key): \(formatMetric($0.value))" }.joined(separator: " · "))
                                .font(.caption)
                        }
                    }
                    .padding(10)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.background, in: RoundedRectangle(cornerRadius: 8))
                    .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.2)))
                }
            }

            Text("Original ideas")
                .font(.headline)
            if project.ideas.isEmpty {
                Text("Run plan after research to propose ideas and build the shot list.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(project.ideas) { idea in
                    Button {
                        edit {
                            $0.ideas = $0.ideas.map { item in
                                var next = item
                                next.selected = item.id == idea.id
                                return next
                            }
                        }
                    } label: {
                        HStack(alignment: .top) {
                            Image(systemName: idea.selected ? "largecircle.fill.circle" : "circle")
                            VStack(alignment: .leading, spacing: 4) {
                                Text(idea.title).font(.headline)
                                Text(idea.hook)
                                Text(idea.rationale)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                            Spacer()
                        }
                    }
                    .buttonStyle(.plain)
                    .disabled(locked)
                }
            }
        }
    }

    private func publishTab(_ project: CreatorProject) -> some View {
        let matching = accounts.filter { $0.platform == project.platform }
        return VStack(alignment: .leading, spacing: 12) {
            Picker("Platform", selection: platformBinding) {
                ForEach(Self.platforms, id: \.self) { item in
                    Text(item).tag(item)
                }
            }
            .disabled(locked)

            Picker("Existing social account", selection: stringBinding(\.accountID)) {
                Text("Select a connected account").tag("")
                ForEach(matching) { account in
                    Text("\(account.label) (\(account.status))").tag(account.id)
                }
            }
            .disabled(locked)

            Text("Manage accounts and browser in Social media.")
                .font(.caption)
                .foregroundStyle(.secondary)

            LabeledContent("Caption") {
                TextEditor(text: stringBinding(\.caption))
                    .frame(minHeight: 80)
                    .disabled(locked)
            }

            Picker("Publishing permission", selection: stringBinding(\.publishMode)) {
                Text("Draft only").tag("draft")
                Text("Allow publishing to selected account").tag("auto")
            }
            .disabled(locked)

            Text("Publication: \(project.publication.status)")
            inlineError(project.publication.error)
            if let url = URL(string: project.publication.postURL), !project.publication.postURL.isEmpty {
                Button("Open published video") { NSWorkspace.shared.open(url) }
                    .buttonStyle(.link)
            }
            if project.finalPath.isEmpty {
                Text("Assemble the shots before uploading.")
                    .foregroundStyle(.secondary)
            } else {
                artifactVideo(project.finalPath)
                Button("Download final MP4") {
                    NSWorkspace.shared.open(model.client.creatorArtifactURL(projectID: project.id, path: project.finalPath))
                }
            }
            if project.publication.status == "blocked" || project.publication.status == "uploading" {
                VStack(alignment: .leading, spacing: 8) {
                    Text("An uncertain upload must be checked on the account before allowing another attempt.")
                    TextEditor(text: $proof)
                        .frame(minHeight: 70)
                    Button("Confirm not published") {
                        Task { await reconcile() }
                    }
                    .disabled(locked || proof.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
                .padding(12)
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.3)))
            }
        }
    }

    private func scheduleTab(_ project: CreatorProject) -> some View {
        let linked = jobs.filter { $0.contentProjectID == project.id }
        return VStack(alignment: .leading, spacing: 12) {
            Form {
                TextField("Schedule name", text: $scheduleName)
                TextField("Cron expression", text: $scheduleCron)
                    .fontDesign(.monospaced)
                Picker("Stage", selection: $scheduleStage) {
                    ForEach(Self.stages, id: \.self) { stage in
                        Text(stage).tag(stage)
                    }
                }
                Picker("Publishing mode", selection: $schedulePublish) {
                    Text("Draft only").tag("draft")
                    Text("Allow publishing").tag("auto")
                }
                Button("Create schedule") {
                    Task { await createSchedule() }
                }
                .disabled(
                    !busy.isEmpty
                        || scheduleName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        || (scheduleStage == "publish" && schedulePublish == "draft")
                )
            }
            .formStyle(.grouped)

            ForEach(linked) { job in
                HStack {
                    VStack(alignment: .leading) {
                        Text(job.name).font(.headline)
                        Text("\(job.schedule) · \(job.contentStage ?? "") · \(job.publishMode ?? "") · \(job.enabled ? "enabled" : "disabled")")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Spacer()
                    Button("Remove", role: .destructive) {
                        Task { await removeSchedule(job) }
                    }
                    .disabled(!busy.isEmpty)
                }
                .padding(10)
                .overlay(RoundedRectangle(cornerRadius: 8).stroke(Color.secondary.opacity(0.2)))
            }
        }
    }

    private var modelsForm: some View {
        Form {
            Text("OpenAI-compatible image generation/edits and asynchronous video endpoints. Changing a provider does not migrate in-flight video jobs.")
                .foregroundStyle(.secondary)
            providerFields("Image model", patch: $imagePatch, hasKey: settings?.image.hasKey ?? false, video: false)
            providerFields("Video model", patch: $videoPatch, hasKey: settings?.video.hasKey ?? false, video: true)
            Text("FFmpeg: \(settings?.ffmpeg == true ? "available" : "not installed") · FFprobe: \(settings?.ffprobe == true ? "available" : "not installed")")
            Button("Save media settings") {
                Task { await saveSettings() }
            }
            .disabled(!busy.isEmpty)
        }
        .formStyle(.grouped)
    }

    private func providerFields(
        _ title: String,
        patch: Binding<CreatorProviderPatch>,
        hasKey: Bool,
        video: Bool
    ) -> some View {
        Section(title) {
            Toggle("Enable \(video ? "video" : "image") generation", isOn: boolPatch(patch, \.enabled))
            Text(hasKey ? "Credential configured" : "No credential configured")
                .foregroundStyle(.secondary)
            TextField("Provider ID (optional)", text: stringPatch(patch, \.provider))
            TextField("Model ID", text: stringPatch(patch, \.model))
            TextField("Base URL", text: stringPatch(patch, \.baseURL))
            SecureField("API key (blank keeps current)", text: stringPatch(patch, \.apiKey))
            TextField("Default resolution", text: stringPatch(patch, \.size))
            if video {
                TextField("Default clip seconds", value: intPatch(patch, \.seconds), format: .number)
            }
        }
    }

    @ViewBuilder
    private func artifactImage(_ path: String, label: String) -> some View {
        if path.isEmpty {
            RoundedRectangle(cornerRadius: 8)
                .fill(.quaternary)
                .frame(width: 140, height: 100)
                .overlay {
                    Text("Not generated")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
        } else if let project {
            CreatorArtifactImage(projectID: project.id, path: path, label: label)
                .environmentObject(model)
        }
    }

    @ViewBuilder
    private func artifactVideo(_ path: String) -> some View {
        if !path.isEmpty, let project {
            CreatorArtifactVideo(url: model.client.creatorArtifactURL(projectID: project.id, path: path))
        }
    }

    @ViewBuilder
    private func inlineError(_ text: String) -> some View {
        if !text.isEmpty {
            Text(text)
                .font(.caption)
                .foregroundStyle(.red)
                .textSelection(.enabled)
        }
    }

    private func statusLine(_ project: CreatorProject) -> String {
        let status = project.isRunning ? "Running \(project.runStage)" : project.status
        return "\(status) · revision \(project.revision)\(dirty ? " · unsaved changes" : "")"
    }

    private func load() async {
        isLoading = projects.isEmpty && project == nil
        errorMessage = nil
        do {
            async let listed = model.client.listCreatorProjects()
            async let loadedSettings = model.client.creatorSettings()
            async let loadedAccounts = model.client.listSocialAccounts()
            async let loadedJobs = model.client.listCronJobs()
            let response = try await listed
            settings = try await loadedSettings
            accounts = try await loadedAccounts
            jobs = try await loadedJobs.jobs
            if let settings {
                imagePatch = settings.image.patch
                videoPatch = settings.video.patch
            }
            projects = response.projects
            if let current = project, let latest = projects.first(where: { $0.id == current.id }) {
                if !dirty && busy.isEmpty {
                    project = try await model.client.creatorProject(latest.id)
                }
            } else if let first = projects.first {
                project = try await model.client.creatorProject(first.id)
                dirty = false
            }
        } catch {
            errorMessage = describe(error)
        }
        isLoading = false
    }

    private func choose(_ id: String, force: Bool = false) async {
        pendingSwitchID = nil
        if dirty && !force {
            pendingSwitchID = id
            return
        }
        await act("load") {
            project = try await model.client.creatorProject(id)
            dirty = false
        }
    }

    private func refreshSelected(quiet: Bool = false) async {
        guard let id = project?.id, !dirty, busy.isEmpty else { return }
        do {
            let latest = try await model.client.creatorProject(id)
            if selectedID == id, !dirty, busy.isEmpty {
                project = latest
                accept(latest)
            }
        } catch {
            if !quiet { errorMessage = describe(error) }
        }
    }

    private func save() async {
        guard let project else { return }
        await act("save") {
            accept(try await model.client.updateCreatorProject(project))
        }
    }

    private func media(_ action: String, _ targetID: String?) async {
        guard let project else { return }
        if dirty {
            errorMessage = "Save your plan before generating media."
            return
        }
        await act(action) {
            accept(try await model.client.creatorAction(
                project.id,
                CreatorActionRequest(action: action, targetID: targetID)
            ))
        }
    }

    private func reset(_ kind: String, _ target: String) async {
        pendingReset = nil
        guard let project else { return }
        await act(kind) {
            accept(try await model.client.creatorAction(
                project.id,
                CreatorActionRequest(action: kind, targetID: target, confirmed: true)
            ))
        }
    }

    private func run(_ stage: String, confirmed: Bool = false) async {
        pendingStage = nil
        guard let project else { return }
        if dirty {
            errorMessage = "Save your plan first."
            return
        }
        if (stage == "publish" || stage == "full") && project.publishMode == "auto" && !confirmed {
            pendingStage = stage
            return
        }
        await act("run-\(stage)") {
            let result = try await model.client.runCreatorProject(
                project.id,
                stage: stage,
                publishMode: project.publishMode
            )
            var latest = try await model.client.creatorProject(project.id)
            latest.runSessionID = result.sessionID
            accept(latest)
        }
    }

    private func reconcile() async {
        guard let project else { return }
        await act("reconcile publication") {
            accept(try await model.client.reconcileCreatorProject(project.id, proof: proof))
            proof = ""
        }
    }

    private func createSchedule(confirmed: Bool = false) async {
        guard let project else { return }
        if schedulePublish == "auto" && !confirmed {
            confirmPublishSchedule = true
            return
        }
        confirmPublishSchedule = false
        await act("schedule") {
            _ = try await model.client.createCreatorCronJob(
                CreatorCronJobRequest(
                    name: scheduleName.trimmingCharacters(in: .whitespacesAndNewlines),
                    schedule: scheduleCron.trimmingCharacters(in: .whitespacesAndNewlines),
                    role: "content-creator",
                    contentProjectID: project.id,
                    contentStage: scheduleStage,
                    publishMode: schedulePublish
                )
            )
            jobs = try await model.client.listCronJobs().jobs
            scheduleName = ""
        }
    }

    private func removeSchedule(_ job: CronJob) async {
        await act("remove schedule") {
            let _: DeletedFlagResponse = try await model.client.delete("/api/cron/jobs/\(job.id)")
            jobs = try await model.client.listCronJobs().jobs
        }
    }

    private func saveSettings() async {
        await act("settings") {
            let saved = try await model.client.saveCreatorSettings(
                CreatorSettingsPatch(image: imagePatch, video: videoPatch)
            )
            settings = saved
            imagePatch = saved.image.patch
            videoPatch = saved.video.patch
            imagePatch.apiKey = nil
            videoPatch.apiKey = nil
        }
    }

    private func accept(_ next: CreatorProject) {
        project = next
        dirty = false
        if let index = projects.firstIndex(where: { $0.id == next.id }) {
            projects[index] = next
            projects.move(fromOffsets: IndexSet(integer: index), toOffset: 0)
        } else {
            projects.insert(next, at: 0)
        }
    }

    private func edit(_ mutate: (inout CreatorProject) -> Void) {
        guard var project else { return }
        mutate(&project)
        self.project = project
        dirty = true
    }

    private func moveShot(_ index: Int, _ delta: Int) {
        edit {
            let to = index + delta
            guard $0.shots.indices.contains(to) else { return }
            $0.shots.swapAt(index, to)
        }
    }

    private func act(_ name: String, _ work: () async throws -> Void) async {
        guard busy.isEmpty else { return }
        busy = name
        errorMessage = nil
        defer { busy = "" }
        do {
            try await work()
        } catch {
            errorMessage = describe(error)
            if let id = project?.id, !dirty {
                if let latest = try? await model.client.creatorProject(id) {
                    project = latest
                }
            }
        }
    }

    private func uid(_ prefix: String) -> String {
        prefix + UUID().uuidString.replacingOccurrences(of: "-", with: "").prefix(12)
    }

    private func formatMetric(_ value: Double) -> String {
        value.rounded() == value ? String(Int(value)) : String(value)
    }

    private var platformBinding: Binding<String> {
        Binding(
            get: { project?.platform ?? "youtube" },
            set: { value in
                guard project?.platform != value else { return }
                edit {
                    $0.platform = value
                    $0.accountID = ""
                }
            }
        )
    }

    private func stringBinding(_ keyPath: WritableKeyPath<CreatorProject, String>) -> Binding<String> {
        Binding(
            get: { project?[keyPath: keyPath] ?? "" },
            set: { value in edit { $0[keyPath: keyPath] = value } }
        )
    }

    private func intBinding(_ keyPath: WritableKeyPath<CreatorProject, Int>) -> Binding<Int> {
        Binding(
            get: { project?[keyPath: keyPath] ?? 0 },
            set: { value in edit { $0[keyPath: keyPath] = value } }
        )
    }

    private func refString(_ id: String, _ keyPath: WritableKeyPath<CreatorReference, String>) -> Binding<String> {
        Binding(
            get: { project?.references.first { $0.id == id }?[keyPath: keyPath] ?? "" },
            set: { value in
                edit {
                    if let index = $0.references.firstIndex(where: { $0.id == id }) {
                        $0.references[index][keyPath: keyPath] = value
                    }
                }
            }
        )
    }

    private func shotString(_ id: String, _ keyPath: WritableKeyPath<CreatorShot, String>) -> Binding<String> {
        Binding(
            get: { project?.shots.first { $0.id == id }?[keyPath: keyPath] ?? "" },
            set: { value in
                edit {
                    if let index = $0.shots.firstIndex(where: { $0.id == id }) {
                        $0.shots[index][keyPath: keyPath] = value
                    }
                }
            }
        )
    }

    private func shotInt(_ id: String, _ keyPath: WritableKeyPath<CreatorShot, Int>) -> Binding<Int> {
        Binding(
            get: { project?.shots.first { $0.id == id }?[keyPath: keyPath] ?? 0 },
            set: { value in
                edit {
                    if let index = $0.shots.firstIndex(where: { $0.id == id }) {
                        $0.shots[index][keyPath: keyPath] = value
                    }
                }
            }
        )
    }

    private func shotHasRef(_ shotID: String, _ refID: String) -> Binding<Bool> {
        Binding(
            get: { project?.shots.first { $0.id == shotID }?.referenceIDs.contains(refID) ?? false },
            set: { enabled in
                edit {
                    guard let index = $0.shots.firstIndex(where: { $0.id == shotID }) else { return }
                    if enabled {
                        if !$0.shots[index].referenceIDs.contains(refID) {
                            $0.shots[index].referenceIDs.append(refID)
                        }
                    } else {
                        $0.shots[index].referenceIDs.removeAll { $0 == refID }
                    }
                }
            }
        )
    }

    private func stringPatch(
        _ patch: Binding<CreatorProviderPatch>,
        _ keyPath: WritableKeyPath<CreatorProviderPatch, String?>
    ) -> Binding<String> {
        Binding(
            get: { patch.wrappedValue[keyPath: keyPath] ?? "" },
            set: { patch.wrappedValue[keyPath: keyPath] = $0 }
        )
    }

    private func boolPatch(
        _ patch: Binding<CreatorProviderPatch>,
        _ keyPath: WritableKeyPath<CreatorProviderPatch, Bool?>
    ) -> Binding<Bool> {
        Binding(
            get: { patch.wrappedValue[keyPath: keyPath] ?? false },
            set: { patch.wrappedValue[keyPath: keyPath] = $0 }
        )
    }

    private func intPatch(
        _ patch: Binding<CreatorProviderPatch>,
        _ keyPath: WritableKeyPath<CreatorProviderPatch, Int?>
    ) -> Binding<Int> {
        Binding(
            get: { patch.wrappedValue[keyPath: keyPath] ?? 8 },
            set: { patch.wrappedValue[keyPath: keyPath] = $0 }
        )
    }
}

private struct CreateCreatorProjectSheet: View {
    @EnvironmentObject private var model: AppModel
    var onCreated: (CreatorProject) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var title = ""
    @State private var brief = ""
    @State private var platform = "youtube"
    @State private var size = "720x1280"
    @State private var targetSeconds = 24
    @State private var errorMessage: String?
    @State private var isSaving = false

    var body: some View {
        NavigationStack {
            Form {
                TextField("Video title", text: $title)
                Picker("Platform", selection: $platform) {
                    ForEach(ContentCreatorViewPlatforms.platforms, id: \.self) { item in
                        Text(item).tag(item)
                    }
                }
                TextEditor(text: $brief)
                    .frame(minHeight: 90)
                TextField("Target duration (seconds)", value: $targetSeconds, format: .number)
                Picker("Resolution", selection: $size) {
                    ForEach(ContentCreatorViewPlatforms.sizes, id: \.self) { item in
                        Text(item).tag(item)
                    }
                }
                if let errorMessage {
                    Text(errorMessage)
                        .foregroundStyle(.red)
                }
            }
            .navigationTitle("New video")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(isSaving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "Creating…" : "Create") {
                        Task { await create() }
                    }
                    .disabled(title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        || brief.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                        || isSaving)
                }
            }
        }
        .frame(minWidth: 460, minHeight: 380)
    }

    private func create() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        do {
            let created = try await model.client.createCreatorProject(
                CreatorProjectDraft(
                    title: title.trimmingCharacters(in: .whitespacesAndNewlines),
                    brief: brief.trimmingCharacters(in: .whitespacesAndNewlines),
                    platform: platform,
                    size: size,
                    targetSeconds: targetSeconds,
                    style: "",
                    publishMode: "draft"
                )
            )
            onCreated(created)
            dismiss()
        } catch {
            errorMessage = describe(error)
        }
    }
}

private enum ContentCreatorViewPlatforms {
    static let platforms = ["youtube", "tiktok", "instagram", "facebook", "x", "threads"]
    static let sizes = ["720x1280", "1280x720", "1024x1792", "1792x1024"]
}

private struct CreatorArtifactImage: View {
    @EnvironmentObject private var model: AppModel
    let projectID: String
    let path: String
    let label: String

    @State private var image: NSImage?
    @State private var failed = false

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .scaledToFit()
                    .frame(maxWidth: 180, maxHeight: 140)
                    .clipShape(RoundedRectangle(cornerRadius: 8))
                    .accessibilityLabel(label)
            } else if failed {
                Text(label)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .frame(width: 140, height: 100)
            } else {
                ProgressView()
                    .frame(width: 140, height: 100)
            }
        }
        .task(id: path) { await load() }
    }

    private func load() async {
        failed = false
        image = nil
        do {
            let data = try await model.client.creatorArtifact(projectID: projectID, path: path)
            image = NSImage(data: data)
            failed = image == nil
        } catch {
            failed = true
        }
    }
}

private struct CreatorArtifactVideo: View {
    let url: URL
    @State private var player: AVPlayer?

    var body: some View {
        VideoPlayer(player: player)
            .frame(minHeight: 160, maxHeight: 240)
            .clipShape(RoundedRectangle(cornerRadius: 8))
            .onAppear { player = AVPlayer(url: url) }
            .onDisappear {
                player?.pause()
                player = nil
            }
    }
}
