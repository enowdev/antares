# macOS SwiftUI Dashboard Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a native macOS SwiftUI app that launches or attaches to the existing Go `antares` backend and covers every current dashboard page without wrapping the React SPA.

**Architecture:** `macos/AntaresKit` (testable HTTP/SSE/process supervisor) + `macos/Antares` (SwiftUI). The app bundles `bin/antares` and talks only to `http://127.0.0.1:8787`. Web dashboard remains embedded for Linux/Windows.

**Tech Stack:** Swift 6, macOS 14, SwiftUI `NavigationSplitView`, `URLSession`, XCTest via `swift test`, existing Go API (`internal/server/routes.go`).

**Spec:** `docs/superpowers/specs/2026-09-18-macos-swiftui-dashboard-design.md`

---

## File map

```
macos/AntaresKit/Package.swift
macos/AntaresKit/Sources/AntaresKit/
  APIClient.swift          JSON GET/POST, ApiError, bearer + cookies
  SSEClient.swift          byte-accurate port of web/src/lib/sse.ts
  RuntimeSupervisor.swift  health probe, spawn --foreground, stop child
  ChatEvents.swift         decode chat SSE payloads
  Models.swift             shared Codable DTOs used by several pages
macos/AntaresKit/Tests/AntaresKitTests/
  SSEClientTests.swift
  APIClientTests.swift
  RuntimeSupervisorTests.swift
  ChatEventsTests.swift
macos/Antares/Antares/
  AntaresApp.swift
  AppModel.swift           owns supervisor + client, login/setup gate
  LaunchView.swift
  LoginView.swift
  SetupView.swift
  AppShell.swift           NavigationSplitView + sidebar
  Pages/*.swift            one file per routeManifest id
macos/Antares/Antares.xcodeproj   generated or checked in; Kit is a local package
Makefile                   add `macos` target
docs/getting-started.md    Mac app section
```

Do not delete `web/`. Do not add WKWebView.

---

### Task 1: AntaresKit package skeleton

**Files:**
- Create: `macos/AntaresKit/Package.swift`
- Create: `macos/AntaresKit/Sources/AntaresKit/AntaresKit.swift`
- Create: `macos/AntaresKit/Tests/AntaresKitTests/AntaresKitTests.swift`

- [ ] **Step 1: Write Package.swift**

```swift
// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "AntaresKit",
    platforms: [.macOS(.v14)],
    products: [.library(name: "AntaresKit", targets: ["AntaresKit"])],
    targets: [
        .target(name: "AntaresKit"),
        .testTarget(name: "AntaresKitTests", dependencies: ["AntaresKit"]),
    ]
)
```

Empty `AntaresKit.swift`:

```swift
public enum AntaresKit {
    public static let defaultBaseURL = URL(string: "http://127.0.0.1:8787")!
}
```

- [ ] **Step 2: Write a failing test**

```swift
import XCTest
@testable import AntaresKit

final class AntaresKitTests: XCTestCase {
    func testDefaultBaseURLIsLoopback8787() {
        XCTAssertEqual(AntaresKit.defaultBaseURL.absoluteString, "http://127.0.0.1:8787")
    }
}
```

- [ ] **Step 3: Run tests**

```bash
cd macos/AntaresKit && swift test
```

Expected: PASS (skeleton). If the URL constant is missing, FAIL then add it.

- [ ] **Step 4: Commit**

```bash
git add macos/AntaresKit
git commit -m "feat(macos): add AntaresKit Swift package skeleton"
```

---

### Task 2: SSE parser (TDD)

**Files:**
- Create: `macos/AntaresKit/Sources/AntaresKit/SSEClient.swift`
- Create: `macos/AntaresKit/Tests/AntaresKitTests/SSEClientTests.swift`

Port semantics from `web/src/lib/sse.ts`.

- [ ] **Step 1: Write the failing test**

