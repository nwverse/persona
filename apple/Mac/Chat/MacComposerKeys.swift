import AppKit
import SwiftUI

/// ⏎ and ⇧⏎ in a composer (docs/apple/mac.md「Details」: ⏎ sends, ⇧⏎ adds a new line). The field is a SwiftUI TextField, an AppKit field
/// editor underneath: on its own, ⏎ and ⇧⏎ both ended editing and selected all the text, so the next key typed
/// replaced it, and ⌥⏎ was the only way to a new line (#785).
nonisolated enum MacComposerKeys {
    enum Action: Equatable {
        /// Left to the field, the menus and the send button's ⌘⏎.
        case pass
        case send
        /// A new line at the cursor (what ⌥⏎ does on its own).
        case newline
        /// Taken and dropped: ⏎ with nothing to send, which would otherwise select all the text.
        case ignore
    }

    static let returnKey: UInt16 = 36
    static let keypadEnter: UInt16 = 76

    /// What a key pressed in a focused composer does. While an input method holds text not yet confirmed (pinyin),
    /// ⏎ is its own: it confirms the candidate.
    static func action(keyCode: UInt16, modifiers: NSEvent.ModifierFlags, composing: Bool, canSend: Bool) -> Action {
        guard keyCode == returnKey || keyCode == keypadEnter, !composing else { return .pass }
        switch modifiers.intersection([.command, .shift, .option, .control]) {
        case []: return canSend ? .send : .ignore
        case .shift: return .newline
        default: return .pass
        }
    }
}

/// The wheel over a composer whose text is taller than the field: the field editor follows the cursor but not the
/// wheel, so the wheel moves the clip view around it (AppKit's `_NSKeyboardFocusClipView`).
nonisolated enum MacComposerWheel {
    /// The editor's line height when it has no font to measure.
    static let fallbackLine: CGFloat = 16

    /// The clip view's new top after a wheel step of `delta` points (positive: the wheel turned up, toward the first
    /// line), within `top...bottom`; nil when it cannot move that way, so the page scrolls instead. Flipped
    /// coordinates, as the text view and so its clip view are: a smaller top shows earlier lines.
    static func offset(from y: CGFloat, delta: CGFloat, top: CGFloat, bottom: CGFloat) -> CGFloat? {
        guard bottom > top else { return nil }
        let next = min(max(y - delta, top), bottom)
        return abs(next - y) < 0.5 ? nil : next
    }

    /// Scrolls the field editor's clip view by a wheel event over it; false leaves the event to the page.
    @MainActor static func scroll(_ event: NSEvent, in editor: NSTextView) -> Bool {
        guard let clip = editor.superview as? NSClipView, clip.bounds.contains(clip.convert(event.locationInWindow, from: nil))
        else { return false }
        let line = editor.font.map { editor.layoutManager?.defaultLineHeight(for: $0) ?? $0.pointSize } ?? fallbackLine
        let delta = event.hasPreciseScrollingDeltas ? event.scrollingDeltaY : event.scrollingDeltaY * line
        guard let y = offset(from: clip.bounds.origin.y, delta: delta, top: editor.frame.minY,
                             bottom: editor.frame.maxY - clip.bounds.height) else { return false }
        clip.scroll(to: NSPoint(x: clip.bounds.origin.x, y: y))
        return true
    }
}

/// A composer's send history (`MacSendHistory`): the conversation it is kept under and the field's text.
struct MacComposerHistory {
    var key: String
    var text: Binding<String>
    /// Gives the field the focus back (the search took it).
    var focus: () -> Void
}

extension View {
    /// ⏎ sends and ⇧⏎ starts a new line in a focused composer's field (`MacComposerKeys`); the wheel scrolls a field
    /// grown past its last line (`MacComposerWheel`). With `history`: ↑ / ↓ bring back what was sent and ⌃R searches it
    /// (`MacSendHistory`), and ⏎ remembers what it sends.
    func macComposerKeys(focused: Bool, canSend: Bool, history: MacComposerHistory? = nil, send: @escaping () -> Void) -> some View {
        modifier(MacComposerKeysModifier(focused: focused, canSend: canSend, history: history, send: send))
    }
}

private struct MacComposerKeysModifier: ViewModifier {
    var focused: Bool
    var canSend: Bool
    var history: MacComposerHistory?
    var send: () -> Void
    @State private var recall = Recall()
    @State private var searching = false

