import AntaresKit
import SwiftUI

struct ConfigView: View {
    private static let essentialsID = "__essentials"
    private static let yamlID = "__yaml"

    @EnvironmentObject private var model: AppModel

    @State private var response: ConfigResponse?
    @State private var edits: [String: ConfigJSON] = [:]
    @State private var listDrafts: [String: String] = [:]
    @State private var revealed = Set<String>()
    @State private var section = ConfigView.essentialsID
    @State private var filter = ""
    @State private var showAdvanced = false
    @State private var yaml = ""
    @State private var yamlDraft: String?
    @State private var restartFields: [String] = []
    @State private var errorMessage: String?
    @State private var statusMessage: String?
    @State private var isLoading = false
    @State private var isSaving = false
    @State private var didLoad = false

    private var fields: [ConfigField] {
        response?.formFields ?? []
    }

    private var groups: [String] {
        var seen: [String] = []
        for field in fields where !seen.contains(field.group) {
            seen.append(field.group)
        }
        return seen
    }

    private var query: String {
        filter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
    }

    private var isSearching: Bool { !query.isEmpty }

    private var visibleFields: [ConfigField] {
        if isSearching {
            return fields.filter {
                $0.path.lowercased().contains(query) || $0.label.lowercased().contains(query)
            }
        }
        let sectionFields: [ConfigField]
        if section == Self.essentialsID {
            sectionFields = fields.filter { $0.tier == "essential" }
        } else {
            sectionFields = fields.filter { $0.group == section }
        }
        if showAdvanced {
            return sectionFields
        }
        return sectionFields.filter { $0.tier != "advanced" }
    }

    private var hiddenAdvancedCount: Int {
        guard !isSearching, section != Self.yamlID else { return 0 }
        let sectionFields: [ConfigField]
        if section == Self.essentialsID {
            sectionFields = fields.filter { $0.tier == "essential" }
        } else {
            sectionFields = fields.filter { $0.group == section }
        }
        return max(0, sectionFields.count - visibleFields.count)
    }

    private var dirtyCount: Int { edits.count }
    private var yamlDirty: Bool { yamlDraft != nil }

