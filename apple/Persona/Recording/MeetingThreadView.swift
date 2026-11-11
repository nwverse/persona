#if os(iOS)
import SwiftUI

/// The meeting page's one thread (#3862, docs/assistant/meeting-minutes.md「One thread」). While the meeting records: the
/// room conversation (Persona joining, the lines it was fed folded to "N lines from the meeting", the questions and its
/// answers) with this phone's own questions the room does not hold yet, failed or answered from the meeting's material;
/// with Just Me on, the owner's private questions and the main chat's answers to them instead. After the meeting: the room,
/// **After the Meeting**, then the follow-up conversation (its first message's material folded) and the messages on their
/// way to it. It subscribes to the conversations it shows while it is on screen and derives its rows from their items
/// only when those change (`MeetingThread`).
struct MeetingThreadView: View {
    /// What the thread shows, for the page around it: its rows' ids in order (Jump to Latest counts the new ones), and the
    /// exchanges it draws as `MeetingExchangeRow` cards, which carry their own Stop Speaking.
    struct Shown: Equatable {
        var ids: [String] = []
        var exchangeRows: Set<UUID> = []
    }

    var entry: RecordingEntry
    var mode: MeetingPage.Mode
    var justMe: Bool
    var exchanges: [MeetingAsk.Exchange]
    @Binding var shown: Shown

    init(entry: RecordingEntry, mode: MeetingPage.Mode, justMe: Bool, exchanges: [MeetingAsk.Exchange],
         shown: Binding<Shown> = .constant(Shown())) {
        self.entry = entry
        self.mode = mode
        self.justMe = justMe
        self.exchanges = exchanges
        _shown = shown
    }

    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var size
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var conversation = MeetingConversation.shared
    /// This view's hold on the conversations it shows (`AppModel.subscribe`).
    @State private var owner = UUID()
    @State private var visible = false
    @State private var subscribed: Set<SessionKey> = []
    @State private var roomEntries: [MeetingThread.Entry] = []
    @State private var followupEntries: [MeetingThread.Entry] = []
    /// Each Just Me question's answer in the main chat, read from its items when they change.
    @State private var replies: [UUID: Reply] = [:]
    /// The folded rows opened, and the material shown whole.
    @State private var open: Set<String> = []
    @State private var whole: Set<String> = []
    /// The follow-up's answers already there: a new one is announced; nil until its items first came.
    @State private var followupAnswers: Int?

