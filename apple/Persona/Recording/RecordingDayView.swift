#if os(iOS) || os(visionOS)
import SwiftUI
import UIKit

// All-day recording on the phone (docs/assistant/day-recap.md): a recorded day's page (its span and gaps, the hub's
// recap, the transcript), the rows that open it, and on the iPhone the Recordings page's Today section with the All-Day
// Recording switch. Everything here reads `RecordingDaySummaries` and its own models, never `RecordingLibrary` entries in
// `body`: a day changes every ten seconds while it records (C30).

/// A recorded day: when it was recorded and what is missing from it, the hub's recap (summary, the timeline by time of
/// day, suggested to-dos) with each item's time opening the transcript there, the transcript, erasing the last ten
/// minutes of today's recording, and delete.
struct RecordingDayView: View {
    var id: UUID
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var summaries = RecordingDaySummaries.shared
    @State private var sync = RecordingSync.shared
    @State private var recap = RecordingDayRecapModel()
    /// The day isn't on this phone (deleted here or by retention).
    @State private var missing = false
    @State private var deleting = false
    @State private var erasing = false
    /// An erase asked for and not done yet: the button waits for it.
    @State private var erasingNow = false
    @State private var failure: String?
    /// The transcript opened at a recap item's time.
    @State private var transcriptAt: RecordingDayTranscriptFocus?