```swift
import XCTest
@testable import AntaresKit

final class SSEClientTests: XCTestCase {
    func testDispatchesJSONOnBlankLineAndStripsLeadingSpace() throws {
        let raw = "data: {\"delta\":\"Hel\"}\n\ndata: {\"delta\":\"lo\"}\r\n\r\n"
        var events: [[String: String]] = []
        try SSEParser.parse(Data(raw.utf8)) { json in
            events.append(json)
        }
        XCTAssertEqual(events, [["delta": "Hel"], ["delta": "lo"]])
    }

    func testIgnoresCommentsAndDoneSentinel() throws {
        let raw = ": keep-alive\n\ndata: [DONE]\n\ndata: {\"ok\":true}\n\n"
        var count = 0
        try SSEParser.parse(Data(raw.utf8)) { _ in count += 1 }
        XCTAssertEqual(count, 1)
    }

    func testDropsUnfinishedTrailingRecord() throws {
        let raw = "data: {\"a\":1}\n\ndata: {\"b\":2}"
        var count = 0
        try SSEParser.parse(Data(raw.utf8)) { _ in count += 1 }
        XCTAssertEqual(count, 1)
    }
}
```

- [ ] **Step 2: Run to confirm RED**

```bash
cd macos/AntaresKit && swift test --filter SSEClientTests
```

Expected: FAIL, `SSEParser` not found.

- [ ] **Step 3: Implement `SSEParser`**

```swift
import Foundation

public enum SSEParser {
    public static func parse(_ data: Data, onEvent: ([String: Any]) throws -> Void) throws {
        var buffer = String(decoding: data, as: UTF8.self)
        if buffer.hasSuffix("\r") { buffer.removeLast() }
        var dataLines: [String] = []
        // Split on \r\n | \n | \r. Walk character-by-character matching sse.ts.
        var lineStart = buffer.startIndex
        var i = buffer.startIndex
        func flushLine(_ line: Substring) throws {
            if line.hasPrefix(":") { return }
            if line.isEmpty {
                let payload = dataLines.joined(separator: "\n")
                dataLines = []
                guard !payload.isEmpty, payload != "[DONE]" else { return }
                let obj = try JSONSerialization.jsonObject(with: Data(payload.utf8))
                guard let dict = obj as? [String: Any] else { return }
                try onEvent(dict)
                return
            }
            if line.hasPrefix("data:") {
                var v = String(line.dropFirst(5))
                if v.first == " " { v.removeFirst() }
                dataLines.append(v)
            }
        }
        while i < buffer.endIndex {
            let ch = buffer[i]
            if ch == "\r" {
                try flushLine(buffer[lineStart..<i])
                let next = buffer.index(after: i)
                if next < buffer.endIndex, buffer[next] == "\n" {
                    i = buffer.index(after: next)
                } else {
                    i = next
                }
                lineStart = i
                continue
            }
            if ch == "\n" {
                try flushLine(buffer[lineStart..<i])
                i = buffer.index(after: i)
                lineStart = i
                continue
            }
            i = buffer.index(after: i)
        }
        // trailing record with no blank line: discarded
    }
}
```

Also add `SSEClient.stream(session:request:onEvent:)` that reads `URLSession.AsyncBytes`, accumulates chunks, and calls `SSEParser` incrementally (split a helper `SSEAccumulator` if the one-shot parse is easier to test first). Incremental parsing can wait until chat attach; tests above cover the dispatcher.

- [ ] **Step 4: Run tests GREEN**

```bash
cd macos/AntaresKit && swift test --filter SSEClientTests
```

Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add macos/AntaresKit
git commit -m "feat(macos): port dashboard SSE parser into AntaresKit"
```

---

### Task 3: API client + error envelope

**Files:**
- Create: `macos/AntaresKit/Sources/AntaresKit/APIClient.swift`
- Create: `macos/AntaresKit/Tests/AntaresKitTests/APIClientTests.swift`

Mirror `web/src/lib/api.ts`: `{error: string}` body, 401 vs 428.

- [ ] **Step 1: Failing tests**

```swift
func testDecodeErrorMessageFromJSONBody() {
    let body = #"{"error":"nope"}"#.data(using: .utf8)!
    let err = APIError(status: 400, body: body)
    XCTAssertEqual(err.message, "nope")
}

