import AntaresKit
import SwiftUI

struct MemoryView: View {
    @EnvironmentObject private var model: AppModel

    @State private var memories: [MemoryItem] = []
    @State private var query = ""
    @State private var isSearching = false
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var showingEditor = false
    @State private var draftKey = ""
    @State private var draftContent = ""
    @State private var draftScope = "global"
    @State private var isSaving = false
    @State private var pendingDelete: MemoryItem?

    var body: some View {
        Group {
            if let errorMessage, memories.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Memory",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && memories.isEmpty {
                ContentUnavailableView(
                    isSearching ? "No Matches" : "No Memories",
                    systemImage: "brain",
                    description: Text(isSearching ? "Try a different search." : "Add a memory to persist facts across sessions.")
                )
            } else {
                List {
                    ForEach(memories) { item in
                        VStack(alignment: .leading, spacing: 6) {
                            HStack {
                                Text(item.scope)
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                if !item.key.isEmpty {
                                    Text(item.key)
                                        .font(.subheadline)
                                        .fontDesign(.monospaced)
                                        .lineLimit(1)
                                }
                                Spacer()
                                Button("Delete", role: .destructive) {
                                    pendingDelete = item
                                }
                            }
                            Text(item.content)
                                .font(.body)
                            if !item.updatedAt.isEmpty {
                                Text(item.updatedAt)
                                    .font(.caption2)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        .swipeActions {
                            Button("Delete", role: .destructive) {
                                pendingDelete = item
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading memory…") }
        }
        .navigationTitle("Memory")
        .searchable(text: $query, prompt: "Search memory")
        .onSubmit(of: .search) {
            Task { await search() }
        }
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Add") {
                    draftKey = ""
                    draftContent = ""
                    draftScope = "global"
                    showingEditor = true
                }
            }
        }
        .task { await load() }
        .refreshable { await load() }
        .sheet(isPresented: $showingEditor) {
            NavigationStack {
                Form {
                    TextField("Key (optional)", text: $draftKey)
                    TextField("Scope", text: $draftScope)
                    TextEditor(text: $draftContent)
                        .frame(minHeight: 120)
                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                    }
                }
                .navigationTitle("Add Memory")
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button("Cancel") { showingEditor = false }
                    }
                    ToolbarItem(placement: .confirmationAction) {
                        Button(isSaving ? "Saving…" : "Save") {
                            Task { await save() }
                        }
                        .disabled(draftContent.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || isSaving)
                    }
                }
            }
            .frame(minWidth: 420, minHeight: 320)
        }
        .confirmationDialog(
            "Delete this memory?",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                if let item = pendingDelete {
                    Task { await delete(item) }
                }
                pendingDelete = nil
            }
            Button("Cancel", role: .cancel) {
                pendingDelete = nil
            }
        }
    }

    private func load() async {
        isLoading = memories.isEmpty
        isSearching = false
        errorMessage = nil
        do {
            let list: MemoryListResponse = try await model.client.get("/api/memory")
            memories = list.memories
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func search() async {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        if q.isEmpty {
            await load()
            return
        }
        isLoading = memories.isEmpty
        errorMessage = nil
        do {
            let list: MemoryListResponse = try await model.client.get(
                "/api/memory/search",
                query: [
                    URLQueryItem(name: "q", value: q),
                    URLQueryItem(name: "limit", value: "30"),
                ]
            )
            memories = list.memories
            isSearching = true
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func save() async {
        let content = draftContent.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !content.isEmpty else { return }
        isSaving = true
        errorMessage = nil
        do {
            let _: MemoryItem = try await model.client.post(
                "/api/memory",
                body: PutMemoryRequest(
                    id: "",
                    key: draftKey,
                    content: content,
                    scope: draftScope.isEmpty ? "global" : draftScope
                )
            )
            showingEditor = false
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isSaving = false
    }

    private func delete(_ item: MemoryItem) async {
        do {
            let _: DeletedFlagResponse = try await model.client.delete("/api/memory/\(item.id)")
            memories.removeAll { $0.id == item.id }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
