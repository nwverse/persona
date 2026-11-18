import Foundation
import Testing
@testable import Persona

/// What the hub can do for To-dos, remembered per group, and the V1 gate it opens (#10465, decision S2-1): on for the
/// hub-level group once its hello lists `todo_view`, or at launch when its last hello did, and then on until the app
/// quits; a hub that never listed it keeps today's screens.
@MainActor
struct TodoFeaturesTests {
    private func defaults() throws -> UserDefaults {
        let name = "TodoFeaturesTests." + UUID().uuidString
        let d = try #require(UserDefaults(suiteName: name))
        d.removePersistentDomain(forName: name)
        return d
    }

    @Test func theHelloSaysWhatTheHubCanDo() {
        let f = TodoFeatures(["todo_view", "todo_places", "todo_seen", "sessions_list"])
        #expect(f == TodoFeatures(view: true, places: true))
        #expect(TodoFeatures(["todo_timeline", "todo_asks"]) == TodoFeatures(timeline: true, asks: true))
        #expect(TodoFeatures([]) == TodoFeatures())
    }

    /// Kept per group, written only when it changes, and never the hello's send marker (trusted from the current
    /// hello only).
    @Test func eachGroupsLastHelloIsRemembered() throws {
        let d = try defaults()
        #expect(TodoFeatures.remembered("g", in: d) == TodoFeatures())
        TodoFeatures.remember(TodoFeatures(view: true, places: true, asks: true, sendState: true), group: "g", in: d)
        #expect(TodoFeatures.remembered("g", in: d) == TodoFeatures(view: true, places: true, asks: true))
        #expect(TodoFeatures.remembered("other", in: d) == TodoFeatures())
        TodoFeatures.remember(TodoFeatures(), group: "g", in: d)
        #expect(TodoFeatures.remembered("g", in: d) == TodoFeatures())
    }

    /// The remembered value turns V1 on before this launch's hello, so the first frame is drawn the way it will stay.
    @Test func theRememberedValueTurnsV1OnInTheFirstFrame() throws {
        let d = try defaults()
        TodoFeatures.remember(TodoFeatures(view: true), group: "g", in: d)
        #expect(TodosV1(allowed: true, defaults: d, launchGroup: "g", hubGroup: { "g" }).isOn)
        #expect(!TodosV1(allowed: true, defaults: d, launchGroup: "another", hubGroup: { "another" }).isOn)
        d.set("g", forKey: AppModel.hubGroupKey)
        #expect(TodosV1(allowed: true, defaults: d, hubGroup: { "g" }).isOn, "the hub-level group as the app kept it")
    }

    /// Once on, a hello without `todo_view` (a manager reconnecting) never takes the page back.
    @Test func aHelloWithoutTheViewMidSessionKeepsItOn() throws {
        let gate = TodosV1(allowed: true, defaults: try defaults(), launchGroup: "g", hubGroup: { "g" })
        #expect(!gate.isOn)
        gate.saw(TodoFeatures(["todo_view", "todo_places"]), group: "g")
        #expect(gate.isOn)
        gate.saw(TodoFeatures(["todo_seen"]), group: "g")
        #expect(gate.isOn)
    }

    /// A hub that never lists `todo_view` keeps today's screens; so does another group's hello, and every hub while
    /// this build has V1 off (before the switch-on).
    @Test func anOldHubKeepsItOff() throws {
        let gate = TodosV1(allowed: true, defaults: try defaults(), launchGroup: "g", hubGroup: { "g" })
        gate.saw(TodoFeatures(["todo_seen", "sessions_list"]), group: "g")
        gate.saw(TodoFeatures(["todo_view"]), group: "elsewhere")
        #expect(!gate.isOn)
        let d = try defaults()
        TodoFeatures.remember(TodoFeatures(view: true), group: "g", in: d)
        let dormant = TodosV1(allowed: false, defaults: d, launchGroup: "g", hubGroup: { "g" })
        dormant.saw(TodoFeatures(["todo_view"]), group: "g")
        #expect(!dormant.isOn && !TodosV1.released)
    }

    /// A group's link answers with what its last hello said until this launch's hello comes, then with the hello, and
    /// the hello is remembered for the next launch.
    @Test func aLinkUsesTheLastHelloUntilItsOwn() {
        let group = "todo-features-" + UUID().uuidString
        defer {
            for key in ["hubTodoFeatures.", "hubSideFold.", "hubExtensionsAPI."] { UserDefaults.standard.removeObject(forKey: key + group) }
        }
        TodoFeatures.remember(TodoFeatures(view: true, places: true), group: group)
        let config = ServerConfig(url: URL(string: "https://fake.invalid")!, token: "t")
        let link = HubLink(group: group, config: config, pins: PinsStore(deps: .init(api: { nil }, report: { _ in }, suffix: group)))
        #expect(link.todoFeatures == TodoFeatures(view: true, places: true))
        link.received(HubHello(features: ["todo_view", "sessions_list"]))
        #expect(link.todoFeatures == TodoFeatures(view: true))
        #expect(TodoFeatures.remembered(group) == TodoFeatures(view: true))
    }
}
