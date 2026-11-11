#if DEBUG
// i18n-ignore-file: DEBUG-only made-up recording for screenshots
import Foundation

/// Demo and screenshots use made-up text only and never start the microphone; with a hub paired they sync into its Knowledge as usual (UI tests check this step with the mock hub).
enum RecordingSamples {
    /// Once per launch: the page appears again after its recording was deleted, and must not bring it back.
    nonisolated(unsafe) private static var installed = false

    static func installIfAsked() -> UUID? {
        guard UserDefaults.standard.bool(forKey: "showRecordingSample"), !installed else { return nil }
        installed = true
        let id = UUID(uuidString: "00000000-0000-0000-0000-000000000001")!
        RecordingLibrary.shared.restore(id)
        var entry = RecordingEntry(id: id, createdAt: Date(timeIntervalSince1970: 1_800_000_000), source: .watch,
                                   title: "项目讨论", active: false, expectedSegments: 1)
        entry.segments = [RecordingSegment(index: 0, start: 0, duration: 90, sentences: [
            RecordingSentence(start: 0, end: 12, text: "这次讨论确定三个重点：先验证核心流程，再补充测试，最后检查体验。"),
            RecordingSentence(start: 28, end: 40, text: "下周整理一次进展，把未解决的问题列出来。"),
            RecordingSentence(start: 65, end: 78, text: "需要把今天的结论变成待办，避免遗漏。"),
        ])]
        entry.marks = [28]
        try? RecordingLibrary.shared.save(entry)
        return id
    }

    /// The meeting being recorded (`-showMeetingSample recording|paused|interrupted`).
    static let liveMeeting = UUID(uuidString: "00000000-0000-0000-0000-000000000002")!
    /// A finished meeting, transcribed (`-showMeetingSample minutes|pending` opens it).
    static let pastMeeting = UUID(uuidString: "00000000-0000-0000-0000-000000000003")!

    #if os(iOS)
    /// The lines the `flow` sample hears, one segment of one sentence each, a few seconds apart.
    private static let flowLines = [
        "好，我们开始，今天先看录音入口的进展。",
        "新版入口已经合进主干，开始录音只要一步。",
        "锁屏和灵动岛可以看到时长，也能直接停止。",
        "决定：默认用中文转写，中英混说也要能用。",
        "张三负责周五前补上长录音的真机测试。",
        "还没定的是 Mac 上要不要也支持录音。",
        "下周同一时间再对一次进展，今天就到这里。",
    ]
    nonisolated(unsafe) private static var flowTask: Task<Void, Never>?

    /// `-showMeetingSample flow`: a meeting that records itself with no microphone. The clock and the level move, a line is
    /// heard every few seconds, and Stop, Pause and Bookmark work (`RecordingCapture`'s DEBUG sample paths), so the
    /// simulator can show the whole way from the live sheet through the wrap-up to the minutes (mock hub, `recording_minutes`).
    @MainActor private static func startFlow(_ meeting: RecordingMeeting) {
        RecordingLibrary.shared.restore(liveMeeting)
        var live = RecordingEntry(id: liveMeeting, createdAt: .now, source: .phone, title: "产品周会", active: true)
        live.meeting = meeting
        live.attendance = RecordingAttendance(role: .participant, session: nil, sessionTitle: "Persona")
        try? RecordingLibrary.shared.save(live)
        let capture = RecordingCapture.shared
        capture.showSample(liveMeeting, elapsed: 1)
        flowTask?.cancel()
        flowTask = Task { @MainActor in
            var elapsed = 1.0
            var heard = 0
            var lastLine = 0.0
            while !Task.isCancelled, capture.recordingID == liveMeeting {
                try? await Task.sleep(for: .milliseconds(100))
                guard capture.state == .recording else { capture.advanceSample(elapsed: elapsed, level: 0); continue }
                elapsed += 0.1
                // Speech in bursts: a slow swell and a quicker flutter.
                let swell = max(0, sin(elapsed * 0.9)), flutter = 0.5 + 0.5 * sin(elapsed * 9)
                capture.advanceSample(elapsed: elapsed, level: min(1, 0.08 + 0.7 * swell * flutter))
                if elapsed - lastLine >= 6, heard < flowLines.count {
                    lastLine = elapsed
                    let start = max(0, elapsed - 8), line = flowLines[heard], index = heard
                    heard += 1
                    RecordingLibrary.shared.change(liveMeeting) { entry in
                        entry.segments.append(RecordingSegment(index: index, start: start, duration: 6, sentences: [
                            RecordingSentence(start: start, end: start + 5, text: line)]))
                    }
                }
            }
        }
    }
    #endif

