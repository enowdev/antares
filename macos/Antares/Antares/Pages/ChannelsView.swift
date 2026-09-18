import AntaresKit
import SwiftUI

struct ChannelsView: View {
    @EnvironmentObject private var model: AppModel

    @State private var channels: [ChannelInfo] = []
    @State private var pairings: [PairingInfo] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyID: String?
    @State private var configFor: ChannelInfo?
    @State private var fieldValues: [String: String] = [:]
    @State private var revealedKeys: Set<String> = []

    var body: some View {
        Group {
            if let errorMessage, channels.isEmpty, pairings.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Channels",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && channels.isEmpty && pairings.isEmpty {
                ContentUnavailableView(
                    "No Channels",
                    systemImage: "antenna.radiowaves.left.and.right",
                    description: Text("Messaging channels will appear here.")
                )
            } else {
                List {
                    if let errorMessage, configFor == nil {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    Section("Channels") {
                        ForEach(channels) { channel in
                            VStack(alignment: .leading, spacing: 8) {
                                HStack {
                                    Text(channel.label)
                                        .font(.headline)
                                    Spacer()
                                    Text(statusLabel(channel))
                                        .font(.caption)
                                        .foregroundStyle(channel.connected ? .green : .secondary)
                                }
                                if let botName = channel.botName, !botName.isEmpty, channel.connected {
                                    Text(botName)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                } else if !channel.detail.isEmpty {
                                    Text(channel.detail)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                }
                                Button(channel.configured ? "Change token" : "Connect") {
                                    openConfig(channel)
                                }
                            }
                        }
                    }

                    Section("Devices") {
                        if pairings.isEmpty {
                            Text("No paired devices.")
                                .foregroundStyle(.secondary)
                        } else {
                            ForEach(pairings) { pairing in
                                HStack {
                                    VStack(alignment: .leading, spacing: 4) {
                                        HStack {
                                            Text(pairing.platform)
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                            Text(pairing.displayName.isEmpty ? pairing.externalID : pairing.displayName)
                                                .font(.headline)
                                        }
                                        Text("\(pairing.externalID) · \(pairing.status)")
                                            .font(.caption)
                                            .fontDesign(.monospaced)
                                            .foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    if pairing.status == "pending" {
                                        Button("Approve") {
                                            Task { await setPairing(pairing, approve: true) }
                                        }
                                        .disabled(busyID != nil)
                                    } else {
                                        Button("Revoke", role: .destructive) {
                                            Task { await setPairing(pairing, approve: false) }
                                        }
                                        .disabled(busyID != nil)
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading channels…") }
        }
        .navigationTitle("Channels")
        .sheet(item: $configFor) { channel in
            NavigationStack {
                Form {
                    if !channel.detail.isEmpty {
                        Text(channel.detail)
                            .foregroundStyle(.secondary)
                    }

                    ForEach(channel.fields) { field in
                        fieldRow(field)
                    }

                    if let docs = channel.docs, let url = URL(string: docs) {
                        Link("Get a token for \(channel.label)", destination: url)
                    }

                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                    }
                }
                .navigationTitle(channel.configured ? "Change token" : "Connect \(channel.label)")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { configFor = nil }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button(busyID == channel.id ? "Saving…" : "Verify and save") {
                            Task { await saveConfig(channel) }
                        }
                        .disabled(
                            ChannelConfigForm.hasMissingRequired(channel.fields, values: fieldValues)
                                || busyID != nil
                        )
                    }
                }
            }
            .frame(minWidth: 460, minHeight: 360)
        }
        .task { await load() }
        .refreshable { await load() }
    }

    @ViewBuilder
    private func fieldRow(_ field: ChannelField) -> some View {
        let binding = Binding(
            get: { fieldValues[field.key] ?? "" },
            set: { fieldValues[field.key] = $0 }
        )
        let placeholder = field.placeholder ?? (field.set ? "••••••••" : "")
        VStack(alignment: .leading, spacing: 6) {
            HStack {
                Text(field.label)
                if field.set {
                    Text("· set")
                        .foregroundStyle(.secondary)
                }
            }
            HStack {
                if field.secret && !revealedKeys.contains(field.key) {
                    SecureField(placeholder, text: binding)
                        .textContentType(.password)
                } else {
                    TextField(placeholder, text: binding)
                }
                if field.secret {
                    Button(revealedKeys.contains(field.key) ? "Hide" : "Show") {
                        if revealedKeys.contains(field.key) {
                            revealedKeys.remove(field.key)
                        } else {
                            revealedKeys.insert(field.key)
                        }
                    }
                    .buttonStyle(.borderless)
                }
            }
        }
    }

    private func statusLabel(_ channel: ChannelInfo) -> String {
        if channel.connected { return "Connected" }
        if channel.enabled { return "Connecting" }
        if channel.configured { return "Token set" }
        return "Disabled"
    }

    private func openConfig(_ channel: ChannelInfo) {
        fieldValues = [:]
        revealedKeys = []
        errorMessage = nil
        configFor = channel
    }

    private func load() async {
        isLoading = channels.isEmpty && pairings.isEmpty
        errorMessage = nil
        do {
            let response: ChannelsResponse = try await model.client.get("/api/channels")
            channels = response.channels
            pairings = response.pairings
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func saveConfig(_ channel: ChannelInfo) async {
        let fields = ChannelConfigForm.collectedFields(fieldValues)
        busyID = channel.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            let result: ChannelMutationResponse = try await model.client.post(
                "/api/channels/\(channel.id)/config",
                body: ChannelConfigRequest(
                    fields: fields,
                    enabled: channel.configured ? nil : true
                )
            )
            if result.ok {
                configFor = nil
                fieldValues = [:]
                revealedKeys = []
                await load()
            } else {
                errorMessage = result.error ?? "Token rejected."
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func setPairing(_ pairing: PairingInfo, approve: Bool) async {
        busyID = pairing.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            let path = approve ? "/api/pairing/approve" : "/api/pairing/revoke"
            let _: PairingInfo = try await model.client.post(
                path,
                body: PairingIDRequest(id: pairing.id)
            )
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
