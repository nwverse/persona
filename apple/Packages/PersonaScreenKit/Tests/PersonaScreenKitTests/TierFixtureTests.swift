import Foundation
import XCTest
@testable import PersonaScreenKit

/// The quality tiers' shared cases (spec/fixtures/screen-tiers, #6396 / #6558): the browser's port
/// (web/src/screen/tiers/) must give exactly what these Swift types give. One scenario per file, its input and the
/// expected output, no internal state. `PERSONA_WRITE_TIER_FIXTURES=1 swift test --filter TierFixtureTests` writes them;
/// otherwise every committed file must be exactly what the scenarios write now, so a change in behaviour on either side
/// fails until the files and the other side follow. `policy/` comes with `TierPolicy`'s save-data cap (live-tier ②b).
final class TierFixtureTests: XCTestCase {
    // MARK: Ladder

    private struct LadderCase {
        let name: String
        let description: String
        let points: [Int]
        let pixels: [Int]
        var maxSize = 2556
        var bias: TierLadder.Bias = .auto
        var capKbps = 12_000
        var maxFps = 60
        var codec = "hevc"
    }

    private static let retina = (points: [1512, 982], pixels: [3024, 1964])

    private static let ladders: [LadderCase] = [
        LadderCase(name: "retina_auto_wifi", description: "A 14-inch Retina MacBook, Auto, the phone on Wi-Fi.",
                   points: retina.points, pixels: retina.pixels),
        LadderCase(name: "retina_auto_cellular", description: "The same on cellular: 4 Mbit/s cap.",
                   points: retina.points, pixels: retina.pixels, capKbps: 4_000),
        LadderCase(name: "retina_auto_low_data", description: "Low Data Mode: 1.5 Mbit/s cap; tiers whose floor is over it go.",
                   points: retina.points, pixels: retina.pixels, capKbps: 1_500),
        LadderCase(name: "retina_sharper", description: "Sharper: the pixels kept, frame rate given up first.",
                   points: retina.points, pixels: retina.pixels, bias: .sharper),
        LadderCase(name: "retina_smoother", description: "Smoother: the frame rate kept, pixels given up first.",
                   points: retina.points, pixels: retina.pixels, bias: .smoother),
        LadderCase(name: "retina_warm", description: "Low Power Mode or a warm phone: 15 fps at most; tiers merge.",
                   points: retina.points, pixels: retina.pixels, maxFps: 15),
        LadderCase(name: "retina_no_cap", description: "No cap from the client (0): the helper's own 50 Mbit/s.",
                   points: retina.points, pixels: retina.pixels, capKbps: 0),
        LadderCase(name: "retina_browser_desktop", description: "A desktop browser: no long-edge cap (0), Wi-Fi.",
                   points: retina.points, pixels: retina.pixels, maxSize: 0),
        LadderCase(name: "retina_h264", description: "A browser without HEVC: the ladder for H.264.",
                   points: retina.points, pixels: retina.pixels, codec: "h264"),
        LadderCase(name: "display_1x_1080p", description: "A 1× display: 2×, 1.5× and 1× are the same pixels and merge.",
                   points: [1920, 1080], pixels: [1920, 1080]),
        LadderCase(name: "studio_display_5k", description: "A 5K display under a 1920 long-edge cap.",
                   points: [2560, 1440], pixels: [5120, 2880], maxSize: 1920),
        LadderCase(name: "portrait", description: "A display in portrait.", points: [982, 1512], pixels: [1964, 3024]),
        LadderCase(name: "tiny_cap", description: "A cap at the helper's floor: only the last tier is left.",
                   points: [800, 600], pixels: [800, 600], capKbps: 200),
    ]

    private static func ladderFile(_ c: LadderCase) -> [String: Any] {
        let tiers = TierLadder.make(points: (c.points[0], c.points[1]), pixels: (c.pixels[0], c.pixels[1]), maxSize: c.maxSize,
                                    bias: c.bias, capKbps: c.capKbps, maxFps: c.maxFps, codec: c.codec)
        return ["name": c.name, "description": c.description,
                "input": ["points": c.points, "pixels": c.pixels, "maxSize": c.maxSize, "bias": c.bias.rawValue,
                          "capKbps": c.capKbps, "maxFps": c.maxFps, "codec": c.codec],
                "expected": tiers.map { ["scale": $0.scale, "fps": $0.fps, "width": $0.width, "height": $0.height,
                                         "minKbps": $0.minKbps, "maxKbps": $0.maxKbps] }]
    }

