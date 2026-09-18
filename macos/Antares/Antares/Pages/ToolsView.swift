import AntaresKit
import SwiftUI

struct ToolsView: View {
    @EnvironmentObject private var model: AppModel

    @State private var response: ToolsResponse?
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyName: String?

    var body: some View {
        Group {
            if let errorMessage, response == nil, !isLoading {
                ContentUnavailableView(
                    "Tools",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if let response, response.tools.isEmpty, !isLoading {
                ContentUnavailableView(
                    "No Tools",
                    systemImage: "wrench.and.screwdriver",
                    description: Text("No tools are registered on this backend.")
                )
            } else {
                List {
                    if let errorMessage {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    if let response {
                        Section("Toolset") {
                            Text(response.toolset.isEmpty ? "—" : response.toolset)
                            if !response.toolsets.isEmpty {
                                Text(response.toolsets.joined(separator: ", "))
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }

                        Section("Tools") {
                            ForEach(response.tools) { tool in
                                HStack(alignment: .top) {
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text(tool.name)
                                            .font(.headline)
                                            .fontDesign(.monospaced)
                                        Text(tool.description)
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                        HStack(spacing: 6) {
                                            if tool.requiresApproval {
                                                Text("Mutating")
                                                    .font(.caption2)
                                                    .foregroundStyle(.orange)
                                            }
                                            ForEach(tool.toolsets, id: \.self) { set in
                                                Text(set)
                                                    .font(.caption2)
                                                    .foregroundStyle(.secondary)
                                            }
                                        }
                                    }
                                    Spacer()
                                    Toggle(
                                        "Enabled",
                                        isOn: Binding(
                                            get: { tool.enabled },
                                            set: { enabled in
                                                Task { await toggle(name: tool.name, enabled: enabled) }
                                            }
                                        )
                                    )
                                    .labelsHidden()
                                    .disabled(busyName != nil)
                                }
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading tools…") }
        }
        .navigationTitle("Tools")
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = response == nil
        errorMessage = nil
        do {
            let loaded: ToolsResponse = try await model.client.get("/api/tools")
            response = loaded
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
                "/api/tools/toggle",
                body: ToggleToolRequest(name: name, enabled: enabled)
            )
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
