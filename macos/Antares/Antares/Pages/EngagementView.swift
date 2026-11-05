import AntaresKit
import AppKit
import SwiftUI

struct EngagementView: View {
    @EnvironmentObject private var model: AppModel

    @State private var sessions: [EngagementSession] = []
    @State private var selectedID = ""
    @State private var snapshot: EngagementSnapshot?
    @State private var rawReport: String?
    @State private var tab = EngagementTab.overview
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var isDeleting = false
    @State private var pendingDelete: EngagementSession?
    @State private var copied = false

    private var active: EngagementSession? {
        sessions.first { $0.id == selectedID } ?? sessions.first
    }

    var body: some View {
        Group {
            if let errorMessage, sessions.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Engagement",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && sessions.isEmpty {
                ContentUnavailableView(
                    "No Engagements",
                    systemImage: "checkmark.shield",
                    description: Text("Security findings and intel from chat sessions will appear here.")
                )
            } else {
                List {
                    if let errorMessage {
                        Section {
                            Text(errorMessage)
                                .foregroundStyle(.red)
                        }
                    }

                    Section {
                        Picker("Session", selection: $selectedID) {
                            ForEach(sessions) { session in
                                Text(sessionLabel(session)).tag(session.id)
                            }
                        }
                        Picker("View", selection: $tab) {
                            Text("Overview").tag(EngagementTab.overview)
                            Text("Raw").tag(EngagementTab.raw)
                        }
                        .pickerStyle(.segmented)
                    }

                    if tab == .raw {
                        rawSection
                    } else {
                        overviewSections
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading engagement…") }
        }
        .navigationTitle("Engagement")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Report") {
                    Task { await downloadReport() }
                }
                .disabled(active == nil)
            }
            ToolbarItem(placement: .primaryAction) {
                Button("Delete", role: .destructive) {
                    pendingDelete = active
                }
                .disabled(active == nil || isDeleting)
            }
        }
        .task { await loadSessions() }
        .task(id: selectedID) {
            guard !selectedID.isEmpty else { return }
            await loadSnapshot()
            if tab == .raw {
                await loadReport()
            }
        }
        .onChange(of: tab) { _, newValue in
            if newValue == .raw {
                Task { await loadReport() }
            }
        }
        .refreshable { await loadSessions() }
        .confirmationDialog(
            "Delete this engagement?",
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
        } message: {
            Text("Clears findings and intel for \(pendingDelete?.title.isEmpty == false ? pendingDelete?.title ?? "" : pendingDelete?.id ?? ""). The chat session stays.")
        }
    }

    @ViewBuilder
    private var overviewSections: some View {
        Section("Methodology") {
            if let snapshot, !snapshot.phases.isEmpty {
                ForEach(snapshot.phases) { phase in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(phaseMark(phase.status))
                                .foregroundStyle(phaseColor(phase.status))
                            Text(phase.title.isEmpty ? phase.name : phase.title)
                                .font(.headline)
                            if phase.evidence > 0 {
                                Text("\(phase.evidence) recorded")
                                    .font(.caption)
                                    .foregroundStyle(.secondary)
                            }
                        }
                        if !phase.summary.isEmpty {
                            Text(phase.summary)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                    }
                }
            } else {
                Text("No phases yet.")
                    .foregroundStyle(.secondary)
            }
            if let next = snapshot?.next, !next.isEmpty {
                Text("→ \(next)")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }

        Section("Coverage") {
            let percent = snapshot?.coveragePercent ?? 0
            ProgressView(value: Double(percent), total: 100) {
                Text("\(percent)%")
            }
            if let coverage = snapshot?.coverage, !coverage.isEmpty {
                ForEach(coverage) { area in
                    HStack {
                        Image(systemName: area.covered ? "checkmark.square.fill" : "square")
                            .foregroundStyle(area.covered ? .green : .secondary)
                        Text(area.title.isEmpty ? area.name : area.title)
                            .foregroundStyle(area.covered ? .primary : .secondary)
                    }
                }
            }
        }

        if let chains = snapshot?.chains, !chains.isEmpty {
            Section("Chains") {
                ForEach(chains) { chain in
                    VStack(alignment: .leading, spacing: 4) {
                        Text(chain.name)
                            .font(.headline)
                            .foregroundStyle(.red)
                        Text(chain.impact)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                }
            }
        }

        Section("Findings (\(sortedFindings.count))") {
            if sortedFindings.isEmpty {
                Text("No findings recorded.")
                    .foregroundStyle(.secondary)
            } else {
                ForEach(sortedFindings) { finding in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack {
                            Text(finding.severity)
                                .font(.caption)
                                .foregroundStyle(severityColor(finding.severity))
                            Text(finding.title)
                                .font(.headline)
                        }
                        HStack(spacing: 8) {
                            if let cwe = finding.cwe, !cwe.isEmpty {
                                Text(cwe)
                            }
                            if let target = finding.target, !target.isEmpty {
                                Text(target)
                                    .fontDesign(.monospaced)
                            }
                            if let status = finding.status, !status.isEmpty, status != "new", status != "confirmed" {
                                Text(status)
                            }
                        }
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    }
                }
            }
        }

        if let intel = snapshot?.intel, !intel.isEmpty {
            Section("Intel (\(intel.count))") {
                ForEach(intel) { item in
                    HStack(alignment: .top) {
                        Text(item.type)
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        Text(item.value)
                            .fontDesign(.monospaced)
                            .lineLimit(1)
                        if let detail = item.detail, !detail.isEmpty {
                            Text(detail)
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                    }
                    .font(.callout)
                }
            }
        }
    }

    @ViewBuilder
    private var rawSection: some View {
        Section {
            HStack {
                Text("Raw report")
                Spacer()
                Button(copied ? "Copied" : "Copy") {
                    copyReport()
                }
                .disabled(rawReport == nil || rawReport?.isEmpty == true)
            }
            if let rawReport {
                Text(rawReport)
                    .font(.callout)
                    .fontDesign(.monospaced)
                    .textSelection(.enabled)
            } else {
                Text("Loading report…")
                    .foregroundStyle(.secondary)
            }
        }
    }

    private var sortedFindings: [EngagementFinding] {
        let order = ["critical": 0, "high": 1, "medium": 2, "low": 3, "info": 4]
        return (snapshot?.findings ?? []).sorted {
            (order[$0.severity] ?? 9) < (order[$1.severity] ?? 9)
        }
    }

    private func sessionLabel(_ session: EngagementSession) -> String {
        let title = session.title.isEmpty ? session.id : session.title
        return "\(title) · \(session.findings)✚ / \(session.intel)◆"
    }

    private func phaseMark(_ status: String) -> String {
        switch status {
        case "complete": "●"
        case "in_progress": "◐"
        case "blocked": "▲"
        default: "○"
        }
    }

    private func phaseColor(_ status: String) -> Color {
        switch status {
        case "complete": .green
        case "in_progress": .accentColor
        case "blocked": .orange
        default: .secondary
        }
    }

    private func severityColor(_ severity: String) -> Color {
        switch severity {
        case "critical", "high": .red
        case "medium": .orange
        case "low": .secondary
        default: .secondary
        }
    }

    private func loadSessions() async {
        isLoading = sessions.isEmpty
        errorMessage = nil
        do {
            let response = try await model.client.listEngagementSessions()
            sessions = response.sessions
            if selectedID.isEmpty || !sessions.contains(where: { $0.id == selectedID }) {
                selectedID = sessions.first?.id ?? ""
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func loadSnapshot() async {
        guard !selectedID.isEmpty else {
            snapshot = nil
            return
        }
        do {
            snapshot = try await model.client.engagement(sessionID: selectedID)
            errorMessage = nil
        } catch {
            snapshot = nil
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func loadReport() async {
        guard let active else {
            rawReport = nil
            return
        }
        rawReport = nil
        do {
            rawReport = try await model.client.engagementReport(
                sessionID: active.id,
                title: active.title.isEmpty ? "Security Assessment" : active.title
            )
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func copyReport() {
        guard let rawReport, !rawReport.isEmpty else { return }
        NSPasteboard.general.clearContents()
        NSPasteboard.general.setString(rawReport, forType: .string)
        copied = true
        Task {
            try? await Task.sleep(for: .seconds(1.5))
            copied = false
        }
    }

    private func downloadReport() async {
        guard let active else { return }
        do {
            let markdown = try await model.client.engagementReport(
                sessionID: active.id,
                title: active.title.isEmpty ? "Security Assessment" : active.title
            )
            let url = FileManager.default.temporaryDirectory
                .appendingPathComponent("report-\(active.id).md")
            try Data(markdown.utf8).write(to: url)
            NSWorkspace.shared.activateFileViewerSelecting([url])
            errorMessage = nil
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func delete(_ session: EngagementSession) async {
        isDeleting = true
        defer { isDeleting = false }
        errorMessage = nil
        do {
            _ = try await model.client.deleteEngagement(sessionID: session.id)
            if selectedID == session.id { selectedID = "" }
            snapshot = nil
            rawReport = nil
            await loadSessions()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}

private enum EngagementTab: Hashable {
    case overview
    case raw
}
