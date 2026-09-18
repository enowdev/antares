import AntaresKit
import Charts
import SwiftUI

struct AnalyticsView: View {
    @EnvironmentObject private var model: AppModel

    private enum RangeOption: String, CaseIterable, Identifiable {
        case hours24 = "24h"
        case days7 = "7d"
        case days30 = "30d"

        var id: String { rawValue }
        var title: String {
            switch self {
            case .hours24: "24h"
            case .days7: "7d"
            case .days30: "30d"
            }
        }
        var bucket: String { self == .hours24 ? "hour" : "day" }
    }

    @State private var range: RangeOption = .days7
    @State private var data: AnalyticsResponse?
    @State private var errorMessage: String?
    @State private var isLoading = false

    var body: some View {
        Group {
            if let errorMessage, data == nil, !isLoading {
                ContentUnavailableView(
                    "Analytics",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else if let data, data.totals.calls == 0, !isLoading {
                ContentUnavailableView(
                    "No Usage",
                    systemImage: "chart.xyaxis.line",
                    description: Text("Token usage will show up here after chats run.")
                )
            } else if let data {
                ScrollView {
                    VStack(alignment: .leading, spacing: 16) {
                        if let errorMessage {
                            Text(errorMessage).foregroundStyle(.red)
                        }
                        LazyVGrid(columns: Array(repeating: GridItem(.flexible()), count: 4), spacing: 12) {
                            stat("Calls", formatCount(data.totals.calls))
                            stat("Tokens in", formatCount(data.totals.tokensIn))
                            stat("Tokens out", formatCount(data.totals.tokensOut))
                            stat("Cost", String(format: "$%.4f", data.totals.cost))
                        }
                        chart(data.series)
                        modelTable(data.byModel)
                    }
                    .padding(16)
                }
            } else {
                Color.clear
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading analytics…") }
        }
        .navigationTitle("Analytics")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Picker("Range", selection: $range) {
                    ForEach(RangeOption.allCases) { option in
                        Text(option.title).tag(option)
                    }
                }
                .pickerStyle(.segmented)
                .frame(maxWidth: 240)
            }
        }
        .task(id: range) { await load() }
        .refreshable { await load() }
    }

    private func stat(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(label.uppercased())
                .font(.caption)
                .foregroundStyle(.secondary)
            Text(value)
                .font(.title2.monospacedDigit().weight(.semibold))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(14)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    @ViewBuilder
    private func chart(_ series: [AnalyticsUsagePoint]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Per period").font(.headline)
            if series.isEmpty {
                Text("No series for this range.")
                    .foregroundStyle(.secondary)
            } else {
                Chart {
                    ForEach(series) { point in
                        AreaMark(
                            x: .value("Period", point.bucket),
                            y: .value("Tokens", point.tokensOut),
                            series: .value("Kind", "Tokens out")
                        )
                        .foregroundStyle(by: .value("Kind", "Tokens out"))
                        .opacity(0.28)
                        LineMark(
                            x: .value("Period", point.bucket),
                            y: .value("Tokens", point.tokensOut),
                            series: .value("Kind", "Tokens out")
                        )
                        .foregroundStyle(by: .value("Kind", "Tokens out"))
                        .interpolationMethod(.catmullRom)
                        AreaMark(
                            x: .value("Period", point.bucket),
                            y: .value("Tokens", point.tokensIn),
                            series: .value("Kind", "Tokens in")
                        )
                        .foregroundStyle(by: .value("Kind", "Tokens in"))
                        .opacity(0.22)
                        LineMark(
                            x: .value("Period", point.bucket),
                            y: .value("Tokens", point.tokensIn),
                            series: .value("Kind", "Tokens in")
                        )
                        .foregroundStyle(by: .value("Kind", "Tokens in"))
                        .interpolationMethod(.catmullRom)
                    }
                }
                .chartForegroundStyleScale([
                    "Tokens out": Color.accentColor,
                    "Tokens in": Color.green,
                ])
                .chartLegend(position: .bottom)
                .frame(height: 220)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    @ViewBuilder
    private func modelTable(_ rows: [AnalyticsModelUsage]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("By model").font(.headline)
            if rows.isEmpty {
                Text("No per-model usage.")
                    .foregroundStyle(.secondary)
            } else {
                Table(rows) {
                    TableColumn("Model") { Text($0.model).font(.body.monospaced()) }
                    TableColumn("Provider") { Text($0.provider) }.width(100)
                    TableColumn("Calls") { Text("\($0.calls)") }.width(70)
                    TableColumn("In") { Text(formatCount($0.tokensIn)) }.width(80)
                    TableColumn("Out") { Text(formatCount($0.tokensOut)) }.width(80)
                    TableColumn("Cost") { Text(String(format: "$%.4f", $0.cost)) }.width(90)
                }
                .frame(minHeight: 160)
            }
        }
        .padding(14)
        .background(.background, in: RoundedRectangle(cornerRadius: 10))
        .overlay(
            RoundedRectangle(cornerRadius: 10)
                .stroke(Color.secondary.opacity(0.2))
        )
    }

    private func load() async {
        isLoading = data == nil
        errorMessage = nil
        do {
            data = try await model.client.analytics(range: range.rawValue, bucket: range.bucket)
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func formatCount(_ value: Int) -> String {
        value.formatted(.number.notation(.compactName))
    }
}
