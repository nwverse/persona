import XCTest

/// A bounded iOS-reference set (#6871), not another run of the existing continuity corpus. Every screen below remains
/// the native production UI: PersonaCore is not created in a view or on the launch path in this phase.
final class PerfReferenceTests: PerfTestCase {
    private static let heavyChat = "server/a1f3c2d4-0000-4000-8000-000000000008"
    private static let streaming = "server/b2e4d6f8-0000-4000-8000-000000000004"

    @MainActor private func referenceLaunch(_ arguments: [String], observing: Bool = false) {
        let run = currentRun
        var arguments = arguments
        if let run {
            arguments += ["-perfRunID", run.id, "-perfRunName", run.name, "-perfRunWarmup", run.warmUp ? "YES" : "NO",
                          "-perfRunAction", run.action, "-perfRunIterations", String(run.expectedIterations)]
        }
        launch((observing ? ["-showCoreReference", "YES"] : []) + arguments)
        if let run {
            app.launchArguments += ["-perfRunID", run.id, "-perfRunName", run.name, "-perfRunWarmup", run.warmUp ? "YES" : "NO",
                                    "-perfRunAction", run.action, "-perfRunIterations", String(run.expectedIterations)]
        }
    }

    private var currentRun: (id: String, name: String, warmUp: Bool, action: String, expectedIterations: Int)?

    private func beginRun(warmUp: Bool, action: String) {
        currentRun = (UUID().uuidString, testName, warmUp, action, iterations)
        if let run = currentRun {
            app.launchArguments = (app.launchArguments.filter { !$0.hasPrefix("-perfRun") } + [
                "-perfRunID", run.id, "-perfRunName", run.name, "-perfRunWarmup", run.warmUp ? "YES" : "NO",
                "-perfRunAction", run.action, "-perfRunIterations", String(run.expectedIterations)])
        }
    }

    private func endRun() {
        guard let run = currentRun else { XCTFail("no active reference run"); return }
        let deadline = Date.now.addingTimeInterval(20)
        var found: [[String: Any]] = []
        repeat {
            let rows = (mock("frames?run_id=" + run.id)["runs"] as? [[String: Any]]) ?? []
            found = rows.filter { ($0["run_id"] as? String) == run.id }
            if found.isEmpty { usleep(200_000) }
        } while found.isEmpty && Date.now < deadline
        XCTAssertEqual(found.count, 1, "the app did not upload exactly one sample for \(run.action)/\(run.id)")
        currentRun = nil
    }

    @MainActor func testNativeFirstFrame() throws {
        preparePairingIfNeeded()
        mock("frames_clear?kind=first_frame")
        mock("frames_label?name=" + testName)
        let metric = XCTApplicationLaunchMetric()
        var iteration = 0
        measure(metrics: [metric], options: options) { @MainActor in
            beginRun(warmUp: iteration == 0, action: "launch")
            referenceLaunch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant", "-newAssistant", "YES"], observing: true)
            see(any("assistant-welcome"), "the native first screen", timeout: 30)
            endRun()
            app.terminate()
            iteration += 1
        }
        attachObservations("first_frame", minimum: iterations + 1)
    }

    @MainActor func testNativeStreamingTrace() throws {
        preparePairingIfNeeded()
        referenceLaunch(["-dev.openChat", "", "-openChatsFresh", "YES", "-tab", "assistant", "-openAssistant", Self.streaming])
        var iteration = 0
        measure(metrics: metrics(), options: options) {
            beginRun(warmUp: iteration == 0, action: "stream")
            referenceLaunch(["-dev.openChat", "", "-openChatsFresh", "YES", "-tab", "assistant", "-openAssistant", Self.streaming])
            darwinPost("com.persona.perf.frames.context")
            darwinPost("com.persona.perf.frames.start")
            _ = mock("say?trace=markdown&user=%E5%86%99%E4%B8%80%E4%B8%8B&node=trail")
            sleep(24)
            darwinPost("com.persona.perf.frames.stop")
            endRun()
            app.terminate()
            iteration += 1
        }
        let rows = (mock("frames?name=" + testName)["runs"] as? [[String: Any]]) ?? []
        attach(runs: rows)
        XCTAssertEqual(rows.count, iterations + 1)
        XCTAssertEqual(rows.filter { $0["warm_up"] as? Bool == true }.count, 1)
        for row in rows {
            XCTAssertGreaterThan((row["text_changes"] as? NSNumber)?.intValue ?? 0, 0)
            XCTAssertGreaterThan((row["frames"] as? NSNumber)?.intValue ?? 0, 0)
        }
    }

