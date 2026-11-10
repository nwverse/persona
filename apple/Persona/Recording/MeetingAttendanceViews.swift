#if os(iOS)
import SwiftUI

// Persona in a recorded meeting (#2981, docs/apple/recording.md「Persona in meetings」): the role and the session that
// attends, picked as a meeting recording starts; while it records, that Persona is there, Ask Persona, and the answers.
// Only with a hub that lists `meeting_attendance`: without one these rows never show and the page is as it was.

/// Before a meeting recording starts (the Recordings page, under Meeting Recording): Persona's part in it, pre-selected
/// from the hub's default, and who attends when it isn't off.
struct MeetingAttendanceStart: View {
    @Binding var role: MeetingAttendance
    var attendee: MeetingAttendees.Candidate?
    var pick: (MeetingAttendees.Candidate) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Label("Persona in This Meeting", systemImage: Theme.Icon.persona)
                .font(Theme.Font.bodyStrong)
            SegmentedChoice(String(localized: "Persona in This Meeting"), MeetingAttendance.allCases.map { (value: $0, label: $0.title) },
                            selection: $role)
                .accessibilityIdentifier("meeting-attendance-role")
            Text(role.detail).font(Theme.Font.caption).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("meeting-attendance-detail")
        }
        .padding(.vertical, Theme.Space.xs)
        if role.attends {
            NavigationLink { MeetingAttendeePicker(selected: attendee?.key, pick: pick) } label: {
                MeetingAttendeeLabel(title: attendee?.title, detail: attendee?.detail)
            }
            .accessibilityIdentifier("meeting-attendee")
        }
    }
}

/// "Who Attends: <session>", or a prompt to choose one.
struct MeetingAttendeeLabel: View {
    var title: String?
    var detail: String?

    var body: some View {
        LabeledContent {
            if let title {
                VStack(alignment: .trailing, spacing: Theme.Space.xxs) {
                    Text(title).foregroundStyle(.primary).lineLimit(1)
                    if let detail, !detail.isEmpty { Text(detail).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1) }
                }
            } else {
                Text("Choose [Meeting Attendee]").foregroundStyle(Theme.accent).lineLimit(1)
            }
        } label: {
            Text("Who Attends").lineLimit(1)
        }
        .font(Theme.Font.body)
    }
}

/// Who attends: the main chat, Persona's other chats, or one running Agent (`MeetingAttendees`).
struct MeetingAttendeePicker: View {
    var selected: SessionKey?
    var pick: (MeetingAttendees.Candidate) -> Void
    @Environment(AppModel.self) private var model
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        let all = MeetingAttendees.candidates(model)
        let chats = all.filter(\.persona)
        let agents = all.filter { !$0.persona }
        List {
            Section {
                ForEach(chats) { row($0) }
            } header: {
                Text("Persona")
            } footer: {
                Text("The main chat answers from earlier meetings, your projects and to-dos.")
            }
            Section {
                if agents.isEmpty {
                    Text("No Agents are running. Start one in Agents, or let the main chat attend.")
                        .font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                ForEach(agents) { row($0) }
            } header: {
                Text("Agents")
            } footer: {
                Text("An Agent also answers about the project it works on. Sessions on a work computer, and ones that never ask before they act, can't attend.")
            }
        }
        .listStyle(.insetGrouped)
        .themedPage()
        .navigationTitle(String(localized: "Who Attends"))
        .navigationBarTitleDisplayMode(.inline)
    }

    private func row(_ c: MeetingAttendees.Candidate) -> some View {
        Button {
            pick(c)
            dismiss()
        } label: {
            HStack(spacing: Theme.Space.m) {
                Group {
                    if c.persona {
                        ThemedIcon(Theme.Icon.persona).foregroundStyle(Theme.ai)
                    } else if let s = model.session(c.key) {
                        AgentMark(s)
                    }
                }
                .scaledFrame(width: Theme.Size.markS)
                .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                    Text(c.title).font(Theme.Font.body).foregroundStyle(.primary).lineLimit(1)
                        .accessibilityIdentifier("meeting-attendee-title")
                    if !c.detail.isEmpty {
                        Text(c.detail).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
                    }
                }
                Spacer(minLength: Theme.Space.s)
                if c.key == selected {
                    ThemedIcon(Theme.Icon.done).foregroundStyle(Theme.accent)
                        .accessibilityLabel(Text("Selected [Meeting Attendee]"))
                }
            }
        }
        .accessibilityIdentifier(c.main ? "meeting-attendee-main" : c.persona ? "meeting-attendee-chat" : "meeting-attendee-agent")
    }
}

