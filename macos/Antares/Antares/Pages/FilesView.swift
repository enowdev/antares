import AntaresKit
import AppKit
import SwiftUI

struct FilesView: View {
    @EnvironmentObject private var model: AppModel

    @State private var path = "."
    @State private var listing: FileListResponse?
    @State private var preview: FilePreview?
    @State private var errorMessage: String?
    @State private var isLoading = false
    @State private var loadingPreview = false

    var body: some View {
        Group {
            if let errorMessage, listing == nil, !isLoading {
                ContentUnavailableView(
                    "Files",
                    systemImage: "exclamationmark.triangle",
                    description: Text(errorMessage)
                )
            } else {
                VStack(alignment: .leading, spacing: 0) {
                    breadcrumb
                    if let errorMessage {
                        Text(errorMessage)
                            .foregroundStyle(.red)
                            .padding(.horizontal, 16)
                            .padding(.top, 8)
                    }
                    HSplitView {
                        fileList
                            .frame(minWidth: 280)
                        previewPane
                            .frame(minWidth: 320)
                    }
                }
            }
        }
        .overlay {
            if isLoading { ProgressView("Loading files…") }
        }
        .navigationTitle("Files")
        .toolbar {
            ToolbarItem(placement: .automatic) {
                Button("Home") {
                    path = "."
                    preview = nil
                }
            }
            ToolbarItem(placement: .automatic) {
                Button("Up") {
                    if let parent = listing?.parent, listing?.path != "." {
                        path = parent.isEmpty ? "." : parent
                        preview = nil
                    }
                }
                .disabled(listing == nil || listing?.path == "." || listing?.parent.isEmpty != false)
            }
        }
        .task(id: path) { await load() }
        .refreshable { await load() }
    }

    private var breadcrumb: some View {
        HStack(spacing: 4) {
            Button("Workspace") {
                path = "."
                preview = nil
            }
            .buttonStyle(.plain)
            ForEach(crumbs(listing?.path ?? path), id: \.path) { crumb in
                Image(systemName: "chevron.right")
                    .font(.caption2)
                    .foregroundStyle(.tertiary)
                Button(crumb.name) {
                    path = crumb.path
                    preview = nil
                }
                .buttonStyle(.plain)
                .font(.body.monospaced())
            }
            Spacer()
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private var fileList: some View {
        let entries = listing?.entries ?? []
        if !isLoading && entries.isEmpty {
            ContentUnavailableView("Empty Folder", systemImage: "folder")
        } else {
            List(entries, selection: Binding(
                get: { preview?.entry.path },
                set: { _ in }
            )) { entry in
                Button {
                    Task { await open(entry) }
                } label: {
                    HStack {
                        Image(systemName: entry.isDir ? "folder.fill" : "doc")
                            .foregroundStyle(entry.isDir ? Color.accentColor : Color.secondary)
                        Text(entry.name)
                            .lineLimit(1)
                        Spacer()
                        if !entry.isDir {
                            Text(formatBytes(entry.size))
                                .font(.caption.monospacedDigit())
                                .foregroundStyle(.secondary)
                        }
                    }
                }
                .buttonStyle(.plain)
            }
        }
    }

    @ViewBuilder
    private var previewPane: some View {
        if let preview {
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Text(preview.entry.name)
                        .font(.headline.monospaced())
                        .lineLimit(1)
                    Spacer()
                    Text(formatBytes(preview.entry.size))
                        .font(.caption)
                        .foregroundStyle(.secondary)
                    Button("Close") { self.preview = nil }
                }
                .padding(12)
                Divider()
                if loadingPreview {
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let error = preview.error {
                    Text(error)
                        .foregroundStyle(.red)
                        .padding()
                } else if let image = preview.image {
                    Image(nsImage: image)
                        .resizable()
                        .scaledToFit()
                        .frame(maxWidth: .infinity, maxHeight: .infinity)
                        .padding()
                } else if preview.binary {
                    ContentUnavailableView(
                        "Binary File",
                        systemImage: "doc.zipper",
                        description: Text("This file is not shown as text.")
                    )
                } else {
                    ScrollView {
                        Text(preview.content)
                            .font(.system(.caption, design: .monospaced))
                            .textSelection(.enabled)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .padding(12)
                    }
                }
            }
        } else {
            ContentUnavailableView(
                "Select a File",
                systemImage: "doc.text",
                description: Text("Choose a file to preview its contents.")
            )
        }
    }

    private func load() async {
        isLoading = listing == nil
        errorMessage = nil
        do {
            listing = try await model.client.listFiles(path: path)
        } catch {
            errorMessage = (error as? APIError)?.message ?? error.localizedDescription
        }
        isLoading = false
    }

    private func open(_ entry: FileEntry) async {
        if entry.isDir {
            path = entry.path
            preview = nil
            return
        }
        loadingPreview = true
        preview = FilePreview(entry: entry)
        defer { loadingPreview = false }
        let ext = (entry.name as NSString).pathExtension.lowercased()
        let imageExts: Set<String> = ["png", "jpg", "jpeg", "gif", "webp", "bmp", "ico"]
        do {
            if imageExts.contains(ext) {
                let data = try await model.client.getData(
                    "/api/files/raw",
                    query: [URLQueryItem(name: "path", value: entry.path)]
                )
                let image = NSImage(data: data)
                preview = FilePreview(entry: entry, binary: image == nil, image: image)
                return
            }
            let read = try await model.client.readFile(path: entry.path)
            preview = FilePreview(entry: entry, content: read.content, binary: read.binary)
        } catch {
            preview = FilePreview(
                entry: entry,
                error: (error as? APIError)?.message ?? error.localizedDescription
            )
        }
    }

    private func crumbs(_ p: String) -> [(name: String, path: String)] {
        if p.isEmpty || p == "." || p == "/" { return [] }
        let parts = p.split(separator: "/").map(String.init).filter { $0 != "." && !$0.isEmpty }
        var acc = ""
        var out: [(name: String, path: String)] = []
        for part in parts {
            acc = acc.isEmpty ? part : "\(acc)/\(part)"
            out.append((part, acc))
        }
        return out
    }

    private func formatBytes(_ size: Int) -> String {
        let value = Double(size)
        if value >= 1_048_576 { return String(format: "%.1f MB", value / 1_048_576) }
        if value >= 1024 { return String(format: "%.1f KB", value / 1024) }
        return "\(size) B"
    }
}

private struct FilePreview {
    var entry: FileEntry
    var content: String = ""
    var binary: Bool = false
    var image: NSImage?
    var error: String?
}