    var body: some View {
        let meeting = conversation.meeting(entry.id)
        let roomKey = conversation.roomKey(entry.id)
        let followupKey = mode == .after ? conversation.followupKey(entry.id) : nil
        let local = localExchanges(hasRoom: roomKey != nil)
        let mine = showsJustMe ? conversation.justMe(for: entry.id) : []
        let sends = mode == .after ? conversation.followups(for: entry.id) : []
        let report = Shown(ids: rowIDs(roomKey: roomKey, meeting: meeting, local: local, mine: mine, sends: sends),
                           exchangeRows: showsJustMe ? [] : Set(local.map(\.id)))
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            if showsJustMe {
                justMeRows(mine)
            } else {
                roomRows(roomKey: roomKey, meeting: meeting, local: local)
                if mode == .after { followupRows(followupKey, sends: sends) }
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        // Rows that land in one change arrive as one group.
        .animation(reduceMotion ? nil : Theme.Motion.reveal, value: report.ids)
        .onTabVisibility(appear: {
            visible = true
            resubscribe()
        }, disappear: {
            visible = false
            resubscribe()
        })
        .onChange(of: wanted) { resubscribe() }
        .onChange(of: "\(signature(roomKey))#\(meeting.asks.count)", initial: true) { rebuildRoom() }
        .onChange(of: signature(followupKey), initial: true) { rebuildFollowup() }
        .onChange(of: sends) { markDelivered() }
        .onChange(of: JustMeInput(asks: mine, chats: chatKeys(mine).map { signature($0) }), initial: true) { rebuildReplies() }
        .onChange(of: report, initial: true) { _, now in shown = now }
    }

    private var showsJustMe: Bool { mode == .live && justMe }

    // MARK: Subscriptions

    /// The conversations to follow: the room (unless Just Me hides it while recording), the follow-up after the meeting,
    /// and the main chat of the Just Me questions sent from here.
    private var wanted: Set<SessionKey> {
        var keys: Set<SessionKey> = []
        if !showsJustMe, let room = conversation.roomKey(entry.id) { keys.insert(room) }
        if mode == .after, let followup = conversation.followupKey(entry.id) { keys.insert(followup) }
        if showsJustMe { keys.formUnion(chatKeys(conversation.justMe(for: entry.id))) }
        return keys
    }

    /// Subscribes to what is wanted while on screen and lets go of the rest (all of it once the page is gone), so the
    /// app's reconnect catch-up never carries a conversation nobody looks at.
    private func resubscribe() {
        let want = visible ? wanted : []
        for key in subscribed.subtracting(want) { model.unsubscribe(key, by: owner) }
        for key in want.subtracting(subscribed) { model.subscribe(key, by: owner) }
        subscribed = want
    }

    private func chatKeys(_ asks: [MeetingConversation.JustMe]) -> [SessionKey] {
        var keys: [SessionKey] = []
        for ask in asks {
            if case .sent(let key, _) = ask.state, !keys.contains(key) { keys.append(key) }
        }
        return keys
    }

    /// What a conversation's items are now, cheaply: rows are derived again only when it changes.
    private func signature(_ key: SessionKey?) -> String {
        guard let key else { return "" }
        let items = model.chat(key).items
        return "\(key.path)|\(items.count)|\(items.first?.id ?? "")|\(items.last?.id ?? "")"
    }

    // MARK: Deriving rows

    private func rebuildRoom() {
        guard let key = conversation.roomKey(entry.id) else {
            roomEntries = []
            return
        }
        let asks = conversation.meeting(entry.id).asks.map {
            MeetingThread.AskInfo(id: $0.id?.lowercased() ?? "", spoken: $0.questionSource == "spoken", at: $0.at)
        }
        roomEntries = MeetingThread.room(model.chat(key).items, recordingID: entry.id, asks: asks)
    }

    private func rebuildFollowup() {
        guard mode == .after, let key = conversation.followupKey(entry.id) else {
            followupEntries = []
            return
        }
        let store = model.chat(key)
        let built = MeetingThread.followup(store.items)
        // A new answer is announced (the microphone is closed after the meeting); the ones there when it first loads
        // are not news.
        let answers = built.compactMap { entry -> String? in
            if case .answer(_, _, let text) = entry { return text }
            return nil
        }
        if store.loadedOnce {
            if let before = followupAnswers, answers.count > before, let latest = answers.last {
                AccessibilityNotification.Announcement(latest).post()
            }
            followupAnswers = answers.count
        }
        followupEntries = built
        markDelivered()
    }

    /// A follow-up message on its way is shown until its own message is in the follow-up's transcript, matched by its
    /// id only (two messages may say the same words).
    private func markDelivered() {
        guard mode == .after, let key = conversation.followupKey(entry.id) else { return }
        let arrived = Set(model.chat(key).items.compactMap { $0.clientMsgID?.lowercased() })
        guard !arrived.isEmpty else { return }
        for send in conversation.followups(for: entry.id)
        where MeetingConversation.clientMsgIDs(for: send).contains(where: { arrived.contains($0.lowercased()) }) {
            conversation.delivered(send.id)
        }
    }

    /// Each sent Just Me question's answer: the main chat's assistant text after its message, up to the owner's next one.
    private func rebuildReplies() {
        var out: [UUID: Reply] = [:]
        for ask in conversation.justMe(for: entry.id) {
            guard case .sent(let key, let clientMsgID) = ask.state else { continue }
            let items = model.chat(key).items
            let wanted = clientMsgID.lowercased()
            guard let start = items.lastIndex(where: { $0.clientMsgID?.lowercased() == wanted }) else { continue }
            var reply = Reply()
            var texts: [String] = []
            for item in items[(start + 1)...] {
                if item.role == "user", item.kind == .text, item.injected == nil, item.clientMsgID?.lowercased() != wanted {
                    reply.latest = false
                    break
                }
                if item.role == "assistant", item.kind == .text, let text = item.text, !text.isEmpty { texts.append(text) }
            }
            reply.answer = texts.isEmpty ? nil : texts.joined(separator: "\n\n")
            out[ask.id] = reply
        }
        replies = out
    }

    /// This phone's questions the thread draws itself: while recording, with a room thread, those it does not hold yet,
    /// those that failed and those answered from the meeting's material; without one, all of them.
    private func localExchanges(hasRoom: Bool) -> [MeetingAsk.Exchange] {
        guard mode == .live else { return [] }
        guard hasRoom else { return exchanges }
        let held = MeetingThread.askIDs(roomEntries)
        return exchanges.filter { exchange in
            if case .failed = exchange.state { return true }
            if exchange.via == "material" { return true }
            return !held.contains(exchange.id.uuidString.lowercased())
        }
    }

    /// The rows' ids in order, for Jump to Latest's count.
    private func rowIDs(roomKey: SessionKey?, meeting: MeetingConversation.Meeting, local: [MeetingAsk.Exchange],
                        mine: [MeetingConversation.JustMe], sends: [MeetingConversation.FollowupSend]) -> [String] {
        if showsJustMe {
            return mine.flatMap { ask in replies[ask.id]?.answer == nil ? ["me-\(ask.id)"] : ["me-\(ask.id)", "me-\(ask.id)-answer"] }
        }
        var ids = roomKey == nil ? (mode == .after ? Self.pairs(meeting.asks, materialOnly: false).map(\.id) : []) : roomEntries.map(\.id)
        if roomKey != nil, mode == .after { ids += Self.pairs(meeting.asks, materialOnly: true).map(\.id) }
        ids += local.map { "exchange-\($0.id)" }
        ids += followupEntries.map(\.id)
        ids += sends.map { "send-\($0.id)" }
        return ids
    }

    /// The hub's answered questions as pairs: all of them for a meeting whose room never came up, or those answered from
    /// the meeting's material beside a room thread.
    private static func pairs(_ asks: [MeetingAskWire.Ask], materialOnly: Bool) -> [AskPair] {
        asks.enumerated().compactMap { index, ask in
            guard ask.state == "answered", let answer = ask.answer, !answer.isEmpty else { return nil }
            if materialOnly, ask.via != "material" { return nil }
            return AskPair(id: "ask-\(ask.id?.lowercased() ?? String(index))", question: ask.question, answer: answer)
        }
    }

    /// Whether the room thread holds a question or an answer yet (its header and fed lines don't count).
    private static func asked(_ entries: [MeetingThread.Entry]) -> Bool {
        entries.contains { entry in
            switch entry {
            case .question, .answer, .heard: true
            default: false
            }
        }
    }

    // MARK: The room

    @ViewBuilder private func roomRows(roomKey: SessionKey?, meeting: MeetingConversation.Meeting,
                                       local: [MeetingAsk.Exchange]) -> some View {
        let role = entry.attendance?.role ?? .off
        if let roomKey {
            let store = model.chat(roomKey)
            if store.hasMore { earlier(roomKey, identifier: "meeting-thread-earlier") }
            ForEach(roomEntries) { item in row(item, markdown: false) }
            if !store.liveText.isEmpty {
                answer(store.liveText, markdown: false, identifier: "meeting-thread-answer-live")
            }
            if mode == .after {
                ForEach(Self.pairs(meeting.asks, materialOnly: true)) { askRows($0) }
            }
        } else if mode == .after {
            ForEach(Self.pairs(meeting.asks, materialOnly: false)) { askRows($0) }
        }
        if mode == .live {
            if roomKey == nil, role.attends, meeting.room?.known == .starting {
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Persona is joining the meeting…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("meeting-thread-joining")
            }
            ForEach(local) { exchange in
                MeetingExchangeRow(exchange: exchange, entry: entry)
                    .padding(Theme.Space.m)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
            }
            if roomKey != nil, meeting.room?.known == .ready, role.attends, local.isEmpty, !Self.asked(roomEntries) {
                Text(role.speaks ? String(localized: "Ask Persona about the meeting. Answers may be heard by the room.")
                     : String(localized: "Ask Persona about the meeting. Answers are shown on this screen."))
                    .font(Theme.Font.secondary).foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("meeting-thread-empty")
            }
        }
    }

    /// One answered question of the hub's `asks`, as the thread draws a room question and its answer.
    @ViewBuilder private func askRows(_ asked: AskPair) -> some View {
        question(asked.question ?? String(localized: "What was just said"), spoken: false)
        answer(asked.answer, markdown: false)
    }

    // MARK: After the meeting

    @ViewBuilder private func followupRows(_ key: SessionKey?, sends: [MeetingConversation.FollowupSend]) -> some View {
        afterDivider
        if let key {
            let store = model.chat(key)
            if store.hasMore { earlier(key, identifier: "meeting-followup-earlier") }
            ForEach(followupEntries) { item in row(item, markdown: true) }
            if !store.liveText.isEmpty {
                answer(store.liveText, markdown: true, identifier: "meeting-thread-answer-live")
            }
        }
        ForEach(sends) { send in
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                question(send.text, spoken: false)
                switch send.state {
                case .sending:
                    sending
                case .sent:
                    EmptyView()
                case .failed(let message):
                    failure(message, identifier: "meeting-followup-retry") { conversation.retryFollowup(send.id) }
                }
            }
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("meeting-followup-pending")
        }
    }

