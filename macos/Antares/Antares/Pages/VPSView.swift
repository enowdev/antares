import AntaresKit
import SwiftUI

struct VPSView: View {
    @EnvironmentObject private var model: AppModel

    @State private var hosts: [VPSHost] = []
    @State private var metrics: [String: VPSMetrics] = [:]
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var showingAdd = false
    @State private var editing: VPSHost?
    @State private var pendingRemove: VPSHost?
    @State private var processHost: VPSHost?

    var body: some View {
        Group {
            if let errorMessage, hosts.isEmpty, !isLoading {
                ContentUnavailableView(
                    "VPS",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if !isLoading && hosts.isEmpty {
                ContentUnavailableView(
                    "No Hosts",
                    systemImage: "server.rack",
                    description: Text("Add an SSH host to watch CPU, memory, and disk.")
                )
            } else {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        LazyVGrid(
                            columns: [GridItem(.adaptive(minimum: 320), spacing: 12)],
                            spacing: 12
                        ) {
                            ForEach(hosts) { host in
                                VPSHostCard(
                                    host: host,
                                    metrics: metrics[host.id],
                                    onRefresh: { Task { await loadMetrics(host.id) } },
                                    onEdit: { editing = host },
                                    onRemove: { pendingRemove = host },
                                    onProcesses: { processHost = host }
                                )
                            }
                        }
                    }
                    .padding(16)
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading VPS…") }
        }
        .navigationTitle("VPS")
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("Add") { showingAdd = true }
            }
        }
        .sheet(isPresented: $showingAdd) {
            VPSEditorSheet(host: nil) {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .sheet(item: $editing) { host in
            VPSEditorSheet(host: host) {
                Task { await load() }
            }
            .environmentObject(model)
        }
        .sheet(item: $processHost) { host in
            VPSProcessSheet(host: host)
                .environmentObject(model)
        }
        .confirmationDialog(
            "Remove \(pendingRemove?.label ?? "host")?",
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
            Text("This deletes the stored SSH credentials for this host.")
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        isLoading = hosts.isEmpty
        errorMessage = nil
        do {
            hosts = try await model.client.listVPS().hosts
            for host in hosts where metrics[host.id] == nil {
                await loadMetrics(host.id)
            }
        } catch {
            errorMessage = Self.message(error)
        }
        isLoading = false
    }

    private func loadMetrics(_ id: String) async {
        do {
            metrics[id] = try await model.client.vpsMetrics(id: id)
        } catch {
            metrics[id] = VPSMetrics(reachable: false, error: Self.message(error))
        }
    }

    private func remove(_ host: VPSHost) async {
        pendingRemove = nil
        do {
            _ = try await model.client.deleteVPS(host.id)
            metrics[host.id] = nil
            await load()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private static func message(_ error: Error) -> String {
        (error as? APIError)?.message ?? error.localizedDescription
    }

}

private struct VPSHostCard: View {
    let host: VPSHost
    let metrics: VPSMetrics?
    var onRefresh: () -> Void
    var onEdit: () -> Void
    var onRemove: () -> Void
    var onProcesses: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(host.label.isEmpty ? host.host : host.label)
                            .font(.headline)
                        if let metrics {
                            Text(metrics.reachable ? "Online" : "Offline")
                                .font(.caption)
                                .foregroundStyle(metrics.reachable ? .green : .red)
                        }
                    }
                    Text("\(host.username)@\(host.host):\(host.port)")
                        .font(.caption.monospaced())
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
                Spacer()
                Button(action: onRefresh) { Image(systemName: "arrow.clockwise") }
                    .help("Refresh")
                Button(action: onEdit) { Image(systemName: "pencil") }
                    .help("Edit")
                Button(role: .destructive, action: onRemove) { Image(systemName: "trash") }
                    .help("Remove")
            }

            if let metrics, !metrics.reachable {
                Text(metrics.error ?? "Unreachable")
                    .font(.caption)
                    .foregroundStyle(.red)
                    .padding(8)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(.red.opacity(0.08), in: RoundedRectangle(cornerRadius: 6))
            } else if let metrics {
                let identity = [
                    metrics.os.isEmpty ? nil : metrics.os,
                    metrics.kernel.isEmpty ? nil : "kernel \(metrics.kernel)",
                    metrics.uptime.isEmpty ? nil : "up \(metrics.uptime)",
                ]
                .compactMap { $0 }
                .joined(separator: " · ")
                if !identity.isEmpty {
                    Text(identity)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
                gauge("CPU", percent: metrics.cpuPercent, detail: "\(fmt(metrics.load1)) / \(fmt(metrics.load5)) / \(fmt(metrics.load15)) · \(metrics.cpuCores) cores")
                gauge("Memory", percent: metrics.memPercent, detail: "\(fmtMB(metrics.memUsedMB)) / \(fmtMB(metrics.memTotalMB))")
                gauge("Disk", percent: metrics.diskPercent, detail: "\(fmt(metrics.diskUsedGB))G / \(fmt(metrics.diskTotalGB))G")
                if metrics.swapTotalMB > 0 {
                    gauge("Swap", percent: metrics.swapPercent, detail: "\(fmtMB(metrics.swapUsedMB)) / \(fmtMB(metrics.swapTotalMB))")
                }
                HStack {
                    Text("\(metrics.processes) processes")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Spacer()
                    Button("Show processes", action: onProcesses)
                }
            } else {
                ProgressView().frame(maxWidth: .infinity, minHeight: 80)
            }
        }
        .padding(14)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private func gauge(_ label: String, percent: Double, detail: String) -> some View {
        let clamped = min(max(percent, 0), 100)
        return VStack(alignment: .leading, spacing: 4) {
            HStack {
                Text(label).font(.caption).foregroundStyle(.secondary)
                Spacer()
                Text("\(Int(clamped.rounded()))% · \(detail)")
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
            ProgressView(value: clamped, total: 100)
                .tint(clamped >= 90 ? .red : clamped >= 70 ? .orange : .accentColor)
        }
    }

    private func fmt(_ value: Double) -> String {
        value.formatted(.number.precision(.fractionLength(0...1)))
    }

    private func fmtMB(_ mb: Int) -> String {
        if mb >= 1024 { return String(format: "%.1fG", Double(mb) / 1024) }
        return "\(mb)M"
    }
}

private struct VPSEditorSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    let host: VPSHost?
    var onSaved: () -> Void

    @State private var label = ""
    @State private var hostname = ""
    @State private var port = "22"
    @State private var username = "root"
    @State private var authMethod = "password"
    @State private var password = ""
    @State private var privateKey = ""
    @State private var passphrase = ""
    @State private var testResult: VPSTestResponse?
    @State private var errorMessage: String?
    @State private var testing = false
    @State private var saving = false

    private var isEdit: Bool { host != nil }

    var body: some View {
        NavigationStack {
            Form {
                TextField("Label", text: $label)
                HStack {
                    TextField("Host", text: $hostname)
                    TextField("Port", text: $port)
                        .frame(width: 70)
                }
                TextField("Username", text: $username)
                Picker("Auth", selection: $authMethod) {
                    Text("Password").tag("password")
                    Text("Private key").tag("key")
                }
                if authMethod == "password" {
                    SecureField(isEdit && host?.hasPassword == true ? "Password (stored)" : "Password", text: $password)
                } else {
                    TextEditor(text: $privateKey)
                        .font(.body.monospaced())
                        .frame(minHeight: 100)
                    SecureField("Passphrase", text: $passphrase)
                }
                if let testResult {
                    Text(testResult.ok ? "Connected as \(testResult.asUser ?? "")" : (testResult.error ?? "Test failed"))
                        .foregroundStyle(testResult.ok ? .green : .red)
                }
                if let errorMessage {
                    Text(errorMessage).foregroundStyle(.red)
                }
            }
            .navigationTitle(isEdit ? "Edit host" : "Add host")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { dismiss() }
                }
                ToolbarItem(placement: .automatic) {
                    Button(testing ? "Testing…" : "Test") { Task { await test() } }
                        .disabled(hostname.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || testing || saving)
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button(saving ? "Saving…" : "Save") { Task { await save() } }
                        .disabled(hostname.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || testing || saving)
                }
            }
        }
        .frame(minWidth: 460, minHeight: 420)
        .onAppear {
            label = host?.label ?? ""
            hostname = host?.host ?? ""
            port = host.map { String($0.port) } ?? "22"
            username = host?.username ?? "root"
            authMethod = host?.authMethod.isEmpty == false ? (host?.authMethod ?? "password") : "password"
        }
    }

    private func bodyRequest() -> VPSSaveRequest {
        VPSSaveRequest(
            id: host?.id,
            label: label.trimmingCharacters(in: .whitespacesAndNewlines),
            host: hostname.trimmingCharacters(in: .whitespacesAndNewlines),
            port: Int(port) ?? 22,
            username: username.trimmingCharacters(in: .whitespacesAndNewlines),
            authMethod: authMethod,
            password: authMethod == "password" ? password : "",
            privateKey: authMethod == "key" ? privateKey : "",
            passphrase: authMethod == "key" ? passphrase : ""
        )
    }

    private func test() async {
        testing = true
        errorMessage = nil
        defer { testing = false }
        do {
            testResult = try await model.client.testVPS(bodyRequest())
        } catch {
            testResult = VPSTestResponse(ok: false, asUser: nil, error: (error as? APIError)?.message ?? error.localizedDescription)
        }
    }

    private func save() async {
        saving = true
        errorMessage = nil
        defer { saving = false }
        do {
            _ = try await model.client.saveVPS(bodyRequest())
            onSaved()
            dismiss()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }
}

private struct VPSProcessSheet: View {
    @EnvironmentObject private var model: AppModel
    @Environment(\.dismiss) private var dismiss

    let host: VPSHost

    @State private var processes: [VPSProcess] = []
    @State private var errorMessage: String?
    @State private var query = ""
    @State private var isLoading = true

    private var shown: [VPSProcess] {
        let q = query.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        guard !q.isEmpty else { return processes }
        return processes.filter {
            $0.command.lowercased().contains(q) || $0.user.lowercased().contains(q) || String($0.pid).contains(q)
        }
    }

    var body: some View {
        NavigationStack {
            Group {
                if isLoading {
                    ProgressView("Loading processes…")
                } else if let errorMessage, processes.isEmpty {
                    ContentUnavailableView("Processes", systemImage: "exclamationmark.triangle", description: Text(errorMessage))
                } else if shown.isEmpty {
                    ContentUnavailableView("No Processes", systemImage: "list.bullet", description: Text("Nothing matched the filter."))
                } else {
                    Table(shown) {
                        TableColumn("PID") { Text("\($0.pid)").font(.caption.monospaced()) }.width(70)
                        TableColumn("User") { Text($0.user) }.width(80)
                        TableColumn("CPU") { Text($0.cpu.formatted(.number.precision(.fractionLength(1)))) }.width(60)
                        TableColumn("Mem") { Text($0.mem.formatted(.number.precision(.fractionLength(1)))) }.width(60)
                        TableColumn("Command") { Text($0.command).font(.caption.monospaced()).lineLimit(1) }
                    }
                }
            }
            .navigationTitle("Processes — \(host.label)")
            .searchable(text: $query, prompt: "Filter processes")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { dismiss() }
                }
            }
        }
        .frame(minWidth: 640, minHeight: 420)
        .task {
            do {
                let list = try await model.client.vpsProcesses(id: host.id)
                processes = list.processes
                errorMessage = list.error
            } catch {
                errorMessage = (error as? APIError)?.message ?? error.localizedDescription
            }
            isLoading = false
        }
    }
}