    /// `-showMeetingSample recording|paused|interrupted`: a meeting 12:34 into its recording, as the recording page and its
    /// Live Activity show it (no microphone); `listening|taking-part|answered`: the same with Persona attending (#2981);
    /// `minutes` / `pending`: a finished meeting whose page opens (its minutes from the hub, or still being written). Both
    /// recordings are made up.
    static func showMeetingIfAsked() {
        #if os(iOS)
        guard let mode = UserDefaults.standard.string(forKey: "showMeetingSample") else {
            // A run without them: an earlier run's made-up meetings go, so other tests see only their own recordings.
            for id in [liveMeeting, pastMeeting] where RecordingLibrary.shared.entry(id) != nil { try? RecordingLibrary.shared.remove(id) }
            return
        }
        let start = Date(timeIntervalSince1970: 1_800_003_600)
        let meeting = RecordingMeeting(eventID: "sample-weekly", title: "产品周会", start: start, end: start.addingTimeInterval(3600),
                                       location: "3 楼会议室", attendees: ["张三", "李四", "王五"])
        RecordingLibrary.shared.restore(pastMeeting)
        var past = RecordingEntry(id: pastMeeting, createdAt: start, source: .phone, title: "产品周会", active: false, expectedSegments: 2)
        past.meeting = meeting
        past.segments = [
            RecordingSegment(index: 0, start: 0, duration: 60, sentences: [
                RecordingSentence(start: 2, end: 14, text: "今天先过一下新版录音入口，目标是一步就能开始录。"),
                RecordingSentence(start: 21, end: 38, text: "锁屏和灵动岛要能看到时长，也能直接停止。"),
                RecordingSentence(start: 44, end: 58, text: "决定：默认用中文转写，中英混说也要能用。"),
            ]),
            RecordingSegment(index: 1, start: 60, duration: 60, sentences: [
                RecordingSentence(start: 63, end: 80, text: "张三负责周五前补上长录音的真机测试。"),
                RecordingSentence(start: 86, end: 101, text: "还没定的是 Mac 上要不要也支持录音。"),
                RecordingSentence(start: 105, end: 118, text: "李四负责下周同一时间约大家再对一次进展。"),
            ]),
        ]
        past.marks = [44]
        try? RecordingLibrary.shared.save(past)
        if mode == "minutes" || mode == "pending" { return }
        if mode == "flow" { startFlow(meeting); return }

        RecordingLibrary.shared.restore(liveMeeting)
        var live = RecordingEntry(id: liveMeeting, createdAt: .now.addingTimeInterval(-754), source: .phone, title: "产品周会", active: true)
        live.meeting = meeting
        live.segments = [RecordingSegment(index: 0, start: 0, duration: 30, sentences: [
            RecordingSentence(start: 1, end: 9, text: "好，我们开始，今天先看录音入口的进展。"),
        ])]
        live.marks = [65, 300]
        // `listening` / `taking-part` / `answered`: Persona attends (#2981), listening in, taking part, or taking part with two
        // answers given. The main chat attends (no session named); the made-up meeting's upload tells the hub, as a real one
        // would, and its lines go to the hub's live log, the last one just before the tap (12:34).
        if ["listening", "taking-part", "answered"].contains(mode) {
            live.attendance = RecordingAttendance(role: mode == "listening" ? .listener : .participant, session: nil, sessionTitle: "Persona")
            live.segments.append(RecordingSegment(index: 1, start: 720, duration: 30, sentences: [
                RecordingSentence(start: 741, end: 748, text: "上周定的长录音测试谁负责？"),
            ]))
        }
        try? RecordingLibrary.shared.save(live)
        RecordingCapture.shared.showSample(liveMeeting, elapsed: 754, paused: mode == "paused", interrupted: mode == "interrupted")
        if mode == "answered" {
            let first = MeetingAsk.Exchange(recordingID: liveMeeting, at: 612, question: "Mac 上的录音现在谁在跟？", typed: true,
                                            state: .answered("还没有定负责人。上次周会把它列为待定问题，计划下周同一时间再对。"),
                                            sources: ["产品周会 · 2026-11-03 14:00–15:00"])
            let second = MeetingAsk.Exchange(recordingID: liveMeeting, at: 731, question: "上周定的长录音测试谁负责？", typed: false,
                                             state: .answered("上周的周会定了：录音入口只做一步开始，默认用中文转写。长录音的真机测试由张三负责，周五前完成。"),
                                             sources: ["产品周会 · 2026-11-03 14:00–15:00"])
            MeetingAsk.shared.showSample([first, second])
            MeetingSpeaker.shared.showSample(second.id)
        }
        #endif
    }
}