    /// **After the Meeting**: a hairline across the thread with the words in the middle, as the chat marks a compaction
    /// (`CompactDivider`).
    private var afterDivider: some View {
        HStack(spacing: Theme.Space.m) {
            rule
            Text("After the Meeting").font(Theme.Font.captionMedium).foregroundStyle(.secondary)
                .lineLimit(1).minimumScaleFactor(0.7)
                .layoutPriority(1)
            rule
        }
        .padding(.vertical, Theme.Space.xs)
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(.isHeader)
        .accessibilityIdentifier("meeting-after-divider")
    }

    private var rule: some View {
        let beautiful = AppStyle.current.usesBeautifulUI
        return Rectangle().fill(beautiful ? Theme.cardStroke : Color(uiColor: .separator))
            .frame(height: beautiful ? Theme.Size.line : Theme.Size.hairline)
            .frame(maxWidth: .infinity)
            .accessibilityHidden(true)
    }

    // MARK: Just Me

    @ViewBuilder private func justMeRows(_ mine: [MeetingConversation.JustMe]) -> some View {
        if mine.isEmpty {
            Text("Ask privately. Answers stay on this screen and in the main chat.")
                .font(Theme.Font.secondary).foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier("meeting-just-me-empty")
        }
        ForEach(mine) { ask in justMeRow(ask, last: ask.id == mine.last?.id) }
        // The turn's cards are answered in the main chat (their one home); here only that it waits.
        if chatKeys(mine).contains(where: { !model.interactions(for: $0).isEmpty }) {
            Label { Text("Persona is waiting for your answer in the main chat.") } icon: {
                ThemedIcon(Theme.Icon.question).foregroundStyle(Theme.caution)
            }
            .font(Theme.Font.secondary)
            .fixedSize(horizontal: false, vertical: true)
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-just-me-waiting")
        }
    }

