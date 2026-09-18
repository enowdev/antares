import AntaresKit
import SwiftUI

struct SkillsView: View {
    @EnvironmentObject private var model: AppModel

    @State private var skills: [SkillInfo] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyName: String?
    @State private var showingHub = false

    var body: some View {
        Group {
            if let errorMessage, skills.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Skills",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && skills.isEmpty {
                ContentUnavailableView(
                    "No Skills",
                    systemImage: "sparkles",
                    description: Text("Install a skill from the hub or wait until the agent learns one.")
                )
            } else {
                List {
                    if let errorMessage {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    Section("Skills") {
                        ForEach(skills) { skill in
                            HStack(alignment: .top) {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(skill.name)
                                        .font(.headline)
                                        .fontDesign(.monospaced)
                                    Text(skill.description)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                    HStack(spacing: 8) {
                                        Text(skill.source)
                                        if skill.usageCount > 0 {
                                            Text("Used \(skill.usageCount)×")
                                        }
                                    }
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                                }
                                Spacer()
                                Toggle(
                                    "Enabled",
                                    isOn: Binding(
                                        get: { skill.enabled },
                                        set: { enabled in
                                            Task { await toggle(name: skill.name, enabled: enabled) }
                                        }
                                    )
                                )
                                .labelsHidden()
                                .disabled(busyName != nil)
                            }
                            .opacity(skill.enabled ? 1 : 0.6)
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading skills…") }
        }
        .navigationTitle("Skills")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Hub") { showingHub = true }
            }
        }
        .sheet(isPresented: $showingHub) {
            HubSkillsSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = skills.isEmpty
        errorMessage = nil
        do {
            let response: SkillsResponse = try await model.client.get("/api/skills")
            skills = response.skills
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func toggle(name: String, enabled: Bool) async {
        busyName = name
        defer { busyName = nil }
        do {
            let _: OKResponse = try await model.client.post(
                "/api/skills/toggle",
                body: ToggleToolRequest(name: name, enabled: enabled)
            )
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}

private struct HubSkillsSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var onInstalled: () -> Void

    @State private var query = ""
    @State private var entries: [HubSkill] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyID: String?

    var body: some View {
        NavigationStack {
            Group {
                if let errorMessage, entries.isEmpty, !isLoading {
                    ContentUnavailableView(
                        "Hub",
                        systemImage: "exclamationmark.triangle",
                        description: Text(errorMessage)
                    )
                } else if !isLoading && entries.isEmpty {
                    ContentUnavailableView(
                        "No Hub Skills",
                        systemImage: "storefront",
                        description: Text("Try a different search.")
                    )
                } else {
                    List {
                        if let errorMessage {
                            Section {
                                Text(errorMessage)
                                    .foregroundStyle(.red)
                            }
                        }
                        ForEach(entries) { entry in
                            HStack(alignment: .top) {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(entry.name)
                                        .font(.headline)
                                    Text(entry.summary)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                    Text(entry.source)
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }
                                Spacer()
                                if entry.installed {
                                    Text("Installed")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                } else {
                                    Button("Install") {
                                        Task { await install(entry) }
                                    }
                                    .disabled(busyID != nil)
                                }
                            }
                        }
                    }
                }
            }
            .overlay {
                if isLoading { ProgressView("Searching hub…") }
            }
            .navigationTitle("Skill hub")
            .searchable(text: $query, prompt: "Search skills")
            .onSubmit(of: .search) {
                Task { await search() }
            }
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { dismiss() }
                }
            }
            .task { await search() }
        }
        .frame(minWidth: 480, minHeight: 420)
    }

    private func search() async {
        isLoading = entries.isEmpty
        errorMessage = nil
        do {
            let response: HubSkillsResponse = try await model.client.get(
                "/api/hub/skills",
                query: [URLQueryItem(name: "q", value: query)]
            )
            entries = response.skills
            errorMessage = response.error
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func install(_ entry: HubSkill) async {
        busyID = entry.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            let result: HubInstallResponse = try await model.client.post(
                "/api/hub/skills/install",
                body: HubInstallRequest(id: entry.id)
            )
            if result.ok {
                onInstalled()
                await search()
            } else {
                errorMessage = result.error ?? "Install failed."
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
