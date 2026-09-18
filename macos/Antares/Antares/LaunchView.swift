import SwiftUI

struct LaunchView: View {
    var body: some View {
        VStack(spacing: 12) {
            ProgressView()
            Text("Starting Antares backend...")
                .foregroundStyle(.secondary)
        }
        .frame(minWidth: 420, minHeight: 260)
        .padding()
    }
}
