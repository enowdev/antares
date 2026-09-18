import AntaresKit
import SwiftUI

struct ProvidersView: View {
    @EnvironmentObject private var model: AppModel

    @State private var options: ModelOptionsResponse?
    @State private var models: [ProviderModel] = []
    @State private var providerErrors: [ProviderModelError] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var savingID: String?

    var body: some View {
        Group {
            if let errorMessage, options == nil, models.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Providers",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else {
                List {
                    if let active = options?.active {
                        Section("Active") {
                            LabeledContent("Model", value: active.model.isEmpty ? "—" : active.model)
                            LabeledContent("Provider", value: active.provider.isEmpty ? "—" : active.provider)
                        }
                    }

                    if !models.isEmpty {
                        Section("Models") {
                            ForEach(models, id: \.stableID) { item in
                                HStack(alignment: .top) {
                                    VStack(alignment: .leading, spacing: 4) {
                                        Text(item.name.isEmpty ? item.id : item.name)
                                            .font(.headline)
                                        Text("\(item.providerLabel) · \(item.id)")
                                            .font(.caption)
                                            .foregroundStyle(.secondary)
                                        HStack(spacing: 8) {
                                            if item.vision { Text("Vision") }
                                            if item.tools { Text("Tools") }
                                            if item.reasoning { Text("Reasoning") }
                                            if item.contextWindow > 0 {
                                                Text("\(item.contextWindow) ctx")
                                            }
                                        }
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                    }
                                    Spacer()
                                    Button(isActive(item) ? "Default" : "Set default") {
                                        Task { await setDefault(item) }
                                    }
                                    .disabled(savingID != nil || isActive(item))
                                }
                            }
                        }
                    }

                    if let providers = options?.providers, !providers.isEmpty {
                        Section("Providers") {
                            ForEach(providers) { provider in
                                VStack(alignment: .leading, spacing: 4) {
                                    HStack {
                                        Text(provider.label)
                                            .font(.headline)
                                        Spacer()
                                        if provider.active {
                                            Text("Active")
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                        }
                                    }
                                    Text(provider.baseURL.isEmpty ? provider.kind : provider.baseURL)
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .lineLimit(1)
                                    Text(providerStatus(provider))
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                    }

                    if let errorMessage, options != nil {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    if !providerErrors.isEmpty {
                        Section("Provider errors") {
                            ForEach(providerErrors, id: \.provider) { item in
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(item.label)
                                        .font(.headline)
                                    Text(item.error)
                                        .font(.caption)
                                        .foregroundStyle(.red)
                                }
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading models…") }
        }
        .navigationTitle("Providers")
        .task { await load() }
        .refreshable { await load() }
    }

    private func isActive(_ item: ProviderModel) -> Bool {
        guard let active = options?.active else { return false }
        return active.model == item.id && active.provider == item.provider
    }

    private func providerStatus(_ provider: ModelProvider) -> String {
        if provider.hasKey { return "Connected" }
        if provider.custom == true, let headers = provider.headers, !headers.isEmpty {
            return "Connected"
        }
        if provider.local { return "Local" }
        return "Needs key"
    }

    private func load() async {
        isLoading = options == nil && models.isEmpty
        errorMessage = nil
        do {
            let loaded: ModelOptionsResponse = try await model.client.get("/api/model/options")
            options = loaded
            do {
                let all: ModelListAllResponse = try await model.client.get("/api/model/list-all")
                models = all.models
                providerErrors = all.errors
                options = ModelOptionsResponse(active: all.active, providers: loaded.providers)
            } catch {
                models = []
                providerErrors = []
                errorMessage = (error as? APIError)?.message ?? error.localizedDescription
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func setDefault(_ item: ProviderModel) async {
        savingID = item.stableID
        defer { savingID = nil }
        do {
            let _: ModelSetResponse = try await model.client.post(
                "/api/model/set",
                body: ModelSetRequest(model: item.id, provider: item.provider)
            )
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