func test428IsDashboardPasswordRequired() {
    XCTAssertTrue(APIError(status: 428, body: Data()).isDashboardPasswordRequired)
    XCTAssertFalse(APIError(status: 401, body: Data()).isDashboardPasswordRequired)
}

func testJoinPathOntoBase() {
    let c = APIClient(baseURL: URL(string: "http://127.0.0.1:8787")!)
    XCTAssertEqual(c.url("/api/health").absoluteString, "http://127.0.0.1:8787/api/health")
}
```

- [ ] **Step 2: Run RED**

```bash
cd macos/AntaresKit && swift test --filter APIClientTests
```

- [ ] **Step 3: Minimal `APIClient`**

```swift
public struct APIError: Error {
    public var status: Int
    public var message: String
    public var isDashboardPasswordRequired: Bool { status == 428 }
    public var isUnauthorized: Bool { status == 401 }

    public init(status: Int, body: Data) {
        self.status = status
        if let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
           let e = obj["error"] as? String {
            self.message = e
        } else {
            self.message = HTTPURLResponse.localizedString(forStatusCode: status)
        }
    }
}

public final class APIClient: @unchecked Sendable {
    public let baseURL: URL
    public var bearerToken: String?
    public let session: URLSession

    public init(baseURL: URL, session: URLSession = .shared) {
        self.baseURL = baseURL
        self.session = session
    }

    public func url(_ path: String) -> URL {
        URL(string: path, relativeTo: baseURL)!
    }

    public func get<T: Decodable>(_ path: String) async throws -> T { try await send(path, method: "GET", body: nil as Data?) }
    public func post<T: Decodable, B: Encodable>(_ path: String, body: B) async throws -> T {
        try await send(path, method: "POST", body: JSONEncoder().encode(body))
    }

    private func send<T: Decodable>(_ path: String, method: String, body: Data?) async throws -> T {
        var req = URLRequest(url: url(path))
        req.httpMethod = method
        req.httpBody = body
        if body != nil { req.setValue("application/json", forHTTPHeaderField: "Content-Type") }
        if let bearerToken, !bearerToken.isEmpty {
            req.setValue("Bearer \(bearerToken)", forHTTPHeaderField: "Authorization")
        }
        let (data, resp) = try await session.data(for: req)
        let status = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard (200..<300).contains(status) else { throw APIError(status: status, body: data) }
        if T.self == Empty.self { return Empty() as! T }
        return try JSONDecoder().decode(T.self, from: data)
    }
}

public struct Empty: Decodable {}
```

Health GET can decode `[String: String]` or a small `Health` struct `{"ok":true}` — inspect `handleHealth` and match the real JSON.

- [ ] **Step 4: GREEN + commit**

```bash
cd macos/AntaresKit && swift test --filter APIClientTests
git add macos/AntaresKit
git commit -m "feat(macos): add AntaresKit HTTP client and error envelope"
```

---

### Task 4: Runtime supervisor (spawn vs attach)

**Files:**
- Create: `macos/AntaresKit/Sources/AntaresKit/RuntimeSupervisor.swift`
- Create: `macos/AntaresKit/Tests/AntaresKitTests/RuntimeSupervisorTests.swift`

Decision table only in unit tests (do not spawn a real binary here).

- [ ] **Step 1: Failing tests**

```swift
func testHealthyServerMeansAttach() {
    XCTAssertEqual(RuntimePlan.decide(healthOK: true, portBusy: false), .attach)
}

func testUnhealthyAndFreePortMeansSpawn() {
    XCTAssertEqual(RuntimePlan.decide(healthOK: false, portBusy: false), .spawn)
}

func testUnhealthyBusyPortIsError() {
    XCTAssertEqual(RuntimePlan.decide(healthOK: false, portBusy: true), .conflict)
}