    /// Where ↑ / ↓ are: the index (oldest first) of the message the box shows, and that message; -1 / nil when the
    /// box holds something else.
    final class Recall {
        var index = -1
        var shown: String?
    }

    func body(content: Content) -> some View {
        content
            .modifier(MacComposerEventMonitor(focused: focused, matching: [.keyDown, .scrollWheel]) { event in
                guard let editor = event.window?.firstResponder as? NSTextView else { return event }
                if event.type == .scrollWheel { return MacComposerWheel.scroll(event, in: editor) ? nil : event }
                if let history, let key = MacSendHistory.key(keyCode: event.keyCode, modifiers: event.modifierFlags,
                                                             composing: editor.hasMarkedText()) {
                    switch key {
                    case .openSearch:
                        searching = true
                        return nil
                    case .recall(let dir):
                        return walk(dir, history, editor) ? nil : event
                    }
                }
                switch MacComposerKeys.action(keyCode: event.keyCode, modifiers: event.modifierFlags,
                                              composing: editor.hasMarkedText(), canSend: canSend) {
                case .pass: return event
                case .send:
                    if let history { MacSendHistory.shared.record(history.key, history.text.wrappedValue) }
                    recall.index = -1
                    recall.shown = nil
                    send()
                case .newline: editor.insertNewlineIgnoringFieldEditor(nil)
                case .ignore: break
                }
                return nil
            })
            // A popover over the field (the web's panel above the box): the composer's card clips what it holds.
            .popover(isPresented: $searching, arrowEdge: .top) {
                if let history {
                    MacSentSearch(seed: Self.debugSeed) { text in
                        searching = false
                        recall.index = -1
                        recall.shown = nil
                        history.text.wrappedValue = text
                        history.focus()
                    } cancel: {
                        searching = false
                        history.focus()
                    }
                }
            }
            #if DEBUG
            .onAppear {
                // `-openSentSearch <query>`: the search open over an agent's composer (not Persona's starter), that typed in.
                if Self.debugSeed != nil, !Self.debugOpened, let history, history.key != "assistant.new" {
                    Self.debugOpened = true
                    Task { try? await Task.sleep(for: .seconds(1)); searching = true }
                }
            }
            #endif
    }

    #if DEBUG
    private static let debugSeed = UserDefaults.standard.string(forKey: "openSentSearch")
    private static var debugOpened = false
    #else
    private static let debugSeed: String? = nil
    #endif

    /// ↑ / ↓ in the field: the next message of the history into the box. false leaves the key to the field (moving
    /// through its own lines, or nothing to recall).
    private func walk(_ dir: MacSendHistory.Direction, _ history: MacComposerHistory, _ editor: NSTextView) -> Bool {
        let draft = history.text.wrappedValue
        let lines = MacSendHistory.lines(editor.string, cursor: editor.selectedRange())
        guard MacSendHistory.canRecall(draft: draft, shown: recall.shown, dir, firstLine: lines.first, lastLine: lines.last)
        else { return false }
        if draft.isEmpty { recall.index = -1 }
        let list = MacSendHistory.shared.history(history.key)
        let next = MacSendHistory.step(count: list.count, index: recall.index, dir)
        if next == recall.index { return !draft.isEmpty } // at the oldest: stay there; an empty box with nothing: the field's
        recall.index = next
        let text = next < 0 ? "" : list[next].text
        recall.shown = next < 0 ? nil : text
        history.text.wrappedValue = text
        return true
    }
}

/// ⌃R over the composer, a terminal's reverse search: what every conversation sent, filtered as you type, newest
/// first. ↑↓ / ⌃R ⌃P / ⌃S ⌃N move, ⏎ or Tab puts it in the box (it is not sent), Esc or ⌃G cancels.
struct MacSentSearch: View {
    var seed: String?
    var pick: (String) -> Void
    var cancel: () -> Void
    @State private var query = ""
    @State private var active = 0
    @State private var candidates = MacSendHistory.shared.everything()
    @FocusState private var focused: Bool