/// While a meeting records (the Recordings page, below its controls): that Persona is there and who attends, Persona's
/// part (changeable), Ask Persona, and the latest answers.
struct MeetingAttendanceSection: View {
    var entry: RecordingEntry
    @Environment(AppModel.self) private var model
    @State private var ask = MeetingAsk.shared
    @State private var live = MeetingLive.shared
    @State private var sync = RecordingSync.shared
    /// The typed question (the system's text-entry alert, DESIGN_SYSTEM.md 4.4), shown while `typing`.
    @State private var typing = false
    @State private var question = ""

    private var attendance: RecordingAttendance { entry.attendance ?? RecordingAttendance(role: .off) }

    var body: some View {
        Section {
            if entry.attended {
                Label {
                    Text(attendance.role.speaks ? String(localized: "Persona is taking part") : String(localized: "Persona is listening"))
                        .font(Theme.Font.bodyStrong).lineLimit(2)
                } icon: {
                    ThemedIcon(Theme.Icon.voiceListening).foregroundStyle(Theme.ai)
                }
                .accessibilityIdentifier("meeting-attending")
            }
            Picker(selection: Binding(get: { attendance.role }, set: { MeetingRecorder.shared.setRole($0, recordingID: entry.id.uuidString) })) {
                ForEach(MeetingAttendance.allCases, id: \.self) { Text($0.title).tag($0) }
            } label: {
                Label("Persona", systemImage: Theme.Icon.persona)
            }
            .pickerStyle(.menu)
            .accessibilityIdentifier("meeting-attendance-now")
            if attendance.role.attends {
                NavigationLink {
                    MeetingAttendeePicker(selected: attendance.key ?? model.assistantMain) { MeetingRecorder.shared.setSession($0, recordingID: entry.id) }
                } label: {
                    MeetingAttendeeLabel(title: attendance.sessionTitle, detail: nil)
                }
                .accessibilityIdentifier("meeting-attendee")
            }
            if let problem {
                Label(problem, systemImage: Theme.Icon.info)
                    .font(Theme.Font.secondary).foregroundStyle(.secondary)
                    .accessibilityIdentifier("meeting-attendance-problem")
            }
            if entry.attended { askButtons }
            ForEach(ask.exchanges(for: entry.id).suffix(3).reversed()) { exchange in
                MeetingExchangeRow(exchange: exchange, entry: entry)
            }
        } header: {
            Text("Persona in This Meeting")
        } footer: {
            if attendance.role.attends {
                Text("Persona answers only from meeting material: earlier minutes, project progress and who owns a to-do. It never brings up private chats, mail or health.")
            }
        }
    }

    /// Why Persona can't follow the meeting right now, in plain words.
    private var problem: String? {
        if attendance.stopped == .unsupported { return String(localized: "This hub can't follow meetings, so Persona stopped attending this one.") }
        guard attendance.role.attends else { return nil }
        if attendance.stopped == .liveFull { return String(localized: "The hub takes no more of this meeting's lines. Questions still work.") }
        if sync.attendanceProblem[entry.id] != nil { return String(localized: "The hub didn't take Persona's part in this meeting. Choose the role again.") }
        switch live.state(entry.id).problem {
        case nil, "": return nil
        case "attendee_unavailable": return String(localized: "Who attends can't take messages now. Persona catches up once it can.")
        case "attendee_not_allowed": return String(localized: "The hub won't pass other people's words to this session. Questions still work.")
        case "live_full": return String(localized: "The hub takes no more of this meeting's lines. Questions still work.")
        default: return String(localized: "Persona can't follow this meeting right now.")
        }
    }