    var body: some View {
        let summary = summaries.byID[id]
        Group {
            if let summary {
                content(summary)
            } else if missing {
                EmptyStateView(symbol: Theme.Icon.allDay, title: String(localized: "Recording Deleted"),
                               message: String(localized: "Deleted from this device. Removal from Knowledge may still be pending."))
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .themedPage()
        .navigationTitle("Recording")
        .navigationBarTitleDisplayMode(.inline)
        // A day not summarized yet (opened before the summaries were read): read now, else it is gone.
        .task(id: summary == nil) {
            guard summaries.byID[id] == nil else { return }
            await summaries.refresh([id])
            missing = summaries.byID[id] == nil
        }
        // Each change of where the day is with the hub looks at the recap again; while it is on its way, it is followed.
        .tabActiveTask(id: RecapKey(summary, round: recap.round)) { await followRecap() }
        .navigationDestination(item: $transcriptAt) { focus in
            RecordingDayTranscriptView(id: id, timeZone: focus.timeZone, focus: focus.at)
        }
        .confirmationDialog("Delete This Recording?", isPresented: $deleting, titleVisibility: .visible) {
            Button("Delete", role: .destructive) { delete() }
        } message: {
            Text(watchRecordsThisDay
                 ? String(localized: "All-day recording stops on your Apple Watch, and today's audio and text are deleted from it and this iPhone now. Its text in Knowledge is removed after sync succeeds.")
                 : String(localized: "The day's audio and text are deleted from this iPhone and your Apple Watch now. Its text in Knowledge is removed after sync succeeds."))
        }
        .confirmationDialog("Erase the last 10 minutes?", isPresented: $erasing, titleVisibility: .visible) {
            Button("Erase", role: .destructive) { Task { await eraseRecent() } }
        } message: {
            Text("What was said in the last 10 minutes is deleted from this iPhone and your Apple Watch now, and from Knowledge after sync succeeds.")
        }
        .alert("Couldn't Finish", isPresented: Binding(get: { failure != nil }, set: { if !$0 { failure = nil } })) {
            Button("OK [Recordings Section de32e2]") { failure = nil }.lineLimit(1)
        } message: { Text(failure ?? "") }
    }

    private func content(_ summary: RecordingDaySummary) -> some View {
        let zone = RecordingDayStatus.zone(summary.timeZone)
        return List {
            // The recap state this page last read, before the day's summary catches up with it.
            RecordingDayPageHeader(summary: recap.shown(summary), zone: zone)
            RecordingDayRecapSections(summary: summary, model: recap, zone: zone) { open($0, zone: zone) }
            Section {
                NavigationLink { RecordingDayTranscriptView(id: id, timeZone: zone, focus: nil) } label: {
                    Label("Transcript", systemImage: Theme.Icon.text)
                }
                .accessibilityIdentifier("day-transcript")
                // Today's recording only, while it records: one tap away, as on the watch
                // (docs/assistant/day-recap.md「Retention and privacy」).
                if RecordingDayStatus.offersErase(summary, now: .now) {
                    Button(role: .destructive) { erasing = true } label: {
                        HStack(spacing: Theme.Space.s) {
                            Label("Erase Last 10 Minutes", systemImage: Theme.Icon.delete).lineLimit(1)
                            if erasingNow { ProgressView() }
                        }
                    }
                    .disabled(erasingNow)
                    .accessibilityIdentifier("day-erase")
                }
                Button("Delete Recording", role: .destructive) { deleting = true }
                    .lineLimit(1)
                    .accessibilityIdentifier("day-delete")
            } footer: {
                // Read once, at the end: not between the header and the recap.
                Text("Audio stays on this iPhone until 3 days after the day's final recap, 7 days at most. Only the text goes to Knowledge, where it's kept for \(sync.store.days) days; the recap is kept like meeting minutes.")
            }
        }
        // The recap unfolds when it arrives (Reduce Motion: at once).
        .animation(reduceMotion ? nil : Theme.Motion.reveal, value: recap.state)
    }

    /// Looks at the recap, then again every `pollSeconds` while it is on its way (each look reads the light list, and a
    /// recap only when its state moved).
    private func followRecap() async {
        guard let summary = summaries.byID[id] else { return }
        await recap.load(summary)
        while !Task.isCancelled, recap.waiting {
            try? await Task.sleep(for: .seconds(RecordingDayRecapModel.pollSeconds))
            guard !Task.isCancelled, let summary = summaries.byID[id] else { return }
            await recap.load(summary)
        }
    }

    private func open(_ at: Date?, zone: TimeZone) {
        transcriptAt = RecordingDayTranscriptFocus(at: at, timeZone: zone)
    }

    /// The watch is recording into this very day: deleting it also stops all-day recording there, or the rest of the
    /// day would be recorded and dropped unseen.
    private var watchRecordsThisDay: Bool {
        guard let watch = summaries.watch else { return false }
        return watch.recording == id && watch.isOn
    }

    private func delete() {
        let recorded = watchRecordsThisDay
        do {
            // Every part of the day goes from the hub, its transcription, upload and store let go of it here, and the
            // watch is asked to delete its copy until it confirms (`RecordingDaySync.removedHere`), which also stops it
            // when it records this day (docs/assistant/day-recap.md「Deleting」).
            try sync.delete(id)
            #if os(iOS)
            if recorded { RecordingDaySwitch.shared.deletedRecordedDay() }
            #endif
            dismiss()
        } catch {
            failure = String(localized: "The recording wasn't deleted. Try again later.")
        }
    }

    /// Erases the last ten minutes on this phone, the hub and the watch, as one erase: from every day recording it
    /// touches (one that spans midnight reaches both; docs/assistant/day-recap.md「Erasing」). The store keeps the request
    /// for the watch until the watch confirms it, and the upload repairs the hub.
    private func eraseRecent() async {
        guard !erasingNow else { return }
        erasingNow = true
        defer { erasingNow = false }
        let now = Date.now
        let erased = await summaries.requestErase(from: now.addingTimeInterval(-RecordingRolling.eraseMinutes * 60), to: now)
        if erased.isEmpty {
            failure = String(localized: "The last 10 minutes weren't erased. Try again.")
        } else {
            Haptics.success()
            AppModel.shared.toast = Toast(String(localized: "Erased the last 10 minutes"), style: .success)
        }
    }
}

/// What the day page's recap task is keyed on: where the day is with the hub (its recap's state and whether it is the
/// final one), its parts, and Retry.
private nonisolated struct RecapKey: Hashable, Sendable {
    var sync: RecordingDaySyncState?
    var recapState: String?
    var recapFinal: Bool
    var hubIDs: [UUID]
    var complete: Bool
    var round: Int

    init(_ summary: RecordingDaySummary?, round: Int) {
        sync = summary?.sync
        recapState = summary?.recapState
        recapFinal = summary?.recapFinal ?? false
        hubIDs = summary?.hubIDs ?? []
        complete = summary?.complete ?? false
        self.round = round
    }
}

/// Where the transcript page opens.
nonisolated struct RecordingDayTranscriptFocus: Hashable, Sendable {
    var at: Date?
    var timeZone: TimeZone
}

// MARK: - Header

/// The day's date, when it was recorded, how much is transcribed, its one state word, and its gaps by reason.
private struct RecordingDayPageHeader: View {
    var summary: RecordingDaySummary
    var zone: TimeZone

    var body: some View {
        Section {
            Text(RecordingDayStatus.title(day: summary.day, timeZone: summary.timeZone)).font(Theme.Font.heading)
                .accessibilityIdentifier("day-title")
            if let span = RecordingDayStatus.span(summary) {
                Label(RecordingDayStatus.spanText(span, in: zone), systemImage: Theme.Icon.time)
                    .font(Theme.Font.secondary).foregroundStyle(.secondary)
            }
            if let progress = RecordingDayStatus.progress(summary) {
                Text(progress.text(in: zone)).font(Theme.Font.secondary).foregroundStyle(.secondary)
            }
            RecordingDayWordText(word: RecordingDayStatus.word(summary)).font(Theme.Font.secondary)
                .accessibilityIdentifier("day-state")
            ForEach(RecordingDayStatus.gapTotals(summary.gaps), id: \.reason) { gap in
                LabeledContent(gap.reason.text, value: RecordingDayStatus.duration(gap.seconds))
                    .font(Theme.Font.secondary)
                    .accessibilityIdentifier("day-gap")
            }
        }
    }
}

/// A day's state word in its tone.
struct RecordingDayWordText: View {
    var word: RecordingDayStatus.Word

    var body: some View {
        Text(word.text).foregroundStyle(word.tone.color)
    }
}

extension RecordingDayStatus.Tone {
    var color: Color {
        switch self {
        case .secondary: .secondary
        case .accent: Theme.accent
        case .positive: Theme.positive
        case .caution: Theme.caution
        case .danger: Theme.danger
        }
    }
}

// MARK: - The recap

/// The recap's sections, in the minutes' layout (`RecordingMinutesSections`): before it exists, what it waits for; once
/// it does, the summary with Copy and Share, the timeline under Morning / Afternoon / Evening with meetings marked, and
/// the suggested to-dos (read-only: they are confirmed on the web). Every time opens the transcript there.
private struct RecordingDayRecapSections: View {
    var summary: RecordingDaySummary
    var model: RecordingDayRecapModel
    var zone: TimeZone
    var open: (Date?) -> Void

    var body: some View {
        switch model.state {
        case .none:
            EmptyView()
        case .waitingForText:
            waiting(String(localized: "The hub writes it in the evening, and again once the day's recording ends."), busy: false)
        case .waitingForSync:
            waiting(String(localized: "It's written once the day's text reaches the hub."), busy: false)
        case .writing:
            waiting(String(localized: "The hub is turning the day's text into a timeline, key points and suggested to-dos."), busy: true)
        case .unsupported:
            Section("Day Recap") {
                Text("This hub doesn't write day recaps yet. The day's text stays on this iPhone until it's updated.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
                    .accessibilityIdentifier("dayrecap-unsupported")
            }
        case .failed:
            Section("Day Recap") {
                Label("The recap couldn't be written.", systemImage: Theme.Icon.error).foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("dayrecap-failed")
                retry
            }
        case .ready(let content, let note):
            ready(content, note: note)
        }
    }

    private func waiting(_ detail: String, busy: Bool) -> some View {
        Section("Day Recap") {
            HStack(spacing: Theme.Space.s) {
                if busy { ProgressView() }
                Text(busy ? String(localized: "Writing the recap…") : String(localized: "The recap comes later")).font(Theme.Font.bodyStrong)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier(busy ? "dayrecap-writing" : "dayrecap-waiting")
            Text(detail).font(Theme.Font.caption).foregroundStyle(.secondary)
        }
    }

    private var retry: some View {
        Button { Task { await model.regenerate(summary) } } label: {
            Label("Retry", systemImage: Theme.Icon.retry).lineLimit(1)
        }
        .themedButton()
        .disabled(model.asking)
        .accessibilityIdentifier("dayrecap-retry")
    }

    @ViewBuilder private func ready(_ content: RecordingDayRecapContent, note: RecordingDayRecapState.Note?) -> some View {
        Section {
            if content.isEmpty {
                Text("Nothing in the day's text was worth noting.").font(Theme.Font.secondary).foregroundStyle(.secondary)
            } else if !content.summary.isEmpty {
                Text(content.summary).font(Theme.Font.body).textSelection(.enabled)
                    .accessibilityIdentifier("dayrecap-summary")
            }
            // Whether newer text may still change it, then when it was written.
            Text(content.finality.text).font(Theme.Font.caption).foregroundStyle(.secondary)
                .accessibilityIdentifier("dayrecap-finality")
            if let written = content.writtenAt {
                Text("Written \(written.formatted(.relative(presentation: .named)))").font(Theme.Font.caption).foregroundStyle(.secondary)
            }
            if content.partial {
                Text("The day was too long to read in full, so part of it isn't in the recap.")
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
            }
            switch note {
            case .updating:
                HStack(spacing: Theme.Space.s) {
                    ProgressView()
                    Text("Updating the recap…").font(Theme.Font.secondary).foregroundStyle(.secondary)
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("dayrecap-updating")
            case .failed:
                Label("The newest recap couldn't be written.", systemImage: Theme.Icon.error)
                    .font(Theme.Font.secondary).foregroundStyle(Theme.danger)
                    .accessibilityIdentifier("dayrecap-failed")
                retry
            case nil:
                EmptyView()
            }
            if !content.isEmpty {
                HStack(spacing: Theme.Space.m) {
                    Button { copy(content) } label: { Label("Copy", systemImage: Theme.Icon.copy).lineLimit(1) }
                        .themedButton()
                        .accessibilityIdentifier("dayrecap-copy")
                    ShareLink(item: content.shareText, subject: Text(RecordingDayStatus.title(day: summary.day, timeZone: summary.timeZone))) {
                        Label("Share", systemImage: Theme.Icon.share).lineLimit(1)
                    }
                    .themedButton()
                    .accessibilityIdentifier("dayrecap-share")
                }
            }
        } header: {
            Label("Day Recap", systemImage: Theme.Icon.dayRecap)
        }
        ForEach(content.sections) { section in
            Section(section.period.title) {
                ForEach(section.entries) { entry in
                    entryHeader(entry)
                    ForEach(entry.points) { point in
                        item(point.text, byline: point.who, at: point.at, symbol: Theme.Icon.keyPoint)
                            .accessibilityIdentifier("dayrecap-point")
                    }
                }
            }
        }
        if !content.actions.isEmpty {
            Section {
                ForEach(content.actions) { action in
                    item(action.what, byline: RecordingMinutes.byline(.init(text: action.what, who: action.who, by: action.by,
                                                                            dueDate: action.dueDate)), at: action.at,
                         symbol: action.confirmed ? Theme.Icon.success : Theme.Icon.todo)
                        .accessibilityValue(action.confirmed ? Text("Added to to-dos") : Text(verbatim: ""))
                        .accessibilityIdentifier("dayrecap-action")
                }
            } header: {
                Text("Suggested To-Dos")
            } footer: {
                Text("Suggested from what was said. Confirm them on the web to add them to your to-dos.")
            }
        }
    }

    /// A stretch of the day: its title, its time, and whether it was a meeting (by name when the hub knows it). The
    /// time opens the transcript at its start.
    private func entryHeader(_ entry: RecordingDayRecapContent.Entry) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(entry.title).font(Theme.Font.bodyStrong)
                if entry.meeting {
                    Label {
                        Text(entry.meetingName.flatMap { $0 == entry.title ? nil : $0 } ?? String(localized: "Meeting"))
                    } icon: {
                        ThemedIcon(Theme.Icon.meetingMark)
                    }
                    .font(Theme.Font.caption).foregroundStyle(Theme.accent)
                }
            }
            Spacer(minLength: Theme.Space.s)
            timeChip(entry.from, text: RecordingDayStatus.spanText(entry.from..<entry.to, in: zone))
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("dayrecap-entry")
    }

    /// A point or a to-do: its mark, its words with who and by when, and the time it was said.
    private func item(_ text: String, byline: String?, at: Date?, symbol: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            ThemedIcon(symbol).font(Theme.Font.secondary).foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.iconXS)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text(text).font(Theme.Font.body).textSelection(.enabled)
                if let byline {
                    Text(byline).font(Theme.Font.caption).foregroundStyle(.secondary)
                }
            }
            Spacer(minLength: Theme.Space.s)
            if let at { timeChip(at, text: RecordingDayStatus.time(at, in: zone)) }
        }
    }