func testSpawnedChildIsStoppedOnShutdownAttachedIsNot() {
    XCTAssertTrue(RuntimePlan.shouldStopChild(mode: .spawned))
    XCTAssertFalse(RuntimePlan.shouldStopChild(mode: .attached))
}
```

- [ ] **Step 2: RED, then implement**

```swift
public enum RuntimePlan {
    public enum Mode: Equatable { case attach, spawn, conflict }

    public static func decide(healthOK: Bool, portBusy: Bool) -> Mode {
        if healthOK { return .attach }
        if portBusy { return .conflict }
        return .spawn
    }

    public static func shouldStopChild(mode: Mode) -> Bool { mode == .spawned }
}
```

Wait: `shouldStopChild` uses `.spawned` but `Mode` has `.spawn`. Use one name:

```swift
public enum Mode: Equatable { case attached, spawned, conflict }
public static func decide(...) -> Mode {
    if healthOK { return .attached }
    if portBusy { return .conflict }
    return .spawned
}
public static func shouldStopChild(mode: Mode) -> Bool { mode == .spawned }
```

Fix the tests to match `.attached` / `.spawned`.

`RuntimeSupervisor` wraps `Process`: executable URL of bundled `antares`, arguments `["--foreground"]`, `environment["HOME"] = NSHomeDirectory()`, poll `GET /api/health` every 200ms up to 30s. Capture stderr into an actor-isolated `lastLog` string for `LaunchView`.

- [ ] **Step 3: GREEN + commit**

```bash
cd macos/AntaresKit && swift test --filter RuntimeSupervisorTests
git add macos/AntaresKit
git commit -m "feat(macos): decide spawn vs attach for the Go backend"
```

---

### Task 5: Chat SSE event reducer (TDD)

**Files:**
- Create: `macos/AntaresKit/Sources/AntaresKit/ChatEvents.swift`
- Create: `macos/AntaresKit/Tests/AntaresKitTests/ChatEventsTests.swift`

Match events in `docs/api.md`: `session`, `turn`, `text`, `reasoning`, `tool_call`, `tool_progress`, `tool_result`, `usage`, `notice`, `error`, `done`.

- [ ] **Step 1: Failing test**

```swift
func testTextDeltasAppendAndDoneMarksComplete() {
    var s = ChatTranscript()
    s.apply(["event": "session", "id": "s1", "title": "T"])
    s.apply(["event": "text", "delta": "Hel"])
    s.apply(["event": "text", "delta": "lo"])
    s.apply(["event": "done"])
    XCTAssertEqual(s.sessionID, "s1")
    XCTAssertEqual(s.assistantText, "Hello")
    XCTAssertTrue(s.done)
}
```

Inspect the real SSE frames from `handleChat` / `web/src/lib/useChatStream.ts` and decode the same field names (`event` vs `type`). The reducer must use the production wire format, not a guessed schema.

- [ ] **Step 2: Implement `ChatTranscript.apply`**, GREEN, commit.

```bash
git commit -m "feat(macos): reduce chat SSE events into transcript state"
```

---

### Task 6: SwiftUI app shell + launch gate

**Files:**
- Create: `macos/Antares/Antares/AntaresApp.swift`
- Create: `macos/Antares/Antares/AppModel.swift`
- Create: `macos/Antares/Antares/LaunchView.swift`
- Create: `macos/Antares/Antares/AppShell.swift`
- Create: `macos/Antares/Antares/SidebarItem.swift`
- Add local package reference to `../AntaresKit`

Xcode: File → New → Project → macOS App, bundle id `dev.enow.antares`, then add AntaresKit. Or generate with:

```bash
cd macos && mkdir -p Antares/Antares
```

`SidebarItem` is the static list matching `ROUTE_MANIFEST` ids (chat first).

- [ ] **Step 1: `SidebarItem.all` ordered like the spec table**

```swift
struct SidebarItem: Identifiable, Hashable {
    let id: String
    let title: String
    static let all: [SidebarItem] = [
        .init(id: "chat", title: "Chat"),
        .init(id: "sessions", title: "Sessions"),
        // … every id from the spec
        .init(id: "social-media", title: "Social media"),
    ]
}
```

- [ ] **Step 2: `AppModel.start()`**

```swift
@MainActor
final class AppModel: ObservableObject {
    @Published var phase: Phase = .launching
    enum Phase { case launching, login, setup, ready, failed(String) }

