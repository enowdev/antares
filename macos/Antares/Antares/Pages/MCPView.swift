import AntaresKit
import SwiftUI

struct MCPView: View {
    @EnvironmentObject private var model: AppModel

    enum Tab: String, CaseIterable, Identifiable {
        case servers
        case docs
        var id: String { rawValue }
        var title: String { self == .servers ? "Servers" : "Docs" }
    }

    @State private var tab: Tab = .servers
    @State private var enabled = true
    @State private var servers: [MCPServerStatus] = []
    @State private var openName: String?
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var refreshing = false
    @State private var showingAdd = false
    @State private var showingHub = false
    @State private var pendingRemove: MCPServerStatus?

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Picker("Tab", selection: $tab) {
                ForEach(Tab.allCases) { item in
                    Text(item.title).tag(item)
                }
            }
            .pickerStyle(.segmented)
            .padding(16)

            if tab == .docs {
                docs
            } else {
                serversTab
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading MCP…") }
        }
        .navigationTitle("MCP")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button(refreshing ? "Refreshing…" : "Refresh") {
                    Task { await refresh() }
                }
                .disabled(refreshing)
            }
            ToolbarItem(placement: .automatic) {
                Button("Add") { showingAdd = true }
            }
            ToolbarItem(placement: .primaryAction) {
                Button("Hub") { showingHub = true }
            }
        }
        .sheet(isPresented: $showingAdd) {
            AddMCPSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .sheet(isPresented: $showingHub) {
            HubMCPSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .confirmationDialog(
            "Remove \(pendingRemove?.name ?? "server")?",
            isPresented: Binding(
                get: { pendingRemove != nil },
                set: { if !$0 { pendingRemove = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Remove", role: .destructive) {
                if let pendingRemove {
                    Task { await remove(pendingRemove) }
                }
            }
            Button("Cancel", role: .cancel) { pendingRemove = nil }
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private var serversTab: some View {
        Group {
            if !isLoading && servers.isEmpty {
                ContentUnavailableView(
                    "No MCP Servers",
                    systemImage: "cable.connector",
                    description: Text("Install a server from the hub or add one by command or URL.")
                )
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        if !enabled {
                            Text("MCP is disabled in config. Adding a server turns it on.")
                                .foregroundStyle(.orange)
                        }
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 240), spacing: 12)], spacing: 12) {
                            ForEach(servers) { server in
                                serverCard(server)
                            }
                        }
                    }
                    .padding(16)
                }
            }
        }
    }

    private func serverCard(_ server: MCPServerStatus) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top) {
                Image(systemName: server.connected ? "checkmark.circle.fill" : "xmark.circle.fill")
                    .foregroundStyle(server.connected ? .green : .red)
                Text(server.name)
                    .font(.headline)
                    .lineLimit(1)
                Spacer()
                Button(role: .destructive) {
                    pendingRemove = server
                } label: {
                    Image(systemName: "trash")
                }
                .buttonStyle(.borderless)
            }
            HStack {
                Text(server.connected ? "Connected" : server.started ? "Waiting" : "Failed")
                    .font(.caption)
                    .foregroundStyle(server.connected ? .green : server.started ? .orange : .red)
                if server.connected {
                    Text("\(server.tools.count) tools")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            if let error = server.error, !error.isEmpty {
                Text(error).font(.caption).foregroundStyle(.red)
            }
            if !server.tools.isEmpty {
                Button(openName == server.name ? "Hide tools" : "Show tools") {
                    openName = openName == server.name ? nil : server.name
                }
                .buttonStyle(.plain)
                .font(.caption)
                if openName == server.name {
                    ForEach(server.tools) { tool in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(tool.name).font(.caption.monospaced())
                            if !tool.description.isEmpty {
                                Text(tool.description).font(.caption2).foregroundStyle(.secondary)
                            }
                        }
                        .padding(6)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.secondary.opacity(0.2)))
                    }
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.2)))
    }

    private var docs: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                Text("How to add an MCP server")
                    .font(.headline)
                Text("Servers can be stdio commands or remote HTTP endpoints. Example config:")
                    .foregroundStyle(.secondary)
                Text(Self.docsYAML)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .padding(12)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 8))
            }
            .padding(16)
        }
    }

    private func load() async {
        isLoading = servers.isEmpty
        errorMessage = nil
        do {
            let response = try await model.client.mcpStatus()
            enabled = response.enabled
            servers = response.servers
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func refresh() async {
        refreshing = true
        errorMessage = nil
        do {
            let response = try await model.client.refreshMCP()
            enabled = response.enabled
            servers = response.servers
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        refreshing = false
    }

    private func remove(_ server: MCPServerStatus) async {
        pendingRemove = nil
        errorMessage = nil
        do {
            _ = try await model.client.deleteMCPServer(server.name)
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private static let docsYAML = """
    mcp:
      enabled: true
      servers:
        filesystem:
          enabled: true
          transport: stdio
          command: npx
          args: ["-y", "@modelcontextprotocol/server-filesystem", "/home/you/data"]
        remote:
          enabled: true
          transport: http
          url: https://example.com/mcp
          headers:
            Authorization: "Bearer ..."
    """
}

private struct AddMCPSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var onAdded: () -> Void

    @State private var transport = "stdio"
    @State private var name = ""
    @State private var command = ""
    @State private var args = ""
    @State private var url = ""
    @State private var envText = ""
    @State private var errorMessage: String?
    @State private var isSaving = false

    private var canSave: Bool {
        !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && (transport == "stdio"
                ? !command.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                : !url.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
    }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $name)
                Picker("Transport", selection: $transport) {
                    Text("stdio").tag("stdio")
                    Text("http").tag("http")
                }
                .pickerStyle(.segmented)
                if transport == "stdio" {
                    TextField("Command", text: $command)
                    TextField("Args", text: $args)
                    TextField("Env (KEY=value per line)", text: $envText, axis: .vertical)
                        .lineLimit(3...8)
                } else {
                    TextField("URL", text: $url)
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .navigationTitle("Add MCP server")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "Adding…" : "Add") {
                        Task { await save() }
                    }
                    .disabled(!canSave || isSaving)
                }
            }
        }
        .frame(minWidth: 440, minHeight: 360)
    }

    private func save() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        let trimmedName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let request: AddMCPServerRequest
        if transport == "stdio" {
            switch MCPEnvParser.parse(envText) {
            case .invalid:
                errorMessage = "Each env line must be KEY=value with a valid variable name."
                return
            case .ok(let env):
                request = AddMCPServerRequest(
                    name: trimmedName,
                    transport: "stdio",
                    command: command.trimmingCharacters(in: .whitespacesAndNewlines),
                    args: args.split(whereSeparator: \.isWhitespace).map(String.init).filter { !$0.isEmpty },
                    env: env.isEmpty ? nil : env
                )
            }
        } else {
            request = AddMCPServerRequest(
                name: trimmedName,
                transport: "http",
                url: url.trimmingCharacters(in: .whitespacesAndNewlines)
            )
        }
        do {
            let result = try await model.client.addMCPServer(request)
            if result.ok {
                onAdded()
                dismiss()
            } else {
                errorMessage = result.error ?? "Add failed."
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}

private struct HubMCPSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var onInstalled: () -> Void

    @State private var query = ""
    @State private var entries: [HubMCPServer] = []
    @State private var errorMessage: String?
    @State private var note: String?
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
                        "No MCP Servers",
                        systemImage: "storefront",
                        description: Text("Try a different search.")
                    )
                } else {
                    List {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        if let note {
                            Text(note).foregroundStyle(.orange)
                        }
                        ForEach(entries) { entry in
                            HStack(alignment: .top) {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(entry.name).font(.headline)
                                    Text(entry.summary).font(.caption).foregroundStyle(.secondary)
                                    if let command = entry.command, !command.isEmpty {
                                        Text(command).font(.caption2.monospaced()).foregroundStyle(.secondary)
                                    }
                                    if let keys = entry.needsKeys, !keys.isEmpty {
                                        Text("Needs \(keys.joined(separator: ", "))")
                                            .font(.caption2)
                                            .foregroundStyle(.orange)
                                    }
                                }
                                Spacer()
                                if entry.installed {
                                    Text("Installed").font(.caption).foregroundStyle(.secondary)
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
            .navigationTitle("MCP hub")
            .searchable(text: $query, prompt: "Search MCP servers")
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
            let response = try await model.client.hubMCP(query: query)
            entries = response.servers
            errorMessage = response.error
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func install(_ entry: HubMCPServer) async {
        busyID = entry.id
        defer { busyID = nil }
        errorMessage = nil
        note = nil
        do {
            let result = try await model.client.installHubMCP(id: entry.id)
            if result.ok {
                if let keys = result.missingKeys, !keys.isEmpty {
                    note = "Installed. Still needs: \(keys.joined(separator: ", "))."
                }
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
