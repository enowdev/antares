import SwiftUI

@main
struct AntaresApp: App {
    @StateObject private var model = AppModel()

    var body: some Scene {
        WindowGroup {
            rootView
                .environmentObject(model)
                .task {
                    await model.start()
                }
        }
    }

    @ViewBuilder
    private var rootView: some View {
        switch model.phase {
        case .launching:
            LaunchView()
        case .login:
            LoginView()
        case .setup:
            SetupView()
        case .ready:
            AppShell()
        case .failed(let message):
            ContentUnavailableView(
                "Backend",
                systemImage: "exclamationmark.triangle",
                description: Text(message)
            )
        }
    }
}
