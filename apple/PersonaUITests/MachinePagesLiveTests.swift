import XCTest

/// The machine pages against a real hub: a local test hub (fake HOME, its own database, an isolated herdr session;
/// docs/operations/testing.md「Machine pages」), never the one in use, since this reconnects machines and starts a session.
///   TEST_RUNNER_PERSONA_LIVE_MACHINES_HUB=http://127.0.0.1:<port> TEST_RUNNER_PERSONA_LIVE_MACHINES_TOKEN=<token>
/// The hub needs one online `local` machine and one ssh machine that cannot connect.
final class MachinePagesLiveTests: XCTestCase {
    private var hub = ""
    private var token = ""
    private var app: XCUIApplication!

    override func setUpWithError() throws {
        continueAfterFailure = false
        let env = ProcessInfo.processInfo.environment
        guard let h = env["PERSONA_LIVE_MACHINES_HUB"], let t = env["PERSONA_LIVE_MACHINES_TOKEN"] else {
            throw XCTSkip("needs a local test hub (PERSONA_LIVE_MACHINES_HUB, PERSONA_LIVE_MACHINES_TOKEN)")
        }
        hub = h
        token = t
    }

    override func tearDown() {
        if let app, (testRun?.totalFailureCount ?? 0) > 0 {
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "failure"
            shot.lifetime = .keepAlways
            add(shot)
            let tree = XCTAttachment(string: app.debugDescription)
            tree.name = "view tree"
            tree.lifetime = .keepAlways
            add(tree)
        }
        super.tearDown()
    }

    private func launch(_ args: [String]) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=" + token
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", ""] + args
        app.launchPinned()
    }

    private func api(_ path: String) -> [String: Any] {
        var req = URLRequest(url: URL(string: hub + path)!)
        req.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        let done = expectation(description: path)
        var out: [String: Any] = [:]
        URLSession.shared.dataTask(with: req) { data, _, _ in
            out = data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] } ?? [:]
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 15)
        return out
    }

    @discardableResult
    private func see(_ e: XCUIElement, _ what: String, timeout: TimeInterval = 15) -> XCUIElement {
        if !e.waitForExistence(timeout: timeout) { XCTFail("\(what) not found") }
        return e
    }

    private func button(_ s: String) -> XCUIElement {
        app.buttons.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    private func text(_ s: String) -> XCUIElement {
        app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    /// Settings › Machines: every machine the hub lists; each card opens its details (on top, not under the list); the offline
    /// one shows the hub's error and Reconnect answers (still offline: a toast says why).
    @MainActor func testMachinesFromTheHub() throws {
        let machines = (api("/api/v1/machines")["machines"] as? [[String: Any]]) ?? []
        let online = machines.filter { $0["online"] as? Bool == true }
        let offline = machines.filter { $0["online"] as? Bool != true }
        XCTAssertFalse(online.isEmpty, "the test hub needs an online machine")
        XCTAssertFalse(offline.isEmpty, "the test hub needs an offline machine")
        launch(["-tab", "settings"])
        see(button("\(online.count) / \(machines.count) 在线"), "设置 › 机器 row with the hub's counts").tap()
        for m in machines {
            let name = m["name"] as! String
            see(text(name), "card \(name)").tap()
            see(app.navigationBars[name], "\(name) details on top")
            if m["online"] as? Bool == true {
                see(text("启动器"), "\(name) launchers")
                XCTAssertFalse(text("机器离线").exists)
            } else {
                see(text("机器离线，连上后显示"), "\(name) says it is offline")
                button("重新连接").tap()
                see(button("正在重新连接"), "reconnecting", timeout: 5)
                let toast = see(app.staticTexts.matching(NSPredicate(format: "label BEGINSWITH %@", "还没连上")).firstMatch,
                                "the toast after reconnecting fails", timeout: 30)
                // It sits over the bar's back button until it goes.
                expectation(for: NSPredicate(format: "exists == false"), evaluatedWith: toast)
                waitForExpectations(timeout: 15)
            }
            app.navigationBars[name].buttons.firstMatch.tap() // not a toast's button
            see(app.navigationBars["机器"], "back on 机器")
        }
    }

    /// New Session on the online machine: pick a directory in the browser and start: the chat opens and the hub has the
    /// session. The test closes it again. PERSONA_LIVE_MACHINES_CODE is a directory on that machine holding `demo`
    /// (the browser starts there; the node's recent directories are the machine's real ones).
    @MainActor func testNewSessionOnTheLocalMachine() throws {
        let machines = (api("/api/v1/machines")["machines"] as? [[String: Any]]) ?? []
        let local = try XCTUnwrap(machines.first { $0["online"] as? Bool == true })
        let mid = local["id"] as! String
        let before = Set(((api("/api/v1/sessions")["sessions"] as? [[String: Any]]) ?? []).compactMap { $0["session_id"] as? String })
        let code = try XCTUnwrap(ProcessInfo.processInfo.environment["PERSONA_LIVE_MACHINES_CODE"], "PERSONA_LIVE_MACHINES_CODE")
        launch(["-tab", "dev", "-newChat.machine", mid, "-cwd.\(mid)", code])
        see(app.buttons["新建会话"], "新建会话").tap()
        see(button(local["name"] as! String), "the online machine's card").tap()
        see(app.buttons["newChat.directory"], "directory row").tap()
        see(button("上一级"), "the directory browser")
        see(app.buttons.matching(NSPredicate(format: "label BEGINSWITH %@", "demo")).firstMatch, "~/code/demo").tap()
        see(button("使用「demo」"), "use demo").tap()
        let start = see(button("开始对话"), "开始对话")
        XCTAssertTrue(start.isEnabled)
        start.tap()
        // Starting runs claude and waits for its input box: up to a minute.
        let sheet = app.navigationBars["新对话"]
        let deadline = Date.now.addingTimeInterval(90)
        while sheet.exists, Date.now < deadline {
            let err = app.descendants(matching: .any).matching(identifier: "newChat.error").firstMatch
            if err.exists {
                XCTFail("the start failed: \(err.label)")
                return
            }
            usleep(500_000)
        }
        XCTAssertFalse(sheet.exists, "the sheet closes once the session started")
        var started: [String: Any]?
        for _ in 0..<20 where started == nil {
            started = ((api("/api/v1/sessions")["sessions"] as? [[String: Any]]) ?? []).first {
                !before.contains($0["session_id"] as? String ?? "") && $0["machine_id"] as? String == mid
            }
            if started == nil { sleep(1) }
        }
        let s = try XCTUnwrap(started, "the hub lists the new session")
        XCTAssertTrue((s["cwd"] as? String ?? "").hasSuffix("/code/demo"))
        see(app.buttons["返回"].firstMatch, "the new chat")
        // Close it: POST …/close.
        var req = URLRequest(url: URL(string: hub + "/api/v1/sessions/\(mid)/\(s["session_id"] as! String)/close")!)
        req.httpMethod = "POST"
        req.setValue("Bearer " + token, forHTTPHeaderField: "Authorization")
        let done = expectation(description: "close")
        URLSession.shared.dataTask(with: req) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 30)
    }
}