    @MainActor private func attach(runs: [[String: Any]]) {
        if let data = try? JSONSerialization.data(withJSONObject: runs, options: [.prettyPrinted, .sortedKeys]) {
            let attachment = XCTAttachment(data: data, uniformTypeIdentifier: "public.json")
            attachment.name = "frames.json"
            attachment.lifetime = .keepAlways
            add(attachment)
        }
    }

    @MainActor func testNativeMainConversationGlide() throws {
        referenceLaunch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant", "-openAssistant",
                         "server/b2e4d6f8-0000-4000-8000-000000000001", "-loadAll", "YES", "-holdChromeOpen", "YES"])
        see(app.buttons["assistant-sidebar-button"], "the long native conversation", timeout: 30)
        var iteration = 0
        measure(metrics: metrics(), options: options) {
            beginRun(warmUp: iteration == 0, action: "glide")
            referenceLaunch(["-dev.openChat", "", "-app.tab", "assistant", "-tab", "assistant", "-openAssistant",
                             "server/b2e4d6f8-0000-4000-8000-000000000001", "-loadAll", "YES", "-holdChromeOpen", "YES"])
            see(app.buttons["assistant-sidebar-button"], "the long native conversation", timeout: 30)
            darwinPost("com.persona.perf.frames.context")
            darwinPost("com.persona.perf.frames.glide")
            endRun()
            iteration += 1
        }
        let rows = (mock("frames?name=" + testName)["runs"] as? [[String: Any]]) ?? []
        attach(runs: rows)
        XCTAssertEqual(rows.count, iterations + 1)
        XCTAssertEqual(rows.filter { $0["warm_up"] as? Bool == true }.count, 1)
        for row in rows {
            XCTAssertNil(row["error"])
            XCTAssertEqual(row["sample_source"] as? String, "app-list-glide")
            XCTAssertGreaterThan((row["scroll_offset_delta_pt"] as? NSNumber)?.doubleValue ?? 0, 24)
        }
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "verified-glide-page"
        shot.lifetime = .keepAlways
        add(shot)
    }

    /// Ten actual chat -> Agents list round trips. AppModel may legitimately retain up to eight native ChatStores;
    /// this is not a PersonaCore lifecycle result. The app samples its own phys_footprint outside timing spans.
    @MainActor func testNativeChatTenOpenCloseMemory() throws {
        mock("frames_clear?kind=memory")
        mock("frames_label?name=" + testName)
        referenceLaunch(["-app.tab", "chats", "-tab", "dev", "-dev.openChat", "", "-openAgentsFresh", "YES"], observing: true)
        see(app.buttons["agents.filter.all"], "the native Agents list", timeout: 30)
        app.revealAgentsSearch()
        let search = app.searchFields.firstMatch
        search.tap()
        search.typeText("整理构建脚本")
        let row = app.buttons.matching(NSPredicate(format: "identifier IN %@ AND label CONTAINS %@",
                                                     ["agents.row", "agents.compactRow"], "整理构建脚本")).firstMatch
        see(row, "the fixed native chat row", timeout: 15)
        let ready = any("shell-composer-field")
        func roundTrip() {
            row.tapCenter()
            see(ready, "the native chat composer", timeout: 15)
            back()
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
