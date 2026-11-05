import AntaresKit
import SwiftUI

struct CronView: View {
    @EnvironmentObject private var model: AppModel

    @State private var jobs: [CronJob] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyID: String?
    @State private var showingCreate = false
    @State private var draftName = ""
    @State private var draftSchedule = "0 8 * * *"
    @State private var draftPrompt = ""
    @State private var isSaving = false

    var body: some View {
        Group {
            if let errorMessage, jobs.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Cron",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && jobs.isEmpty {
                ContentUnavailableView(
                    "No Schedules",
                    systemImage: "clock.arrow.2.circlepath",
                    description: Text("Create a job to run a prompt on a cron schedule.")
                )
            } else {
                List {
                    if let errorMessage {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    Section("Jobs") {
                        ForEach(jobs) { job in
                            VStack(alignment: .leading, spacing: 8) {
                                HStack {
                                    Text(job.name)
                                        .font(.headline)
                                    Spacer()
                                    if !job.lastState.isEmpty {
                                        Text(job.lastState)
                                            .font(.caption)
                                            .foregroundStyle(job.lastState == "ok" ? .green : .red)
                                    }
                                }
                                Text(job.schedule)
                                    .font(.caption)
                                    .fontDesign(.monospaced)
                                Text(job.prompt)
                                    .font(.callout)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(2)
                                HStack {
                                    Toggle(
                                        "Enabled",
                                        isOn: Binding(
                                            get: { job.enabled },
                                            set: { enabled in
                                                Task { await toggle(job, enabled: enabled) }
                                            }
                                        )
                                    )
                                    .labelsHidden()
                                    .disabled(busyID != nil)
                                    Text(job.enabled ? "On" : "Off")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                    Spacer()
                                    Button("Run") {
                                        Task { await run(job) }
                                    }
                                    .disabled(busyID != nil)
                                }
                            }
                            .opacity(job.enabled ? 1 : 0.6)
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading cron…") }
        }
        .navigationTitle("Cron")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("New") {
                    draftName = ""
                    draftSchedule = "0 8 * * *"
                    draftPrompt = ""
                    showingCreate = true
                }
            }
        }
        .sheet(isPresented: $showingCreate) {
            NavigationStack {
                Form {
                    TextField("Name", text: $draftName)
                    TextField("Schedule", text: $draftSchedule)
                        .fontDesign(.monospaced)
                    TextEditor(text: $draftPrompt)
                        .frame(minHeight: 100)
                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                    }
                }
                .navigationTitle("New schedule")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { showingCreate = false }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button(isSaving ? "Creating…" : "Create") {
                            Task { await create() }
                        }
                        .disabled(!canCreate || isSaving)
                    }
                }
            }
            .frame(minWidth: 420, minHeight: 320)
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private var canCreate: Bool {
        !draftName.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !draftPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !draftSchedule.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func load() async {
        isLoading = jobs.isEmpty
        errorMessage = nil
        do {
            let response: CronJobsResponse = try await model.client.get("/api/cron/jobs")
            jobs = response.jobs
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func create() async {
        let name = draftName.trimmingCharacters(in: .whitespacesAndNewlines)
        let schedule = draftSchedule.trimmingCharacters(in: .whitespacesAndNewlines)
        let prompt = draftPrompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !prompt.isEmpty else { return }
        isSaving = true
        errorMessage = nil
        do {
            let _: CronJob = try await model.client.post(
                "/api/cron/jobs",
                body: CreateCronRequest(name: name, schedule: schedule, prompt: prompt)
            )
            showingCreate = false
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isSaving = false
    }

    private func toggle(_ job: CronJob, enabled: Bool) async {
        busyID = job.id
        defer { busyID = nil }
        do {
            let _: CronJob = try await model.client.post(
                "/api/cron/jobs/\(job.id)/toggle",
                body: ToggleEnabledRequest(enabled: enabled)
            )
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func run(_ job: CronJob) async {
        busyID = job.id
        defer { busyID = nil }
        do {
            let _: StartedFlagResponse = try await model.client.post(
                "/api/cron/jobs/\(job.id)/run",
                body: EmptyBody()
            )
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