    // MARK: Signals

    /// One step of a signals scenario: an event, or `take` with what it returned.
    private enum Step {
        case frame(capture: UInt64, arrival: UInt64, firstByte: UInt64?, bytes: Int, refinement: Bool, fps: Int)
        case decoded(ms: Double, at: UInt64)
        case shown(replaced: Bool, at: UInt64)
        case pathChanged
        case take(now: UInt64)
    }

    private struct SignalsCase {
        let name: String
        let description: String
        let steps: [Step]
    }

    /// `count` frames from `start` µs every `every` µs, each `delay(i)` µs after its capture (on a clock 5 s off, as two
    /// machines' are), `bytes(i)` long, and a take after the last.
    private static func frames(_ count: Int, start: UInt64 = 1_000_000, every: UInt64 = 16_667, fps: Int = 60,
                               delay: (Int) -> UInt64 = { _ in 20_000 }, bytes: (Int) -> Int = { _ in 20_000 },
                               firstByte: (Int) -> UInt64? = { _ in nil }, refinement: (Int) -> Bool = { _ in false },
                               decode: Double? = nil, replaced: (Int) -> Bool? = { _ in nil }) -> [Step] {
        var steps: [Step] = []
        for i in 0 ..< count {
            let capture = start + UInt64(i) * every
            let arrival = capture + 5_000_000 + delay(i)
            steps.append(.frame(capture: capture, arrival: arrival, firstByte: firstByte(i).map { arrival - $0 },
                                bytes: bytes(i), refinement: refinement(i), fps: fps))
            if let decode { steps.append(.decoded(ms: decode, at: arrival + UInt64(decode * 1000))) }
            if let r = replaced(i) { steps.append(.shown(replaced: r, at: arrival + 8_000)) }
        }
        return steps
    }

    private static func lastArrival(_ steps: [Step]) -> UInt64 {
        steps.reduce(0) { at, s in if case let .frame(_, a, _, _, _, _) = s { max(at, a) } else { at } }
    }

    private static let signals: [SignalsCase] = {
        var cases: [SignalsCase] = []
        func add(_ name: String, _ description: String, _ steps: [Step], takes: [UInt64] = [0]) {
            let end = lastArrival(steps)
            cases.append(SignalsCase(name: name, description: description, steps: steps + takes.map { .take(now: end + $0) }))
        }
        add("steady", "60 fps at a constant delay: no queue, nothing late; decoding and showing keep up.",
            frames(120, decode: 3, replaced: { _ in false }), takes: [0, 600_000])
        add("queue_rising", "The delay grows 2 ms a frame for a second: the queue rises and frames come late.",
            frames(60, delay: { 20_000 + UInt64($0) * 2_000 }))
        add("queue_falling", "The delay rises for 40 frames, then drains: over the last second the queue falls.",
            frames(80, delay: { $0 < 40 ? 20_000 + UInt64($0) * 4_000 : 180_000 - UInt64($0 - 40) * 4_000 }))
        add("busy_link", "Every frame queued over 50 ms behind the one before: the delivered rate is the link's capacity.",
            frames(40, every: 25_000, fps: 30, delay: { 80_000 + UInt64($0) * 9_000 }, bytes: { _ in 60_000 }))
        add("quiet_demand", "A few frames, no queue: the delivered rate is what came over the last second.",
            frames(8, every: 100_000, fps: 30, bytes: { _ in 5_000 }))
        add("bursts", "Large frames over several reads say how fast the link moved them; a second take has none left.",
            frames(30, every: 33_333, fps: 30, bytes: { $0 % 10 == 0 ? 64_000 : 8_000 },
                   firstByte: { $0 % 10 == 0 ? 20_000 : nil }), takes: [0, 1])
        add("refinement", "Idle refinement's large frames count as bursts but not in the queue or the delivered rate.",
            frames(12, every: 100_000, fps: 30, delay: { $0 % 3 == 2 ? 300_000 : 20_000 },
                   bytes: { $0 % 3 == 2 ? 48_000 : 4_000 }, firstByte: { $0 % 3 == 2 ? 40_000 : nil },
                   refinement: { $0 % 3 == 2 }))
        add("decode_behind", "Slow decodes and pictures replaced before they showed.",
            frames(60, decode: 24, replaced: { $0 % 2 == 0 }))
        var moved = frames(30)
        moved.append(.pathChanged)
        moved += frames(30, start: 1_600_000, delay: { _ in 60_000 })
        add("path_changed", "A new route: the delay floor starts over, so a slower route's delay alone isn't queueing.", moved)
        cases.append(SignalsCase(name: "still_screen", description: "Nothing arrived: no frames, nothing measured.",
                                 steps: [.take(now: 5_000_000)]))
        var window = frames(10, delay: { _ in 10_000 })
        window += frames(10, start: 12_500_000, delay: { _ in 40_000 })
        add("baseline_window", "A delay floor older than 10 s is dropped: the new floor is the newer frames'.", window)
        return cases
    }()

