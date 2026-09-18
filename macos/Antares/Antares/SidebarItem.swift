import Foundation

struct SidebarItem: Identifiable, Hashable {
    let id: String
    let title: String

    static let all: [SidebarItem] = [
        SidebarItem(id: "chat", title: "Chat"),
        SidebarItem(id: "sessions", title: "Sessions"),
        SidebarItem(id: "providers", title: "Providers"),
        SidebarItem(id: "tools", title: "Tools"),
        SidebarItem(id: "memory", title: "Memory"),
        SidebarItem(id: "roles", title: "Roles"),
        SidebarItem(id: "soul", title: "Soul"),
        SidebarItem(id: "skills", title: "Skills"),
        SidebarItem(id: "cron", title: "Cron"),
        SidebarItem(id: "channels", title: "Channels"),
        SidebarItem(id: "autopilot", title: "Autopilot"),
        SidebarItem(id: "engagement", title: "Engagement"),
        SidebarItem(id: "board", title: "Board"),
        SidebarItem(id: "intercept", title: "Intercept"),
        SidebarItem(id: "mcp", title: "MCP"),
        SidebarItem(id: "plugins", title: "Plugins"),
        SidebarItem(id: "proxies", title: "Proxies"),
        SidebarItem(id: "vps", title: "VPS"),
        SidebarItem(id: "analytics", title: "Analytics"),
        SidebarItem(id: "files", title: "Files"),
        SidebarItem(id: "logs", title: "Logs"),
        SidebarItem(id: "config", title: "Config"),
        SidebarItem(id: "system", title: "System"),
        SidebarItem(id: "content-creator", title: "Content creator"),
        SidebarItem(id: "social-media", title: "Social media"),
    ]
}
