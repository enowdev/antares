import SwiftUI

struct ChatComposer: View {
    @Binding var text: String
    var isStreaming: Bool
    var isDisabled: Bool = false
    var onSend: () -> Void
    var onInterrupt: () -> Void

    private var canSend: Bool {
        !isDisabled && !isStreaming && !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        HStack(alignment: .bottom, spacing: 10) {
            TextEditor(text: $text)
                .font(.body)
                .frame(minHeight: 52, maxHeight: 140)
                .scrollContentBackground(.hidden)
                .padding(8)
                .background(.background, in: RoundedRectangle(cornerRadius: 8))
                .overlay(
                    RoundedRectangle(cornerRadius: 8)
                        .strokeBorder(.separator)
                )
                .disabled(isDisabled || isStreaming)
                .accessibilityLabel("Message")

            if isStreaming {
                Button("Interrupt", role: .destructive, action: onInterrupt)
                    .keyboardShortcut(.cancelAction)
                    .disabled(isDisabled)
            } else {
                Button("Send", action: onSend)
                    .keyboardShortcut(.return, modifiers: .command)
                    .disabled(!canSend)
            }
        }
        .padding(12)
        .background(.bar)
    }
}
