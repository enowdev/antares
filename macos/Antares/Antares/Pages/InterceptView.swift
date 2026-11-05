import AntaresKit
import AppKit
import SwiftUI
import UniformTypeIdentifiers

struct InterceptView: View {
    @EnvironmentObject private var model: AppModel

    enum Tab: String, CaseIterable, Identifiable {
        case traffic
        case interceptors
        case breakpoints
        var id: String { rawValue }
        var title: String {
            switch self {
            case .traffic: "Traffic"
            case .interceptors: "Interceptors"
            case .breakpoints: "Breakpoints"
            }
        }
    }

    @State private var tab: Tab = .traffic
    @State private var status: InterceptStatus?
    @State private var exchanges: [InterceptExchange] = []
    @State private var rules: [InterceptRule] = []
    @State private var interceptors: [InterceptorInfo] = []
    @State private var sessions: [InterceptorSession] = []
    @State private var paused: [InterceptPaused] = []
    @State private var selectedID: InterceptExchange.ID?
    @State private var port = "8899"
    @State private var match = ""
    @State private var mockStatus = ""
    @State private var mockBody = ""
    @State private var block = false
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var busy = false
    @State private var envText: String?

    private var selected: InterceptExchange? {
        exchanges.first { $0.id == selectedID }
    }

    private var running: Bool { status?.running == true }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if let errorMessage {
                Text(errorMessage)
                    .foregroundStyle(.red)
                    .padding(.horizontal, 16)
                    .padding(.top, 8)
            }
            Picker("Tab", selection: $tab) {
                ForEach(Tab.allCases) { item in
                    Text(item.title).tag(item)
                }
            }
            .pickerStyle(.segmented)
            .padding(16)