    /// A time that opens the transcript where it was said (the minutes' time chip, on the clock).
    private func timeChip(_ at: Date, text: String) -> some View {
        Button { open(at) } label: {
            Chip(text: text, look: .tint(Theme.accent), compact: true)
                .scaledFrame(minWidth: Theme.Size.tapTarget, minHeight: Theme.Size.tapTarget)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityLabel(String(localized: "Show in transcript at \(RecordingDayStatus.time(at, in: zone))"))
        .accessibilityIdentifier("dayrecap-time")
    }

    private func copy(_ content: RecordingDayRecapContent) {
        UIPasteboard.general.string = content.shareText
        Haptics.success()
        AppModel.shared.toast = Toast(String(localized: "Recap copied"), style: .success)
    }
}

extension RecordingDayRecapContent.Period {
    var title: String {
        switch self {
        case .morning: String(localized: "Morning")
        case .afternoon: String(localized: "Afternoon")
        case .evening: String(localized: "Evening")
        }
    }
}

extension RecordingDayRecapContent.Finality {
    var text: String {
        switch self {
        case .soFar: String(localized: "Recap so far")
        case .final: String(localized: "Final recap")
        case .cutoff: String(localized: "Final recap of what reached the hub in time")
        }
    }
}

// MARK: - The transcript

/// The day's transcript from this phone, by the hour, read off the main actor when the page opens. Opened from a recap
/// item, it scrolls to the line said then and lights it for a moment.
struct RecordingDayTranscriptView: View {
    var id: UUID
    var timeZone: TimeZone
    var focus: Date?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var hours: Loadable<[RecordingDayTranscript.Hour]> = .loading
    @State private var lit: String?

