import Darwin
import Foundation
import XCTest

/// Real app + real loopback HTTP/WebSocket fixture. No production hub, node, credentials or data are involved.
final class AgentsPagingTests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    private var cliPort: UInt16 = 0

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PAGING_HUB"],
              let port = ProcessInfo.processInfo.environment["PAGING_CLI_PORT"].flatMap(UInt16.init) else {
            throw XCTSkip("needs the isolated 8000-row paging fixture and a dedicated CLI port")
        }
        self.hub = hub
        cliPort = port
        _ = fixture("reset-metrics")
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            shot("paging-failure")
            let tree = XCTAttachment(string: app.debugDescription)
            tree.name = "paging-failure-tree"
            tree.lifetime = .keepAlways
            add(tree)
        }
        super.tearDown()
    }

    @MainActor private func launch(_ appearance: String = "Light") -> Double {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-app.tab", "chats", "-tab", "dev", "-dev.openChat", "",                                "-openAgentsFresh", "YES", "-dev.grouping", "recent", "-dev.folded", "",
                               "-ui.scheme", appearance.lowercased(), "-runtimeCLI", String(cliPort), "-runtimeCLIToken", "paging-test"]
        let start = Date.now
        app.launchPinned()
        let firstRow = app.buttons.matching(identifier: "agents.row").firstMatch
        XCTAssertTrue(firstRow.waitForExistence(timeout: 30))
        acceptIconAlert(timeout: 2)
        XCTAssertTrue(firstRow.isHittable, "a system prompt must not cover first-screen evidence")
        let state = command("state")
        XCTAssertEqual(state["appearance"] as? String, appearance.lowercased(), "the screenshot must use the requested appearance")
        return Date.now.timeIntervalSince(start) * 1000
    }

    @MainActor func testFirstPageScrollAndRevisionRecovery() throws {
        let firstMs = launch()
        let window = command("agents-list-window")
        XCTAssertEqual(window["loaded"] as? Int, 50)
        XCTAssertEqual(window["total"] as? Int, 8000)
        XCTAssertFalse(app.buttons["agents.filter.all"].label.contains("8,000"), "unknown visible membership must not display the transport total")
        XCTAssertFalse(app.buttons["agents.filter.working"].label.contains("2,000"), "unknown visible counts must not become partial/global numbers")
        let requests = fixture("metrics")["requests"] as? [[String: Any]] ?? []
        let baselines = requests.filter { r in
            let q = r["query"] as? [String: String] ?? [:]
            return r["path"] as? String == "/api/v1/sessions" && q["counts"] == nil && q["since"] == nil
        }
        XCTAssertEqual(baselines.count, 1, "first screen must issue exactly one row page")
        XCTAssertEqual(baselines.first?["rows"] as? Int, 50)
        let networkBytes = baselines.first?["bytes"] as? Int ?? 0
        print("PAGING_FIRST_SCREEN ms=\(firstMs) bytes=\(networkBytes) loaded=50 total=8000")
        shot("paging-first-light")

        for _ in 0..<12 {
            let scroll = app.collectionViews.allElementsBoundByIndex.first(where: { $0.isHittable }) ?? app.collectionViews.firstMatch
            scroll.swipeUp(velocity: .fast)
            if (command("agents-list-window")["loaded"] as? Int ?? 0) > 50 { break }
        }
        let deadline = Date.now.addingTimeInterval(10)
        var loaded = 50
        while Date.now < deadline {
            loaded = command("agents-list-window")["loaded"] as? Int ?? 0
            if loaded > 50 { break }
            usleep(100_000)
        }
        XCTAssertGreaterThan(loaded, 50)
        XCTAssertLessThan(loaded, 8000)
        let after = fixture("metrics")["requests"] as? [[String: Any]] ?? []
        XCTAssertTrue(after.contains { ($0["query"] as? [String: String])?["cursor"] != nil })
        shot("paging-next-light")
        for round in 1...3 {
            let fps = command("agents-list-glide", args: ["seconds": 6, "velocity": 2000])
            print("PAGING_GLIDE round=\(round) " + String(data: try JSONSerialization.data(withJSONObject: fps, options: .sortedKeys), encoding: .utf8)!)
            XCTAssertGreaterThanOrEqual(fps["seconds"] as? Double ?? 0, 5.9)
            XCTAssertGreaterThan(fps["sampledFPS"] as? Double ?? 0, 50, "simulator cadence must remain usable")
        }

        _ = fixture("change?row=0")
        let changedDeadline = Date.now.addingTimeInterval(10)
        var deltas: [[String: Any]] = []
        repeat {
            deltas = (fixture("metrics")["requests"] as? [[String: Any]] ?? []).filter { ($0["query"] as? [String: String])?["since"] != nil && ($0["query"] as? [String: String])?["counts"] == nil }
            if deltas.contains(where: { ($0["rows"] as? Int ?? 0) > 0 }) { break }
            usleep(100_000)
        } while Date.now < changedDeadline
        XCTAssertTrue(deltas.contains { ($0["rows"] as? Int ?? 0) == 1 })
        let appliedDeadline = Date.now.addingTimeInterval(10)
        var checkpoint = ""
        repeat {
            let lists = command("agents-list-window")["lists"] as? [String: [String: Any]] ?? [:]
            checkpoint = lists[""]?["revision"] as? String ?? ""
            if checkpoint == "fixture-revision-1" { break }
            usleep(100_000)
        } while Date.now < appliedDeadline
        XCTAssertEqual(checkpoint, "fixture-revision-1", "wait for the client to apply the delta, not merely for HTTP to serve it")
        print("PAGING_SCROLL_TO_TOP \(command("scroll", args: ["to": "top"]))")
        let updated = app.buttons.matching(NSPredicate(format: "identifier == %@ AND label CONTAINS %@", "agents.row", "已更新 Agent 00000")).firstMatch
        XCTAssertTrue(updated.waitForExistence(timeout: 10), "the loaded row must show the revision update")
        XCTAssertTrue(updated.isHittable, "the updated row must be on screen")
        print("PAGING_AFTER_UPDATE " + String(data: try JSONSerialization.data(withJSONObject: command("agents-list-window"), options: .sortedKeys), encoding: .utf8)!)
        shot("paging-update-light")
        if let shots = ProcessInfo.processInfo.environment["PAGING_SHOTS"] {
            let ledger = try JSONSerialization.data(withJSONObject: fixture("metrics"))
            try ledger.write(to: URL(fileURLWithPath: shots).deletingLastPathComponent().appendingPathComponent("paged-http-metrics.json"))
        }

        // A pane/host filter cannot be expressed by source_kind. Exercise the actual chip: it must obtain the old
        // complete collection, not discard matches that happened to be beyond the first fifty rows.
        let terminal = app.buttons["agents.filter.terminal"]
        XCTAssertTrue(terminal.waitForExistence(timeout: 10))
        terminal.tap()
        let fallbackDeadline = Date.now.addingTimeInterval(20)
        var full = false
        repeat {
            let state = command("agents-list-window")
            let pages = state["lists"] as? [String: [String: Any]] ?? [:]
            full = (pages[""]?["paged"] as? Bool == false) && (state["loaded"] as? Int == 8000)
            if full { break }
            usleep(100_000)
        } while Date.now < fallbackDeadline
        XCTAssertTrue(full, "host filtering must have all 8000 rows")
        XCTAssertTrue(fixture("metrics")["requests"] is [[String: Any]])
        let fullRequests = (fixture("metrics")["requests"] as? [[String: Any]] ?? []).filter {
            ($0["query"] as? [String: String])?.isEmpty == true && $0["path"] as? String == "/api/v1/sessions"
        }
        XCTAssertTrue(fullRequests.contains { $0["rows"] as? Int == 8000 })
        let search = app.searchFields.firstMatch
        XCTAssertTrue(search.waitForExistence(timeout: 10))
        search.tap()
        search.typeText("示例 Agent 07999")
        let tail = app.buttons.matching(NSPredicate(format: "identifier == %@ AND label CONTAINS %@", "agents.row", "示例 Agent 07999")).firstMatch
        XCTAssertTrue(tail.waitForExistence(timeout: 15), "local search must retain a match from the last row of the complete source")
        print("PAGING_FALLBACK host=terminal loaded=8000 deep_search=07999 passed")
        shot("paging-fallback-tail-light")
    }

    @MainActor func testDarkFirstPage() {
        _ = launch("Dark")
        XCTAssertEqual(command("agents-list-window")["loaded"] as? Int, 50)
        shot("paging-first-dark")
    }

    private func fixture(_ path: String) -> [String: Any] {
        guard let data = try? Data(contentsOf: URL(string: hub + "/mock/paging/" + path)!),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { return [:] }
        return obj
    }

    private func shot(_ name: String) {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let directory = ProcessInfo.processInfo.environment["PAGING_SHOTS"] {
            try? screenshot.pngRepresentation.write(to: URL(fileURLWithPath: directory).appendingPathComponent(name + ".png"))
        }
    }

    private func command(_ name: String, args: [String: Any] = [:]) -> [String: Any] {
        let fd = Darwin.socket(AF_INET, SOCK_STREAM, 0)
        guard fd >= 0 else { XCTFail("CLI socket failed"); return [:] }
        defer { Darwin.close(fd) }
        var timeout = timeval(tv_sec: 20, tv_usec: 0)
        _ = withUnsafePointer(to: &timeout) { setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, $0, socklen_t(MemoryLayout<timeval>.size)) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_port = cliPort.bigEndian
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        let result = withUnsafePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { Darwin.connect(fd, $0, socklen_t(MemoryLayout<sockaddr_in>.size)) }
        }
        guard result == 0 else { XCTFail("CLI did not connect"); return [:] }
        let request = try! JSONSerialization.data(withJSONObject: ["token": "paging-test", "cmd": name, "args": args]) + Data([10])
        _ = request.withUnsafeBytes { Darwin.send(fd, $0.baseAddress!, request.count, 0) }
        var data = Data()
        var buffer = [UInt8](repeating: 0, count: 65536)
        while data.count < 1 << 20 {
            let n = Darwin.recv(fd, &buffer, buffer.count, 0)
            if n <= 0 { break }
            data.append(contentsOf: buffer.prefix(n))
            if data.contains(10) { break }
        }
        guard let json = try? JSONSerialization.jsonObject(with: data) as? [String: Any] else { XCTFail("Invalid CLI response"); return [:] }
        XCTAssertEqual(json["ok"] as? Bool, true, "\(json)")
        return json
    }
}