            Group {
                switch tab {
                case .traffic:
                    trafficTab
                case .interceptors:
                    interceptorsTab
                case .breakpoints:
                    breakpointsTab
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading intercept…") }
        }
        .navigationTitle("Intercept")
        .toolbar { toolbar }
        .sheet(item: Binding(
            get: { selected.map(ExchangeDetail.init) },
            set: { if $0 == nil { selectedID = nil } }
        )) { detail in
            exchangeSheet(detail.exchange)
        }
        .sheet(item: Binding(
            get: { envText.map(EnvPayload.init) },
            set: { if $0 == nil { envText = nil } }
        )) { payload in
            envSheet(payload.text)
        }
        .task { await loadAll() }
        .task(id: running) {
            guard running else { return }
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(2))
                await loadExchanges(quiet: true)
                await loadStatus(quiet: true)
            }
        }
        .task(id: running) {
            guard running else {
                paused = []
                return
            }
            do {
                try await model.client.streamInterceptBreakpoints { snapshot in
                    await MainActor.run {
                        paused = snapshot.paused
                    }
                }
            } catch is CancellationError {
                return
            } catch {
                if !Task.isCancelled {
                    errorMessage = Self.message(error)
                }
            }
        }
        .refreshable { await loadAll() }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if running {
            ToolbarItem(placement: .automatic) {
                Text(status?.addr ?? "Running")
                    .font(.caption)
                    .foregroundStyle(.green)
            }
            ToolbarItem(placement: .automatic) {
                Button("Stop") { Task { await stop() } }
                    .disabled(busy)
            }
        } else {
            ToolbarItem(placement: .automatic) {
                TextField("Port", text: $port)
                    .frame(width: 70)
                    .font(.body.monospaced())
            }
            ToolbarItem(placement: .automatic) {
                Button("Start") { Task { await start() } }
                    .disabled(busy)
            }
        }
        ToolbarItem(placement: .automatic) {
            Button("CA cert") { Task { await saveCA() } }
                .disabled(busy)
        }
        ToolbarItem(placement: .automatic) {
            Button("Clear") { Task { await clear() } }
                .disabled(busy || exchanges.isEmpty)
        }
    }

    private var trafficTab: some View {
        HSplitView {
            VStack(alignment: .leading, spacing: 0) {
                Text("Traffic (\(exchanges.count))")
                    .font(.headline)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                if exchanges.isEmpty {
                    ContentUnavailableView(
                        "No Traffic",
                        systemImage: "dot.radiowaves.left.and.right",
                        description: Text("Start the proxy and send traffic through it.")
                    )
                } else {
                    Table(exchanges, selection: $selectedID) {
                        TableColumn("Method") { row in
                            Text(row.method).fontDesign(.monospaced)
                        }
                        .width(70)
                        TableColumn("Status") { row in
                            Text(row.status == 0 ? "—" : "\(row.status)")
                                .foregroundStyle(statusColor(row.status))
                        }
                        .width(60)
                        TableColumn("URL") { row in
                            HStack {
                                Text(row.url)
                                    .font(.caption.monospaced())
                                    .lineLimit(1)
                                if row.mocked { Text("mock").font(.caption2).foregroundStyle(.secondary) }
                                if row.blocked { Text("block").font(.caption2).foregroundStyle(.red) }
                            }
                        }
                        TableColumn("Time") { row in
                            Text("\(row.durationMS)ms")
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        .width(70)
                    }
                }
            }
            .frame(minWidth: 420)

            Form {
                TextField("Match (URL substring)", text: $match)
                HStack {
                    TextField("Mock status", text: $mockStatus)
                        .frame(width: 110)
                    Toggle("Block", isOn: $block)
                    Spacer()
                    Button("Add rule") { Task { await addRule() } }
                        .disabled(match.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || busy)
                }
                TextField("Mock body", text: $mockBody)
                if rules.isEmpty {
                    Text("No rules. Matching requests can be mocked or blocked.")
                        .foregroundStyle(.secondary)
                } else {
                    ForEach(rules) { rule in
                        HStack {
                            Text(rule.match)
                                .font(.caption.monospaced())
                                .lineLimit(1)
                            Spacer()
                            Text(rule.block ? "block" : "mock \(rule.mockStatus)")
                                .font(.caption)
                                .foregroundStyle(rule.block ? .red : .secondary)
                            Button("Delete", role: .destructive) {
                                Task { await deleteRule(rule) }
                            }
                            .disabled(busy)
                        }
                    }
                }
            }
            .formStyle(.grouped)
            .frame(minWidth: 280, idealWidth: 320)
        }
    }

    private var interceptorsTab: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                if !sessions.isEmpty {
                    Text("Active sessions")
                        .font(.headline)
                    ForEach(sessions) { session in
                        HStack {
                            Text(session.interceptor)
                                .font(.caption)
                                .foregroundStyle(.secondary)
                            Text(session.id)
                                .font(.body.monospaced())
                                .lineLimit(1)
                            Spacer()
                            Button("Stop", role: .destructive) {
                                Task { await deactivate(session) }
                            }
                            .disabled(busy)
                        }
                    }
                }

                ForEach(groupedInterceptors, id: \.category) { group in
                    Text(group.category.capitalized)
                        .font(.headline)
                    LazyVGrid(columns: [GridItem(.adaptive(minimum: 220), spacing: 12)], spacing: 12) {
                        ForEach(group.items) { item in
                            VStack(alignment: .leading, spacing: 8) {
                                HStack {
                                    Text(item.label).font(.headline)
                                    Spacer()
                                    Text(item.available ? "Ready" : "Needs setup")
                                        .font(.caption)
                                        .foregroundStyle(item.available ? .green : .secondary)
                                }
                                Text(item.id)
                                    .font(.caption.monospaced())
                                    .foregroundStyle(.secondary)
                                if let reason = item.reason, !item.available {
                                    Text(reason).font(.caption).foregroundStyle(.secondary)
                                }
                                Button("Activate") {
                                    Task { await activate(item) }
                                }
                                .disabled(!item.available || busy)
                            }
                            .padding(12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(.background, in: RoundedRectangle(cornerRadius: 10))
                            .overlay(RoundedRectangle(cornerRadius: 10).stroke(Color.secondary.opacity(0.2)))
                        }
                    }
                }

                if !running {
                    Text("Activating an interceptor starts the proxy automatically if it is stopped.")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
            .padding(16)
        }
    }

    private var breakpointsTab: some View {
        Group {
            if !running {
                ContentUnavailableView(
                    "Proxy Off",
                    systemImage: "pause.circle",
                    description: Text("Start the intercept proxy to pause matching requests.")
                )
            } else if paused.isEmpty {
                ContentUnavailableView(
                    "No Breakpoints",
                    systemImage: "pause.circle",
                    description: Text("Matching requests will pause here until you resume or abort them.")
                )
            } else {
                List(paused) { item in
                    VStack(alignment: .leading, spacing: 8) {
                        HStack {
                            Text("Paused")
                                .font(.caption)
                                .foregroundStyle(.orange)
                            Text(item.method)
                                .font(.headline.monospaced())
                            Text(item.url)
                                .font(.caption.monospaced())
                                .foregroundStyle(.secondary)
                                .lineLimit(1)
                        }
                        if !item.body.isEmpty {
                            Text(item.body)
                                .font(.caption.monospaced())
                                .textSelection(.enabled)
                                .lineLimit(8)
                        }
                        HStack {
                            Button("Resume") { Task { await resume(item, abort: false) } }
                                .disabled(busy)
                            Button("Abort", role: .destructive) { Task { await resume(item, abort: true) } }
                                .disabled(busy)
                        }
                    }
                }
            }
        }
    }

    private var groupedInterceptors: [(category: String, items: [InterceptorInfo])] {
        var order: [String] = []
        var buckets: [String: [InterceptorInfo]] = [:]
        for item in interceptors {
            if buckets[item.category] == nil {
                order.append(item.category)
            }
            buckets[item.category, default: []].append(item)
        }
        return order.map { (category: $0, items: buckets[$0] ?? []) }
    }

    private func exchangeSheet(_ exchange: InterceptExchange) -> some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    HStack {
                        Text(exchange.method).font(.headline.monospaced())
                        Text(exchange.status == 0 ? "—" : "\(exchange.status)")
                            .foregroundStyle(statusColor(exchange.status))
                        Text("\(exchange.durationMS)ms")
                            .font(.caption)
                            .foregroundStyle(.secondary)
                    }
                    Text(exchange.url)
                        .font(.caption.monospaced())
                        .textSelection(.enabled)
                    section("Request", headers: exchange.reqHeaders, body: exchange.reqBody)
                    section("Response", headers: exchange.respHeaders, body: exchange.respBody)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(16)
            }
            .navigationTitle("Exchange")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { selectedID = nil }
                }
            }
        }
        .frame(minWidth: 560, minHeight: 420)
    }

    private func envSheet(_ text: String) -> some View {
        NavigationStack {
            ScrollView {
                Text(text)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(16)
            }
            .navigationTitle("Terminal environment")
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Close") { envText = nil }
                }
                ToolbarItem(placement: .primaryAction) {
                    Button("Copy") {
                        NSPasteboard.general.clearContents()
                        NSPasteboard.general.setString(text, forType: .string)
                    }
                }
            }
        }
        .frame(minWidth: 480, minHeight: 280)
    }

    @ViewBuilder
    private func section(_ title: String, headers: [String: [String]], body: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title)
                .font(.caption)
                .foregroundStyle(.secondary)
            if !headers.isEmpty {
                Text(headersText(headers))
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
            }
            if body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                Text("Empty body").font(.caption).foregroundStyle(.secondary)
            } else {
                Text(prettyBody(body))
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
            }
        }
    }

    private func loadAll() async {
        isLoading = status == nil && exchanges.isEmpty
        errorMessage = nil
        await loadStatus()
        await loadExchanges()
        await loadRules()
        await loadInterceptors()
        isLoading = false
    }

    private func loadStatus(quiet: Bool = false) async {
        do {
            status = try await model.client.interceptStatus()
            if !quiet { errorMessage = nil }
        } catch {
            if !quiet { errorMessage = Self.message(error) }
        }
    }

    private func loadExchanges(quiet: Bool = false) async {
        do {
            exchanges = try await model.client.interceptExchanges().exchanges
        } catch {
            if !quiet { errorMessage = Self.message(error) }
        }
    }

    private func loadRules() async {
        do {
            rules = try await model.client.interceptRules().rules
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func loadInterceptors() async {
        do {
            let response = try await model.client.interceptors()
            interceptors = response.interceptors
            sessions = response.sessions
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func start() async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            status = try await model.client.startIntercept(port: Int(port) ?? 8899)
            await loadAll()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func stop() async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            _ = try await model.client.stopIntercept()
            await loadStatus()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func clear() async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            _ = try await model.client.clearIntercept()
            exchanges = []
            selectedID = nil
            await loadStatus()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func addRule() async {
        let value = match.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty else { return }
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            _ = try await model.client.addInterceptRule(
                InterceptRuleRequest(
                    match: value,
                    block: block,
                    mockStatus: Int(mockStatus) ?? 0,
                    mockBody: mockBody
                )
            )
            match = ""
            mockStatus = ""
            mockBody = ""
            block = false
            await loadRules()
            await loadStatus()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func deleteRule(_ rule: InterceptRule) async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            _ = try await model.client.deleteInterceptRule(id: rule.id)
            await loadRules()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func activate(_ item: InterceptorInfo) async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            let session = try await model.client.activateInterceptor(item.id)
            envText = session.envText
            await loadInterceptors()
            await loadStatus()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func deactivate(_ session: InterceptorSession) async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            _ = try await model.client.deactivateInterceptorSession(session.id)
            await loadInterceptors()
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func resume(_ item: InterceptPaused, abort: Bool) async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            _ = try await model.client.resumeBreakpoint(id: item.id, abort: abort)
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func saveCA() async {
        busy = true
        defer { busy = false }
        errorMessage = nil
        do {
            let data = try await model.client.interceptCA()
            let panel = NSSavePanel()
            panel.nameFieldStringValue = "antares-intercept-ca.pem"
            panel.allowedContentTypes = [UTType(filenameExtension: "pem") ?? .plainText]
            guard panel.runModal() == .OK, let url = panel.url else { return }
            try data.write(to: url)
        } catch {
            errorMessage = Self.message(error)
        }
    }

    private func statusColor(_ status: Int) -> Color {
        if status >= 500 { return .red }
        if status >= 400 { return .orange }
        if status >= 200 { return .green }
        return .secondary
    }

    private func headersText(_ headers: [String: [String]]) -> String {
        headers.keys.sorted().map { key in
            "\(key): \((headers[key] ?? []).joined(separator: ", "))"
        }.joined(separator: "\n")
    }

    private func prettyBody(_ body: String) -> String {
        let trimmed = body.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.first == "{" || trimmed.first == "[" else { return body }
        guard let data = trimmed.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: data),
              let pretty = try? JSONSerialization.data(withJSONObject: obj, options: [.prettyPrinted]),
              let text = String(data: pretty, encoding: .utf8)
        else { return body }
        return text
    }

    private static func message(_ error: Error) -> String {
        (error as? APIError)?.message ?? error.localizedDescription
    }
}

private struct ExchangeDetail: Identifiable {
    var id: Int { exchange.id }
    var exchange: InterceptExchange
}

private struct EnvPayload: Identifiable {
    var id: String { text }
    var text: String
}