    var body: some View {
        LoadableContent(state: hours, retry: nil) { hours in
            if hours.isEmpty {
                EmptyStateView(symbol: Theme.Icon.text, title: String(localized: "Transcript"), message: String(localized: "No transcript yet."))
            } else {
                ScrollViewReader { proxy in
                    List {
                        ForEach(hours) { hour in
                            Section(RecordingDayStatus.time(hour.id, in: timeZone)) {
                                ForEach(hour.lines) { line in row(line) }
                            }
                        }
                    }
                    .task(id: focus) { await jump(in: hours, proxy: proxy) }
                }
            }
        }
        .themedPage()
        .navigationTitle("Transcript")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
    }

    private func row(_ line: RecordingDayTranscript.Line) -> some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            Text(line.at.formatted(Date.FormatStyle(date: .omitted, time: .standard, timeZone: timeZone)))
                .font(Theme.Font.caption.monospacedDigit()).foregroundStyle(.secondary)
            Text(line.text).font(Theme.Font.body).textSelection(.enabled)
        }
        .id(line.id)
        .listRowBackground(lit == line.id ? Theme.accentTint : nil)
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier(lit == line.id ? "day-transcript-line-lit" : "day-transcript-line")
    }

    /// The day's lines from the store (its queue), built into hours off the main actor.
    private func load() async {
        let record = await RecordingDayStore.shared.record(id)
        let zone = timeZone
        let built = await Task.detached(priority: .userInitiated) {
            record.map { RecordingDayTranscript.hours(RecordingDayTranscript.lines($0), in: zone) } ?? []
        }.value
        hours = .loaded(built)
    }

    /// To the line said at `focus`, lit for two seconds.
    private func jump(in hours: [RecordingDayTranscript.Hour], proxy: ScrollViewProxy) async {
        guard let focus, let line = RecordingDayTranscript.line(at: focus, in: hours.flatMap(\.lines)) else { return }
        // The list lays its rows out after it appears; a scroll asked for before that lands nowhere.
        try? await Task.sleep(for: .milliseconds(100))
        guard !Task.isCancelled else { return }
        withAnimation(reduceMotion ? nil : Theme.Motion.layout) { proxy.scrollTo(line.id, anchor: .center) }
        lit = line.id
        Task {
            try? await Task.sleep(for: .seconds(2))
            if lit == line.id { withAnimation(reduceMotion ? nil : Theme.Motion.standard) { lit = nil } }
        }
    }
}

