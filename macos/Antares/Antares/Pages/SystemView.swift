import AntaresKit
import SwiftUI

struct SystemView: View {
    @EnvironmentObject private var model: AppModel

    @State private var stats: SystemStats?
    @State private var status: RuntimeStatus?
    @State private var errorMessage: String?
    @State private var isLoading = false

    var body: some View {
        Group {
            if let errorMessage, stats == nil, !isLoading {
                ContentUnavailableView(
                    "System",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if let stats {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        if let status {
                            statusStrip(status)
                        }
                        LazyVGrid(columns: Array(repeating: GridItem(.flexible()), count: 4), spacing: 12) {
                            stat("Uptime", formatUptime(stats.uptimeSeconds), hint: "v\(stats.version)")
                            stat(
                                "Tokens",
                                formatCount(stats.store.tokensIn + stats.store.tokensOut),
                                hint: String(format: "$%.4f", stats.store.cost)
                            )
                            stat("Sessions", formatCount(stats.store.sessions), hint: "\(formatCount(stats.store.messages)) messages")
                            stat("Database", formatBytes(stats.database.sizeBytes), hint: stats.database.driver)
                        }
                        card("Runtime") {
                            row("Go", "\(stats.goVersion) · \(stats.os)/\(stats.arch)")
                            row("Goroutines", "\(stats.goroutines)")
                            row("Heap", formatBytes(stats.memoryAlloc))
                            row("Sys memory", formatBytes(stats.memorySys))
                            row("CPU", "\(stats.cpuCount) cores")
                            row("Active shells", "\(stats.activeShells)")
                        }
                        card("Active config") {
                            row("Model", stats.model.isEmpty ? "—" : stats.model)
                            row("Provider", stats.provider.isEmpty ? "—" : stats.provider)
                            row("RAG", stats.ragProvider.isEmpty ? "inactive" : stats.ragProvider)
                            row("Memories", formatCount(stats.store.memories))
                            row("Workspace", stats.workspace, wrap: true)
                            row("Config path", stats.configPath, wrap: true)
                        }
                    }
                    .padding(16)
                }
            } else {
                Color.clear
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading system…") }
        }
        .navigationTitle("System")
        .task {
            await load()
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                await load(quiet: true)
            }
        }
        .refreshable { await load() }
    }

    private func statusStrip(_ status: RuntimeStatus) -> some View {
        HStack(spacing: 16) {
            Label(status.ok ? "Backend ok" : "Backend down", systemImage: status.ok ? "checkmark.circle.fill" : "xmark.circle.fill")
                .foregroundStyle(status.ok ? .green : .red)
            Text(status.providerReady ? "Provider ready" : "Needs setup")
                .foregroundStyle(status.providerReady ? Color.secondary : Color.orange)
            Text("\(status.activeSessions) active sessions")
                .foregroundStyle(.secondary)
            Spacer()
        }
        .font(.caption)
        .padding(12)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private func stat(_ label: String, _ value: String, hint: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(label.uppercased())
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title2.monospacedDigit().weight(.semibold))
            Text(hint)
                .font(.caption)
                .foregroundStyle(.secondary)
                .lineLimit(1)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private func card(_ title: String, @ViewBuilder content: () -> some View) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(.headline)
            content()
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private func row(_ label: String, _ value: String, wrap: Bool = false) -> some View {
        Group {
            if wrap {
                VStack(alignment: .leading, spacing: 2) {
                    Text(label).foregroundStyle(.secondary)
                    Text(value)
                        .font(.body.monospaced())
                        .textSelection(.enabled)
                }
                .font(.caption)
                .padding(.vertical, 4)
            } else {
                HStack {
                    Text(label).foregroundStyle(.secondary)
                    Spacer()
                    Text(value)
                        .fontWeight(.medium)
                        .lineLimit(1)
                }
                .font(.caption)
                .padding(.vertical, 4)
            }
        }
    }

    private func load(quiet: Bool = false) async {
        if !quiet { isLoading = stats == nil }
        errorMessage = nil
        do {
            async let statsLoad = model.client.systemStats()
            async let statusLoad = model.client.runtimeStatus()
            stats = try await statsLoad
            status = try await statusLoad
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func formatUptime(_ seconds: Int) -> String {
        let d = seconds / 86_400
        let h = (seconds % 86_400) / 3600
        let m = (seconds % 3600) / 60
        if d > 0 { return "\(d)d \(h)h" }
        if h > 0 { return "\(h)h \(m)m" }
        return "\(m)m"
    }

    private func formatCount(_ value: Int) -> String {
        value.formatted(.number.notation(.compactName))
    }

    private func formatBytes(_ size: Int) -> String {
        let value = Double(size)
        if value >= 1_073_741_824 { return String(format: "%.1f GB", value / 1_073_741_824) }
        if value >= 1_048_576 { return String(format: "%.1f MB", value / 1_048_576) }
        if value >= 1024 { return String(format: "%.1f KB", value / 1024) }
        return "\(size) B"
    }
}
