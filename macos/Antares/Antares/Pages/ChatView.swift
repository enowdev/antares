import AntaresKit
import SwiftUI

struct ChatView: View {
    @EnvironmentObject private var model: AppModel
    @StateObject private var chat = ChatController()

    var body: some View {
        VStack(spacing: 0) {
            transcript
            if let errorMessage = chat.errorMessage {
                Text(errorMessage)
                    .font(.callout)
                    .foregroundStyle(.red)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
            }
            ChatComposer(
                text: $chat.draft,
                isStreaming: chat.isStreaming,
                isDisabled: chat.isLoading,
                onSend: { chat.send() },
                onInterrupt: { chat.interrupt() }
            )
        }
        .navigationTitle(chat.title)
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Button("New Chat") {
                    chat.newChat()
                }
            }
        }
        .task {
            await chat.start(client: model.client)
        }
    }

    private var transcript: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    if chat.visibleMessages.isEmpty && !chat.isLoading && !chat.isStreaming {
                        ContentUnavailableView(
                            "Chat",
                            systemImage: "bubble.left.and.bubble.right",
                            description: Text("Send a message to start a conversation.")
                        )
                        .frame(maxWidth: .infinity, minHeight: 240)
                    }

                    ForEach(chat.visibleMessages) { message in
                        messageBlock(message)
                            .id(message.id)
                    }

                    Color.clear
                        .frame(height: 1)
                        .id(ChatController.bottomID)
                }
                .padding(16)
            }
            .onChange(of: chat.scrollToken) { _, _ in
                proxy.scrollTo(ChatController.bottomID, anchor: .bottom)
            }
            .overlay {
                if chat.isLoading {
                    ProgressView("Loading conversation…")
                }
            }
        }
    }

    @ViewBuilder
    private func messageBlock(_ message: ChatHistoryMessage) -> some View {
        switch message.role {
        case "user":
            UserPromptView(text: message.content)
        default:
            VStack(alignment: .leading, spacing: 8) {
                AssistantTextView(
                    text: message.content,
                    reasoning: message.reasoning,
                    error: message.error,
                    isStreamingPlaceholder: chat.isStreaming && message.id == ChatController.liveAssistantID
                )
                ForEach(message.toolCalls) { call in
                    ToolCallRow(call: call)
                }
                if let notice = chat.live.notice, chat.isStreaming, message.id == ChatController.liveAssistantID {
                    Text(notice)
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            }
        }
    }
}

@MainActor
final class ChatController: ObservableObject {
    static let liveAssistantID = "live-assistant"
    static let bottomID = "chat-bottom"
    static let lastSessionKey = "antares.lastSession"

    @Published var draft = ""
    @Published var messages: [ChatHistoryMessage] = []
    @Published var live = ChatTranscript()
    @Published var isStreaming = false
    @Published var isLoading = false
    @Published var errorMessage: String?
    @Published var sessionID: String?
    @Published var title = "Chat"
    @Published private(set) var scrollToken = 0

    private var client: APIClient?
    private var streamTask: Task<Void, Never>?
    private var streamGeneration: UInt64 = 0
    private var receivedStreamContent = false

    var visibleMessages: [ChatHistoryMessage] {
        var rows = messages
        if isStreaming {
            rows.append(
                ChatHistoryMessage(
                    id: Self.liveAssistantID,
                    role: "assistant",
                    content: live.assistantText,
                    reasoning: live.reasoning,
                    toolCalls: live.toolCalls,
                    error: live.error
                )
            )
        }
        return rows
    }

    func start(client: APIClient) async {
        self.client = client
        if sessionID == nil {
            sessionID = UserDefaults.standard.string(forKey: Self.lastSessionKey).flatMap {
                $0.isEmpty ? nil : $0
            }
        }
        guard let sessionID else { return }
        await loadSession(sessionID)
        guard self.sessionID == sessionID, streamTask == nil, !isStreaming else { return }
        let generation = bumpGeneration()
        let task = Task { await attachLive(sessionID, generation: generation) }
        streamTask = task
        await task.value
    }

