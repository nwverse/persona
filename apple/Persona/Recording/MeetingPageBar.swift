#if os(iOS)
import BeautifulUI
import SwiftUI

/// The meeting page's bottom bar (#3862, docs/assistant/meeting-minutes.md「The meeting card」), pinned under the thumb:
/// the Just Me switch, the composer, and Pause, Bookmark and Stop while the meeting records; the composer alone after it.
/// The composer is the shell composer's skin as #4486's typed question is (`ComposerSurface`, `ComposerRoundButton`, its
/// own vertical field), with a [+] menu, not `ShellComposerBar`: that one's dictation would take the microphone the
/// recording holds. Its words go to the room (`…/ask`), to the main chat with Just Me (`…/just-me`), or after the meeting
/// to the follow-up (`…/followup`, then the conversation's messages). While the field has the keyboard, Pause, Bookmark
/// and Stop step aside so the thread stays in view; at accessibility sizes they are icons and a More menu.
///
/// Stop asks first, as the live sheet's bar does: a stopped recording cannot be continued.
struct MeetingPageBar: View {
    var entry: RecordingEntry
    var mode: MeetingPage.Mode
    var paused = false
    @Binding var justMe: Bool

    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @Environment(\.buiTheme) private var theme
    @State private var capture = RecordingCapture.shared
    @State private var ask = MeetingAsk.shared
    @State private var conversation = MeetingConversation.shared
    @State private var draft = ""
    @FocusState private var focused: Bool
    @State private var confirmingStop = false

    private enum Target { case room, justMe, followup }

    private var target: Target { mode == .after ? .followup : justMe ? .justMe : .room }
    private var role: MeetingAttendance { entry.attendance?.role ?? .off }
    /// Persona is off and Just Me is off: there is nobody to ask.
    private var silenced: Bool { target == .room && !role.attends }
    /// The hub would still answer the follow-up `409 meeting_running`.
    private var finishing: Bool { target == .followup && conversation.running(entry) }
    /// The keyboard is up: the controls under the composer step aside (#4486's rule).
    private var writing: Bool { focused }

    private var canSend: Bool {
        guard MeetingAsk.hasWords(draft) else { return false }
        switch target {
        // One question at a time, deliberately stricter than the hub's two: an answer sits next to its question.
        case .room: return entry.attended && !ask.busy
        // Just Me needs only a meeting that records; it never waits on the room's question.
        case .justMe: return entry.active
        case .followup: return !finishing
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            if mode == .live, !(writing && size.isAccessibilitySize) { justMeRow }
            composer
            if let caption {
                Text(caption).font(Theme.Font.caption).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("meeting-ask-caption")
            }
            if mode == .live, !writing { controls }
        }
        .padding(.horizontal, Theme.Space.page)
        .padding(.vertical, Theme.Space.s)
        .frame(maxWidth: .infinity)
        .background { Theme.groupedPage.ignoresSafeArea(edges: .bottom) }
        .animation(reduceMotion ? nil : Theme.Motion.state, value: writing)
        .onChange(of: fieldDisabled) { _, disabled in
            if disabled { focused = false }
        }
        .confirmationDialog("Stop Recording?", isPresented: $confirmingStop, titleVisibility: .visible) {
            Button("Stop Recording", role: .destructive) { MeetingRecorder.shared.stop(recordingID: entry.id.uuidString) }
        } message: {
            Text("The audio is saved on this device and its text goes to Knowledge. A stopped recording can't be continued.")
        }
    }

    // MARK: Just Me

