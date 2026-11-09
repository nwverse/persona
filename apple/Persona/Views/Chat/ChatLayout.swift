import SwiftUI

// The new conversation layout of Persona's main conversation (#1666, docs/apple/ios.md "New conversation layout"):
// Settings › Appearance › Conversation. On (the default), who talks to whom reads at a glance: Persona's messages to a
// task read "Persona → @task", an agent's reports "task → @Persona", supervision and scheduled runs "Persona → @Persona";
// every message sits in a box; what Persona and its agents say to each other folds; a slide along the right edge goes to
// any message the user typed. Off, the conversation looks as it did. Only Persona's conversations on iPhone and iPad:
// the Agents chat, the Mac and Vision Pro keep their look. Reopening where it was left is not part of it (every chat).

/// How much of what Persona and a task's agent say to each other shows before Expand.
nonisolated enum ChatFold: String, CaseIterable, Identifiable, Sendable {
    /// Only who talks to whom; the words open with a tap.
    case folded
    /// The first lines (`ChatLayout.lines`).
    case lines
    /// All of it.
    case expanded

    var id: String { rawValue }
}

/// What a chat draws with (the environment's `chatLayout`): off everywhere but Persona's conversations on iPhone / iPad.
nonisolated struct ChatLayout: Equatable, Sendable {
    var on: Bool
    var fold: ChatFold = .lines
    var lines: Int = ChatLayoutSettings.defaultLines

    static let off = ChatLayout(on: false)

    /// The lines an exchange shows folded: nil for all of it, 0 for none.
    var shownLines: Int? {
        switch fold {
        case .folded: 0
        case .lines: lines
        case .expanded: nil
        }
    }
}

extension EnvironmentValues {
    @Entry var chatLayout: ChatLayout = .off
}

/// The choice, kept in UserDefaults (`ui.newChatLayout`, a key of its own: not the shell's Layout switch). Read once,
/// observed by the chat page only: rows get the result through the environment, so no row watches UserDefaults.
@MainActor @Observable final class ChatLayoutSettings {
    static let shared = ChatLayoutSettings()

    nonisolated static let key = "ui.newChatLayout"
    nonisolated static let foldKey = "ui.newChatLayout.fold"
    nonisolated static let linesKey = "ui.newChatLayout.lines"
    nonisolated static let defaultLines = 3
    nonisolated static let lineRange = 1...10

    @ObservationIgnored private let defaults: UserDefaults

    var on: Bool { didSet { defaults.set(on, forKey: Self.key) } }
    var fold: ChatFold { didSet { defaults.set(fold.rawValue, forKey: Self.foldKey) } }
    var lines: Int { didSet { defaults.set(lines, forKey: Self.linesKey) } }

    init(defaults: UserDefaults = .standard) {
        self.defaults = defaults
        // On unless turned off (`-ui.newChatLayout NO` in a launch argument is a string: `bool(forKey:)` reads it).
        on = defaults.object(forKey: Self.key) == nil ? true : defaults.bool(forKey: Self.key)
        fold = defaults.string(forKey: Self.foldKey).flatMap(ChatFold.init(rawValue:)) ?? .lines
        let n = defaults.integer(forKey: Self.linesKey)
        lines = Self.lineRange.contains(n) ? n : Self.defaultLines
    }

    /// The layout a chat draws with: the new one only in Persona's conversations (`assistant`) on iPhone and iPad.
    func layout(assistant: Bool) -> ChatLayout {
        #if os(iOS)
        guard assistant, on else { return .off }
        return ChatLayout(on: true, fold: fold, lines: lines)
        #else
        return .off
        #endif
    }
}

extension Injected {
    /// Persona writing to itself: the supervision digest (`system` / `supervise`) and scheduled runs, follow-ups and
    /// wake-ups (`followup`). Drawn under the Supervision identity, "Persona → @Persona", in the new layout.
    var isPatrol: Bool { kind == "followup" || (kind == "system" && state == "supervise") }
}

/// The box a message of the new layout sits in (`Theme.ChatBox`).
struct ChatBoxed: ViewModifier {
    var fill: Color
    var on = true

    func body(content: Content) -> some View {
        if on {
            content
                .padding(.horizontal, Theme.Space.l)
                .padding(.vertical, Theme.Space.m)
                .background(fill, in: .rect(cornerRadius: Theme.Radius.bubble, style: .continuous))
        } else {
            content
        }
    }
}

extension View {
    /// In its box; `on` false preserves the baseline conversation presentation.
    func chatBoxed(_ fill: Color, on: Bool = true) -> some View { modifier(ChatBoxed(fill: fill, on: on)) }
}

/// Who talks to whom over a message of the new layout: "Persona → @task", "task → @Persona", with the identity
/// before it when it has one (Supervision). One line; the addressee in the accent.
struct ExchangeHeader: View {
    var identity: String? = nil
    var from: String
    var to: String
    var icon: String? = nil
    var tint: Color = .secondary

    var body: some View {
        HStack(spacing: Theme.Space.xs) {
            if let icon { ThemedIcon(icon).font(Theme.Font.labelStrong).foregroundStyle(tint) }
            if let identity {
                Text(identity).font(Theme.Font.captionStrong).foregroundStyle(.primary).lineLimit(1).fixedSize()
                Text(verbatim: "·").foregroundStyle(.tertiary)
            }
            Text("\(from) → \(Text(verbatim: "@" + to).foregroundStyle(Theme.accent))")
                .lineLimit(1)
                .truncationMode(.middle)
        }
        .font(Theme.Font.captionMedium)
        .foregroundStyle(.secondary)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("exchange.header")
    }
}