// MARK: - Rows

/// A past day in Recent Recordings: its date, when it was recorded and its state word, from its summary (never
/// `RecordingRow`, whose words are a clip's).
struct RecordingDayRow: View {
    var entry: RecordingEntry
    @State private var summaries = RecordingDaySummaries.shared
    @Environment(\.dynamicTypeSize) private var size

    var body: some View {
        let summary = summaries.byID[entry.id]
        HStack(spacing: Theme.Space.m) {
            ThemedIcon(Theme.Icon.allDay).font(Theme.Font.heading).foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.icon)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                Text(entry.displayTitle).font(Theme.Font.body).lineLimit(1)
                HStack(spacing: Theme.Space.xs) {
                    if let summary {
                        if let span = RecordingDayStatus.span(summary) {
                            Text(verbatim: "\(RecordingDayStatus.spanText(span, in: RecordingDayStatus.zone(summary.timeZone))) ·")
                        }
                        RecordingDayWordText(word: RecordingDayStatus.word(summary))
                            .accessibilityIdentifier("recording-day-state")
                    } else {
                        // Until the day's summary is read (a second after the page opens).
                        Text(entry.sourceTitle)
                    }
                }
                .font(Theme.Font.caption).foregroundStyle(.secondary)
                .lineLimit(size.isAccessibilitySize ? 3 : 1)
            }
        }
    }
}