    private var justMeRow: some View {
        Toggle(isOn: $justMe) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("Just Me").font(Theme.Font.bodyStrong)
                Text("Only you see it, in the main chat").font(Theme.Font.caption).foregroundStyle(.secondary)
            }
        }
        .disabled(!entry.active)
        .accessibilityIdentifier("meeting-just-me")
    }

    // MARK: The composer

    @ViewBuilder private var composer: some View {
        if size.isAccessibilitySize {
            // The field takes the whole width; [+] and send go under it, send at the trailing edge.
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                field
                HStack(spacing: Theme.Space.m) {
                    plus
                    Spacer(minLength: Theme.Space.s)
                    sendButton
                }
            }
        } else {
            HStack(alignment: .bottom, spacing: Theme.Space.s) {
                plus
                field
                sendButton
            }
        }
    }

    private var placeholder: String {
        switch target {
        case .room: role.attends ? String(localized: "Ask Persona…") : String(localized: "Persona is off")
        case .justMe: String(localized: "Ask privately…")
        case .followup: String(localized: "Ask about this meeting…")
        }
    }

    private var fieldDisabled: Bool { silenced || finishing }

    /// Why the field can't take words now, and the way out.
    private var caption: String? {
        if silenced { return String(localized: "Turn Persona on in the menu to ask, or use Just Me.") }
        if finishing { return String(localized: "Persona is finishing the meeting. You can ask about it in a moment.") }
        return nil
    }

    private var field: some View {
        TextField(placeholder, text: $draft, axis: .vertical)
            .font(Theme.Font.body)
            .lineLimit(1...(size.isAccessibilitySize ? Theme.Recording.askLinesLargeType : Theme.Recording.askLines))
            .focused($focused)
            .submitLabel(.send)
            .onSubmit { send() }
            // A vertical field takes Return as a new line: one typed at the end sends instead (#4486). A pasted text
            // with new lines in it is not one keystroke, so it stays.
            .onChange(of: draft) { old, new in
                guard new == old + "\n" else { return }
                draft = old
                send()
            }
            .padding(.horizontal, Theme.Space.l)
            .padding(.vertical, Theme.Space.s)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
            .modifier(ComposerSurface())
            .contentShape(.rect(cornerRadius: Theme.Shell.composerRadius))
            .onTapGesture { if !fieldDisabled { focused = true } }
            .disabled(fieldDisabled)
            .accessibilityIdentifier("meeting-ask-field")
    }

    /// Send stays in its place when there is nothing to send, dimmed, so the row does not jump.
    private var sendButton: some View {
        ComposerRoundButton(symbol: Theme.Icon.send, label: String(localized: "Send"), id: "meeting-ask-send", pressScales: true) { send() }
            .disabled(!canSend)
            .opacity(canSend ? 1 : Theme.Recording.askSendIdle)
    }

    /// [+]: what else the composer can send. Phase 1 has one item, the question about what was just said, asked of the
    /// room; with nothing to offer (Just Me, after the meeting, Persona off) the button is not there.
    @ViewBuilder private var plus: some View {
        if mode == .live, target == .room, entry.attended {
            Menu {
                Button { askHeard() } label: { Label("What Was Just Said", systemImage: Theme.Icon.question) }
                    .disabled(ask.busy)
                    .accessibilityIdentifier("meeting-heard")
            } label: {
                plusFace
            }
            .menuStyle(.button)
            .buttonStyle(.plain)
            .accessibilityLabel(Text("More [Meeting Composer]"))
            .accessibilityIdentifier("meeting-plus")
            .shellScaled()
        }
    }

    /// The round button's face (`ComposerRoundButton`), drawn here because a menu can't hold that button: the add symbol
    /// in an ink circle, or the style's ink in BeautifulUI.
    private var plusFace: some View {
        ThemedIcon(Theme.Icon.add)
            .font(Theme.Font.bodyHeavy)
            .foregroundStyle(AppStyle.current.usesBeautifulUI ? theme.colors.canvas : Theme.Shell.inkText)
            .scaledFrame(width: Theme.Size.wellL, height: Theme.Size.wellL)
            .background(AppStyle.current.usesBeautifulUI ? theme.colors.ink : Theme.Shell.ink, in: .circle)
            .scaledFrame(width: Theme.Size.tapTarget, height: Theme.Size.tapTarget)
            .contentShape(.circle)
    }

    // MARK: Pause, Bookmark, Stop

    @ViewBuilder private var controls: some View {
        if size.isAccessibilitySize {
            // Pause or Continue, Stop and More side by side as icons (their names are their accessibility labels), so the
            // row stays one row tall whatever the text size and Pause stays one tap away.
            HStack(spacing: Theme.Space.m) {
                iconButton(pauseTitle, paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
                    .accessibilityIdentifier("recording-pause")
                iconButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
                    .accessibilityIdentifier("recording-stop")
                Menu {
                    Button { addBookmark() } label: { Label("Bookmark", systemImage: Theme.Icon.pin) }
                        .accessibilityIdentifier("recording-bookmark")
                } label: {
                    ThemedIcon(Theme.Icon.moreMenu).font(Theme.Font.heading)
                        .frame(maxWidth: .infinity)
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                }
                .themedButton()
                .accessibilityLabel(Text("More"))
                .accessibilityIdentifier("recording-more")
            }
        } else {
            HStack(spacing: Theme.Space.m) {
                barButton(pauseTitle, paused ? Theme.Icon.run : Theme.Icon.pause) { togglePause() }
                    .accessibilityIdentifier("recording-pause")
                barButton(String(localized: "Bookmark"), Theme.Icon.pin) { addBookmark() }
                    .accessibilityIdentifier("recording-bookmark")
                barButton(String(localized: "Stop [Car Play Voice Screen c7b24e]"), Theme.Icon.interrupt) { confirmingStop = true }
                    .accessibilityIdentifier("recording-stop")
            }
        }
    }

    private var pauseTitle: String { paused ? String(localized: "Continue") : String(localized: "Pause") }

    /// A standard button, its icon over its label, at least a finger tall.
    private func barButton(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            VStack(spacing: Theme.Space.xxs) {
                ThemedIcon(symbol).font(Theme.Font.heading).scaledFrame(height: Theme.Size.icon)
                Text(title).font(Theme.Font.captionStrong).lineLimit(1).minimumScaleFactor(0.8)
            }
            .frame(maxWidth: .infinity)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
    }

    private func iconButton(_ title: String, _ symbol: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            ThemedIcon(symbol).font(Theme.Font.heading)
                .frame(maxWidth: .infinity)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
        }
        .themedButton()
        .accessibilityLabel(Text(title))
    }

    // MARK: Actions

    /// Sends the draft where the bar points; the keyboard goes down after, so the new question is in view.
    private func send() {
        guard canSend else { return }
        let text = draft
        let sent: Bool
        switch target {
        case .room: sent = ask.ask(entry, text: text) != nil
        case .justMe: sent = conversation.sendJustMe(entry, text: text) != nil
        case .followup: sent = conversation.sendFollowup(entry.id, text: text) != nil
        }
        guard sent else { return }
        draft = ""
        focused = false
        Haptics.tap()
    }

    /// The question about what was just said (`…/ask` with the time of the tap).
    private func askHeard() {
        guard ask.ask(entry) != nil else { return }
        Haptics.tap()
    }

    /// Pause: the haptic after the microphone is paused (a pulse on touch-down would overlap live capture). Continue:
    /// before it opens again. The state is announced.
    private func togglePause() {
        if paused {
            Haptics.tap()
            capture.togglePause()
            AccessibilityNotification.Announcement(String(localized: "Recording continued")).post()
        } else {
            capture.togglePause()
            Haptics.tap()
            AccessibilityNotification.Announcement(String(localized: "Recording paused")).post()
        }
    }

    private func addBookmark() {
        MeetingRecorder.shared.addMark(recordingID: entry.id.uuidString)
        AccessibilityNotification.Announcement(String(localized: "Bookmark added")).post()
    }
}
#endif