    let client = APIClient(baseURL: AntaresKit.defaultBaseURL)
    let runtime = RuntimeSupervisor()

    func start() async {
        do {
            try await runtime.start(binary: Bundle.main.url(forAuxiliaryExecutable: "antares"))
            // GET /api/setup/status then /api/auth/status → set phase
            phase = .ready
        } catch {
            phase = .failed(error.localizedDescription)
        }
    }
}
```

`AntaresApp`:

```swift
@main
struct AntaresApp: App {
    @StateObject private var model = AppModel()
    var body: some Scene {
        WindowGroup {
            switch model.phase {
            case .launching: LaunchView()
            case .login: LoginView()
            case .setup: SetupView()
            case .ready: AppShell()
            case .failed(let m): ContentUnavailableView("Backend", body: Text(m))
            }
        }
        .environmentObject(model)
    }
}
```

`AppShell`: `NavigationSplitView` sidebar `List(SidebarItem.all)` and `detail` switch on `selection`. Unimplemented pages show `PageStub(id:)`.

- [ ] **Step 3: Manual check** — run the app without a binary: LaunchView shows spawn error. With `antares --foreground` already on :8787: attach, skip spawn.

- [ ] **Step 4: Commit**

```bash
git add macos/Antares
git commit -m "feat(macos): add SwiftUI app shell and backend launch gate"
```

---

### Task 7: Bundle Go binary (`make macos`)

**Files:**
- Modify: `Makefile`
- Modify: `docs/getting-started.md`
- Modify: `README.md` (one line under Two interfaces)

- [ ] **Step 1: Makefile target**

```make
.PHONY: macos
macos: build ## Build Antares.app with the darwin antares binary inside
	@test "$(shell uname -s)" = Darwin || (echo "macos target requires Darwin" && exit 1)
	@xcodebuild -project macos/Antares/Antares.xcodeproj -scheme Antares \
	  -configuration Release -derivedDataPath macos/build \
	  ANTARES_GO_BIN=$(CURDIR)/bin/antares
	@cp bin/antares macos/build/Build/Products/Release/Antares.app/Contents/MacOS/antares
	@echo "built macos/build/Build/Products/Release/Antares.app"
```

Copy step can also be an Xcode Run Script Build Phase: `cp "${ANTARES_GO_BIN}" "${BUILT_PRODUCTS_DIR}/${EXECUTABLE_FOLDER_PATH}/antares"`. Prefer the script so Debug runs from Xcode work: it should `make -C ../.. build` or copy `../../bin/antares`.

- [ ] **Step 2: Document** in `docs/getting-started.md`:

```markdown
## macOS app

```bash
make macos
open macos/build/Build/Products/Release/Antares.app
```

The app starts `antares --foreground` if nothing is listening on :8787.
Linux and Windows still use the web dashboard.
```

- [ ] **Step 3: Commit**

```bash
git add Makefile docs/getting-started.md README.md macos/Antares
git commit -m "build: bundle the Go binary inside Antares.app"
```

---

### Task 8: Login + setup

**Files:**
- Create: `macos/Antares/Antares/LoginView.swift`
- Create: `macos/Antares/Antares/SetupView.swift`

Endpoints: `GET /api/auth/status`, `POST /api/auth/login`, `POST /api/auth/password`, `GET /api/setup/status`, `POST /api/setup/test`, `POST /api/setup/complete`.

- [ ] **Step 1: Codable structs matching the web pages** (`LoginPage.tsx`, `SetupPage.tsx`). Decode fixture JSON in Kit tests.

- [ ] **Step 2: SwiftUI forms.** 428 → password bootstrap. Setup incomplete → provider + key, same fields as web.

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(macos): native login and first-run setup"
```

