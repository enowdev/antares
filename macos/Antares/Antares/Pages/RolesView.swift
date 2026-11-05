import AntaresKit
import SwiftUI

struct RolesView: View {
    @EnvironmentObject private var model: AppModel

    @State private var response: RolesResponse?
    @State private var sessionRole = ""
    @State private var selectedRole = ""
    @State private var errorMessage: String?
    @State private var isLoading = false

    private var sessionID: String? {
        UserDefaults.standard.string(forKey: ChatController.lastSessionKey).flatMap {
            $0.isEmpty ? nil : $0
        }
    }

    private var pickerRoles: [RoleInfo] {
        (response?.roles ?? []).filter { !$0.isSubrole && $0.name != "assistant" }
    }

    var body: some View {
        Group {
            if let errorMessage, response == nil, !isLoading {
                ContentUnavailableView(
                    "Roles",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if let response, response.roles.isEmpty, !isLoading {
                ContentUnavailableView(
                    "No Roles",
                    systemImage: "person.3",
                    description: Text("Specialist roles will appear here.")
                )
            } else {
                List {
                    if let errorMessage {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    Section("Session role") {
                        if let sessionID {
                            LabeledContent("Session", value: sessionID)
                            LabeledContent("Current", value: sessionRole.isEmpty ? "Orchestrator (default)" : sessionRole)
                        } else {
                            Text("No active chat session. The next message will use this role.")
                                .foregroundStyle(.secondary)
                        }
                        Picker("Next message", selection: $selectedRole) {
                            Text("Orchestrator (default)").tag("")
                            ForEach(pickerRoles) { role in
                                Text(role.title.isEmpty ? role.name : role.title).tag(role.name)
                            }
                        }
                        .onChange(of: selectedRole) { _, newValue in
                            persistRole(newValue)
                        }
                    }

                    if let active = response?.active, !active.isEmpty {
                        Section("Working now") {
                            ForEach(active) { agent in
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(agent.role)
                                        .font(.headline)
                                    Text(agent.task)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }

                    if let groups = groupedRoles, !groups.isEmpty {
                        ForEach(groups, id: \.category) { group in
                            Section(categoryLabel(group.category)) {
                                ForEach(group.roles) { role in
                                    roleRow(role)
                                }
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading roles…") }
        }
        .navigationTitle("Roles")
        .task { await load() }
        .refreshable { await load() }
    }

    private var groupedRoles: [(category: String, roles: [RoleInfo])]? {
        guard let roles = response?.roles else { return nil }
        var groups: [(category: String, roles: [RoleInfo])] = []
        var index: [String: Int] = [:]
        for role in roles {
            if let i = index[role.category] {
                groups[i].roles.append(role)
            } else {
                index[role.category] = groups.count
                groups.append((role.category, [role]))
            }
        }
        return groups
    }

    @ViewBuilder
    private func roleRow(_ role: RoleInfo) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(role.title.isEmpty ? role.name : role.title)
                    .font(.headline)
                Spacer()
                Text(role.source == "local" ? "Custom" : "Built-in")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            Text(role.name)
                .font(.caption)
                .fontDesign(.monospaced)
                .foregroundStyle(.secondary)
            if !role.summary.isEmpty {
                Text(role.summary)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
            HStack(spacing: 8) {
                if role.isSubrole {
                    Text("Specialist of \(role.parent ?? "")")
                }
                if role.isDanger {
                    Text("Authorized testing")
                        .foregroundStyle(.orange)
                }
                if !role.toolset.isEmpty {
                    Text(role.toolset)
                }
                if !role.model.isEmpty {
                    Text(role.model)
                }
            }
            .font(.caption2)
            .foregroundStyle(.secondary)
        }
    }

    private func categoryLabel(_ category: String) -> String {
        switch category {
        case "general": return "General"
        case "engineering": return "Engineering"
        case "research": return "Research"
        case "writing": return "Writing"
        case "security": return "Security — authorized testing"
        default: return category
        }
    }

    private func persistRole(_ name: String) {
        if name.isEmpty {
            UserDefaults.standard.removeObject(forKey: ChatController.lastRoleKey)
        } else {
            UserDefaults.standard.set(name, forKey: ChatController.lastRoleKey)
        }
    }

    private func load() async {
        isLoading = response == nil
        errorMessage = nil
        selectedRole = UserDefaults.standard.string(forKey: ChatController.lastRoleKey) ?? ""
        do {
            let loaded: RolesResponse = try await model.client.get("/api/roles")
            response = loaded
            if let sessionID {
                let current: SessionRoleResponse = try await model.client.get("/api/sessions/\(sessionID)/role")
                sessionRole = current.role
                if !current.role.isEmpty {
                    selectedRole = current.role
                    persistRole(current.role)
                }
            } else {
                sessionRole = ""
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }
}