#if os(iOS)
// MARK: - Today

/// Knowledge › Recordings' Today (between the capture controls and Recent Recordings): the All-Day Recording switch,
/// which mirrors the watch (on while it records or waits for Continue) and asks it to start or stop; until the user agreed
/// to the consent sheet, a row that opens it (the watch points here: "Agree on your iPhone to start"); today's day
/// recording; and a caution when the hub doesn't write recaps yet. Shown with a paired watch that has Persona, or once a
/// day exists.
struct RecordingDayTodaySection: View {
    /// Today's day recording (`RecordingDayStatus.todayID`).
    var dayID: UUID?
    /// The hub lists `recording_day`.
    var offered: Bool
    @State private var summaries = RecordingDaySummaries.shared
    @State private var control = RecordingDaySwitch.shared
    @State private var consent = RecordingDayConsent.shared
    @State private var sync = RecordingSync.shared
    @Environment(\.dynamicTypeSize) private var size

    /// Whether the section shows without a day: the phone always tells a paired watch that it takes all-day recordings
    /// (docs/assistant/day-recap.md「Watch and phone capabilities」), so the watch offers the mode, and the phone must offer
    /// the agreement and the switch, whatever the hub supports.
    static var watchOffersIt: Bool {
        #if DEBUG
        // The made-up days stand in for a watch (`-showDaySample`).
        if RecordingSamples.daySampleMode != nil { return true }
        #endif
        return WatchSync.shared.watchAppInstalled
    }