#if os(iOS)
// MARK: - All-day recording

extension RecordingSamples {
    /// The made-up past day (`-openDayRecap YES` opens it): yesterday, ended, with a meeting mark, pauses, a call, an
    /// erase and a silent minute. Its recap is the hub's: the mock writes one when told to (`POST /mock/day_recap`).
    static let pastDay = UUID(uuidString: "00000000-0000-0000-0000-0000000000da")!
    /// Today's made-up day, which the watch is recording (`today`) or waits on after a call (`paused`).
    static let todayDay = UUID(uuidString: "00000000-0000-0000-0000-0000000000db")!

    /// The first install's work; later calls wait for it.
    private static var dayInstall: Task<Void, Never>?

    /// `-showDaySample today|paused|recap|pending|failed`: yesterday's day in Recent Recordings and today's in the Today
    /// section, both transcribed (no microphone, no speech recognition), so the phone uploads them to the hub as it would
    /// a real day; `paused` has the watch waiting for Continue. `recap|pending|failed` name the hub's answer the screenshot
    /// is for (the UI test sets the mock to it). Without the argument an earlier run's samples go, so other tests see only
    /// their own recordings. Once per launch.
    static func installDaySampleIfAsked() async {
        if dayInstall == nil {
            let mode = UserDefaults.standard.string(forKey: "showDaySample")
            dayInstall = Task { await installDays(mode) }
        }
        await dayInstall?.value
    }

    private static func installDays(_ mode: String?) async {
        let library = RecordingLibrary.shared, store = RecordingDayStore.shared
        // Built again on every run (another day, another mode): first the earlier run's, with all the store kept of them.
        for id in [pastDay, todayDay] {
            let stored = await store.record(id) != nil
            guard stored || library.entry(id) != nil else { continue }
            // Removed here only: a delete on the hub would tombstone the id there, and the new sample couldn't go up.
            try? library.remove(id)
            RecordingDaySync.shared.removedHere(id)
            // Read while deleted: the store lets go of the day it holds in memory before the new one is written.
            _ = await store.record(id)
        }
        guard let mode, ["today", "paused", "recap", "pending", "failed"].contains(mode) else { return }
        let zone = TimeZone.current, now = Date.now
        let past = pastSample(now: now, zone: zone), today = todaySample(now: now, zone: zone)
        for day in [past, today] {
            library.restore(day.id)
            await write(day, to: store)
            library.adoptDay(id: day.id, day: day.day, timeZone: zone.identifier, createdAt: day.startedAt, closed: day.ended)
        }
        let paused = mode == "paused"
        RecordingDaySummaries.shared.receive(watch: RecordingDayWatchState(
            recording: today.id, day: today.day, state: paused ? .interrupted : .recording,
            since: paused ? today.lastEnd : today.startedAt, next: today.chunks.count, pending: 0))
    }

    /// A made-up day: its chunks as the watch would send them, and the text the phone would have transcribed.
    private nonisolated struct SampleDay: Sendable {
        nonisolated struct Chunk: Sendable {
            var header: RecordingDayHeader
            var sentences: [RecordingSentence]
            var state: RecordingDayChunk.State
        }

        var id: UUID
        var day: String
        var startedAt: Date
        var ended: Bool
        var chunks: [Chunk]

        var lastEnd: Date { chunks.map { $0.header.startedAt.addingTimeInterval($0.header.duration) }.max() ?? startedAt }
    }

    /// Stores the chunks as the receiver does, then gives them their text as the transcriber would. The store's receiver
    /// calls block on its queue, so they run off the main actor.
    private static func write(_ day: SampleDay, to store: RecordingDayStore) async {
        await Task.detached(priority: .userInitiated) {
            // Never decoded: every chunk is transcribed already. The store needs some audio for a chunk that has it.
            let audio = Data(repeating: 0, count: 64)
            for chunk in day.chunks {
                _ = try? store.store(header: chunk.header, audio: chunk.header.isMarker ? nil : audio)
            }
        }.value
        await store.update(day.id) { record in
            for chunk in day.chunks where !chunk.header.isMarker {
                record.chunks[chunk.header.seq]?.state = chunk.state
                record.chunks[chunk.header.seq]?.attempts = 0
                record.chunks[chunk.header.seq]?.sentences = chunk.state == .transcribed ? chunk.sentences : nil
            }
        }
    }

