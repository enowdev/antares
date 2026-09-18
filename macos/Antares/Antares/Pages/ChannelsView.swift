import AntaresKit
import SwiftUI

struct ChannelsView: View {
    @EnvironmentObject private var model: AppModel

    @State private var channels: [ChannelInfo] = []
    @State private var pairings: [PairingInfo] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyID: String?
    @State private var tokenDrafts: [String: String] = [:]
    @State private var configFor: ChannelInfo?

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
                    if let errorMessage {
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
                                    tokenDrafts[channel.id] = ""
                                    configFor = channel
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
                    if let docs = channel.docs, let url = URL(string: docs) {
                        Link("Get a token", destination: url)
                    }
                    SecureField(
                        channel.configured ? "New token" : "Token",
                        text: Binding(
                            get: { tokenDrafts[channel.id] ?? "" },
                            set: { tokenDrafts[channel.id] = $0 }
                        )
                    )
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
                        Button(busyID == channel.id ? "Saving…" : "Save") {
                            Task { await saveToken(channel) }
                        }
                        .disabled(tokenIsMissing(channel) || busyID != nil)
                    }
                }
            }
            .frame(minWidth: 420, minHeight: 220)
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private func statusLabel(_ channel: ChannelInfo) -> String {
        if channel.connected { return "Connected" }
        if channel.enabled { return "Connecting" }
        if channel.configured { return "Token set" }
        return "Disabled"
    }

    private func tokenIsMissing(_ channel: ChannelInfo) -> Bool {
        let value = tokenDrafts[channel.id]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return value.isEmpty
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

    private func saveToken(_ channel: ChannelInfo) async {
        let token = tokenDrafts[channel.id]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard !token.isEmpty else { return }
        busyID = channel.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            let result: ChannelMutationResponse
            if channel.id == "telegram" || channel.id == "discord" {
                result = try await model.client.post(
                    "/api/channels/\(channel.id)/token",
                    body: ChannelTokenRequest(token: token)
                )
            } else {
                let key = channel.fields.first(where: { $0.secret })?.key ?? "bot_token"
                result = try await model.client.post(
                    "/api/channels/\(channel.id)/config",
                    body: ChannelConfigRequest(
                        fields: [key: token],
                        enabled: channel.configured ? nil : true
                    )
                )
            }
            if result.ok {
                configFor = nil
                tokenDrafts[channel.id] = ""
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