---

### Task 9: Chat page (parity with ChatPage)

**Files:**
- Create: `macos/Antares/Antares/Pages/ChatView.swift`
- Create: `macos/Antares/Antares/Pages/ChatComposer.swift`
- Create: `macos/Antares/Antares/Pages/MessageViews.swift`

Use `ChatTranscript` from Task 5. `POST /api/chat`, `POST /api/chat/interrupt`, `GET /api/sessions/{id}`, `GET /api/chat/attach`, `POST /api/upload`, `POST /api/sessions/{id}/edit`.

Sticky prompt behaviour (clip after first line when docked, 200ms, no fade on one line, inner gradient) is a SwiftUI overlay on the last user bubble — optional v1 polish; v1 must stream text and tools.

- [ ] **Step 1: Kit tests for edit + interrupt request bodies.**

- [ ] **Step 2: `ChatView`** `ScrollView` + composer. Virtualization (`List`) if a session exceeds a few hundred messages.

- [ ] **Step 3: Manual:** send a message against a live backend, interrupt, reload session.

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(macos): native chat with SSE streaming"
```

---

### Task 10: Sessions, providers, tools, memory

**Files:**
- Create: `macos/Antares/Antares/Pages/SessionsView.swift`
- Create: `macos/Antares/Antares/Pages/ProvidersView.swift`
- Create: `macos/Antares/Antares/Pages/ToolsView.swift`
- Create: `macos/Antares/Antares/Pages/MemoryView.swift`

Endpoints from `docs/api.md` (sessions list/search/delete, model options/set, tools toggle, memory CRUD/search).

Each view: `task { try await client.get(...) }`, `List`, error `ContentUnavailableView`.

Replace `PageStub` for these ids in `AppShell`.

- [ ] **Step 1: Decode one fixture per DTO in Kit tests.**

- [ ] **Step 2: Wire lists + primary actions (delete session, set model, toggle tool, add/delete memory).**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(macos): sessions, providers, tools, and memory screens"
```

---

### Task 11: Roles, soul, skills, cron, channels

**Files:**
- Create: `macos/Antares/Antares/Pages/RolesView.swift`
- Create: `macos/Antares/Antares/Pages/SoulView.swift`
- Create: `macos/Antares/Antares/Pages/SkillsView.swift`
- Create: `macos/Antares/Antares/Pages/CronView.swift`
- Create: `macos/Antares/Antares/Pages/ChannelsView.swift`

Read the matching `web/src/pages/*Page.tsx` and `internal/server/routes.go` before coding. Cron: list/create/toggle/run. Channels: token + pairing approve/revoke. Skills: hub install + toggle. Soul: GET/POST body. Roles: list + session role.

- [ ] **Step 1: DTO tests.**

