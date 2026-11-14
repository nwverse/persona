import Foundation
import XCTest
@testable import PersonaScreenKit

/// One end of an in-memory byte pipe, as a transport.
final class PipeEnd: ScreenLinkTransport, @unchecked Sendable {
    private let lock = NSLock()
    private var inbox: [Data] = []
    private var waiters: [CheckedContinuation<Data, Error>] = []
    private var closed = false
    weak var peer: PipeEnd?

    static func pair() -> (PipeEnd, PipeEnd) {
        let a = PipeEnd(), b = PipeEnd()
        a.peer = b
        b.peer = a
        return (a, b)
    }

    func send(_ data: Data) async throws {
        guard let peer, !isClosed else { throw ScreenLinkError.closed }
        peer.put(data)
    }

    func receive() async throws -> Data {
        try await withCheckedThrowingContinuation { k in
            lock.lock()
            if !inbox.isEmpty { let d = inbox.removeFirst(); lock.unlock(); k.resume(returning: d); return }
            if closed { lock.unlock(); k.resume(throwing: ScreenLinkError.closed); return }
            waiters.append(k)
            lock.unlock()
        }
    }

    func close() {
        shut()
        peer?.shut()
    }

    private var isClosed: Bool { lock.withLock { closed } }

    private func put(_ d: Data) {
        lock.lock()
        if let k = waiters.first { waiters.removeFirst(); lock.unlock(); k.resume(returning: d); return }
        inbox.append(d)
        lock.unlock()
    }

    private func shut() {
        lock.lock()
        closed = true
        let ws = waiters
        waiters = []
        lock.unlock()
        ws.forEach { $0.resume(throwing: ScreenLinkError.closed) }
    }
}

/// The node's end of one transport in a test: it reads whole messages and says what the test tells it.
final class FakeNode: @unchecked Sendable {
    let end: PipeEnd
    private var parser = ScreenVideoParser()
    private var pending: [ScreenLinkMessage] = []

    init(_ end: PipeEnd) { self.end = end }

    func say(_ m: ScreenLinkMessage) async throws { try await end.send(m.framed()) }

    /// The next message the phone sent, skipping acks and the session's own pings unless asked for.
    func next(skippingAcks: Bool = true, skippingPings: Bool = true) async throws -> ScreenLinkMessage {
        while true {
            while pending.isEmpty {
                for raw in try parser.push(try await end.receive()) {
                    pending.append(try ScreenLinkMessage.decode(type: raw.type, body: raw.body))
                }
            }
            let m = pending.removeFirst()
            if skippingAcks, case .ack = m { continue }
            if skippingPings, case .ping = m { continue }
            return m
        }
    }
}

final class ScreenLinkSessionTests: XCTestCase {
    private let paths = ScreenLinkPaths(token: "tok", cert: "pin", port: 4000, candidates: [.init(kind: "lan", addr: "192.0.2.10", prefix: 24)])

    private func open() async throws -> (ScreenLinkSession, FakeNode) {
        let (phone, node) = PipeEnd.pair()
        let n = FakeNode(node)
        let first = ScreenLinkMessage.paths(paths).framed()
        let s = ScreenLinkSession(rfbRelay: phone, first: first)
        return (s, n)
    }

    /// The session's events as they came: one task reads the stream (it allows one reader).
    private final class Events: @unchecked Sendable {
        private let lock = NSLock()
        private var seen: [ScreenLinkSession.Event] = []
        init(_ s: ScreenLinkSession) {
            Task { for await e in s.events { self.lock.withLock { self.seen.append(e) } } }
        }
        func wait(timeout: Double = 3, _ match: @escaping (ScreenLinkSession.Event) -> Bool) async -> Bool {
            let deadline = Date().addingTimeInterval(timeout)
            while Date() < deadline {
                if lock.withLock({ seen.contains(where: match) }) { return true }
                try? await Task.sleep(nanoseconds: 20_000_000)
            }
            return false
        }
    }

    func testRFBGoesBothWaysAndCopiesAreTrimmed() async throws {
        let (s, node) = try await open()
        let rfb = s.rfb()
        try await node.say(.data(channel: 1, offset: 0, bytes: Data("RFB ".utf8)))
        try await node.say(.data(channel: 1, offset: 2, bytes: Data("B 003.889\n".utf8))) // overlaps by two
        var got = Data()
        while got.count < 12 { got += try await rfb.receive() }
        XCTAssertEqual(String(decoding: got, as: UTF8.self), "RFB 003.889\n")
        try await rfb.send(Data("RFB 003.008\n".utf8))
        let got1 = try await node.next()
        XCTAssertEqual(got1, .data(channel: 1, offset: 0, bytes: Data("RFB 003.008\n".utf8)))
    }

