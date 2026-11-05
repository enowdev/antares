import AntaresKit
import SwiftUI

struct PluginsView: View {
    @EnvironmentObject private var model: AppModel

    enum Tab: String, CaseIterable, Identifiable {
        case plugins
        case docs
        var id: String { rawValue }
        var title: String { self == .plugins ? "Plugins" : "Docs" }
    }

    @State private var tab: Tab = .plugins
    @State private var enabled = true
    @State private var plugins: [PluginInfo] = []
    @State private var dirs: [String] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyName: String?
    @State private var showingAdd = false
    @State private var showingHub = false

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
                pluginsTab
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading plugins…") }
        }
        .navigationTitle("Plugins")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button("Rescan") { Task { await rescan() } }
                    .disabled(busyName != nil)
            }
            ToolbarItem(placement: .automatic) {
                Button("Hub") { showingHub = true }
            }
            ToolbarItem(placement: .primaryAction) {
                Button("Add") { showingAdd = true }
            }
        }
        .sheet(isPresented: $showingAdd) {
            AddPluginSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .sheet(isPresented: $showingHub) {
            HubPluginsSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private var pluginsTab: some View {
        Group {
            if !isLoading && plugins.isEmpty {
                ContentUnavailableView(
                    "No Plugins",
                    systemImage: "puzzlepiece.extension",
                    description: Text("Drop a plugin into \(dirs.first ?? "~/.antares/plugins") or install one from the hub.")
                )
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 12) {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        if !enabled {
                            Text("Plugins are switched off for this process.")
                                .foregroundStyle(.orange)
                        }
                        LazyVGrid(columns: [GridItem(.adaptive(minimum: 240), spacing: 12)], spacing: 12) {
                            ForEach(plugins) { plugin in
                                pluginCard(plugin)
                            }
                        }
                    }
                    .padding(16)
                }
            }
        }
    }

    private func pluginCard(_ plugin: PluginInfo) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(alignment: .top) {
                Image(systemName: "puzzlepiece.extension.fill")
                    .foregroundStyle(plugin.error == nil ? Color.accentColor : .red)
                Text(plugin.name).font(.headline).lineLimit(1)
                Spacer()
                if plugin.error == nil {
                    Toggle(
                        "Enabled",
                        isOn: Binding(
                            get: { plugin.enabled },
                            set: { enabled in
                                Task { await toggle(plugin, enabled: enabled) }
                            }
                        )
                    )
                    .labelsHidden()
                    .disabled(busyName != nil)
                }
            }
            if !plugin.description.isEmpty {
                Text(plugin.description)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
            }
            if let error = plugin.error, !error.isEmpty {
                Text(error).font(.caption).foregroundStyle(.red)
            }
            HStack {
                if !plugin.version.isEmpty {
                    Text(plugin.version).font(.caption2).foregroundStyle(.secondary)
                }
                if plugin.error != nil {
                    Text("Broken").font(.caption2).foregroundStyle(.red)
                }
                ForEach(plugin.hooks, id: \.self) { hook in
                    Text(hook)
                        .font(.caption2.monospaced())
                        .foregroundStyle(.secondary)
                }
            }
            Text(plugin.command.isEmpty ? plugin.dir : "\(plugin.dir) · \(plugin.command)")
                .font(.caption2.monospaced())
                .foregroundStyle(.secondary)
                .lineLimit(2)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(plugin.error == nil ? Color.secondary.opacity(0.2) : Color.red.opacity(0.4))
        )
    }

    private var docs: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                Text("How to write a plugin")
                    .font(.headline)
                Text("A plugin is a directory with plugin.yaml and an executable that reads one JSON event on stdin.")
                    .foregroundStyle(.secondary)
                Text(Self.docsText)
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
        isLoading = plugins.isEmpty
        errorMessage = nil
        do {
            let response = try await model.client.plugins()
            enabled = response.enabled
            plugins = response.plugins
            dirs = response.dirs
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func rescan() async {
        busyName = "*"
        defer { busyName = nil }
        errorMessage = nil
        do {
            _ = try await model.client.reloadPlugins()
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func toggle(_ plugin: PluginInfo, enabled: Bool) async {
        busyName = plugin.name
        defer { busyName = nil }
        errorMessage = nil
        do {
            _ = try await model.client.togglePlugin(name: plugin.name, enabled: enabled)
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private static let docsText = """
    # ~/.antares/plugins/audit/plugin.yaml
    name: audit
    description: Log every terminal command
    command: ./run.sh
    hooks: [pre_tool_call]

    # ~/.antares/plugins/audit/run.sh
    #!/bin/sh
    cat >> ~/antares-audit.log
    echo '{}'
    """
}

private struct AddPluginSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var onAdded: () -> Void

    private static let hooks = ["pre_tool_call", "post_tool_call", "session_start", "session_end", "turn_end"]

    @State private var name = ""
    @State private var description = ""
    @State private var command = ""
    @State private var args = ""
    @State private var selectedHooks: Set<String> = []
    @State private var errorMessage: String?
    @State private var isSaving = false

    private var canSave: Bool {
        !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !command.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !selectedHooks.isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Name", text: $name)
                TextField("Description", text: $description)
                TextField("Command", text: $command)
                Text("Relative to the plugin directory unless absolute or on PATH.")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                TextField("Args", text: $args)
                Section("Hooks") {
                    ForEach(Self.hooks, id: \.self) { hook in
                        Toggle(hook, isOn: Binding(
                            get: { selectedHooks.contains(hook) },
                            set: { on in
                                if on { selectedHooks.insert(hook) } else { selectedHooks.remove(hook) }
                            }
                        ))
                    }
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .navigationTitle("Add plugin")
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
        .frame(minWidth: 440, minHeight: 420)
    }

    private func save() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        do {
            let result = try await model.client.addPlugin(
                AddPluginRequest(
                    name: name.trimmingCharacters(in: .whitespacesAndNewlines),
                    description: description.trimmingCharacters(in: .whitespacesAndNewlines),
                    command: command.trimmingCharacters(in: .whitespacesAndNewlines),
                    args: args.split(whereSeparator: \.isWhitespace).map(String.init).filter { !$0.isEmpty },
                    hooks: Self.hooks.filter { selectedHooks.contains($0) }
                )
            )
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

private struct HubPluginsSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var onInstalled: () -> Void

    @State private var query = ""
    @State private var entries: [HubPlugin] = []
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
                        "No Plugins",
                        systemImage: "storefront",
                        description: Text("Try a different search.")
                    )
                } else {
                    List {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        ForEach(entries) { entry in
                            HStack(alignment: .top) {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(entry.name).font(.headline)
                                    Text(entry.summary).font(.caption).foregroundStyle(.secondary)
                                    if let command = entry.command, !command.isEmpty {
                                        Text(command).font(.caption2.monospaced()).foregroundStyle(.secondary)
                                    }
                                    if !entry.hooks.isEmpty {
                                        Text(entry.hooks.joined(separator: ", "))
                                            .font(.caption2.monospaced())
                                            .foregroundStyle(.secondary)
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
            .navigationTitle("Plugin hub")
            .searchable(text: $query, prompt: "Search plugins")
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
            let response = try await model.client.hubPlugins(query: query)
            entries = response.plugins
            errorMessage = response.error
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func install(_ entry: HubPlugin) async {
        busyID = entry.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            let result = try await model.client.installHubPlugin(id: entry.id)
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
