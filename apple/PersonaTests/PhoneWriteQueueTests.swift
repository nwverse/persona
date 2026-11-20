import Foundation
import Testing
@testable import Persona

@MainActor struct PhoneWriteQueueTests {
    private let path = "/api/v1/phone/write-queue"

    private func source(_ hub: FakeHub, generation: Int = 1) -> PhoneWriteQueue.Source {
        .init(link: ObjectIdentifier(hub), config: hub.client.config, generation: generation)
    }

    private func wait(until condition: () -> Bool) async throws {
        let deadline = ContinuousClock.now + .seconds(8)
        while !condition(), ContinuousClock.now < deadline { try await Task.sleep(for: .milliseconds(5)) }
        try #require(condition())
    }

    @Test func summaryReadsOnlyTheHubsExplicitCount() async throws {
        let hub = FakeHub()
        for (body, count) in [(#"{"pending_count":2}"#, 2),
                              (#"{"pending_count":1,"items":[{"state":"declined"},null]}"#, 1),
                              (#"{"pending_count":0,"items":[{"state":"queued"}]}"#, 0),
                              (#"{"items":[{"state":"queued"}]}"#, nil),
                              (#"{"pending_count":-1}"#, nil)] {
            hub.on("GET", path, json: body)
            #expect(try await hub.client.manager("a-selected-assistant").pendingPhoneWriteCount() == count)
        }
        #expect(hub.requests.count == 5)
        #expect(hub.requests.allSatisfy { $0.query == ["count_only": "true"] && $0.headers[APIClient.managerHeader] == nil })
    }

    @Test func bothSurfacesShareTheReadAndACompletedWriteRefreshesIt() async throws {
        let hub = FakeHub(), queue = PhoneWriteQueue(), target = source(hub)
        hub.on("GET", path, json: #"{"pending_count":2}"#)
        let settings = Task { await queue.follow(target, using: hub.client) }
        defer { settings.cancel() }
        try await wait { queue.count(for: target) == 2 }
        let alert = Task { await queue.follow(target, using: hub.client) }
        defer { alert.cancel() }
        await Task.yield()
        #expect(hub.sent("GET", path).count == 1)
        hub.on("GET", path, json: #"{"pending_count":1}"#)
        queue.resultSent()
        try await wait { queue.count(for: target) == 1 }
        alert.cancel()
        await alert.value
        #expect(queue.count(for: target) == 1)
        hub.on("GET", path, json: #"{"pending_count":0}"#)
        queue.resultSent()
        try await wait { hub.sent("GET", path).count == 3 && queue.count(for: target) == nil }
        settings.cancel()
        await settings.value
        queue.resultSent()
        #expect(hub.sent("GET", path).count == 3)
    }

    @Test func switchingPairingsAndReconnectingHideTheOldValueAndRejectItsLateReply() async throws {
        let first = FakeHub(url: "https://first.example.invalid"), second = FakeHub(url: "https://second.example.invalid")
        let queue = PhoneWriteQueue(), a = source(first), b = source(second)
        var delayed: CheckedContinuation<Data, Never>?
        first.onAsync("GET", path) { _ in await withCheckedContinuation { delayed = $0 } }
        second.on("GET", path, json: #"{"pending_count":4}"#)
        let old = Task { await queue.follow(a, using: first.client) }
        defer { old.cancel(); delayed?.resume(returning: Data()) }
        try await wait { delayed != nil }
        let fresh = Task { await queue.follow(b, using: second.client) }
        defer { fresh.cancel() }
        try await wait { queue.count(for: b) == 4 }
        delayed?.resume(returning: Data(#"{"pending_count":9}"#.utf8)); delayed = nil
        await old.value
        #expect(queue.count(for: a) == nil && queue.count(for: nil) == nil)
        #expect(queue.count(for: b) == 4 && queue.count(for: source(second, generation: 2)) == nil)
        fresh.cancel()
        await fresh.value
        #expect(queue.count(for: b) == nil)
    }

    @Test func queuedDecisionsKeepThePairingThatOfferedEachWrite() async throws {
        let first = FakeHub(), second = FakeHub(), confirmations = PhoneConfirmations()
        let a = ObjectIdentifier(first), b = ObjectIdentifier(second)
        defer { confirmations.denyAll() }
        let firstAnswer = Task { await confirmations.ask("Create event", hub: a) }
        try await wait { confirmations.current?.hub == a }
        let secondAnswer = Task { await confirmations.ask("Create event", hub: b) }
        try await wait { confirmations.queued == 1 }
        let shown = try #require(confirmations.current)
        confirmations.answer(shown.id, false)
        #expect(await firstAnswer.value == false)
        let next = try #require(confirmations.current)
        #expect(next.hub == b && next.id != shown.id)
        confirmations.answer(next.id, true)
        #expect(await secondAnswer.value)
    }

    @Test func unsupportedAndForbiddenHubsHideAKnownCount() async throws {
        for status in [403, 404] {
            let hub = FakeHub(), queue = PhoneWriteQueue(), target = source(hub)
            hub.on("GET", path, json: #"{"pending_count":3}"#)
            let view = Task { await queue.follow(target, using: hub.client) }
            defer { view.cancel() }
            try await wait { queue.count(for: target) == 3 }
            hub.on("GET", path) { _ in throw APIError(status: status, message: "Unavailable") }
            queue.resultSent()
            try await wait { hub.sent("GET", path).count == 2 && queue.count(for: target) == nil }
            view.cancel()
            await view.value
        }
    }
}