    func testAGapDropsTheRelayAndEndsTheSession() async throws {
        let (s, node) = try await open()
        let rfb = s.rfb()
        try await node.say(.data(channel: 1, offset: 5, bytes: Data("x".utf8)))
        do {
            _ = try await rfb.receive()
            XCTFail("a gap was delivered")
        } catch {}
        let ended = await Events(s).wait { if case .ended = $0 { true } else { false } }
        XCTAssertTrue(ended)
    }

    func testAVideoStreamComesAsWholeMessagesAndAResetDropsAPartialOne() async throws {
        let (s, _) = try await open()
        let (phoneV, nodeV) = PipeEnd.pair()
        let vnode = FakeNode(nodeV)
        try await vnode.say(.resumed([ScreenLinkOffset(2, 0)]))
        let video = try await s.attachVideo(phoneV)
        XCTAssertEqual(video.channel, 2)
        let a = ScreenVideoWire.frame(4, body: Data(#"{"last":1}"#.utf8))
        let b = ScreenVideoWire.frame(3, body: Data(count: 30))
        try await vnode.say(.data(channel: 2, offset: 0, bytes: a.prefix(3)))
        try await vnode.say(.data(channel: 2, offset: 3, bytes: a.dropFirst(3) + b.prefix(7)))
        let got2 = try await video.receive()
        XCTAssertEqual(got2, a)
        let at = UInt64(a.count + 100)
        try await vnode.say(.reset(channel: 2, offset: at)) // the rest of b and more were dropped by the node
        let c = ScreenVideoWire.frame(4, body: Data(#"{"last":2}"#.utf8))
        try await vnode.say(.data(channel: 2, offset: at, bytes: c))
        let got3 = try await video.receive()
        XCTAssertEqual(got3, c, "the partial b is gone; c comes whole")
        try await video.send(ScreenVideoWire.frame(18, body: Data()))
        let got4 = try await vnode.next()
        XCTAssertEqual(got4, .data(channel: 2, offset: 0, bytes: ScreenVideoWire.frame(18, body: Data())))
    }

    func testCloseDeliversEverythingThenEnds() async throws {
        let (s, node) = try await open()
        let rfb = s.rfb()
        try await node.say(.data(channel: 1, offset: 0, bytes: Data("bye".utf8)))
        try await node.say(.close(channel: 1, offset: 3))
        let got5 = try await rfb.receive()
        XCTAssertEqual(got5, Data("bye".utf8))
        do { _ = try await rfb.receive(); XCTFail("past the close") } catch {}
    }

    func testTheWindowHoldsSendsUntilTheNodeAcknowledges() async throws {
        let (s, node) = try await open()
        let rfb = s.rfb()
        let big = Data(count: 256 << 10)
        try await rfb.send(big) // fills the window
        let second = Task { try await rfb.send(Data("more".utf8)) }
        try await Task.sleep(nanoseconds: 100_000_000)
        var sent = 0
        while sent < big.count {
            guard case let .data(_, _, bytes) = try await node.next() else { return XCTFail() }
            sent += bytes.count
        }
        try await node.say(.ack(channel: 1, offset: UInt64(big.count)))
        try await second.value
        let got6 = try await node.next()
        XCTAssertEqual(got6, .data(channel: 1, offset: UInt64(big.count), bytes: Data("more".utf8)))
    }

    /// A direct route: it attaches with the token, answers the first five pings, and the policy picks it; the channels
    /// stay on the relay in this stage.
    func testADirectRouteIsProbedAndChosen() async throws {
        let (s, node) = try await open()
        let events = Events(s)
        let pathsSeen = await events.wait { if case .paths = $0 { true } else { false } }
        XCTAssertTrue(pathsSeen)
        let (phoneD, nodeD) = PipeEnd.pair()
        let direct = FakeNode(nodeD)
        await s.addDirect(phoneD, route: "lan:192.0.2.10", kind: .lan, via: .wifi)
        let got7 = try await direct.next()
        XCTAssertEqual(got7, .attach(token: "tok"))
        try await direct.say(.attached)
        // Answer pings on both: the direct route at once, the relay 30 ms later.
        let answer = { (n: FakeNode, delay: UInt64) in
            Task {
                while case let .ping(v)? = try? await n.next(skippingPings: false) {
                    try? await Task.sleep(nanoseconds: delay)
                    try? await n.say(.pong(v, retransmitted: 0, sent: 10))
                }
            }
        }
        let a = answer(direct, 0), b = answer(node, 30_000_000)
        defer { a.cancel(); b.cancel() }
        let chosen = await events.wait(timeout: 6) { e in
            if case let .routes(routes, current, _) = e, current == "lan:192.0.2.10", routes["lan:192.0.2.10"]?.healthy == true { return true }
            return false
        }
        XCTAssertTrue(chosen, "the direct route measured and chosen")
    }
}