    /// One private question: the words, then sending, what went wrong with Retry, or the main chat's answer (its text
    /// being written while its turn runs). Never announced: VoiceOver would say it to the room.
    private func justMeRow(_ ask: MeetingConversation.JustMe, last: Bool) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            question(ask.text, spoken: false, identifier: "meeting-just-me-question")
            switch ask.state {
            case .sending:
                sending
            case .failed(let message):
                failure(message, identifier: "meeting-just-me-retry") { conversation.retryJustMe(ask.id) }
            case .sent(let key, _):
                let reply = replies[ask.id]
                let live = last && reply?.latest != false ? model.chat(key).liveText : ""
                if let text = reply?.answer {
                    answer(text, markdown: true, identifier: "meeting-just-me-answer")
                }
                if !live.isEmpty {
                    answer(live, markdown: true, identifier: "meeting-just-me-answer-live")
                } else if reply?.answer == nil {
                    HStack(spacing: Theme.Space.s) {
                        ProgressView()
                        Text("Persona is looking…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                    }
                    .accessibilityElement(children: .combine)
                }
            }
        }
        .accessibilityElement(children: .contain)
    }

    // MARK: Rows

    @ViewBuilder private func row(_ item: MeetingThread.Entry, markdown: Bool) -> some View {
        switch item {
        case .joined:
            quiet(String(localized: "Persona joined the meeting"))
                .accessibilityIdentifier("meeting-thread-joined")
        case .ended:
            quiet(String(localized: "The meeting ended"))
                .accessibilityIdentifier("meeting-thread-ended")
        case .lines(let id, _, let lines):
            fold(id, title: lines.count == 1 ? String(localized: "1 line from the meeting")
                 : String(localized: "\(lines.count) lines from the meeting"), symbol: Theme.Icon.quote) {
                linesList(lines)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-thread-lines")
        case .heard(let id, _, let clock, let lines, _):
            fold(id, title: clock.isEmpty ? String(localized: "What was just said") : String(localized: "What was just said (\(clock))"),
                 symbol: Theme.Icon.voiceListening) {
                linesList(lines)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier("meeting-thread-heard")
        case .question(_, _, let text, let via, _):
            question(text, spoken: via == .spoken)
        case .answer(_, _, let text):
            answer(text, markdown: markdown)
        case .image(_, _, let image):
            photo(image)
                .accessibilityIdentifier("meeting-thread-image")
        case .material(let id, _, let title, let text, let seed):
            fold(id, title: seed ? String(localized: "The meeting's material") : (title.isEmpty ? String(localized: "Context") : title),
                 symbol: Theme.Icon.minutes) {
                material(id, text)
            }
            // Its Show All is a control of its own.
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("meeting-thread-material")
        }
    }

    /// The owner's own words ("You"); one spoken in the room says so.
    private func question(_ text: String, spoken: Bool, identifier: String = "meeting-thread-question") -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            HStack(spacing: Theme.Space.s) {
                Text("You").font(Theme.Font.captionStrong).foregroundStyle(.secondary)
                if spoken { Text("Spoken").font(Theme.Font.caption).foregroundStyle(.secondary) }
            }
            Text(text).font(Theme.Font.body).textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(identifier)
    }

    /// Persona's answer, in the AI's color: plain in the room (its replies are plain sentences), Markdown in the
    /// follow-up and the main chat.
    private func answer(_ text: String, markdown: Bool, identifier: String = "meeting-thread-answer") -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
            Label { Text("Persona") } icon: { ThemedIcon(Theme.Icon.persona).accessibilityHidden(true) }
                .font(Theme.Font.captionStrong).foregroundStyle(Theme.ai)
            MeetingAnswerText(text: text, markdown: markdown)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(identifier)
    }

