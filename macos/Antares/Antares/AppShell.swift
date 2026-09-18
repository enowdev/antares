import SwiftUI

struct AppShell: View {
    @State private var selection: SidebarItem? = SidebarItem.all.first

    var body: some View {
        NavigationSplitView {
            List(SidebarItem.all, selection: $selection) { item in
                Text(item.title)
                    .tag(item)
            }
            .navigationTitle("Antares")
        } detail: {
            detailView(for: selection ?? SidebarItem.all[0])
        }
        .frame(minWidth: 900, minHeight: 560)
    }

    @ViewBuilder
    private func detailView(for item: SidebarItem) -> some View {
        switch item.id {
        case "chat":
            ChatView()
        case "sessions":
            PageStub(id: item.id)
        case "providers":
            PageStub(id: item.id)
        case "tools":
            PageStub(id: item.id)
        case "memory":
            PageStub(id: item.id)
        case "roles":
            PageStub(id: item.id)
        case "soul":
            PageStub(id: item.id)
        case "skills":
            PageStub(id: item.id)
        case "cron":
            PageStub(id: item.id)
        case "channels":
            PageStub(id: item.id)
        case "autopilot":
            PageStub(id: item.id)
        case "engagement":
            PageStub(id: item.id)
        case "board":
            PageStub(id: item.id)
        case "intercept":
            PageStub(id: item.id)
        case "mcp":
            PageStub(id: item.id)
        case "plugins":
            PageStub(id: item.id)
        case "proxies":
            PageStub(id: item.id)
        case "vps":
            PageStub(id: item.id)
        case "analytics":
            PageStub(id: item.id)
        case "files":
            PageStub(id: item.id)
        case "logs":
            PageStub(id: item.id)
        case "config":
            PageStub(id: item.id)
        case "system":
            PageStub(id: item.id)
        case "content-creator":
            PageStub(id: item.id)
        case "social-media":
            PageStub(id: item.id)
        default:
            PageStub(id: item.id)
        }
    }
}