    var body: some View {
        Group {
            if let errorMessage, !didLoad, !isLoading {
                ContentUnavailableView(
                    "Config",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else {
                formContent
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading config…") }
        }
        .navigationTitle("Config")
        .searchable(text: $filter, prompt: "Search settings")
        .toolbar {
            ToolbarItem(placement: .principal) {
                if !isSearching {
                    Picker("Section", selection: $section) {
                        Text("Essentials").tag(Self.essentialsID)
                        ForEach(groups, id: \.self) { group in
                            Text(humanizeGroup(group)).tag(group)
                        }
                        Text("YAML").tag(Self.yamlID)
                    }
                    .pickerStyle(.menu)
                    .frame(maxWidth: 240)
                }
            }
            ToolbarItem(placement: .primaryAction) {
                if section == Self.yamlID, !isSearching {
                    Button(isSaving ? "Saving…" : "Save YAML") {
                        Task { await saveYAML() }
                    }
                    .disabled(!yamlDirty || isSaving || isLoading)
                } else {
                    Button(saveLabel) {
                        Task { await save() }
                    }
                    .disabled(dirtyCount == 0 || isSaving || isLoading)
                }
            }
        }
        .onChange(of: section) { _, newValue in
            showAdvanced = false
            if newValue == Self.yamlID {
                Task { await loadRaw(quiet: yaml.isEmpty == false) }
            }
        }
        .task { await load() }
        .refreshable { await load() }
    }

    @ViewBuilder
    private var formContent: some View {
        Form {
            if let errorMessage, didLoad {
                Section {
                    Text(errorMessage)
                        .foregroundStyle(.red)
                }
            }
            if let statusMessage {
                Section {
                    Text(statusMessage)
                        .foregroundStyle(.secondary)
                }
            }
            if !restartFields.isEmpty {
                Section {
                    Text("Restart required for: \(restartFields.joined(separator: ", "))")
                }
            }

            if isSearching {
                searchSection
            } else if section == Self.yamlID {
                yamlSection
            } else {
                settingsSection
            }
        }
        .formStyle(.grouped)
    }

    @ViewBuilder
    private var searchSection: some View {
        Section(visibleFields.isEmpty ? "No matches" : "\(visibleFields.count) matches") {
            if visibleFields.isEmpty {
                Text("Nothing matched the filter.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(visibleFields) { field in
                    fieldRow(field, showGroup: true)
                }
            }
        }
    }

    @ViewBuilder
    private var settingsSection: some View {
        let title = section == Self.essentialsID ? "Essentials" : humanizeGroup(section)
        Section {
            if section == Self.essentialsID {
                Text("Core settings needed to get Antares answering.")
                    .foregroundStyle(.secondary)
            }
            if visibleFields.isEmpty {
                Text("Nothing here.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(visibleFields) { field in
                    fieldRow(field, showGroup: section == Self.essentialsID)
                }
            }
            if hiddenAdvancedCount > 0 {
                Button("Show \(hiddenAdvancedCount) advanced") {
                    showAdvanced = true
                }
            } else if showAdvanced {
                Button("Hide advanced") {
                    showAdvanced = false
                }
            }
        } header: {
            Text(title)
        }
    }

    @ViewBuilder
    private var yamlSection: some View {
        Section("Raw YAML") {
            Text("Direct edits write the whole config file.")
                .foregroundStyle(.secondary)
            TextEditor(text: Binding(
                get: { yamlDraft ?? yaml },
                set: { yamlDraft = $0 }
            ))
            .font(.body.monospaced())
            .frame(minHeight: 360)
            .disabled(isLoading || isSaving)
            if let path = response?.path, !path.isEmpty {
                Text(path)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
        }
    }

    @ViewBuilder
    private func fieldRow(_ field: ConfigField, showGroup: Bool) -> some View {
        let dirty = edits[field.path] != nil
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(field.label)
                    .fontWeight(dirty ? .semibold : .regular)
                    .foregroundStyle(dirty ? Color.accentColor : Color.primary)
                if showGroup {
                    Text(humanizeGroup(field.group))
                        .font(.caption2)
                        .padding(.horizontal, 6)
                        .padding(.vertical, 2)
                        .background(.quaternary, in: Capsule())
                }
                if dirty {
                    Text("Changed")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let reload = field.reload, !reload.isEmpty {
                    Text(reloadLabel(reload))
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
            }
            Text(field.path)
                .font(.caption2.monospaced())
                .foregroundStyle(.tertiary)
            if let help = field.help, !help.isEmpty {
                Text(help)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            control(for: field)
        }
        .padding(.vertical, 4)
    }

    @ViewBuilder
    private func control(for field: ConfigField) -> some View {
        let current = valueOf(field)
        if field.type == .boolean {
            Toggle(field.label, isOn: Binding(
                get: { current.boolValue },
                set: { setValue(field.path, .bool($0)) }
            ))
            .labelsHidden()
        } else if let options = field.enumValues, !options.isEmpty {
            Picker(field.label, selection: Binding(
                get: { current.stringValue },
                set: { setValue(field.path, .string($0)) }
            )) {
                ForEach(options, id: \.self) { option in
                    Text(option).tag(option)
                }
            }
            .labelsHidden()
        } else if field.type == .stringArray {
            TextField("Comma-separated", text: listBinding(for: field, current: current))
        } else if field.secret {
            HStack {
                if revealed.contains(field.path) {
                    TextField("Not set", text: stringBinding(for: field, current: current))
                } else {
                    SecureField("Not set", text: stringBinding(for: field, current: current))
                }
                Button {
                    if revealed.contains(field.path) {
                        revealed.remove(field.path)
                    } else {
                        revealed.insert(field.path)
                    }
                } label: {
                    Image(systemName: revealed.contains(field.path) ? "eye.slash" : "eye")
                }
                .buttonStyle(.borderless)
                .help("Reveal")
            }
        } else if field.type == .number {
            TextField("0", text: Binding(
                get: { current.stringValue },
                set: { text in
                    if let intValue = Int(text) {
                        setValue(field.path, .int(intValue))
                    } else if let doubleValue = Double(text) {
                        setValue(field.path, .double(doubleValue))
                    } else {
                        setValue(field.path, .string(text))
                    }
                }
            ))
        } else {
            TextField("", text: stringBinding(for: field, current: current))
        }
    }

    private var saveLabel: String {
        if isSaving { return "Saving…" }
        if dirtyCount > 0 { return "Save \(dirtyCount)" }
        return "Save"
    }

    private func valueOf(_ field: ConfigField) -> ConfigJSON {
        if let edited = edits[field.path] {
            return edited
        }
        if let value = response?.value(at: field.path) {
            return value.configJSON
        }
        return field.defaultValue?.configJSON ?? .null
    }

    private func setValue(_ path: String, _ value: ConfigJSON) {
        edits[path] = value
        statusMessage = nil
    }

    private func stringBinding(for field: ConfigField, current: ConfigJSON) -> Binding<String> {
        Binding(
            get: { current.stringValue },
            set: { setValue(field.path, .string($0)) }
        )
    }

    private func listBinding(for field: ConfigField, current: ConfigJSON) -> Binding<String> {
        Binding(
            get: {
                if let draft = listDrafts[field.path] {
                    return draft
                }
                return current.stringArray.joined(separator: ", ")
            },
            set: { text in
                listDrafts[field.path] = text
                let parsed = text.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty }
                setValue(field.path, .strings(parsed))
            }
        )
    }

    private func humanizeGroup(_ name: String) -> String {
        let acronyms = ["rag": "RAG", "mcp": "MCP", "yaml": "YAML", "dsn": "DSN", "api": "API"]
        return name.split(separator: "_").enumerated().map { index, part in
            let key = part.lowercased()
            if let acronym = acronyms[key] { return acronym }
            let word = String(part)
            if index == 0 {
                return word.prefix(1).uppercased() + word.dropFirst()
            }
            return word
        }.joined(separator: " ")
    }

    private func reloadLabel(_ reload: String) -> String {
        switch reload {
        case "restart_required":
            return "Restart"
        case "reconciled":
            return "Reconciled"
        default:
            return "Live"
        }
    }

    private func load() async {
        isLoading = !didLoad
        errorMessage = nil
        do {
            let loaded = try await model.client.getConfig()
            response = loaded
            restartFields = loaded.restartFields
            edits = [:]
            listDrafts = [:]
            didLoad = true
            if section == Self.yamlID {
                await loadRaw(quiet: true)
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func loadRaw(quiet: Bool) async {
        if !quiet { isLoading = yaml.isEmpty }
        do {
            let raw = try await model.client.getRawConfig()
            yaml = raw.yaml
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        if !quiet { isLoading = false }
    }

    private func save() async {
        let payload = edits.filter { !$0.value.isMaskedSecret }
        guard !payload.isEmpty else { return }
        isSaving = true
        errorMessage = nil
        statusMessage = nil
        do {
            let result = try await model.client.updateConfig(payload)
            restartFields = result.restartFields
            edits = [:]
            listDrafts = [:]
            statusMessage = "Saved."
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isSaving = false
    }

    private func saveYAML() async {
        guard let yamlDraft else { return }
        isSaving = true
        errorMessage = nil
        statusMessage = nil
        do {
            let result = try await model.client.saveRawConfig(yamlDraft)
            restartFields = result.restartFields
            yaml = yamlDraft
            self.yamlDraft = nil
            statusMessage = "Saved YAML."
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isSaving = false
    }
}
