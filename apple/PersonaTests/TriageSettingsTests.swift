import Foundation
import Testing
@testable import Persona

/// Settings › Session Triage (`TriageSettingsModel`, `TriageSwitches`) without the page.
@MainActor struct TriageSettingsTests {
    private struct Boom: Error {}

    @Test func switchesTheHubLeavesOutAreOff() throws {
        let s = try JSON.decoder.decode(TriageSwitches.self, from: Data(#"{"enabled":true,"auto_continue":true}"#.utf8))
        #expect(s == TriageSwitches(enabled: true, autoContinue: true))
        let back = try JSONSerialization.jsonObject(with: JSONEncoder().encode(s)) as? [String: Bool]
        #expect(back == ["enabled": true, "auto_continue": true, "wake": false, "decide": false, "reply_reminder": true])
    }

    /// Remind If Seen but Unanswered (#5096): on when the hub leaves it out, kept with its minutes on the way back.
    @Test func theReplyReminderIsOnUnlessTheHubSaysOff() throws {
        #expect(try JSON.decoder.decode(TriageSwitches.self, from: Data("{}".utf8)).replyReminder)
        let off = try JSON.decoder.decode(TriageSwitches.self, from: Data(#"{"reply_reminder":false,"reply_reminder_minutes":60}"#.utf8))
        #expect(!off.replyReminder && off.replyReminderMinutes == 60)
        let back = try JSONSerialization.jsonObject(with: JSONEncoder().encode(off)) as? [String: Any]
        #expect(back?["reply_reminder"] as? Bool == false && back?["reply_reminder_minutes"] as? Int == 60)
    }

    /// The hint says the hub's wait: its minutes, 30 when it names none or 0 (as the hub and the web do).
    @Test func theReplyReminderWaitsTheHubsMinutes() throws {
        #expect(TriageSwitches().replyReminderAfter == 30)
        #expect(TriageSwitches(replyReminderMinutes: 0).replyReminderAfter == 30)
        #expect(TriageSwitches(replyReminderMinutes: 90).replyReminderAfter == 90)
        #expect(try JSON.decoder.decode(TriageSwitches.self, from: Data(#"{"reply_reminder_minutes":45}"#.utf8)).replyReminderAfter == 45)
    }

    @Test func theReplyReminderDoesNotWaitForTheTriage() async {
        var saved: [TriageSwitches] = []
        let m = TriageSettingsModel(deps: .init(load: { TriageSwitches() }, save: { saved.append($0); return $0 }))
        await m.load()
        #expect(m.canChange(\.replyReminder) && !m.canChange(\.decide))
        await m.set(\.replyReminder, false)
        #expect(saved == [TriageSwitches(replyReminder: false)] && m.switches?.replyReminder == false)
    }

    @Test func loadsAndSavesTheWholeSet() async {
        var saved: [TriageSwitches] = []
        let m = TriageSettingsModel(deps: .init(load: { TriageSwitches() }, save: { saved.append($0); return $0 }))
        #expect(m.switches == nil && !m.canChange(\.enabled))
        await m.load()
        #expect(m.switches == TriageSwitches())
        await m.set(\.enabled, true)
        await m.set(\.wake, true)
        #expect(saved == [TriageSwitches(enabled: true), TriageSwitches(enabled: true, wake: true)])
        #expect(m.switches == TriageSwitches(enabled: true, wake: true) && m.error == nil && !m.saving)
    }

    @Test func whatItMayDoWaitsForTheTriage() async {
        var saves = 0
        let m = TriageSettingsModel(deps: .init(load: { TriageSwitches() }, save: { saves += 1; return $0 }))
        await m.load()
        #expect(m.canChange(\.enabled))
        #expect(!m.canChange(\.decide) && !m.canChange(\.autoContinue) && !m.canChange(\.wake))
        await m.set(\.decide, true)
        #expect(saves == 0 && m.switches?.decide == false)
    }

    @Test func aFailedSavePutsTheOldValuesBack() async {
        let m = TriageSettingsModel(deps: .init(load: { TriageSwitches(enabled: true) }, save: { _ in throw APIError(status: 503, message: "助手没有连上") }))
        await m.load()
        await m.set(\.autoContinue, true)
        #expect(m.switches == TriageSwitches(enabled: true))
        #expect(m.error == "没改成：助手没有连上")
    }

    @Test func anOlderHubSaysItHasNoTriage() async {
        let m = TriageSettingsModel(deps: .init(load: { throw APIError(status: 404, message: "not found") }, save: { $0 }))
        await m.load()
        #expect(m.unsupported && m.switches == nil && m.error == nil)
    }

    @Test func aReadThatFailsSaysSo() async {
        let m = TriageSettingsModel(deps: .init(load: { throw APIError(status: 503, message: "助手没有连上") }, save: { $0 }))
        await m.load()
        #expect(!m.unsupported && m.switches == nil)
        #expect(m.error == "没读到：助手没有连上")
    }

    /// A timeout says so instead of spinning for good; reading again clears it (#3485).
    @Test func aTimeoutSaysSoAndAReadAgainClearsIt() async {
        var fail = true
        let m = TriageSettingsModel(deps: .init(load: {
            if fail { throw URLError(.timedOut) }
            return TriageSwitches()
        }, save: { $0 }))
        await m.load()
        #expect(m.switches == nil && m.error == "没读到：连不上 hub")
        fail = false
        await m.load()
        #expect(m.switches == TriageSwitches() && m.error == nil)
    }

    /// A read cancelled because the page went away says nothing.
    @Test func aCancelledReadSaysNothing() async {
        let m = TriageSettingsModel(deps: .init(load: { throw CancellationError() }, save: { $0 }))
        await m.load()
        #expect(m.switches == nil && m.error == nil && !m.unsupported)
    }

    /// Each switch's example (#1413) decodes, and the list's own row draws from it what the switch makes: what it
    /// waits for, when the hub retries or wakes it, Awaiting Reply with its question.
    @Test func theExamplesShowWhatEachSwitchDoes() throws {
        let now = Date()
        let triaged = try #require(TriageExample.triaged(now: now))
        #expect(triaged.workLine(now: now) == "在等 CI" && !triaged.awaitsYourReply(muted: false))
        let retrying = try #require(TriageExample.retrying(now: now))
        #expect(retrying.work?.on == "transient" && retrying.workLine(now: now)?.hasPrefix("模型服务繁忙（503）") == true
                && retrying.workLine(now: now)?.contains("重试") == true)
        let waking = try #require(TriageExample.waking(now: now))
        #expect(waking.work?.hubWaits == true && waking.workLine(now: now)?.hasPrefix("在等部署") == true)
        let reply = try #require(TriageExample.awaitingReply(now: now))
        #expect(reply.awaitsYourReply(muted: false) && reply.listPreview(muted: false) == "删掉这 12 个已合并的分支吗？")
        let card = TriageExample.decision(now: now)
        #expect(card.kind == .question && card.questions?.first?.options.map(\.label) == ["推送", "先不"])
    }
}