    private static func signalsFile(_ c: SignalsCase) -> [String: Any] {
        var r = ReceiverSignals()
        var steps: [[String: Any]] = []
        for step in c.steps {
            switch step {
            case let .frame(capture, arrival, firstByte, bytes, refinement, fps):
                r.frame(captureMicros: capture, arrivalMicros: arrival, firstByteMicros: firstByte, bytes: bytes,
                        refinement: refinement, fps: fps)
                steps.append(["frame": ["capture": capture, "arrival": arrival, "firstByte": firstByte.map { $0 as Any } ?? NSNull(),
                                        "bytes": bytes, "refinement": refinement, "fps": fps]])
            case let .decoded(ms, at):
                r.decoded(ms: ms, at: at)
                steps.append(["decoded": ["ms": ms, "at": at]])
            case let .shown(replaced, at):
                r.shown(replaced: replaced, at: at)
                steps.append(["shown": ["replaced": replaced, "at": at]])
            case .pathChanged:
                r.pathChanged()
                steps.append(["pathChanged": [String: Any]()])
            case let .take(now):
                let s = r.take(now: now)
                func v(_ d: Double?) -> Any { d.map { $0 as Any } ?? NSNull() }
                steps.append(["take": ["now": now],
                              "expected": ["frames": s.frames, "queueP50Ms": v(s.queueP50Ms), "queueP95Ms": v(s.queueP95Ms),
                                           "queueRising": s.queueRising, "queueFalling": s.queueFalling,
                                           "deliveredKbps": v(s.deliveredKbps), "burstKbps": s.burstKbps,
                                           "lateShare": v(s.lateShare), "decodeP95Ms": v(s.decodeP95Ms),
                                           "replacedShare": v(s.replacedShare)]])
            }
        }
        return ["name": c.name, "description": c.description, "steps": steps]
    }

    // MARK: Files

    private static var root: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent() // the package
            .deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent() // the repository
            .appendingPathComponent("spec/fixtures/screen-tiers")
    }

    private static func encoded(_ object: [String: Any]) throws -> Data {
        var data = try JSONSerialization.data(withJSONObject: object, options: [.prettyPrinted, .sortedKeys, .withoutEscapingSlashes])
        data.append(0x0a)
        return data
    }

    private func check(_ dir: String, _ files: [(String, [String: Any])]) throws {
        let folder = Self.root.appendingPathComponent(dir)
        let write = ProcessInfo.processInfo.environment["PERSONA_WRITE_TIER_FIXTURES"] == "1"
        if write { try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true) }
        let committed = Set((try? FileManager.default.contentsOfDirectory(atPath: folder.path)) ?? [])
        for (name, object) in files {
            let url = folder.appendingPathComponent(name + ".json")
            let data = try Self.encoded(object)
            if write { try data.write(to: url); continue }
            let have = try Data(contentsOf: url)
            XCTAssertEqual(String(decoding: have, as: UTF8.self), String(decoding: data, as: UTF8.self),
                           "spec/fixtures/screen-tiers/\(dir)/\(name).json is out of date: run PERSONA_WRITE_TIER_FIXTURES=1 swift test --filter TierFixtureTests, and bring web/src/screen/tiers along")
        }
        if !write {
            let stale = committed.subtracting(files.map { $0.0 + ".json" })
            XCTAssertTrue(stale.isEmpty, "files no scenario writes any more: \(stale.sorted())")
        }
    }

    func testTheLadderCases() throws {
        try check("ladder", Self.ladders.map { ($0.name, Self.ladderFile($0)) })
    }

    func testTheSignalsCases() throws {
        try check("signals", Self.signals.map { ($0.name, Self.signalsFile($0)) })
    }
}
