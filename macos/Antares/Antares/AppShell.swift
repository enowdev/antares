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
            SessionsView {
                selection = SidebarItem.all.first { $0.id == "chat" }
            }
        case "providers":
            ProvidersView()
        case "tools":
            ToolsView()
        case "memory":
            MemoryView()
        case "roles":
            RolesView()
        case "soul":
            SoulView()
        case "skills":
            SkillsView()
        case "cron":
            CronView()
        case "channels":
            ChannelsView()
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
