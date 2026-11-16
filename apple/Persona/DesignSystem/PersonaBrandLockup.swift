import SwiftUI

/// Persona's mark wherever the app signs a picture (#9468): the assistant's face, its name and "(Persona)" after it,
/// quiet. One component for every place, so the exported picture's footer and the screenshot banner look the same.
/// `.mark` is the small signature at the foot of an exported picture; `.banner`, the larger one over a screenshot, is
/// drawn the same way at its own size (it falls back to `.mark` until it has one).
struct PersonaBrandLockup: View {
    enum Size {
        case mark, banner
    }

    @Environment(AppModel.self) private var model
    var size: Size = .mark
    /// The name shown: the assistant's (`AppModel.assistantShownName`) unless given.
    var name: String?

    var body: some View {
        let shown = name ?? model.assistantShownName
        HStack(spacing: Theme.Space.xs) {
            PersonaAvatar(size: Theme.Size.badge)
            Text(verbatim: shown)
                .font(Theme.Font.labelStrong)
                .foregroundStyle(.secondary)
            // Its name already says Persona: not "Persona (Persona)".
            if shown.caseInsensitiveCompare(Self.product) != .orderedSame {
                Text("(Persona) [Brand Lockup]")
                    .font(Theme.Font.label)
                    .foregroundStyle(.tertiary)
            }
        }
        .lineLimit(1)
        .accessibilityElement(children: .combine)
    }

    /// The product's own name, never translated.
    static let product = "Persona"
}