    var body: some View {
        let watch = summaries.watch
        let pending = control.pending
        let now = control.lookedAt
        let line = RecordingDayStatus.line(watch: watch, pending: pending, now: now)
        let summary = dayID.flatMap { summaries.byID[$0] }
        Section {
            SettingsSwitchRow(title: String(localized: "All-Day Recording"), detail: line.text(in: .autoupdatingCurrent),
                              symbol: Theme.Icon.allDay, accessibilityID: "allday-switch",
                              isOn: Binding(get: { RecordingDayStatus.isOn(watch: watch, pending: pending, now: now) },
                                            set: { control.set($0) }))
                // On the one row the section always has: a sheet on the section would be one per row.
                .sheet(item: Binding(get: { control.asking }, set: { if $0 == nil { control.consentAnswered(agreed: false) } })) { _ in
                    RecordingDayConsentView(transcriptDays: sync.store.days) { control.consentAnswered(agreed: $0) }
                }
            if !consent.agreed {
                Button { control.askConsent() } label: {
                    Label("Agree to Start on Apple Watch", systemImage: Theme.Icon.privacy)
                        .lineLimit(size.isAccessibilitySize ? 3 : 1)
                }
                .accessibilityIdentifier("allday-consent")
            }
            if let dayID {
                NavigationLink { RecordingDayView(id: dayID) } label: { RecordingDayTodayRow(summary: summary) }
                    .accessibilityIdentifier("allday-today")
            }
            if !offered || summary?.sync == .unsupported {
                NoticeBanner(text: String(localized: "This hub doesn't write day recaps yet. The day's text stays on this iPhone until it's updated."), tone: .caution)
                    .listRowBackground(Color.clear)
                    .listRowInsets(EdgeInsets())
                    .accessibilityIdentifier("allday-hub-update")
            }
        }
        // Also on appearing: the watch may have answered while the section was away.
        .onChange(of: watch, initial: true) { _, next in control.watchChanged(next) }
        // The line moves by itself at each of a command's deadlines; a new command or the watch's answer starts over.
        .task(id: pending) { await control.follow { summaries.watch } }
    }
}

/// Today's day: how much is recorded and transcribed, and where it is (the same word as its page).
private struct RecordingDayTodayRow: View {
    var summary: RecordingDaySummary?

    var body: some View {
        Label {
            VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                Text("Today")
                if let summary {
                    if let progress = RecordingDayStatus.progress(summary) {
                        Text(progress.text(in: RecordingDayStatus.zone(summary.timeZone)))
                            .font(Theme.Font.caption).foregroundStyle(.secondary)
                    }
                    RecordingDayWordText(word: RecordingDayStatus.word(summary)).font(Theme.Font.caption)
                        .accessibilityIdentifier("allday-today-state")
                }
            }
        } icon: {
            ThemedIcon(Theme.Icon.dayRecap)
        }
    }
}

/// The phone's side of the switch: the command it sent the watch, shown as on its way until the watch's state answers
/// it (`RecordingDayStatus.settle`), and the consent sheet that comes before the first start. Shared, so the Recordings
/// page and a deleted day agree, and a page that goes and comes back still shows a command on its way.
@MainActor @Observable final class RecordingDaySwitch {
    static let shared = RecordingDaySwitch()

    /// Why the consent sheet is up: the Agree row, or the switch turned on before the user agreed (the start goes once
    /// they do).
    nonisolated enum ConsentAsk: String, Identifiable, Sendable {
        case agree, start
        var id: String { rawValue }
    }

