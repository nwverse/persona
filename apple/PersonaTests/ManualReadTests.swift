import Foundation
import Testing
@testable import Persona

/// Manual mark as read (#7356): the hub's switch reaches the app (its event, its GET answer, the value kept for the next
/// launch), and the seen outbox keeps which marks were the user's own, so turning the switch on lets go of the marks
/// made by looking and keeps the user's.
@MainActor struct ManualReadTests {
    private let machine = MachineRef(group: "", id: "m")
    private let t0 = Date(timeIntervalSince1970: 1_000_000)

    private func key(_ id: String) -> SessionKey { SessionKey(machineID: "m", sessionID: id) }

    private func session(_ id: String, state: String) -> Session {
        let json = """
        {"machine_id":"m","session_id":"\(id)","status":"done","state":"\(state)",
         "seen_at":"1970-01-01T00:00:00Z","updated_at":"1970-01-01T00:01:40Z"}
        """
        do { return try JSON.decoder.decode(Session.self, from: Data(json.utf8)) }
        catch { fatalError("bad fixture: \(error)") }
    }

    @Test func theOutboxKeepsWhichMarksWereTheUsers() {
        var o = SeenOutbox()
        o.mark(key("looked"), session("looked", state: "unread"), at: t0)
        o.mark(key("pressed"), session("pressed", state: "unread"), at: t0, manual: true)
        #expect(!o.isManual(key("looked")))
        #expect(o.isManual(key("pressed")))
        // a resend keeps it: the route changes, the mark's owner does not
        o.sent(key("pressed"), .http, at: t0 + 6)
        #expect(o.isManual(key("pressed")))
    }

    @Test func turningTheSwitchOnLetsGoOfLooksOnly() {
        var o = SeenOutbox()
        o.mark(key("looked"), session("looked", state: "unread"), at: t0)
        o.mark(key("pressed"), session("pressed", state: "unread"), at: t0, manual: true)
        #expect(o.dropLooks() == [key("looked")])
        #expect(o.marks.keys.sorted { $0.sessionID < $1.sessionID } == [key("pressed")])
        // the user's mark still holds its row seen against a list that does not show it recorded yet
        let out = o.land([machine: [session("looked", state: "unread"), session("pressed", state: "unread")]], at: t0 + 1)
        #expect(out[machine]?.map(\.state) == [.unread, .idle])
        #expect(o.dropLooks().isEmpty)
    }

    @Test func theEventSaysTheSwitch() {
        guard case .readSettings(let on)? = ServerEvent.decode(Data(#"{"type":"read_settings","manual":true}"#.utf8)) else {
            Issue.record("read_settings not decoded")
            return
        }
        #expect(on)
        guard case .readSettings(let off)? = ServerEvent.decode(Data(#"{"type":"read_settings","manual":false}"#.utf8)) else {
            Issue.record("read_settings off not decoded")
            return
        }
        #expect(!off)
        // without a switch there is nothing to apply; another message's `manual` of another shape loses nothing
        #expect(ServerEvent.decode(Data(#"{"type":"read_settings"}"#.utf8)) == nil)
        guard case .inbox? = ServerEvent.decode(Data(#"{"type":"inbox","manual":{"x":1}}"#.utf8)) else {
            Issue.record("an inbox event with an object `manual` was lost")
            return
        }
    }

    @Test func theHubsAnswerDecodes() throws {
        let on = try JSON.decoder.decode(ReadSettings.self, from: Data(#"{"manual":true,"can_write":true}"#.utf8))
        #expect(on == ReadSettings(manual: true, canWrite: true))
        // an answer without the fields is off and read-only, not a failure
        #expect(try JSON.decoder.decode(ReadSettings.self, from: Data("{}".utf8)) == ReadSettings(manual: false, canWrite: false))
    }

    @Test func theLastKnownSwitchSurvivesARelaunch() {
        let saved = UserDefaults.standard.object(forKey: AppModel.manualReadKey)
        defer { UserDefaults.standard.set(saved, forKey: AppModel.manualReadKey) }
        UserDefaults.standard.set(true, forKey: AppModel.manualReadKey)
        #expect(AppModel().manualRead)
        UserDefaults.standard.removeObject(forKey: AppModel.manualReadKey)
        #expect(!AppModel().manualRead) // off by default
    }
}
