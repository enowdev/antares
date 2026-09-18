import SwiftUI

struct PageStub: View {
    let id: String

    var body: some View {
        VStack(spacing: 8) {
            Text(id)
                .font(.title2)
                .fontWeight(.semibold)
            Text("Page stub")
                .foregroundStyle(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .padding()
    }
}
