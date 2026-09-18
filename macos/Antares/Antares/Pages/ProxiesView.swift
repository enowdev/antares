import AntaresKit
import SwiftUI

struct ProxiesView: View {
    @EnvironmentObject private var model: AppModel

    @State private var entries: [ProxyEntry] = []
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var showingAdd = false
    @State private var pendingRemove: ProxyEntry?
    @State private var busyID: String?
    @State private var testResults: [String: ProxyTestResponse] = [:]

    var body: some View {
        Group {
            if let errorMessage, entries.isEmpty, !isLoading {
                ContentUnavailableView(
                    "Proxies",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && entries.isEmpty {
                ContentUnavailableView(
                    "No Proxies",
                    systemImage: "globe",
                    description: Text("Paste a list of HTTP or SOCKS proxies to store them for tools.")
                )
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                            .padding(.horizontal, 16)
                            .padding(.top, 8)
                    }
                    Text("Stored proxies are picked by tools when they need one. Passwords stay masked.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                        .padding(.horizontal, 16)
                    Table(entries) {
                        TableColumn("Label") { entry in
                            HStack {
                                Text(entry.label)
                                if !entry.scheme.isEmpty {
                                    Text(entry.scheme.uppercased())
                                        .font(.caption2)
                                        .foregroundStyle(.secondary)
                                }
                            }
                        }
                        .width(min: 120, ideal: 160)
                        TableColumn("Endpoint") { entry in
                            Text(entry.url)
                                .font(.caption.monospaced())
                                .lineLimit(1)
                        }
                        TableColumn("Test") { entry in
                            testCell(entry)
                        }
                        .width(140)
                        TableColumn("") { entry in
                            Button("Remove", role: .destructive) {
                                pendingRemove = entry
                            }
                            .disabled(busyID != nil)
                        }
                        .width(80)
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading proxies…") }
        }
        .navigationTitle("Proxies")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Add") { showingAdd = true }
            }
        }
        .sheet(isPresented: $showingAdd) {
            AddProxiesSheet {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .confirmationDialog(
            "Remove \(pendingRemove?.label ?? "proxy")?",
            isPresented: Binding(
                get: { pendingRemove != nil },
                set: { if !$0 { pendingRemove = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("Remove", role: .destructive) {
                if let pendingRemove {
                    Task { await remove(pendingRemove) }
                }
            }
            Button("Cancel", role: .cancel) { pendingRemove = nil }
        } message: {
            Text("This deletes the stored proxy. Tools will no longer be able to pick it.")
        }
        .task { await load() }
        .refreshable { await load() }
    }

    @ViewBuilder
    private func testCell(_ entry: ProxyEntry) -> some View {
        if let result = testResults[entry.id] {
            if result.error == "…" {
                Text("Testing…").font(.caption).foregroundStyle(.secondary)
            } else if result.ok {
                Text(result.ip ?? "ok")
                    .font(.caption.monospaced())
                    .foregroundStyle(.green)
            } else {
                Text(result.error ?? "Failed")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .lineLimit(1)
                    .help(result.error ?? "Failed")
            }
        } else {
            Button("Test") {
                Task { await test(entry) }
            }
            .disabled(busyID != nil)
        }
    }

    private func load() async {
        isLoading = entries.isEmpty
        errorMessage = nil
        do {
            entries = try await model.client.listProxies().entries
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func test(_ entry: ProxyEntry) async {
        busyID = entry.id
        defer { busyID = nil }
        testResults[entry.id] = ProxyTestResponse(ok: false, ip: nil, error: "…")
        do {
            testResults[entry.id] = try await model.client.testProxy(id: entry.id)
        } catch {
            testResults[entry.id] = ProxyTestResponse(
                ok: false,
                ip: nil,
                error: (error as? APIError)?.message ?? error.localizedDescription
            )
        }
    }

    private func remove(_ entry: ProxyEntry) async {
        pendingRemove = nil
        busyID = entry.id
        defer { busyID = nil }
        errorMessage = nil
        do {
            _ = try await model.client.deleteProxy(entry.id)
            testResults[entry.id] = nil
            await load()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}

private struct AddProxiesSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    var onSaved: () -> Void

    @State private var text = ""
    @State private var errorMessage: String?
    @State private var result: ProxyBatchResponse?
    @State private var isSaving = false

    private var lineCount: Int {
        text.split(separator: "\n").filter { line in
            let trimmed = line.trimmingCharacters(in: .whitespacesAndNewlines)
            return !trimmed.isEmpty && !trimmed.hasPrefix("#")
        }.count
    }

    var body: some View {
        NavigationStack {
            Form {
                Text("Paste one proxy per line")
                TextEditor(text: $text)
                    .font(.body.monospaced())
                    .frame(minHeight: 160)
                Text("host:port:user:pass, http://user:pass@host:port, socks5://host:port, or host:port")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                if let result {
                    Text("Added \(result.added)")
                        .foregroundStyle(.green)
                    if !result.failed.isEmpty {
                        Text("\(result.failed.count) failed")
                            .foregroundStyle(.red)
                        ForEach(result.failed.prefix(6), id: \.line) { failure in
                            Text("L\(failure.line): \(failure.text) — \(failure.error)")
                                .font(.caption.monospaced())
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .navigationTitle("Add proxies")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(isSaving ? "Adding…" : (lineCount > 0 ? "Add \(lineCount)" : "Save")) {
                        Task { await save() }
                    }
                    .disabled(lineCount == 0 || isSaving)
                }
            }
        }
        .frame(minWidth: 480, minHeight: 380)
    }

    private func save() async {
        isSaving = true
        errorMessage = nil
        defer { isSaving = false }
        do {
            let response = try await model.client.batchAddProxies(text: text)
            result = response
            onSaved()
            if response.failed.isEmpty {
                text = ""
            } else {
                text = response.failed.map(\.text).joined(separator: "\n")
            }
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}
