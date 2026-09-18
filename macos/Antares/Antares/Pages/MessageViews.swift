import AntaresKit
import SwiftUI

struct UserPromptView: View {
    let text: String

    var body: some View {
        HStack {
            Spacer(minLength: 64)
            Text(text)
                .textSelection(.enabled)
                .padding(.horizontal, 12)
                .padding(.vertical, 8)
                .background(.quaternary, in: RoundedRectangle(cornerRadius: 12))
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("You")
    }
}

struct AssistantTextView: View {
    let text: String
    var reasoning: String = ""
    var error: String?
    var isStreamingPlaceholder: Bool = false

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !reasoning.isEmpty {
                Text(reasoning)
                    .font(.callout)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }

            if !text.isEmpty {
                Text(text)
                    .textSelection(.enabled)
            } else if isStreamingPlaceholder, error == nil {
                HStack(spacing: 8) {
                    ProgressView()
                        .controlSize(.small)
                    Text("Thinking…")
                        .foregroundStyle(.secondary)
                }
            }

            if let error, !error.isEmpty {
                Text(error)
                    .foregroundStyle(.red)
                    .textSelection(.enabled)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Antares")
    }
}

struct ToolCallRow: View {
    let call: ChatToolCall

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 8) {
                Image(systemName: iconName)
                Text(call.name.isEmpty ? "tool" : call.name)
                    .fontWeight(.semibold)
                if call.running {
                    ProgressView()
                        .controlSize(.small)
                }
                Spacer()
            }

            if !call.args.isEmpty {
                Text(call.args)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                    .textSelection(.enabled)
            }
            if !call.progress.isEmpty {
                Text(call.progress)
                    .font(.caption)
                    .textSelection(.enabled)
            }
            if let result = call.result, !result.isEmpty {
                Text(result)
                    .font(.caption)
                    .foregroundStyle(call.isError ? .red : .secondary)
                    .textSelection(.enabled)
            }
        }
        .padding(8)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(.quaternary.opacity(0.45), in: RoundedRectangle(cornerRadius: 8))
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Tool \(call.name)")
    }

    private var iconName: String {
        if call.isError { return "xmark.octagon" }
        if call.running { return "gearshape" }
        return "wrench.and.screwdriver"
    }
}