    func send() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !isStreaming else { return }
        draft = ""
        errorMessage = nil
        receivedStreamContent = false
        messages.append(
            ChatHistoryMessage(id: "local-user-\(UUID().uuidString)", role: "user", content: text)
        )
        live = ChatTranscript()
        isStreaming = true
        bumpScroll()
        let generation = bumpGeneration()
        streamTask?.cancel()
        streamTask = Task { await runStream(message: text, generation: generation) }
    }

    func interrupt() {
        guard isStreaming else { return }
        let id = sessionID
        bumpGeneration()
        isStreaming = false
        finishLiveIfNeeded()
        streamTask?.cancel()
        streamTask = nil
        Task {
            guard let id, let client else { return }
            _ = try? await client.interruptChat(sessionID: id)
        }
    }

    func newChat() {
        let id = sessionID
        let wasStreaming = isStreaming
        bumpGeneration()
        streamTask?.cancel()
        streamTask = nil
        sessionID = nil
        messages = []
        live = ChatTranscript()
        title = "Chat"
        errorMessage = nil
        isStreaming = false
        isLoading = false
        receivedStreamContent = false
        UserDefaults.standard.removeObject(forKey: Self.lastSessionKey)
        if wasStreaming, let id {
            Task {
                guard let client else { return }
                _ = try? await client.interruptChat(sessionID: id)
            }
        }
    }

    private func runStream(message: String, generation: UInt64) async {
        guard let client else { return }
        do {
            try await client.streamChat(ChatSendRequest(sessionID: sessionID, message: message)) { [weak self] event in
                await self?.applyLive(event, generation: generation)
            }
        } catch is CancellationError {
            // Local interrupt; the interrupt POST is fired separately.
        } catch {
            guard isCurrent(generation), !Task.isCancelled else { return }
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        guard isCurrent(generation) else { return }
        finishLiveIfNeeded()
        if receivedStreamContent {
            await refreshSession()
            guard isCurrent(generation) else { return }
        }
        bumpScroll()
    }

    private func loadSession(_ id: String) async {
        guard let client else { return }
        isLoading = true
        defer { isLoading = false }
        do {
            let detail = try await client.getSession(id)
            messages = hydrateChat(detail)
            title = detail.session.title.isEmpty ? "Chat" : detail.session.title
            persistSession(id)
            errorMessage = nil
            bumpScroll()
        } catch let error as APIError where error.status == 404 {
            newChat()
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
    }

    private func attachLive(_ id: String, generation: UInt64) async {
        guard let client else { return }
        receivedStreamContent = false
        live = ChatTranscript()
        do {
            try await client.attachChat(sessionID: id) { [weak self] event in
                await self?.applyLive(event, generation: generation)
            }
        } catch is CancellationError {
            return
        } catch {
            if Task.isCancelled || !isCurrent(generation) { return }
            // No live turn, or the attach failed; keep hydrated history.
        }
        guard isCurrent(generation) else { return }
        finishLiveIfNeeded()
        await refreshSession()
        guard isCurrent(generation) else { return }
        bumpScroll()
    }

    private func applyLive(_ event: [String: String], generation: UInt64) {
        guard isCurrent(generation) else { return }
        live.apply(event)
        if event["type"] != "done" {
            isStreaming = true
            receivedStreamContent = true
        }
        if let id = live.sessionID, !id.isEmpty {
            persistSession(id)
        }
        if let title = live.title, !title.isEmpty {
            self.title = title
        }
        bumpScroll()
    }

    private func finishLiveIfNeeded() {
        if live.hasDisplayableContent {
            messages.append(
                ChatHistoryMessage(
                    id: "asst-\(UUID().uuidString)",
                    role: "assistant",
                    content: live.assistantText,
                    reasoning: live.reasoning,
                    toolCalls: live.toolCalls,
                    error: live.error
                )
            )
        }
        live = ChatTranscript()
        isStreaming = false
    }

    @discardableResult
    private func bumpGeneration() -> UInt64 {
        streamGeneration += 1
        return streamGeneration
    }

    private func isCurrent(_ generation: UInt64) -> Bool {
        generation == streamGeneration
    }

    private func refreshSession() async {
        guard let client, let sessionID else { return }
        do {
            let detail = try await client.getSession(sessionID)
            messages = hydrateChat(detail)
            if !detail.session.title.isEmpty {
                title = detail.session.title
            }
        } catch {
            // Keep the locally assembled transcript if refresh fails.
        }
    }

    private func persistSession(_ id: String) {
        sessionID = id
        UserDefaults.standard.set(id, forKey: Self.lastSessionKey)
    }

    private func bumpScroll() {
        scrollToken += 1
    }
}
