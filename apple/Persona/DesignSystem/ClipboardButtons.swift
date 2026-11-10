import SwiftUI
import UniformTypeIdentifiers

/// The system Paste button, drawn the design system's way: a capsule in the accent, its icon and the system's own word.
/// It hands over what is on the clipboard without iOS asking "Allow Paste?" (the tap is the permission), so it sits beside
/// a button that reads the clipboard itself (#3064). Text, images and links; `action` gets them as one clipboard content,
/// nil when none could be read.
struct SystemPasteButton: View {
    var action: (ClipboardContent?) async -> Void

    static let types: [UTType] = [.plainText, .image, .url]

    var body: some View {
        PasteButton(supportedContentTypes: Self.types) { providers in
            Task { @MainActor in await action(await PhonePasteboard.content(of: providers)) }
        }
        .labelStyle(.titleAndIcon)
        .buttonBorderShape(.capsule)
        .tint(Theme.accent)
        .lineLimit(1)
        .accessibilityIdentifier("system-paste")
    }
}