    private(set) var pending: RecordingDayStatus.Pending?
    /// The consent sheet is up, and why; nil while it isn't.
    private(set) var asking: ConsentAsk?
    /// The time the switch's line is worked out for: moved on when the user flips it, when the Today section appears,
    /// and at each of a command's deadlines while it shows (`follow`), so the line changes then and not only on a redraw.
    private(set) var lookedAt: Date
    @ObservationIgnored private let send: (String) -> Void
    @ObservationIgnored private let agreed: () -> Bool
    @ObservationIgnored private let agree: () -> Void
    @ObservationIgnored private let now: () -> Date
    @ObservationIgnored private let sleep: (Date) async throws -> Void

    /// - Parameters:
    ///   - agreed, agree: the consent (`RecordingDayConsent`): whether the user agreed to the sheet's current version,
    ///     and storing that they did (which tells the watch).
    ///   - sleep: waits until the given time; throws when cancelled.
    init(send: @escaping (String) -> Void = { WatchSync.shared.sendDayCommand($0) },
         agreed: @escaping () -> Bool = { RecordingDayConsent.shared.agreed },
         agree: @escaping () -> Void = { RecordingDayConsent.shared.agree() },
         now: @escaping () -> Date = { .now },
         sleep: @escaping (Date) async throws -> Void = { try await Task.sleep(for: .seconds(max(0, $0.timeIntervalSinceNow))) }) {
        self.send = send
        self.agreed = agreed
        self.agree = agree
        self.now = now
        self.sleep = sleep
        lookedAt = now()
    }

    /// The user flipped the switch: the watch is asked to start (it needs a tap there to open the microphone) or stop.
    /// Nothing records before the user agreed to the consent sheet (docs/assistant/day-recap.md「Retention and privacy」):
    /// a start without it opens the sheet instead, and goes once they agree.
    func set(_ on: Bool) {
        if on, !agreed() {
            asking = .start
            return
        }
        lookedAt = now()
        pending = RecordingDayStatus.Pending(start: on, at: lookedAt)
        send(on ? RecordingDayContract.Command.start : RecordingDayContract.Command.stop)
    }

    /// The Agree row: the consent sheet, with nothing to start after it (the user starts on the watch, which asked).
    func askConsent() {
        asking = .agree
    }

    /// The sheet's answer: Agree stores the agreement and, when the switch asked, starts; Not Now (or a swipe down)
    /// leaves everything as it was. An answer without the sheet up does nothing, so the sheet's own dismissal after
    /// Agree is not a second answer.
    func consentAnswered(agreed yes: Bool) {
        guard let asked = asking else { return }
        asking = nil
        guard yes else { return }
        agree()
        if asked == .start { set(true) }
    }

    /// The day the watch records was deleted here: the delete stops it there (docs/assistant/day-recap.md「Deleting」),
    /// so the switch shows a stop on its way, without a command of its own, until the watch says it is off.
    func deletedRecordedDay() {
        lookedAt = now()
        pending = RecordingDayStatus.Pending(start: false, at: lookedAt)
    }

    /// Keeps the line current while the Today section shows: now, then at each deadline of the command on its way (no
    /// answer after `answerWait`, back to the watch's state `noAnswerShown` later) until none is left. Returns when
    /// cancelled; the section starts it again for a new command or the watch's answer.
    func follow(_ watch: () -> RecordingDayWatchState?) async {
        lookedAt = now()
        while let next = RecordingDayStatus.nextChange(watch: watch(), pending: pending, now: lookedAt) {
            do { try await sleep(next) } catch { return }
            // Never behind the deadline it waited for, or the same deadline would be waited for again.
            lookedAt = max(now(), next)
        }
    }

    /// The watch published its state: a command it carried out is no longer on its way.
    func watchChanged(_ watch: RecordingDayWatchState?) {
        let next = RecordingDayStatus.settle(pending, watch: watch)
        if next != pending { pending = next }
    }
}
#endif
#endif