    /// Taking part, one tap asks about what was just said (answered out loud), and typing is a step away; listening in,
    /// the same two, answered on screen. Side by side, or one above the other when the text is large.
    @ViewBuilder private var askButtons: some View {
        ViewThatFits(in: .horizontal) {
            HStack(spacing: Theme.Space.m) { askButton; typeButton }
            VStack(alignment: .leading, spacing: Theme.Space.s) { askButton; typeButton }
        }
        .padding(.vertical, Theme.Space.xs)
        .alert("Ask Persona", isPresented: $typing) {
            TextField(String(localized: "Ask about this meeting"), text: $question)
                .accessibilityIdentifier("meeting-ask-field")
            Button { send() } label: { Text("Ask [Meeting Ask]").lineLimit(1) }
                .disabled(question.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                .accessibilityIdentifier("meeting-ask-send")
            Button("Cancel", role: .cancel) { question = "" }
        } message: {
            Text(attendance.role.speaks
                 ? String(localized: "About an earlier meeting, a project's progress or who owns a to-do. Persona answers out loud.")
                 : String(localized: "About an earlier meeting, a project's progress or who owns a to-do. Persona answers on screen."))
        }
    }

    private var askButton: some View {
        Button {
            ask.ask(entry)
            Haptics.tap()
        } label: {
            Label("Ask Persona", systemImage: Theme.Icon.question)
                .font(Theme.Font.bodyStrong).lineLimit(1)
                .frame(maxWidth: .infinity)
        }
        .themedButton(prominent: true)
        .accessibilityHint(Text("Asks about what was just said"))
        .accessibilityIdentifier("meeting-ask")
    }

    private var typeButton: some View {
        Button { typing = true } label: {
            Label("Type [Meeting Ask]", systemImage: Theme.Icon.keyboard).lineLimit(1)
        }
        .themedButton()
        .accessibilityIdentifier("meeting-ask-type")
    }

    private func send() {
        guard ask.ask(entry, text: question) != nil else { return }
        question = ""
    }
}

/// One question and its answer: what was asked (or heard) at its time on the recording, then Persona looking, the answer
/// with what it drew on (and, said out loud, Stop Speaking), or what went wrong with Retry.
struct MeetingExchangeRow: View {
    var exchange: MeetingAsk.Exchange
    var entry: RecordingEntry
    @State private var ask = MeetingAsk.shared
    @State private var speaker = MeetingSpeaker.shared

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                Text(exchange.question ?? String(localized: "What was just said"))
                    .font(Theme.Font.secondaryMedium).foregroundStyle(.secondary).lineLimit(3)
                Spacer(minLength: Theme.Space.s)
                Text(RecordingFormat.clock(exchange.at)).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
            }
            switch exchange.state {
            case .asking:
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Persona is looking…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                .accessibilityIdentifier("meeting-answer-waiting")
            case .answered(let text):
                Label {
                    Text(text).font(Theme.Font.body).textSelection(.enabled)
                        .accessibilityIdentifier("meeting-answer")
                } icon: {
                    ThemedIcon(Theme.Icon.persona).foregroundStyle(Theme.ai)
                }
                if !exchange.sources.isEmpty {
                    Text("From \(ListFormatter.localizedString(byJoining: exchange.sources))")
                        .font(Theme.Font.caption).foregroundStyle(.secondary)
                        .accessibilityIdentifier("meeting-answer-sources")
                }
                if speaker.speaking == exchange.id {
                    ViewThatFits(in: .horizontal) {
                        HStack(spacing: Theme.Space.m) { speaking; Spacer(minLength: Theme.Space.s); stopSpeaking }
                        VStack(alignment: .leading, spacing: Theme.Space.s) { speaking; stopSpeaking }
                    }
                }
            case .failed(let message):
                Text(message).font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("meeting-answer-failed")
                Button { ask.retry(exchange.id, entry: entry) } label: {
                    Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1)
                }
                .themedButton()
                .accessibilityIdentifier("meeting-answer-retry")
            }
        }
        .padding(.vertical, Theme.Space.xs)
        .accessibilityElement(children: .contain)
    }

    private var speaking: some View {
        Label("Speaking", systemImage: Theme.Icon.voiceSpeaking)
            .font(Theme.Font.secondary).foregroundStyle(Theme.ai).lineLimit(1)
    }

    private var stopSpeaking: some View {
        Button { ask.stopSpeaking() } label: {
            Label("Stop Speaking", systemImage: Theme.Icon.interrupt).lineLimit(1)
        }
        .themedButton()
        .accessibilityIdentifier("meeting-stop-speaking")
    }
}
#endif
