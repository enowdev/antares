import AntaresKit
import SwiftUI

struct SoulView: View {
    @EnvironmentObject private var model: AppModel

    @State private var text = ""
    @State private var saved = ""
    @State private var unset = false
    @State private var errorMessage: String?
    @State private var statusMessage: String?
    @State private var isLoading = false
    @State private var isSaving = false
    @State private var didLoad = false

    private var isDirty: Bool { text != saved }

    var body: some View {
        Group {
            if let errorMessage, !didLoad, !isLoading {
                ContentUnavailableView(
                    "Soul",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else {
                Form {
                    if let errorMessage, didLoad {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    if unset {
                        Section {
                            Text("Soul is still the unset default. Saving here, or chatting, will define the agent’s identity.")
                                .foregroundStyle(.secondary)
                        }
                    }

                    Section("SOUL.md") {
                        TextEditor(text: $text)
                            .font(.body.monospaced())
                            .frame(minHeight: 280)
                            .disabled(isLoading || isSaving)
                    }

                    if let statusMessage {
                        Section {
                            Text(statusMessage)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading soul…") }
        }
        .navigationTitle("Soul")
        .toolbar {
            ToolbarItem(placement: .cancellationAction) {
                Button("Reset") {
                    Task { await save("") }
                }
                .disabled(isSaving || isLoading)
            }
            ToolbarItem(placement: .primaryAction) {
                Button(isSaving ? "Saving…" : "Save") {
                    Task { await save(text) }
                }
                .disabled(!isDirty || isSaving || isLoading)
            }
        }
        .task { await load() }
    }

    private func load() async {
        isLoading = !didLoad
        errorMessage = nil
        do {
            let response: SoulResponse = try await model.client.get("/api/soul")
            text = response.soul
            saved = response.soul
            unset = response.unset
            didLoad = true
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func save(_ content: String) async {
        isSaving = true
        errorMessage = nil
        statusMessage = nil
        do {
            let response: SoulSaveResponse = try await model.client.post(
                "/api/soul",
                body: SoulRequest(soul: content)
            )
            text = content
            saved = content
            unset = response.unset
            statusMessage = content.isEmpty ? "Reset to the unset default." : "Saved."
            didLoad = true
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isSaving = false
    }
}
