import AntaresKit
import SwiftUI

struct LogsView: View {
    @EnvironmentObject private var model: AppModel

    private static let levels = ["ALL", "DEBUG", "INFO", "WARN", "ERROR"]

    @State private var rows: [LogRow] = []
    @State private var level = "ALL"
    @State private var filter = ""
    @State private var live = true
    @State private var errorMessage: String?
    @State private var isLoading = false

    private var visible: [LogRow] {
        let q = filter.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        return rows.filter { row in
            let matchesLevel = level == "ALL" || row.entry.level.caseInsensitiveCompare(level) == .orderedSame
            let matchesFilter = q.isEmpty || row.entry.message.lowercased().contains(q)
            return matchesLevel && matchesFilter
        }
    }

    var body: some View {
        Group {
            if let errorMessage, rows.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Logs",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && visible.isEmpty {
                ContentUnavailableView(
                    "No Logs",
                    systemImage: "terminal",
                    description: Text(filter.isEmpty ? "Waiting for log lines." : "Nothing matched the filter.")
                )
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                            .padding(.horizontal, 16)
                            .padding(.top, 8)
                    }
                    ScrollViewReader { proxy in
                        ScrollView {
                            LazyVStack(alignment: .leading, spacing: 2) {
                                ForEach(visible) { row in
                                    logLine(row.entry)
                                        .id(row.id)
                                }
                            }
                            .padding(12)
                        }
                        .onChange(of: rows.count) { _, _ in
                            guard live, let last = visible.last else { return }
                            proxy.scrollTo(last.id, anchor: .bottom)
                        }
                    }
                    .font(.system(.caption, design: .monospaced))
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading logs…") }
        }
        .navigationTitle("Logs")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Picker("Level", selection: $level) {
                    ForEach(Self.levels, id: \.self) { item in
                        Text(item).tag(item)
                    }
                }
                .pickerStyle(.segmented)
                .frame(maxWidth: 420)
            }
            ToolbarItem(placement: .automatic) {
                TextField("Filter", text: $filter)
                    .frame(width: 180)
            }
            ToolbarItem(placement: .automatic) {
                Button(live ? "Pause" : "Resume") {
                    live.toggle()
                }
            }
            ToolbarItem(placement: .automatic) {
                Button("Clear") { rows = [] }
            }
        }
        .task(id: "\(level)|\(live)") {
            await loadSnapshot()
            guard live else { return }
            do {
                try await model.client.streamLogs { entry in
                    let row = LogRow(entry: entry)
                    await MainActor.run {
                        rows.append(row)
                        if rows.count > 2000 {
                            rows.removeFirst(rows.count - 2000)
                        }
                    }
                }
            } catch is CancellationError {
                return
            } catch {
                if !Task.isCancelled {
                    errorMessage = (error as? APIError)?.message ?? error.localizedDescription
                }
            }
        }
        .refreshable { await loadSnapshot() }
    }

    private func logLine(_ entry: LogEntry) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Circle()
                .fill(levelColor(entry.level))
                .frame(width: 6, height: 6)
            Text(timeLabel(entry.time))
                .foregroundStyle(.secondary)
            Text(entry.level)
                .foregroundStyle(levelColor(entry.level))
                .frame(width: 48, alignment: .leading)
            Text(entry.message)
            if let attrs = entry.attrs, !attrs.isEmpty {
                Text(attrs.map { "\($0.key)=\($0.value.displayString)" }.sorted().joined(separator: " "))
                    .foregroundStyle(.secondary)
            }
        }
        .textSelection(.enabled)
    }

    private func loadSnapshot() async {
        isLoading = rows.isEmpty
        errorMessage = nil
        do {
            let list = try await model.client.listLogs(limit: 500, level: level)
            rows = list.entries.map { LogRow(entry: $0) }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func levelColor(_ level: String) -> Color {
        switch level.uppercased() {
        case "ERROR": .red
        case "WARN": .orange
        case "INFO": .green
        default: .secondary
        }
    }

    private func timeLabel(_ value: String) -> String {
        let prefix = value.dropFirst(11).prefix(8)
        return prefix.isEmpty ? value : String(prefix)
    }
}

private struct LogRow: Identifiable {
    let id = UUID()
    let entry: LogEntry
}
