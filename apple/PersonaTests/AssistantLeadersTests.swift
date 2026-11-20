import Foundation
import Testing
@testable import Persona
import PersonaWire

/// The directions' names, counts and order from the hub's tree, and its `directions` push (#4331, #4352).
struct AssistantLeadersTests {
    /// Every direction of the tree carries its name, what waits on you and whether anything under it runs; the secretary
    /// group is not one the sidebar lists. The row shows `needs_you` in words and `running` as its spinner (#6626).
    @Test func theTreeCarriesTheNamesAndCounts() throws {
        let raw = #"""
        {"directions":[
            {"id":"lead-a","name":"Product","counts":{"working":0,"needs_you":1,"stuck":0,"review":2},"running":true,"tasks":[]},
            {"id":"lead-b","name":"Infra","counts":{"working":2,"needs_you":0,"stuck":0,"review":0},"running":false,"tasks":[]},
            {"id":"main","name":"","secretary":true,"counts":{"working":7,"needs_you":7},"tasks":[]}]}
        """#
        // The one model the app holds (`AppModel.directionTree`, #4352): the lead rows read their counts from it.
        let tree = try JSON.decoder.decode(DirectionTree.self, from: Data(raw.utf8))
        #expect(tree.leaders.map(\.id) == ["lead-a", "lead-b"])
        #expect(tree.leaderNames == ["lead-a": "Product", "lead-b": "Infra"])
        // what runs is the hub's `running`, not the working count: lead-b counts two working to-dos with nothing at work
        #expect(tree.leaders.map(\.running) == [true, false])
        #expect(tree.leaders.map(\.counts.needsYou) == [1, 0])
    }

    @Test func directionAgentCountsRoundTripIndependentlyOfTaskState() throws {
        let raw = #"""
        {"directions":[
            {"id":"product","counts":{"working":12,"needs_you":1},"agent_counts":{"running":2,"stopped":3}},
            {"id":"infra","counts":{"working":8},"agent_counts":{"running":0,"stopped":1}},
            {"id":"empty","agent_counts":{"running":0,"stopped":0}},
            {"id":"partial","agent_counts":{"running":0}},
            {"id":"legacy","running":true,"counts":{"working":5,"needs_you":2}}]}
        """#
        let tree = try JSON.decoder.decode(DirectionTree.self, from: Data(raw.utf8))
        let encoded = try JSONEncoder().encode(tree)
        let object = try #require(try JSONSerialization.jsonObject(with: encoded) as? [String: Any])
        let directions = try #require(object["directions"] as? [[String: Any]])
        let counts = directions.map { $0["agent_counts"] as? [String: Int] }
        #expect(counts == [["running": 2, "stopped": 3], ["running": 0, "stopped": 1],
                           ["running": 0, "stopped": 0], nil, nil])
        #expect(tree.directions.last?.counts.needsYou == 2)
    }

    /// The to-do attention label only counts what waits on you, independently of the Agent totals (#6626, #11712).
    @Test func countsLabelShowsOnlyWhatWaitsOnYou() {
        #expect(DirectionCountsText.label(needsYou: 0) == nil)
        let waiting = DirectionCountsText.label(needsYou: 4)
        #expect(waiting == "等你 4")
    }

    /// The hub's push names no directions: the clients read the tree again on it (and on every (re)connect).
    @Test func theDirectionsPushIsRead() throws {
        let event = try #require(ServerEvent.decode(Data(#"{"type":"directions"}"#.utf8)))
        guard case .directions = event else {
            Issue.record("not directions")
            return
        }
    }
}
