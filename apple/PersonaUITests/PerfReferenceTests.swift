import XCTest

/// A bounded iOS-reference set (#6871), not another run of the existing continuity corpus. Every screen below remains
/// the native production UI: PersonaCore is not created in a view or on the launch path in this phase.
final class PerfReferenceTests: PerfTestCase {
    private static let weather = "macbook/a1f3c2d4-0000-4000-8000-000000000001"
    private static let streaming = "server/b2e4d6f8-0000-4000-8000-000000000004"

    @MainActor    private func referenceLaunch(_ arguments: [String]) {
        launch(["-showCoreReference", "YES"] + arguments)
    }

    @MainActor func testNativeFirstFrame() throws {
        preparePairingIfNeeded()
        mock("frames_clear?kind=first_frame")
        mock("frames_label?name=" + testName)
        let metric = XCTApplicationLaunchMetric()
        measure(metrics: [metric], options: options) { @MainActor in
            referenceLaunch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant", "-newAssistant", "YES"])
            see(any("assistant-welcome"), "the native first screen", timeout: 30)
            app.terminate()
        }
        attachObservations("first_frame")
    }

    @MainActor func testNativeStreamingTrace() throws {
        referenceLaunch(["-dev.openChat", "", "-openChatsFresh", "YES", "-tab", "assistant", "-openAssistant", Self.streaming])
        measureIdle(push: "say?trace=markdown&user=%E5%86%99%E4%B8%80%E4%B8%8B&node=trail", seconds: 24, action: "stream")
    }

    @MainActor func testNativeMainConversationGlide() throws {
        referenceLaunch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant", "-openAssistant",
                         "server/b2e4d6f8-0000-4000-8000-000000000001", "-loadAll", "YES", "-holdChromeOpen", "YES"])
        see(app.buttons["assistant-sidebar-button"], "the long native conversation", timeout: 30)
        measureVerifiedGlide()
    }

    /// Ten actual chat -> Agents list round trips. AppModel may legitimately retain up to eight native ChatStores;
    /// this is not a PersonaCore lifecycle result. The app samples its own phys_footprint outside timing spans.
    @MainActor func testNativeChatTenOpenCloseMemory() throws {
        mock("frames_clear?kind=memory")
        mock("frames_label?name=" + testName)
        referenceLaunch(["-app.tab", "chats", "-tab", "dev", "-dev.openChat", "", "-openAgentsFresh", "YES"])
        see(app.buttons["agents.filter.all"], "the native Agents list", timeout: 30)
        app.revealAgentsSearch()
        let search = app.searchFields.firstMatch
        search.tap()
        search.typeText("给天气 App 加主屏幕小组件")
        let row = app.buttons.matching(NSPredicate(format: "identifier IN %@ AND label CONTAINS %@",
                                                     ["agents.row", "agents.compactRow"], "给天气 App 加主屏幕小组件")).firstMatch
        see(row, "the fixed native chat row", timeout: 15)
        let ready = any("shell-composer-field")
        func roundTrip() {
            row.tapCenter()
            see(ready, "the native chat composer", timeout: 15)
            app.buttons["shell-agents"].firstMatch.tapCenter()
            see(app.buttons["agents.filter.all"], "the native Agents list after close", timeout: 15)
        }
        roundTrip()
        sleep(3)
        darwinPost("com.persona.perf.memory")
        sleep(1)
        let initial = (mock("frames?name=" + testName + "&kind=memory")["runs"] as? [[String: Any]])?.count ?? 0
        XCTAssertEqual(initial, 1, "the app did not report its warmed baseline")
        for _ in 0..<10 {
            roundTrip()
            sleep(1)
            darwinPost("com.persona.perf.memory")
        }
        sleep(5)
        darwinPost("com.persona.perf.memory")
        attachObservations("memory", minimum: 12)
    }

    private func attachObservations(_ kind: String, minimum: Int = 1) {
        let deadline = Date.now.addingTimeInterval(15)
        var runs: [[String: Any]] = []
        repeat {
            runs = (mock("frames?name=" + testName + "&kind=" + kind)["runs"] as? [[String: Any]]) ?? []
            if runs.count < minimum { usleep(200_000) }
        } while runs.count < minimum && Date.now < deadline
        if let data = try? JSONSerialization.data(withJSONObject: runs, options: [.prettyPrinted, .sortedKeys]) {
            let attachment = XCTAttachment(data: data, uniformTypeIdentifier: "public.json")
            attachment.name = "frames.json"
            attachment.lifetime = .keepAlways
            add(attachment)
        }
        XCTAssertGreaterThanOrEqual(runs.count, minimum, "missing native \(kind) observations")
        for run in runs {
            XCTAssertEqual(run["source"] as? String, "native-ios-process")
            XCTAssertGreaterThan((run["phys_footprint_bytes"] as? NSNumber)?.intValue ?? 0, 0)
        }
    }
}
