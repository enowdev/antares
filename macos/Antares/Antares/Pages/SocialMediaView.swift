import AntaresKit
import AppKit
import SwiftUI

struct SocialMediaView: View {
    @EnvironmentObject private var model: AppModel

    @State private var status: SocialStatus?
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busy = ""
    @State private var recoveryKey = ""
    @State private var showIMAP = false
    @State private var showingAdd = false
    @State private var pendingRemove: SocialAccount?

    var body: some View {
        Group {
            if let errorMessage, status == nil, !isLoading {
                ContentUnavailableView(
                    "Social Media",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if let status {
                List {
                    if let errorMessage {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    if !recoveryKey.isEmpty {
                        Section("Recovery key") {
                            Text("Save this recovery key. It will never be shown again.")
                                .foregroundStyle(.secondary)
                            Text(recoveryKey)
                                .font(.body.monospaced())
                                .textSelection(.enabled)
                            HStack {
                                Button("Copy") {
                                    NSPasteboard.general.clearContents()
                                    NSPasteboard.general.setString(recoveryKey, forType: .string)
                                }
                                Button("Dismiss") { recoveryKey = "" }
                            }
                            Text("Restart the backend after generating a key.")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }

                    if !status.encryptionReady {
                        Section("Encryption") {
                            Text("Generate a local master key before storing social credentials.")
                                .foregroundStyle(.secondary)
                            Button(busy == "encryption" ? "Generating…" : "Generate key") {
                                Task { await setupEncryption() }
                            }
                            .disabled(!busy.isEmpty)
                        }
                    }

                    Section("Gmail / IMAP") {
                        if showIMAP {
                            IMAPEditor(
                                defaults: status,
                                busy: $busy,
                                errorMessage: $errorMessage
                            ) {
                                showIMAP = false
                                Task { await load() }
                            } onCancel: {
                                showIMAP = false
                            }
                            .environmentObject(model)
                        } else if status.imapConfigured {
                            LabeledContent("Host", value: "\(status.imapHost):\(status.imapPort)")
                            LabeledContent("Email", value: status.imapUsername)
                            Button("Edit configuration") { showIMAP = true }
                        } else {
                            Text("IMAP is not configured.")
                                .foregroundStyle(.secondary)
                            Button("Configure Gmail") { showIMAP = true }
                                .disabled(!status.encryptionReady)
                        }
                    }

                    Section("Browser") {
                        HStack {
                            Text("Persistent login browser")
                            Spacer()
                            Text(status.browser.state)
                                .font(.caption)
                                .foregroundStyle(status.browser.state == "running" ? .green : .secondary)
                        }
                        Text("Stable fingerprint and login sessions.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        if status.browser.state != "running" {
                            Button(busy == "browser-start" ? "Starting…" : "Start") {
                                Task { await act("browser-start") { _ = try await model.client.startSocialBrowser() } }
                            }
                            .disabled(!status.encryptionReady || !busy.isEmpty)
                        } else {
                            Button(busy == "browser-stop" ? "Stopping…" : "Stop") {
                                Task { await act("browser-stop") { _ = try await model.client.stopSocialBrowser() } }
                            }
                            .disabled(!busy.isEmpty)
                        }
                        if !status.browser.error.isEmpty {
                            Text(status.browser.error)
                                .foregroundStyle(.red)
                        }
                    }

                    Section("Autopilot") {
                        Toggle(
                            "Enable social autopilot",
                            isOn: Binding(
                                get: { status.autopilotEnabled },
                                set: { enabled in
                                    Task {
                                        await act("autopilot") {
                                            _ = try await model.client.setSocialAutopilot(enabled: enabled)
                                        }
                                    }
                                }
                            )
                        )
                        .disabled(!busy.isEmpty)
                        Text("Let the agent manage connected accounts on a schedule.")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }

                    Section("Accounts") {
                        if status.accounts.isEmpty {
                            Text("No connected profiles yet.")
                                .foregroundStyle(.secondary)
                        } else {
                            ForEach(status.accounts) { account in
                                accountRow(account)
                            }
                        }
                    }
                }
            } else {
                Color.clear
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading social media…") }
        }
        .navigationTitle("Social media")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Add account") { showingAdd = true }
                    .disabled(!(status?.encryptionReady ?? false))
            }
        }
        .sheet(isPresented: $showingAdd) {
            AddSocialAccountSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .confirmationDialog(
            "Remove this account?",
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
        } message: {
            Text("Credentials stored for \(pendingRemove?.label ?? "this account") will be deleted.")
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private func accountRow(_ account: SocialAccount) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    Text(account.label)
                        .font(.headline)
                    Text("@\(account.username)")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                Text(account.status)
                    .font(.caption)
                    .foregroundStyle(account.status == "connected" ? .green : .secondary)
            }
            HStack(spacing: 8) {
                Text(account.platform)
                    .font(.caption2)
                    .textCase(.uppercase)
                    .foregroundStyle(.secondary)
                if account.hasPassword {
                    Text("Password")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                if account.hasRecovery {
                    Text("Recovery")
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                Spacer()
                if let url = URL(string: account.profileURL), !account.profileURL.isEmpty {
                    Button("Open profile") { NSWorkspace.shared.open(url) }
                }
                Button("Remove", role: .destructive) {
                    pendingRemove = account
                }
                .disabled(!busy.isEmpty)
            }
        }
    }

    private func load() async {
        isLoading = status == nil
        errorMessage = nil
        do {
            status = try await model.client.socialStatus()
        } catch {
            errorMessage = describe(error)
        }
        isLoading = false
    }

    private func setupEncryption() async {
        await act("encryption") {
            let response = try await model.client.setupSocialEncryption()
            recoveryKey = response.recoveryKey
        }
    }

    private func remove(_ account: SocialAccount) async {
        pendingRemove = nil
        await act("remove") {
            _ = try await model.client.deleteSocialAccount(account.id)
        }
    }

    private func act(_ name: String, _ work: () async throws -> Void) async {
        busy = name
        errorMessage = nil
        defer { busy = "" }
        do {
            try await work()
            status = try await model.client.socialStatus()
        } catch {
            errorMessage = describe(error)
        }
    }
}

private struct IMAPEditor: View {
    @EnvironmentObject private var model: AppModel
    let defaults: SocialStatus
    @Binding var busy: String
    @Binding var errorMessage: String?
    var onDone: () -> Void
    var onCancel: () -> Void

    @State private var host = ""
    @State private var port = "993"
    @State private var username = ""
    @State private var password = ""
    @State private var result = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            TextField("Host", text: $host)
            TextField("Port", text: $port)
            TextField("Username", text: $username)
            SecureField("App password", text: $password)
            if !result.isEmpty {
                Text(result)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            HStack {
                Button(busy == "imap-test" ? "Testing…" : "Test") {
                    Task { await test() }
                }
                .disabled(username.isEmpty || password.isEmpty || !busy.isEmpty)
                Button(busy == "imap-save" ? "Saving…" : "Save") {
                    Task { await save() }
                }
                .disabled(username.isEmpty || !busy.isEmpty)
                Button("Cancel", role: .cancel, action: onCancel)
            }
        }
        .onAppear {
            host = defaults.imapHost.isEmpty ? "imap.gmail.com" : defaults.imapHost
            port = defaults.imapPort == 0 ? "993" : String(defaults.imapPort)
            username = defaults.imapUsername
        }
    }

    private func request() -> SocialIMAPRequest {
        SocialIMAPRequest(
            host: host.trimmingCharacters(in: .whitespacesAndNewlines),
            port: Int(port) ?? 993,
            username: username.trimmingCharacters(in: .whitespacesAndNewlines),
            password: password
        )
    }

    private func test() async {
        busy = "imap-test"
        result = ""
        defer { busy = "" }
        do {
            let response = try await model.client.testSocialIMAP(request())
            if response.ok {
                result = "Connected. Inbox has \(response.inboxCount ?? 0) messages."
            } else {
                result = response.error ?? "IMAP test failed."
            }
        } catch {
            result = describe(error)
        }
    }

    private func save() async {
        busy = "imap-save"
        errorMessage = nil
        defer { busy = "" }
        do {
            _ = try await model.client.saveSocialIMAP(request())
            onDone()
        } catch {
            result = describe(error)
            errorMessage = describe(error)
        }
    }
}

private struct AddSocialAccountSheet: View {
    @EnvironmentObject private var model: AppModel
    var onSaved: () -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var platform = ""
    @State private var username = ""
    @State private var password = ""
    @State private var recovery = ""
    @State private var profileURL = ""
    @State private var errorMessage: String?
    @State private var isSaving = false

    private var canSave: Bool {
        !platform.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !username.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Platform", text: $platform)
                TextField("Username", text: $username)
                SecureField("Password", text: $password)
                TextField("Recovery codes", text: $recovery)
                TextField("Profile URL", text: $profileURL)
                if let errorMessage {
                    Text(errorMessage)
                        .foregroundStyle(.red)
                }
            }
            .navigationTitle("Add account")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                        .disabled(isSaving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "Saving…" : "Save") {
                        Task { await save() }
                    }
                    .disabled(!canSave || isSaving)
                }
            }
        }
        .frame(minWidth: 420, minHeight: 320)
    }

    private func save() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        do {
            _ = try await model.client.addSocialAccount(
                SocialAccountRequest(
                    platform: platform.trimmingCharacters(in: .whitespacesAndNewlines),
                    username: username.trimmingCharacters(in: .whitespacesAndNewlines),
                    password: password,
                    recoveryCodes: recovery,
                    profileURL: profileURL.trimmingCharacters(in: .whitespacesAndNewlines),
                    status: "connected"
                )
            )
            onSaved()
            dismiss()
        } catch {
            errorMessage = describe(error)
        }
    }
}

func describe(_ error: Error) -> String {
    (error as? APIError)?.message ?? error.localizedDescription
}