    var body: some View {
        let rows = MacSendHistory.search(candidates, query)
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: Theme.Space.s) {
                Text("^R").font(Theme.Font.code).foregroundStyle(.secondary).accessibilityHidden(true)
                TextField(String(localized: "Search sent messages"), text: $query)
                    .textFieldStyle(.plain)
                    .focused($focused)
                    .accessibilityLabel("Search sent messages")
            }
            .padding(Theme.Space.m)
            Divider()
            if rows.isEmpty {
                Text(candidates.isEmpty ? String(localized: "No messages sent yet") : String(localized: "No matching messages"))
                    .font(Theme.Font.secondary)
                    .foregroundStyle(.secondary)
                    .padding(Theme.Space.m)
            } else {
                ScrollViewReader { proxy in
                    ScrollView {
                        LazyVStack(alignment: .leading, spacing: 0) {
                            ForEach(Array(rows.enumerated()), id: \.offset) { i, text in
                                Text(text)
                                    .lineLimit(2)
                                    .frame(maxWidth: .infinity, alignment: .leading)
                                    .foregroundStyle(i == active ? .white : .primary)
                                    .padding(.horizontal, Theme.Space.s)
                                    .padding(.vertical, Theme.Space.xs)
                                    .background(i == active ? Color.accentColor : .clear, in: RoundedRectangle(cornerRadius: Theme.Radius.badge))
                                    .contentShape(Rectangle())
                                    .onTapGesture { pick(text) }
                                    .onHover { if $0 { active = i } }
                                    .id(i)
                                    .accessibilityAddTraits(i == active ? [.isButton, .isSelected] : .isButton)
                            }
                        }
                        .padding(Theme.Space.xs)
                    }
                    .frame(maxHeight: Theme.Size.macQuickOpenListMax)
                    .fixedSize(horizontal: false, vertical: true)
                    .onChange(of: active) { _, i in proxy.scrollTo(i) }
                }
            }
        }
        .frame(width: Theme.Size.macQuickOpenWidth)
        .modifier(MacComposerEventMonitor(focused: true) { event in
            let editor = event.window?.firstResponder as? NSTextView
            guard let action = MacSendHistory.searchAction(keyCode: event.keyCode, modifiers: event.modifierFlags,
                                                           composing: editor?.hasMarkedText() ?? false) else { return event }
            switch action {
            case .up, .down: active = MacSendHistory.move(active, count: rows.count, action)
            case .pick: if rows.indices.contains(active) { pick(rows[active]) } else { cancel() }
            case .cancel: cancel()
            }
            return nil
        })
        .onAppear {
            if let seed { query = seed }
            focused = true
        }
        .onChange(of: query) { active = 0 }
    }
}

/// Events of the `matching` kinds in this view's window while its composer has focus, seen before the field gets
/// them: `handle` returns nil to take one. A monitor, because the focused SwiftUI text field (an AppKit field editor)
/// acts on some itself: it takes `paste:` and drops pictures, ⏎ / ⇧⏎ end its editing, the wheel does not scroll it.
/// Only the composer in the window the event is for acts (a conversation can also be open in a window of its own).
struct MacComposerEventMonitor: ViewModifier {
    var focused: Bool
    var matching: NSEvent.EventTypeMask = .keyDown
    var handle: (NSEvent) -> NSEvent?
    @State private var latest = Latest()
    @State private var monitor: Any?

    /// What the monitor reads when an event comes in: the window, the focus and the handler as of the last update,
    /// so it never acts on a stale focus.
    final class Latest {
        weak var window: NSWindow?
        var focused = false
        var handle: (NSEvent) -> NSEvent? = { $0 }
    }

    func body(content: Content) -> some View {
        latest.focused = focused
        latest.handle = handle
        return content
            .background(WindowReader(latest: latest))
            .onAppear { install() }
            .onDisappear {
                if let monitor { NSEvent.removeMonitor(monitor) }
                monitor = nil
            }
    }

    private func install() {
        guard monitor == nil else { return }
        let latest = latest
        monitor = NSEvent.addLocalMonitorForEvents(matching: matching) { event in
            guard latest.focused, let w = latest.window, event.window === w else { return event }
            return latest.handle(event)
        }
    }

    private struct WindowReader: NSViewRepresentable {
        let latest: Latest
        func makeNSView(context: Context) -> NSView { Probe(latest: latest) }
        func updateNSView(_ nsView: NSView, context: Context) {}

        final class Probe: NSView {
            let latest: Latest
            init(latest: Latest) {
                self.latest = latest
                super.init(frame: .zero)
            }
            required init?(coder: NSCoder) { nil }
            override func viewDidMoveToWindow() {
                super.viewDidMoveToWindow()
                latest.window = window
            }
        }
    }
}
