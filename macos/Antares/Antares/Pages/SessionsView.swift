import AntaresKit
import SwiftUI

struct SessionsView: View {
    @EnvironmentObject private var model: AppModel
    var onOpenSession: () -> Void

    @State private var sessions: [SessionSummary] = []
    @State private var hits: [SessionSearchHit] = []
    @State private var query = ""
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var pendingDelete: SessionSummary?

    var body: some View {
        Group {
            if let errorMessage, sessions.isEmpty, hits.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Sessions",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && sessions.isEmpty && hits.isEmpty && query.isEmpty {
                ContentUnavailableView(
                    "No Sessions",
                    systemImage: "bubble.left.and.bubble.right",
                    description: Text("Conversations will appear here.")
                )
            } else {
                List {
                    if !hits.isEmpty {
                        Section("Content matches") {
                            ForEach(hits) { hit in
                                Button {
                                    open(hit.sessionID)
                                } label: {
                                    VStack(alignment: .leading, spacing: 4) {
                                        HStack {
                                            Text(hit.role)
                                                .font(.caption)
                                                .foregroundStyle(.secondary)
                                            Text(hit.sessionTitle.isEmpty ? hit.sessionID : hit.sessionTitle)
                                                .font(.headline)
                                                .lineLimit(1)
                                        }
                                        Text(hit.snippet)
                                            .font(.callout)
                                            .foregroundStyle(.secondary)
                                            .lineLimit(2)
                                    }
                                }
                                .buttonStyle(.plain)
                            }
                        }
                    }

                    Section("Sessions") {
                        ForEach(sessions) { session in
                            Button {
                                open(session.id)
                            } label: {
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(session.title.isEmpty ? session.id : session.title)
                                        .font(.headline)
                                        .lineLimit(1)
                                    HStack(spacing: 8) {
                                        if let modelName = session.model, !modelName.isEmpty {
                                            Text(modelName)
                                        }
                                        Text("\(session.messageCount) messages")
                                        if !session.updatedAt.isEmpty {
                                            Text(session.updatedAt)
                                        }
                                    }
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                                    .lineLimit(1)
                                }
                            }
                            .buttonStyle(.plain)
                            .swipeActions {
                                Button("Delete", role: .destructive) {
                                    pendingDelete = session
                                }
                            }
                            .contextMenu {
                                Button("Open") { open(session.id) }
                                Button("Delete", role: .destructive) {
                                    pendingDelete = session
                                }
                            }
                        }
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading sessions…") }
        }
        .navigationTitle("Sessions")
        .searchable(text: $query, prompt: "Search sessions")
        .onSubmit(of: .search) {
            Task { await load() }
        }
        .onChange(of: query) { _, newValue in
            if newValue.trimmingCharacters(in: .whitespacesAndNewlines).count < 2 {
                hits = []
            }
        }
        .task { await load() }
        .refreshable { await load() }
        .confirmationDialog(
            "Delete this session?",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Delete", role: .destructive) {
                if let session = pendingDelete {
                    Task { await delete(session) }
                }
                pendingDelete = nil
            }
            Button("Cancel", role: .cancel) {
                pendingDelete = nil
            }
        }
    }

    private func load() async {
        isLoading = sessions.isEmpty && hits.isEmpty
        errorMessage = nil
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            let list: SessionListResponse = try await model.client.get(
                "/api/sessions",
                query: [URLQueryItem(name: "limit", value: "100")]
            )
            sessions = list.sessions
            if q.count >= 2 {
                let search: SessionSearchResponse = try await model.client.get(
                    "/api/sessions/search",
                    query: [
                        URLQueryItem(name: "q", value: q),
                        URLQueryItem(name: "limit", value: "30"),
                    ]
                )
                hits = search.hits
            } else {
                hits = []
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func open(_ id: String) {
        UserDefaults.standard.set(id, forKey: ChatController.lastSessionKey)
        onOpenSession()
    }

    private func delete(_ session: SessionSummary) async {
        do {
            let _: DeletedFlagResponse = try await model.client.delete("/api/sessions/\(session.id)")
            if UserDefaults.standard.string(forKey: ChatController.lastSessionKey) == session.id {
                UserDefaults.standard.removeObject(forKey: ChatController.lastSessionKey)
            }
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
