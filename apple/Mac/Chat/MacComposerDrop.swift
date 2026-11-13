import SwiftUI
import UniformTypeIdentifiers

/// Files dragged onto a composer (docs/apple/mac.md「Details」): pictures go into the message as images (as the + menu's Photos
/// does), everything else is uploaded to the session's machine and its path typed in (as Files does).
nonisolated enum MacDrop {
    /// The + menu's photo limit per message.
    static let maxImages = 4

    /// Splits dropped URLs into pictures to attach and files to upload. `already` is how many pictures the composer
    /// holds; pictures past the limit are uploaded as files instead of dropped. Non-file URLs (a dragged web link) are ignored;
    /// folders are counted, not uploaded (an upload is one file).
    static func split(_ urls: [URL], already: Int = 0) -> (images: [URL], files: [URL], folders: Int) {
        var images: [URL] = []
        var files: [URL] = []
        var folders = 0
        for url in urls where url.isFileURL {
            if url.hasDirectoryPath || (try? url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true {
                folders += 1
            } else if isPicture(url), already + images.count < maxImages {
                images.append(url)
            } else {
                files.append(url)
            }
        }
        return (images, files, folders)
    }

    static func isPicture(_ url: URL) -> Bool {
        guard !url.pathExtension.isEmpty, let type = UTType(filenameExtension: url.pathExtension) else { return false }
        return type.conforms(to: .image)
    }
}

extension MacDrop {
    /// Files dropped or pasted on a composer holding `images` pictures: pictures read and `attach`ed, other files
    /// handed to `upload` (the composer's FileUploads, which types the path in), folders refused with a toast.
    @MainActor @discardableResult
    static func take(_ urls: [URL], images: Int, attach: (UIImage) -> Void, upload: ([URL]) -> Void) -> Bool {
        let split = split(urls, already: images)
        if split.folders > 0 { AppModel.shared.toast = Toast(String(localized: "Folders can't be sent. Drag in the files inside instead."), style: .error) }
        guard !split.images.isEmpty || !split.files.isEmpty else { return split.folders > 0 }
        // A picture NSImage cannot read is uploaded as a file, not dropped.
        var unreadable: [URL] = []
        for url in split.images {
            if let img = UIImage(contentsOf: url) { attach(img) } else { unreadable.append(url) }
        }
        if !(split.files + unreadable).isEmpty { upload(split.files + unreadable) }
        return true
    }
}

extension MacDrop {
    /// A picture as the composers send it: at most 1600 px on the long side, JPEG (the + menu's Photos does the same).
    @MainActor static func payload(_ img: UIImage) -> (UIImage, ImagePayload)? {
        let scaled = img.scaledDown(maxSide: 1600)
        guard let jpeg = scaled.jpegData(compressionQuality: 0.75) else { return nil }
        return (scaled, ImagePayload(mime: "image/jpeg", data: jpeg.base64EncodedString()))
    }

    /// Adds a pasted / dropped picture to a starter composer's pictures, with the Agent composer's「一次最多 4 张」.
    @MainActor static func append(_ img: UIImage, to images: inout [(UIImage, ImagePayload)]) {
        guard images.count < maxImages else {
            AppModel.shared.toast = Toast(String(localized: "Up to 4 at a time"), style: .error)
            return
        }
        if let p = payload(img) { images.append(p) }
    }
}

extension View {
    /// Drop and ⌘V of pictures and files on a composer (see `MacDrop.take`).
    func macComposerFiles(images: Int, focused: Bool, attach: @escaping (UIImage) -> Void,
                          upload: @escaping ([URL]) -> Void) -> some View {
        dropDestination(for: URL.self) { urls, _ in MacDrop.take(urls, images: images, attach: attach, upload: upload) }
            .modifier(MacComposerPaste(focused: focused, image: attach, files: {
                MacDrop.take($0, images: images, attach: attach, upload: upload)
            }))
    }
}

extension View {
    /// Files dropped anywhere on a chat's page (not only on its composer, #2113) go to its composer
    /// (`AppModel.composerDrops`), with a 「松手添加到消息」 cover while they are over it.
    func macChatDrop(_ key: SessionKey) -> some View { modifier(MacChatDrop(key: key)) }
}

private struct MacChatDrop: ViewModifier {
    let key: SessionKey
    @State private var over = false

    func body(content: Content) -> some View {
        content
            .dropDestination(for: URL.self) { urls, _ in
                let files = urls.filter(\.isFileURL)
                guard !files.isEmpty else { return false }
                AppModel.shared.composerDrops[key, default: []] += files
                return true
            } isTargeted: { over = $0 }
            .overlay {
                if over || MacChatDrop.showCover {
                    ZStack {
                        RoundedRectangle(cornerRadius: Theme.Radius.panel, style: .continuous)
                            .fill(Theme.accent.opacity(0.08))
                        RoundedRectangle(cornerRadius: Theme.Radius.panel, style: .continuous)
                            .strokeBorder(Theme.accent, style: StrokeStyle(lineWidth: Theme.Size.line, dash: [Theme.Space.sm, Theme.Space.xs]))
                        Label("Drop to add to the message", systemImage: Theme.Icon.attachment)
                            .font(Theme.Font.heading)
                            .foregroundStyle(Theme.accent)
                            .lineLimit(1)
                    }
                    .padding(Theme.Space.m)
                    .allowsHitTesting(false)
                    .transition(.opacity)
                }
            }
            .animation(Theme.Motion.standard, value: over)
    }

    /// `-showMacDropCover YES` (DEBUG): the cover as if files were over the page, for screenshots.
    #if DEBUG
    static let showCover = UserDefaults.standard.bool(forKey: "showMacDropCover")
    #else
    static let showCover = false
    #endif
}

/// What ⌘V in a composer does with the pasteboard.
nonisolated enum MacPaste {
    enum Plan: Equatable {
        case text
        case image
        case files([URL])
    }

    /// Files first (Finder's ⌘C also puts each file's icon on the pasteboard as an image), then text (Numbers, Excel,
    /// PDF viewers copy text with a picture of it), and a picture only when it is all there is (a screenshot).
    static func plan(fileURLs: [URL], hasImage: Bool, hasText: Bool) -> Plan {
        if !fileURLs.isEmpty { return .files(fileURLs) }
        return hasImage && !hasText ? .image : .text
    }

    @MainActor static func current(_ board: NSPasteboard = .general) -> Plan {
        let urls = board.readObjects(forClasses: [NSURL.self], options: [.urlReadingFileURLsOnly: true]) as? [URL] ?? []
        return plan(fileURLs: urls, hasImage: NSImage.canInit(with: board), hasText: board.string(forType: .string) != nil)
    }
}

/// ⌘V while this composer has focus: files and pictures are taken here; text goes on to the field as usual (the
/// focused SwiftUI text field takes `paste:` itself and drops pictures, so this is a key monitor).
struct MacComposerPaste: ViewModifier {
    var focused: Bool
    var image: (NSImage) -> Void
    var files: ([URL]) -> Void

    /// Whether `event` is the plain ⌘V shortcut. Caps Lock is not a modifier here (it is not in the intersection and
    /// charactersIgnoringModifiers keeps the uppercase "V" it produces, so the character is compared case-insensitively);
    /// shift still disqualifies the shortcut, since ⌘⇧V means something else.
    static func isPasteShortcut(_ event: NSEvent) -> Bool {
        guard event.modifierFlags.intersection([.command, .shift, .option, .control]) == .command else { return false }
        return (event.charactersIgnoringModifiers ?? "").lowercased() == "v"
    }

    func body(content: Content) -> some View {
        content.modifier(MacComposerEventMonitor(focused: focused) { event in
            guard Self.isPasteShortcut(event) else { return event }
            switch MacPaste.current() {
            case .text: return event
            case .image:
                guard let img = NSImage(pasteboard: .general) else { return event }
                image(img)
                return nil
            case .files(let urls):
                files(urls)
                return nil
            }
        })
    }
}