    /// Yesterday, 09:12 to 19:07: a design review the watch marked as a meeting, a call, an erase, lunch, the evening, a
    /// silent minute, then Stop for Today.
    private static func pastSample(now: Date, zone: TimeZone) -> SampleDay {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        let yesterday = calendar.date(byAdding: .day, value: -1, to: now) ?? now.addingTimeInterval(-86_400)
        let day = RecordingDayCalendar.day(of: yesterday, in: zone)
        let midnight = calendar.startOfDay(for: yesterday)
        func at(_ hour: Int, _ minute: Int) -> Date {
            calendar.date(bySettingHour: hour, minute: minute, second: 0, of: midnight) ?? midnight
        }
        let start = at(9, 12)
        let paused = RecordingDayContract.Reason.paused
        var chunks: [SampleDay.Chunk] = []
        func add(_ startedAt: Date, seconds: Double = 60, after: String? = nil, level: Double = -32, erased: Bool = false,
                 end: Bool = false, marks: [RecordingDayHeader.Mark]? = nil, lines: [String] = [], skipped: Bool = false) {
            let header = RecordingDayHeader(recording: pastDay, day: day, seq: chunks.count, startedAt: startedAt, duration: seconds,
                                            level: erased || end ? nil : level, erased: erased, end: end, after: after, marks: marks,
                                            recordingStartedAt: start)
            chunks.append(.init(header: header, sentences: sentences(lines, seconds: seconds), state: skipped ? .skipped : .transcribed))
        }
        add(at(9, 12), lines: ["早上先把导出功能的发布清单过一遍。", "超过 4 MB 的条目导出时会被丢掉，这个要先修。"])
        add(at(9, 41), after: paused, marks: [.init(at: 0, label: RecordingDayMark.meeting)],
            lines: ["设计评审开始，先看新的导出流程。", "大家同意把进度条放到列表顶部。"])
        add(at(9, 42), lines: ["Bo 负责明天把上传失败的测试修好。"])
        add(at(10, 20), after: RecordingDayContract.Reason.interrupted, lines: ["刚才接了个电话，继续说导出的事。"])
        add(at(11, 5), after: paused, erased: true)
        add(at(12, 30), after: paused, lines: ["中午和平台组一起吃饭，聊了上传接口的限流。", "他们建议把重试间隔调长一点。"])
        add(at(15, 20), after: paused, lines: ["下午整理周报，记得周五前发出去。"])
        add(at(19, 5), after: paused, lines: ["晚上回顾一下：导出修复和设计评审都有进展。"])
        add(at(19, 6), level: -62, skipped: true)
        add(at(19, 7), seconds: 0, after: paused, end: true)
        return SampleDay(id: pastDay, day: day, startedAt: start, ended: true, chunks: chunks)
    }

    /// Today, from two and a half hours ago (or just after midnight): a pause, then a call, then the last minute.
    private static func todaySample(now: Date, zone: TimeZone) -> SampleDay {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = zone
        let day = RecordingDayCalendar.day(of: now, in: zone)
        let first = calendar.startOfDay(for: now).addingTimeInterval(60)
        let start = max(first, now.addingTimeInterval(-150 * 60))
        // Shorter just after midnight, so every chunk ended before now.
        let scale = min(1, max(0, now.timeIntervalSince(start) - 120) / (62 * 60))
        let plan: [(minutes: Double, after: String?, lines: [String])] = [
            (0, nil, ["今天先把全天录音的手机端过一遍。", "Today 一栏要能看到录了多久、转写到哪。"]),
            (25, RecordingDayContract.Reason.paused, ["下午三点和设计组对一下回顾页的布局。"]),
            (60, RecordingDayContract.Reason.interrupted, ["接完电话了，继续看同步的状态。"]),
            (61, nil, ["记得明天把截图发到群里。"]),
        ]
        let chunks = plan.enumerated().map { seq, item in
            let header = RecordingDayHeader(recording: todayDay, day: day, seq: seq,
                                            startedAt: start.addingTimeInterval(item.minutes * 60 * scale), duration: 60 * max(scale, 0.05),
                                            level: -30, after: item.after, recordingStartedAt: start)
            return SampleDay.Chunk(header: header, sentences: sentences(item.lines, seconds: header.duration), state: .transcribed)
        }
        return SampleDay(id: todayDay, day: day, startedAt: start, ended: false, chunks: chunks)
    }

    /// Lines spread evenly over a chunk, each a moment shorter than its share.
    private nonisolated static func sentences(_ lines: [String], seconds: Double) -> [RecordingSentence] {
        guard !lines.isEmpty else { return [] }
        let share = seconds / Double(lines.count)
        return lines.enumerated().map { i, text in
            RecordingSentence(start: Double(i) * share, end: Double(i) * share + share * 0.9, text: text)
        }
    }
}
#endif
#endif
