import AntaresKit
import SwiftUI

struct BoardView: View {
    @EnvironmentObject private var model: AppModel

    @State private var sessions: [BoardSession] = []
    @State private var selectedID = ""
    @State private var columns: [BoardColumn] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busyID: String?

    private var active: BoardSession? {
        sessions.first { $0.id == selectedID } ?? sessions.first
    }

    private var totalCards: Int {
        columns.reduce(0) { $0 + $1.cards.count }
    }

    private var doneCount: Int {
        columns.first { $0.name == "done" }?.cards.count ?? 0
    }

    var body: some View {
        Group {
            if let errorMessage, sessions.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Board",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && sessions.isEmpty {
                ContentUnavailableView(
                    "No Boards",
                    systemImage: "rectangle.split.3x1",
                    description: Text("Session boards appear when the agent lays out work as cards.")
                )
            } else {
                VStack(alignment: .leading, spacing: 12) {
                    header
                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                            .padding(.horizontal, 16)
                    }
                    if totalCards == 0 {
                        ContentUnavailableView(
                            "No Cards",
                            systemImage: "rectangle.split.3x1",
                            description: Text("This session has a board, but no cards yet.")
                        )
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                    } else {
                        boardColumns
                    }
                }
                .padding(.top, 12)
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading board…") }
        }
        .navigationTitle("Board")
        .task { await loadSessions() }
        .task(id: selectedID) {
            guard !selectedID.isEmpty else { return }
            await loadBoard()
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(2500))
                await loadBoard(quiet: true)
            }
        }
        .refreshable { await loadSessions() }
    }

    private var header: some View {
        HStack(spacing: 12) {
            Picker("Session", selection: $selectedID) {
                ForEach(sessions) { session in
                    Text(session.title.isEmpty ? session.id : session.title).tag(session.id)
                }
            }
            .labelsHidden()
            .frame(minWidth: 180)

            if totalCards > 0 {
                Text("\(doneCount) of \(totalCards) done")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }

            Spacer()

            Button("Clear", role: .destructive) {
                Task { await clear() }
            }
            .disabled(active == nil || totalCards == 0 || busyID != nil)
        }
        .padding(.horizontal, 16)
    }

    private var boardColumns: some View {
        ScrollView(.horizontal) {
            HStack(alignment: .top, spacing: 12) {
                ForEach(columns) { column in
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Circle()
                                .fill(dotColor(column.name))
                                .frame(width: 8, height: 8)
                            Text(title(for: column.name))
                                .font(.caption)
                                .fontWeight(.semibold)
                                .textCase(.uppercase)
                            Spacer()
                            Text("\(column.cards.count)")
                                .font(.caption2)
                                .foregroundStyle(.secondary)
                        }
                        .padding(.horizontal, 4)

                        ScrollView {
                            LazyVStack(alignment: .leading, spacing: 8) {
                                if column.cards.isEmpty {
                                    Text("Empty")
                                        .font(.caption)
                                        .foregroundStyle(.secondary)
                                        .frame(maxWidth: .infinity)
                                        .padding(.vertical, 24)
                                        .overlay(
                                            RoundedRectangle(cornerRadius: 8)
                                                .stroke(style: StrokeStyle(lineWidth: 1, dash: [4]))
                                                .foregroundStyle(.secondary.opacity(0.4))
                                        )
                                } else {
                                    ForEach(column.cards) { card in
                                        boardCard(card, done: column.name == "done")
                                    }
                                }
                            }
                            .padding(4)
                        }
                    }
                    .frame(width: 240)
                    .padding(10)
                    .background(Color.secondary.opacity(0.08), in: RoundedRectangle(cornerRadius: 12))
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 16)
        }
    }

    private func boardCard(_ card: BoardCard, done: Bool) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(alignment: .top) {
                Text(card.title)
                    .font(.callout)
                    .fontWeight(.medium)
                    .strikethrough(done)
                    .foregroundStyle(done ? .secondary : .primary)
                Spacer()
                Button {
                    Task { await remove(card) }
                } label: {
                    Image(systemName: "xmark")
                        .font(.caption2)
                }
                .buttonStyle(.borderless)
                .disabled(busyID != nil)
                .help("Delete")
            }
            if let note = card.note, !note.isEmpty {
                Text(note)
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background, in: RoundedRectangle(cornerRadius: 8))
        .overlay(
            RoundedRectangle(cornerRadius: 8)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private func title(for name: String) -> String {
        switch name {
        case "todo": "To do"
        case "doing": "Doing"
        case "done": "Done"
        default: name
        }
    }

    private func dotColor(_ name: String) -> Color {
        switch name {
        case "doing": .accentColor
        case "done": .green
        default: .secondary
        }
    }

    private func loadSessions() async {
        isLoading = sessions.isEmpty
        errorMessage = nil
        do {
            let response = try await model.client.listBoardSessions()
            sessions = response.sessions
            if selectedID.isEmpty || !sessions.contains(where: { $0.id == selectedID }) {
                selectedID = sessions.first?.id ?? ""
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func loadBoard(quiet: Bool = false) async {
        guard !selectedID.isEmpty else {
            columns = []
            return
        }
        do {
            let snapshot = try await model.client.board(sessionID: selectedID)
            columns = snapshot.columns
            if !quiet { errorMessage = nil }
        } catch {
            if !quiet || columns.isEmpty {
                errorMessage = (error as? APIError)?.message ?? error.localizedDescription
            }
        }
    }

    private func remove(_ card: BoardCard) async {
        guard let active else { return }
        busyID = card.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            _ = try await model.client.deleteBoardCard(sessionID: active.id, id: card.id)
            await loadBoard()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func clear() async {
        guard let active else { return }
        busyID = active.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            _ = try await model.client.clearBoard(sessionID: active.id)
            columns = []
            await loadSessions()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
