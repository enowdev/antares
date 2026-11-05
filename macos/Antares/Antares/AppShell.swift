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
        switch item.route {
        case .chat:
            ChatView()
        case .sessions:
            SessionsView {
                selection = SidebarItem.all.first { $0.id == "chat" }
            }
        case .providers:
            ProvidersView()
        case .tools:
            ToolsView()
        case .memory:
            MemoryView()
        case .roles:
            RolesView()
        case .soul:
            SoulView()
        case .skills:
            SkillsView()
        case .cron:
            CronView()
        case .channels:
            ChannelsView()
        case .autopilot:
            AutopilotView()
        case .engagement:
            EngagementView()
        case .board:
            BoardView()
        case .intercept:
            InterceptView()
        case .mcp:
            MCPView()
        case .plugins:
            PluginsView()
        case .proxies:
            ProxiesView()
        case .vps:
            VPSView()
        case .analytics:
            AnalyticsView()
        case .files:
            FilesView()
        case .logs:
            LogsView()
        case .config:
            ConfigView()
        case .system:
            SystemView()
        case .contentCreator:
            ContentCreatorView()
        case .socialMedia:
            SocialMediaView()
        }
    }
}
