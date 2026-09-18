import SwiftUI
import AntaresKit

struct SetupView: View {
    @EnvironmentObject private var model: AppModel

    @State private var status: SetupStatus?
    @State private var providerID = ""
    @State private var providerName = ""
    @State private var baseURL = ""
    @State private var apiKey = ""
    @State private var selectedModel = ""
    @State private var modelFilter = ""
    @State private var testedModels: [String] = []
    @State private var workspace = ""
    @State private var dbDriver = "sqlite"
    @State private var dbDSN = ""
    @State private var ragEnabled = false
    @State private var embedProvider = "voyage"
    @State private var embedModel = "voyage-4"
    @State private var embedAPIKey = ""
    @State private var telegramToken = ""
    @State private var dashboardPassword = ""
    @State private var passwordForGate = ""
    @State private var confirmPasswordForGate = ""
    @State private var errorMessage: String?
    @State private var noticeMessage: String?
    @State private var isLoading = true
    @State private var isTesting = false
    @State private var isSaving = false
    @State private var showsSetPassword = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if isLoading {
                    ProgressView("Loading setup...")
                        .frame(maxWidth: .infinity, minHeight: 260)
                } else if showsSetPassword {
                    setPasswordForm
                } else {
                    setupForm
                }
            }
            .padding(28)
        }
        .frame(minWidth: 560, minHeight: 620)
        .task {
            await loadStatus()
        }
    }

    private var setupForm: some View {
        Form {
            Section {
                Picker("Provider", selection: $providerID) {
                    ForEach(status?.providers ?? []) { provider in
                        Text(provider.label).tag(provider.id)
                    }
                }
                .onChange(of: providerID) { _, newValue in
                    selectedModel = ""
                    applyProviderDefaults(for: newValue)
                }

                if selectedProvider?.custom == true {
                    TextField("Provider Name", text: $providerName)
                }

                TextField("Base URL", text: $baseURL)
                    .textContentType(.URL)

                SecureField(selectedProvider?.keyLabel ?? "API Key", text: $apiKey)

                if let hint = selectedProvider?.hint, !hint.isEmpty {
                    Text(hint)
                        .font(.footnote)
                        .foregroundStyle(.secondary)
                }

                Button(isTesting ? "Testing..." : "Test Provider") {
                    testProvider()
                }
                .disabled(providerID.isEmpty || isTesting)
            } header: {
                Text("Provider")
                    .font(.title2)
                    .fontWeight(.semibold)
            }

            Section {
                if !modelChoices.isEmpty {
                    Picker("Suggested Model", selection: $selectedModel) {
                        ForEach(modelChoices, id: \.self) { model in
                            Text(model).tag(model)
                        }
                    }
                }

                TextField("Model", text: $selectedModel)
                    .font(.system(.body, design: .monospaced))

                if !modelChoices.isEmpty {
                    TextField("Filter Models", text: $modelFilter)
                }
            } header: {
                Text("Model")
            }

            Section {
                TextField("Workspace Path", text: $workspace)
                    .font(.system(.body, design: .monospaced))

                Picker("Database", selection: $dbDriver) {
                    Text("SQLite").tag("sqlite")
                    Text("Postgres").tag("postgres")
                }
                if dbDriver == "postgres" {
                    TextField("Postgres DSN", text: $dbDSN)
                        .font(.system(.body, design: .monospaced))
                }
            } header: {
                Text("Workspace")
            }

            Section {
                Toggle("Enable RAG", isOn: $ragEnabled)
                if ragEnabled {
                    TextField("Embedding Provider", text: $embedProvider)
                    TextField("Embedding Model", text: $embedModel)
                        .font(.system(.body, design: .monospaced))
                    SecureField("Embedding API Key", text: $embedAPIKey)
                }

                SecureField("Telegram Bot Token", text: $telegramToken)
                SecureField("Dashboard Password", text: $dashboardPassword)
            } header: {
                Text("Optional Extras")
            }

            if let noticeMessage {
                Text(noticeMessage)
                    .font(.footnote)
                    .foregroundStyle(.secondary)
            }

            if let errorMessage {
                Text(errorMessage)
                    .font(.footnote)
                    .foregroundStyle(.red)
            }

            Button(isSaving ? "Saving..." : "Complete Setup") {
                completeSetup()
            }
            .disabled(!canComplete || isSaving)
        }
    }

    private var setPasswordForm: some View {
        Form {
            Section {
                SecureField("New Password", text: $passwordForGate)
                    .textContentType(.newPassword)
                SecureField("Confirm Password", text: $confirmPasswordForGate)
                    .textContentType(.newPassword)

                if let errorMessage {
                    Text(errorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                }
            } header: {
                Text("Set a Dashboard Password")
                    .font(.title2)
                    .fontWeight(.semibold)
            } footer: {
                Text("The server requires a dashboard password before saving sensitive setup values.")
            }

            Button(isSaving ? "Saving..." : "Set Password") {
                setPasswordForGate()
            }
            .disabled(passwordForGate.isEmpty || passwordForGate != confirmPasswordForGate || isSaving)
        }
    }

    private var selectedProvider: SetupProvider? {
        status?.providers.first { $0.id == providerID }
    }

    private var modelChoices: [String] {
        let all = testedModels.isEmpty ? (selectedProvider?.models ?? []) : testedModels
        let query = modelFilter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !query.isEmpty else { return all }
        return all.filter { $0.lowercased().contains(query) }
    }

    private var canComplete: Bool {
        !providerID.isEmpty &&
            (!needsBaseURL || !effectiveBaseURL.isEmpty) &&
            !selectedModel.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty &&
            !workspace.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty &&
            (dbDriver != "postgres" || !dbDSN.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
    }

    private var needsBaseURL: Bool {
        selectedProvider?.custom == true || selectedProvider?.needsBaseURL == true
    }

    private func loadStatus() async {
        isLoading = true
        errorMessage = nil

        do {
            let loaded = try await model.setupStatus()
            status = loaded
            providerID = loaded.provider.isEmpty ? (loaded.providers.first?.id ?? "") : loaded.provider
            selectedModel = loaded.model
            workspace = loaded.workspace
            dbDriver = loaded.database.isEmpty ? "sqlite" : loaded.database
            applyProviderDefaults(for: providerID)
        } catch let error as APIError where error.isDashboardPasswordRequired {
            showsSetPassword = true
        } catch {
            errorMessage = error.localizedDescription
        }

        isLoading = false
    }

    private func applyProviderDefaults(for id: String) {
        guard let provider = status?.providers.first(where: { $0.id == id }) else { return }
        baseURL = provider.baseURL ?? ""
        testedModels = []
        if selectedModel.isEmpty {
            selectedModel = provider.models?.first ?? ""
        }
    }

    private func testProvider() {
        guard !providerID.isEmpty, !isTesting else { return }
        isTesting = true
        errorMessage = nil
        noticeMessage = nil

        Task {
            do {
                let response = try await model.testSetup(
                    SetupTestRequest(
                        provider: providerID,
                        baseURL: effectiveBaseURL,
                        apiKey: apiKey
                    )
                )
                if response.ok {
                    testedModels = response.models ?? []
                    noticeMessage = response.note ?? "Provider test succeeded."
                    if selectedModel.isEmpty {
                        selectedModel = response.suggested?.first ?? response.models?.first ?? ""
                    }
                } else {
                    errorMessage = response.error ?? "Provider test failed."
                }
            } catch let error as APIError where error.isDashboardPasswordRequired {
                showsSetPassword = true
            } catch {
                errorMessage = error.localizedDescription
            }
            isTesting = false
        }
    }

    private func completeSetup() {
        guard canComplete, !isSaving else { return }
        isSaving = true
        errorMessage = nil
        noticeMessage = nil

        Task {
            do {
                try await model.completeSetup(
                    CompleteSetupRequest(
                        provider: providerID,
                        name: providerName,
                        baseURL: effectiveBaseURL,
                        apiKey: apiKey,
                        model: selectedModel.trimmingCharacters(in: .whitespacesAndNewlines),
                        workspace: workspace.trimmingCharacters(in: .whitespacesAndNewlines),
                        database: SetupDatabaseRequest(driver: dbDriver, dsn: dbDriver == "postgres" ? dbDSN : ""),
                        rag: SetupRAGRequest(
                            enabled: ragEnabled,
                            embedProvider: embedProvider,
                            embedModel: embedModel,
                            embedAPIKey: embedAPIKey
                        ),
                        telegramToken: telegramToken,
                        dashboardPassword: dashboardPassword
                    )
                )
            } catch let error as APIError where error.isDashboardPasswordRequired {
                showsSetPassword = true
            } catch {
                errorMessage = error.localizedDescription
            }
            isSaving = false
        }
    }

    private func setPasswordForGate() {
        guard !passwordForGate.isEmpty, passwordForGate == confirmPasswordForGate, !isSaving else { return }
        isSaving = true
        errorMessage = nil

        Task {
            do {
                try await model.setDashboardPassword(passwordForGate)
                try await model.login(password: passwordForGate)
                showsSetPassword = false
            } catch {
                errorMessage = error.localizedDescription
            }
            isSaving = false
        }
    }

    private var effectiveBaseURL: String {
        let trimmed = baseURL.trimmingCharacters(in: .whitespacesAndNewlines)
        return trimmed.isEmpty ? (selectedProvider?.baseURL ?? "") : trimmed
    }
}
