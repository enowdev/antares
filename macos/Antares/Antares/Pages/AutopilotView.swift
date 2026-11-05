import AntaresKit
import SwiftUI

struct AutopilotView: View {
    @EnvironmentObject private var model: AppModel

    @State private var cards: [AutopilotCard] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var isRunning = false
    @State private var showingAdd = false
    @State private var detail: AutopilotCard?
    @State private var statusFilter = ""
    @State private var draftTitle = ""
    @State private var draftPrompt = ""
    @State private var isSaving = false
    @State private var busyID: String?

    private static let statuses = ["pending", "running", "verified", "merged", "failed"]

    private var pendingCount: Int {
        cards.filter { $0.status == "pending" }.count
    }

    private var hasRunning: Bool {
        cards.contains { $0.status == "running" }
    }

    private var shown: [AutopilotCard] {
        statusFilter.isEmpty ? cards : cards.filter { $0.status == statusFilter }
    }

    var body: some View {
        Group {
            if let errorMessage, cards.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Autopilot",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && cards.isEmpty {
                ContentUnavailableView(
                    "No Cards",
                    systemImage: "airplane",
                    description: Text("Add a card and run Autopilot to work through the queue unattended.")
                )
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        if let errorMessage {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }

                        if !cards.isEmpty {
                            filterChips
                        }

                        LazyVGrid(
                            columns: [GridItem(.adaptive(minimum: 240), spacing: 12)],
                            spacing: 12
                        ) {
                            ForEach(shown) { card in
                                cardCell(card)
                            }
                        }
                    }
                    .padding(16)
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading autopilot…") }
        }
        .navigationTitle("Autopilot")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button {
                    Task { await run() }
                } label: {
                    Label(
                        pendingCount > 0 ? "Run (\(pendingCount))" : "Run",
                        systemImage: "play.fill"
                    )
                }
                .disabled(pendingCount == 0 || isRunning)
            }
            ToolbarItem(placement: .primaryAction) {
                Button("Add") {
                    draftTitle = ""
                    draftPrompt = ""
                    showingAdd = true
                }
            }
        }
        .sheet(isPresented: $showingAdd) { addSheet }
        .sheet(item: $detail) { card in
            detailSheet(card)
        }
        .task { await load() }
        .task(id: hasRunning) {
            guard hasRunning else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .milliseconds(2500))
                await load(quiet: true)
            }
        }
        .refreshable { await load() }
    }

    private var filterChips: some View {
        HStack(spacing: 8) {
            chip("All \(cards.count)", selected: statusFilter.isEmpty) {
                statusFilter = ""
            }
            ForEach(Self.statuses, id: \.self) { status in
                let count = cards.filter { $0.status == status }.count
                if count > 0 {
                    chip("\(label(for: status)) \(count)", selected: statusFilter == status) {
                        statusFilter = status
                    }
                }
            }
            Spacer()
        }
    }

    private func chip(_ title: String, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title)
                .font(.caption)
                .padding(.horizontal, 10)
                .padding(.vertical, 5)
                .background(selected ? Color.accentColor.opacity(0.15) : Color.clear, in: Capsule())
                .overlay(Capsule().stroke(selected ? Color.accentColor : Color.secondary.opacity(0.35)))
        }
        .buttonStyle(.plain)
    }

    private func cardCell(_ card: AutopilotCard) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Button {
                detail = card
            } label: {
                VStack(alignment: .leading, spacing: 6) {
                    HStack(alignment: .top) {
                        Text(card.title)
                            .font(.headline)
                            .lineLimit(1)
                        Spacer()
                        Text(label(for: card.status))
                            .font(.caption)
                            .foregroundStyle(color(for: card.status))
                    }
                    Text(card.prompt)
                        .font(.callout)
                        .foregroundStyle(.secondary)
                        .lineLimit(2)
                    if let error = card.error, !error.isEmpty {
                        Text(error)
                            .font(.caption)
                            .foregroundStyle(.red)
                            .lineLimit(2)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .buttonStyle(.plain)

            HStack {
                if let pr = card.pr, let url = URL(string: pr) {
                    Link("PR", destination: url)
                        .font(.caption)
                }
                Spacer()
                if !card.updatedAt.isEmpty {
                    Text(card.updatedAt)
                        .font(.caption2)
                        .foregroundStyle(.secondary)
                }
                if card.status == "pending" || card.status == "failed" {
                    Button("Remove", role: .destructive) {
                        Task { await remove(card) }
                    }
                    .disabled(busyID != nil)
                }
            }
        }
        .padding(12)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private var addSheet: some View {
        NavigationStack {
            Form {
                TextField("Title", text: $draftTitle)
                TextEditor(text: $draftPrompt)
                    .frame(minHeight: 120)
                if let errorMessage {
                    Text(errorMessage)
                        .foregroundStyle(.red)
                }
            }
            .navigationTitle("Add card")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { showingAdd = false }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "Adding…" : "Add") {
                        Task { await add() }
                    }
                    .disabled(!canAdd || isSaving)
                }
            }
        }
        .frame(minWidth: 420, minHeight: 320)
    }

    private func detailSheet(_ card: AutopilotCard) -> some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack {
                        Text(label(for: card.status))
                            .foregroundStyle(color(for: card.status))
                        Spacer()
                        Text(card.id)
                            .font(.caption)
                            .fontDesign(.monospaced)
                            .foregroundStyle(.secondary)
                    }
                    block("Prompt", card.prompt)
                    block("Error", card.error)
                    block("Result", card.result)
                    block("Verify output", card.verifyOutput, mono: true)
                    if let branch = card.branch, !branch.isEmpty {
                        LabeledContent("Branch") {
                            Text(branch).fontDesign(.monospaced)
                        }
                    }
                    if let pr = card.pr, let url = URL(string: pr) {
                        Link(pr, destination: url)
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(16)
            }
            .navigationTitle(card.title)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { detail = nil }
                }
            }
        }
        .frame(minWidth: 520, minHeight: 400)
    }

    @ViewBuilder
    private func block(_ title: String, _ value: String?, mono: Bool = false) -> some View {
        if let value, !value.isEmpty {
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Text(value)
                    .font(mono ? .caption.monospaced() : .body)
                    .textSelection(.enabled)
            }
        }
    }

    private var canAdd: Bool {
        !draftTitle.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && !draftPrompt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    private func label(for status: String) -> String {
        switch status {
        case "pending": "Pending"
        case "running": "Running"
        case "verified": "Verified"
        case "merged": "Merged"
        case "failed": "Failed"
        default: status
        }
    }

    private func color(for status: String) -> Color {
        switch status {
        case "running": .orange
        case "verified", "merged": .green
        case "failed": .red
        default: .secondary
        }
    }

    private func load(quiet: Bool = false) async {
        if !quiet {
            isLoading = cards.isEmpty
            errorMessage = nil
        }
        do {
            let response = try await model.client.listAutopilotCards()
            cards = response.cards
            if let current = detail, let updated = cards.first(where: { $0.id == current.id }) {
                detail = updated
            }
            if !quiet { errorMessage = nil }
        } catch {
            if !quiet || cards.isEmpty {
                errorMessage = (error as? APIError)?.message ?? error.localizedDescription
            }
        }
        isLoading = false
    }

    private func add() async {
        let title = draftTitle.trimmingCharacters(in: .whitespacesAndNewlines)
        let prompt = draftPrompt.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty, !prompt.isEmpty else { return }
        isSaving = true
        errorMessage = nil
        do {
            _ = try await model.client.addAutopilotCard(title: title, prompt: prompt)
            showingAdd = false
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isSaving = false
    }

    private func run() async {
        isRunning = true
        errorMessage = nil
        do {
            _ = try await model.client.runAutopilot()
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isRunning = false
    }

    private func remove(_ card: AutopilotCard) async {
        busyID = card.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            _ = try await model.client.removeAutopilotCard(card.id)
            if detail?.id == card.id { detail = nil }
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