    /// A quiet line across the thread: Persona joined, the meeting ended.
    private func quiet(_ text: String) -> some View {
        Text(text).font(Theme.Font.caption).foregroundStyle(.secondary)
            .multilineTextAlignment(.center)
            .frame(maxWidth: .infinity)
            .accessibilityElement(children: .combine)
    }

    private var sending: some View {
        HStack(spacing: Theme.Space.s) {
            ProgressView()
            Text("Sending…").font(Theme.Font.caption).foregroundStyle(.secondary)
        }
        .accessibilityElement(children: .combine)
    }

    private func failure(_ message: String, identifier: String, retry: @escaping () -> Void) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text(message).font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                .fixedSize(horizontal: false, vertical: true)
            Button(action: retry) { Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1) }
                .themedButton()
                .accessibilityIdentifier(identifier)
        }
    }

    private func earlier(_ key: SessionKey, identifier: String) -> some View {
        Group {
            if model.chat(key).isLoadingOlder {
                ProgressView().frame(maxWidth: .infinity).scaledFrame(minHeight: Theme.Size.tapTarget)
            } else {
                Button { Task { await model.loadOlder(key, turns: Theme.Recording.earlierTurns) } } label: {
                    Text("Show Earlier").font(Theme.Font.secondaryStrong).lineLimit(1)
                        .frame(maxWidth: .infinity)
                        .scaledFrame(minHeight: Theme.Size.tapTarget)
                        .contentShape(.rect)
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier(identifier)
            }
        }
    }

    /// A folded row: one line in a capsule, a tap opens what it holds under it.
    private func fold<Content: View>(_ id: String, title: String, symbol: String, @ViewBuilder content: () -> Content) -> some View {
        let isOpen = open.contains(id)
        return VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Button { toggle(id) } label: {
                HStack(spacing: Theme.Space.s) {
                    ThemedIcon(symbol).accessibilityHidden(true)
                    Text(title)
                        .lineLimit(size.isAccessibilitySize ? nil : 1)
                        .multilineTextAlignment(.leading)
                    ThemedIcon(isOpen ? Theme.Icon.collapse : Theme.Icon.expand).accessibilityHidden(true)
                }
                .font(Theme.Font.caption)
                .foregroundStyle(.secondary)
                .padding(.horizontal, Theme.Space.m)
                .padding(.vertical, Theme.Space.xs)
                .background(Theme.Fill.well, in: .capsule)
                .scaledFrame(minHeight: Theme.Size.tapTarget)
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .accessibilityValue(isOpen ? Text("Expanded") : Text("Collapsed"))
            if isOpen {
                content()
                    .padding(Theme.Space.m)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
                    .transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func toggle(_ id: String) {
        var next = open
        if next.contains(id) { next.remove(id) } else { next.insert(id) }
        withAnimation(reduceMotion ? nil : Theme.Motion.state) { open = next }
    }

    /// The `[m:ss] text` lines of a batch or a heard question, each at its time in the meeting.
    private func linesList(_ lines: [MeetingThread.Line]) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            ForEach(lines.indices, id: \.self) { index in
                let stamp = Text(lines[index].clock).font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
                let words = Text(lines[index].text).font(Theme.Font.secondary).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true)
                if size.isAccessibilitySize {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) { stamp; words }
                } else {
                    HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) { stamp; words }
                }
            }
        }
    }

    /// The meeting's material (the follow-up's first message): a few lines, then Show All, so a long transcript does not
    /// push the thread away.
    private func material(_ id: String, _ text: String) -> some View {
        let all = whole.contains(id)
        let long = text.count > Theme.Recording.materialPreview
            || text.split(separator: "\n", omittingEmptySubsequences: false).count > Theme.Recording.materialLines
        return VStack(alignment: .leading, spacing: Theme.Space.s) {
            Text(text).font(Theme.Font.secondary).foregroundStyle(.secondary)
                .lineLimit(all ? nil : Theme.Recording.materialLines)
                .textSelection(.enabled)
                .fixedSize(horizontal: false, vertical: true)
            if long, !all {
                Button { _ = whole.insert(id) } label: { Text("Show All").lineLimit(1) }
                    .font(Theme.Font.secondaryStrong)
                    .buttonStyle(.borderless)
                    .scaledFrame(minHeight: Theme.Size.tapTarget)
                    .accessibilityIdentifier("meeting-thread-material-all")
            }
        }
    }

    /// A photo in the thread: its thumbnail, or a chip when the item carries no picture.
    @ViewBuilder private func photo(_ image: ImagePayload?) -> some View {
        if let image {
            ChatThumbnail(payload: image)
                .scaledFrame(width: Theme.Recording.photoSide, height: Theme.Recording.photoSide)
                .clipShape(.rect(cornerRadius: Theme.Radius.control, style: .continuous))
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(Text("Photo"))
        } else {
            Chip(text: String(localized: "Photo"), symbol: Theme.Icon.photo)
        }
    }
}

/// A Just Me question's answer in the main chat.
private struct Reply: Equatable {
    var answer: String?
    /// No later message of the owner's follows it: its turn is the chat's newest, so the chat's live text is its answer
    /// being written.
    var latest = true
}

/// What the Just Me rows are derived from: the questions and the state of the chats they went to.
private struct JustMeInput: Equatable {
    var asks: [MeetingConversation.JustMe]
    var chats: [String]
}

/// One of the hub's answered questions (`GET …/live` `asks`).
private struct AskPair: Identifiable {
    var id: String
    var question: String?
    var answer: String
}

/// An answer's text: Markdown's inline styles where the conversation writes them, else as it is. Its own view, so a row
/// parses only when its text changes.
private struct MeetingAnswerText: View, Equatable {
    var text: String
    var markdown: Bool

    var body: some View {
        Group {
            if markdown, let rich = try? AttributedString(markdown: text, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)) {
                Text(rich)
            } else {
                Text(verbatim: text)
            }
        }
        .font(Theme.Font.body)
        .textSelection(.enabled)
        .fixedSize(horizontal: false, vertical: true)
    }
}
#endif
