#if DEBUG || PERF_SIGNPOST
import SwiftUI

/// What the screen shows, for UI tests to check against the hub (docs/apple/ios-state.md「Truth and timeliness」): a part of the
/// screen (`area`) exports what it draws (the rows in their order, their states, the counts, the page) as one line of
/// JSON on a hidden 1×1 element `probe.<area>`, `-probes YES` only. The tests (PersonaUITests/ScreenProbe.swift) read
/// it and compare it with the hub's own data (`/mock/truth`), and how long a change takes to show.
///
/// The value: `{"area":…, "seq":…, "at":…, "page":…, "v":{…}}`; `seq` counts the changes of `v`, `at` is when it last
/// changed (Unix time in ms: the simulator shares the Mac's clock, so a test compares it with when it pushed, even for
/// a change made while its finger was still down), `page` is the shell's page the area is on (both pages stay built:
/// the tests pick the one in front), nil elsewhere.
nonisolated enum ScreenProbe {
    static let enabled = UserDefaults.standard.bool(forKey: "probes")

    struct Envelope<Value: Encodable>: Encodable {
        var area: String
        var seq: Int
        var at: Int64
        var page: String?
        var v: Value
    }

    static func json<Value: Encodable>(_ e: Envelope<Value>) -> String {
        let enc = JSONEncoder()
        enc.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        enc.dateEncodingStrategy = .iso8601
        return (try? enc.encode(e)).flatMap { String(data: $0, encoding: .utf8) } ?? "{}"
    }
}

extension View {
    /// Exports `value` as `probe.<area>` (`-probes YES`); nothing at all otherwise.
    @ViewBuilder func screenProbe<Value: Encodable & Equatable>(_ area: String, page: String? = nil, _ value: Value) -> some View {
        if ScreenProbe.enabled {
            overlay(alignment: .topLeading) { ScreenProbeView(area: area, page: page, value: value) }
        } else {
            self
        }
    }
}

private struct ScreenProbeView<Value: Encodable & Equatable>: View {
    let area: String
    let page: String?
    let value: Value
    @State private var seq = 0
    @State private var at = Date.now

    var body: some View {
        Color.clear
            .frame(width: Theme.Size.line, height: Theme.Size.line)
            .allowsHitTesting(false)
            .accessibilityElement()
            .accessibilityIdentifier("probe." + area)
            .accessibilityValue(ScreenProbe.json(.init(area: area, seq: seq, at: Int64(at.timeIntervalSince1970 * 1000), page: page, v: value)))
            .onChange(of: value) { _, _ in
                seq += 1
                at = .now
            }
    }
}
#endif