- [ ] **Step 2: Screens + `AppShell` switch cases.**

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(macos): roles, soul, skills, cron, and channels screens"
```

---

### Task 12: Autopilot, engagement, board

**Files:**
- Create: `macos/Antares/Antares/Pages/AutopilotView.swift`
- Create: `macos/Antares/Antares/Pages/EngagementView.swift`
- Create: `macos/Antares/Antares/Pages/BoardView.swift`

Board: `LazyVStack` columns; drag-and-drop via `.draggable` / `.dropDestination` if the web page supports move. If the API is status-only, show columns without DnD.

- [ ] Commit: `feat(macos): autopilot, engagement, and board screens`

---

### Task 13: Intercept + MCP + plugins + proxies

**Files:**
- Create: `macos/Antares/Antares/Pages/InterceptView.swift`
- Create: `macos/Antares/Antares/Pages/MCPView.swift`
- Create: `macos/Antares/Antares/Pages/PluginsView.swift`
- Create: `macos/Antares/Antares/Pages/ProxiesView.swift`

Intercept: start/stop, exchanges table, rules, breakpoint SSE (`GET /api/intercept/breakpoints/stream`). MCP: `GET /api/mcp`, hub install. Plugins/proxies: match web pages’ endpoints in `routes.go`.

- [ ] Commit: `feat(macos): intercept, MCP, plugins, and proxies screens`

---

### Task 14: VPS, analytics, files, logs, system

**Files:**
- Create: `macos/Antares/Antares/Pages/VPSView.swift`
- Create: `macos/Antares/Antares/Pages/AnalyticsView.swift`
- Create: `macos/Antares/Antares/Pages/FilesView.swift`
- Create: `macos/Antares/Antares/Pages/LogsView.swift`
- Create: `macos/Antares/Antares/Pages/SystemView.swift`

Logs: `GET /api/logs` then `GET /api/logs/stream` via `SSEClient`. Files: `GET /api/files`, `GET /api/files/read`. Analytics: `GET /api/analytics` (Swift Charts). VPS: poll `system`/VPS routes used by `VPSPage.tsx`. System: `GET /api/system/stats`, `GET /api/status`.

- [ ] Commit: `feat(macos): VPS, analytics, files, logs, and system screens`

---

### Task 15: Config (schema-driven)

**Files:**
- Create: `macos/Antares/Antares/Pages/ConfigView.swift`
- Create: `macos/AntaresKit/Sources/AntaresKit/ConfigSchema.swift`
- Test: `macos/AntaresKit/Tests/AntaresKitTests/ConfigSchemaTests.swift`

`GET /api/config/schema` drives the web Settings form. Decode schema, render `Form` sections, `POST /api/config` per path. Raw YAML tab: `GET/POST /api/config/raw`. Appearance palettes stay web-only until a later assets task.

- [ ] **Step 1: Test that unknown field types fall back to a string field (do not crash).**

- [ ] **Step 2: Build the form.**

- [ ] **Step 3: Commit** `feat(macos): schema-driven config screen`

---

### Task 16: Content creator + social media

**Files:**
- Create: `macos/Antares/Antares/Pages/ContentCreatorView.swift`
- Create: `macos/Antares/Antares/Pages/SocialMediaView.swift`

Creator endpoints in `docs/api.md` (projects CRUD, action, run, artifact, settings). Social: match `SocialMediaPage.tsx` routes (`/api/social/...` in `routes.go`).

- [ ] Commit: `feat(macos): content creator and social media screens`

---

### Task 17: Sidebar complete + smoke checklist

**Files:**
- Modify: `macos/Antares/Antares/AppShell.swift` — no `PageStub` remains
- Create: `docs/superpowers/plans/macos-dashboard-smoke.md` OR a section at the bottom of this plan used as the test plan

- [ ] **Step 1: Compile-time exhaustiveness** — `switch selection.id` must be exhaustive over `SidebarItem.all`.

- [ ] **Step 2: Manual smoke against a live `antares --foreground`:** login/setup, every sidebar row loads without a crash, chat send+stream, config GET.

- [ ] **Step 3: Commit** `feat(macos): complete native sidebar coverage`

---

### Task 18: Docs + HANDOFF

**Files:**
- Modify: `README.md`
- Modify: `docs/getting-started.md`
- Modify: `docs/architecture.md` (add `macos/` next to `web/`)
- Modify: `HANDOFF.md` if it still says the dashboard is React-only on Mac

- [ ] State clearly: Mac app is optional; CLI `antares` still serves the web UI for other platforms.

- [ ] Commit: `docs: document the macOS SwiftUI dashboard`

---

## Out of scope (later)

- getdesign palettes in Asset catalogs
- `surface=macos` command catalogue
- `antares://` deep links
- App Store sandbox
- Deleting `web/`

---

## Execution notes

Foundation (Tasks 1–7) must land before page tasks. Tasks 10–16 can proceed in parallel after Task 9 once `AppShell` + `APIClient` exist. Each page task starts by reading the corresponding `web/src/pages/*Page.tsx` and the routes in `internal/server/routes.go` so DTOs match production JSON.
