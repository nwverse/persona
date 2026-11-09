import XCTest

/// Settings › Model Providers › Add on a hub with built-in presets (#4197, docs/assistant/onboarding.md 4.3「三条路」): A "I already use Claude Code"
/// (one try, answered and failed), B only a key (pick → paste → tested → saved, what was assigned and kept), C "Where to get a model"
/// (Sign Up opens the signup page in the app; back from it, the key is next). Against scripts/mock/mock_scenarios.py,
/// which restarts the mock hub with the variables each test needs:
///   TEST_RUNNER_PERSONA_PRESET_HUB=http://127.0.0.1:<port> TEST_RUNNER_PERSONA_PRESET_CONTROL=http://127.0.0.1:<control>
/// TEST_RUNNER_PERSONA_SHOTS=<dir> also saves every screen there (light and dark) as PNG.
final class ProviderPresetWizardTests: XCTestCase {
    private let env = ProcessInfo.processInfo.environment

    override func setUp() { continueAfterFailure = false }

    /// Restarts the mock hub with these MOCK_* variables (mock_scenarios.py waits until it answers).
    private func scenario(_ vars: String) throws {
        guard let control = env["PERSONA_PRESET_CONTROL"] else { throw XCTSkip("needs mock_scenarios.py (PERSONA_PRESET_CONTROL)") }
        let url = URL(string: control + "/scenario?env=" + vars.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed)!)!
        let done = expectation(description: "scenario")
        URLSession.shared.dataTask(with: url) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 60)
    }

    @MainActor private func launch(scheme: String) throws -> XCUIApplication {
        guard let hub = env["PERSONA_PRESET_HUB"] else { throw XCTSkip("needs mock_scenarios.py (PERSONA_PRESET_HUB)") }
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let app = XCUIApplication()
        app.launchArguments = ["-pairURL", pair, "-ui.scheme", scheme, "-dev.openChat", "", "-tab", "settings",
                               "-openModelLaunchers", "YES", "-openProviderWizard", "pick"]
        app.launchPinned()
        dismissSystemAlerts(wait: 4)
        return app
    }

    /// The notification question and 「你已更改“Persona”的图标」 (the icon follows light / dark) are SpringBoard's.
    private func dismissSystemAlerts(wait: TimeInterval = 1) {
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        var deadline = Date.now.addingTimeInterval(wait)
        // Only an alert it could answer buys more time (one it cannot, left up, would keep it here for ever).
        while Date.now < deadline {
            let alert = springboard.alerts.firstMatch
            guard alert.waitForExistence(timeout: 0.5) else { continue }
            guard let label = ["允许", "Allow", "好", "OK"].first(where: { alert.buttons[$0].exists }) else {
                XCTFail("a system alert nobody answers:\n\(springboard.debugDescription)")
                return
            }
            alert.buttons[label].tap()
            deadline = Date.now.addingTimeInterval(1.5)
        }
    }

    private func shot(_ app: XCUIApplication, _ name: String) {
        dismissSystemAlerts()
        let png = app.screenshot()
        let a = XCTAttachment(screenshot: png)
        a.name = name
        a.lifetime = .keepAlways
        add(a)
        if let dir = env["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("preset-wizard-\(name).png"))
        }
    }

    private func text(_ app: XCUIApplication, _ s: String) -> XCUIElement {
        app.descendants(matching: .any).matching(NSPredicate(format: "label CONTAINS %@", s)).firstMatch
    }

    private func element(_ app: XCUIApplication, _ id: String) -> XCUIElement {
        app.descendants(matching: .any).matching(identifier: id).firstMatch
    }

    /// The first step lists the hub's providers; the gateway typed by hand is last; Anthropic official is not written in.
    @MainActor private func openPick(_ app: XCUIApplication) {
        let first = element(app, "wizard.preset.builtin/deepseek")
        XCTAssertTrue(first.waitForExistence(timeout: 20), "no presets:\n\(app.debugDescription)")
        XCTAssertFalse(element(app, "wizard.kind.anthropic").exists, "the hardcoded Anthropic row is still there")
        // The gateway after the presets, then the two side paths (further down: the list is lazy).
        reveal(app, "wizard.path.claude")
        XCTAssertTrue(element(app, "wizard.kind.custom").exists, "no 自定义兼容网关:\n\(app.debugDescription)")
        XCTAssertFalse(element(app, "wizard.kind.import").exists, "the import is not under 高级")
    }

    /// Scrolls the sheet until the element is on screen.
    @MainActor private func reveal(_ app: XCUIApplication, _ id: String) {
        let e = element(app, id)
        for _ in 0..<6 where !(e.exists && e.isHittable) { app.swipeUp() }
        XCTAssertTrue(e.exists && e.isHittable, "no \(id):\n\(app.debugDescription)")
    }

    /// Scrolls back to the top of the sheet.
    @MainActor private func top(_ app: XCUIApplication, _ id: String) {
        let e = element(app, id)
        for _ in 0..<6 where !(e.exists && e.isHittable) { app.swipeDown() }
    }

    /// A: the main conversation's Claude Code answers one try, then (another hub) it is not logged in.
    @MainActor func testAlreadyUsingClaudeCode() throws {
        for scheme in ["light", "dark"] {
            for (sc, ok) in [("full", true), ("not_logged_in", false)] {
                try scenario("MOCK_READINESS=\(sc)")
                let app = try launch(scheme: scheme)
                openPick(app)
                if sc == "full" {
                    shot(app, "pick-more-\(scheme)")
                }
                element(app, "wizard.path.claude").tap()
                let tryIt = app.buttons["wizard.claude.try"]
                XCTAssertTrue(tryIt.waitForExistence(timeout: 5))
                tryIt.tap()
                let result = element(app, "wizard.claude.result")
                XCTAssertTrue(result.waitForExistence(timeout: 20), "no result:\n\(app.debugDescription)")
                if ok {
                    XCTAssertTrue(text(app, "小林的 MacBook 上的 Claude Code 能用").exists, "\(app.debugDescription)")
                    XCTAssertTrue(app.buttons["wizard.done"].exists)
                } else {
                    XCTAssertTrue(text(app, "没成功").exists && text(app, "Not logged in").exists, "\(app.debugDescription)")
                    XCTAssertTrue(app.buttons["wizard.claude.useKey"].exists)
                }
                shot(app, "claude-\(ok ? "ok" : "failed")-\(scheme)")
                app.terminate()
            }
        }
    }

    /// B: pick → paste → the test passes by itself → saved: the main conversation was changed by hand, so it is kept.
    @MainActor func testOnlyAKey() throws {
        for scheme in ["light", "dark"] {
            try scenario("MOCK_READINESS=not_logged_in,MOCK_ASSIGN_KEPT=assistant:customized")
            let app = try launch(scheme: scheme)
            openPick(app)
            top(app, "wizard.preset.builtin/deepseek")
            shot(app, "pick-\(scheme)")
            element(app, "wizard.preset.builtin/deepseek").tap()
            let key = app.secureTextFields["wizard.preset.key"].firstMatch
            let field = key.exists ? key : app.textFields["wizard.preset.key"].firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 5), "no key field:\n\(app.debugDescription)")
            XCTAssertTrue(element(app, "wizard.preset.signup").exists, "no 「Key 从哪拿」")
            XCTAssertTrue(app.switches["wizard.assign"].exists)
            field.tap()
            field.typeText("sk-bad-0123456789")
            XCTAssertTrue(text(app, "认证失败（401）").waitForExistence(timeout: 10), "no 401 words:\n\(app.debugDescription)")
            XCTAssertTrue(app.buttons["wizard.preset.retest"].exists)
            shot(app, "key-failed-\(scheme)")
            // A key typed over in the same secure field never lets the app settle for XCTest: a fresh sheet for the good one.
            app.terminate()
            let again = try launch(scheme: scheme)
            openPick(again)
            top(again, "wizard.preset.builtin/deepseek")
            element(again, "wizard.preset.builtin/deepseek").tap()
            let key2 = again.secureTextFields["wizard.preset.key"].firstMatch
            let field2 = key2.waitForExistence(timeout: 5) ? key2 : again.textFields["wizard.preset.key"].firstMatch
            XCTAssertTrue(field2.waitForExistence(timeout: 5))
            field2.tap()
            field2.typeText("sk-demo-0123456789")
            XCTAssertTrue(text(app, "连接正常").waitForExistence(timeout: 10), "no test result:\n\(app.debugDescription)")
            shot(app, "key-ok-\(scheme)")
            app.buttons["wizard.save"].tap()
            let done = element(app, "wizard.doneResult")
            XCTAssertTrue(done.waitForExistence(timeout: 10), "no result:\n\(app.debugDescription)")
            XCTAssertTrue(text(app, "接好了").exists && text(app, "新开的 Agent 现在用它").exists, "\(app.debugDescription)")
            XCTAssertTrue(text(app, "主对话没换：你改过").exists, "\(app.debugDescription)")
            PasswordPrompt.assertNone(app, "a provider's API key")
            shot(app, "saved-\(scheme)")
            app.buttons["wizard.done"].tap()
            XCTAssertTrue(app.staticTexts["DeepSeek"].waitForExistence(timeout: 10), "the provider is not in the list:\n\(app.debugDescription)")
            app.terminate()
        }
    }

    /// A Responses direct API is testable, but cannot run the main conversation or Agents (#2223).
    @MainActor func testDirectAPIKeyAndDone() throws {
        for scheme in ["light", "dark"] {
            try scenario("MOCK_READINESS=zero")
            let app = try launch(scheme: scheme)
            openPick(app)
            reveal(app, "wizard.preset.builtin/grok")
            element(app, "wizard.preset.builtin/grok").tap()
            let key = app.secureTextFields["wizard.preset.key"].firstMatch
            let field = key.waitForExistence(timeout: 5) ? key : app.textFields["wizard.preset.key"].firstMatch
            XCTAssertTrue(field.waitForExistence(timeout: 5))
            XCTAssertFalse(app.switches["wizard.assign"].exists, "a direct API must not offer assign")
            XCTAssertTrue(text(app, "主对话和 Agent 不能用它").waitForExistence(timeout: 5), "no direct-API note:\n\(app.debugDescription)")
            field.tap()
            field.typeText("sk-demo-0123456789")
            XCTAssertTrue(text(app, "连接正常").waitForExistence(timeout: 10), "a Responses key was not tested:\n\(app.debugDescription)")
            shot(app, "direct-key-\(scheme)")
            app.buttons["wizard.save"].tap()
            XCTAssertTrue(element(app, "wizard.doneResult").waitForExistence(timeout: 10), "no result:\n\(app.debugDescription)")
            XCTAssertTrue(text(app, "主对话和 Agent 不能用它").exists)
            XCTAssertFalse(text(app, "要让主对话或 Agent 用它").exists, "the done step still asks for an incompatible model assignment")
            shot(app, "direct-done-\(scheme)")
            app.terminate()
        }
    }

    /// C: where each provider gives out keys; Sign Up opens its page in the app, and closing it leads to its key.
    @MainActor func testWhereToGetAModel() throws {
        for scheme in ["light", "dark"] {
            try scenario("MOCK_READINESS=zero")
            let app = try launch(scheme: scheme)
            openPick(app)
            reveal(app, "wizard.path.get")
            element(app, "wizard.path.get").tap()
            let go = app.buttons["wizard.get.builtin/deepseek"]
            XCTAssertTrue(go.waitForExistence(timeout: 5), "no list:\n\(app.debugDescription)")
            XCTAssertFalse(element(app, "wizard.get.builtin/grok").exists, "a direct API cannot satisfy 「Where to get a model」")
            shot(app, "get-\(scheme)")
            // The signup page opens in the app's link sheet; closing it goes on to the provider's key.
            go.tap()
            let close = app.buttons["link-close"]
            XCTAssertTrue(close.waitForExistence(timeout: 10), "no link sheet:\n\(app.debugDescription)")
            close.tap()
            XCTAssertTrue(element(app, "wizard.preset.key").waitForExistence(timeout: 10), "not on the key step:\n\(app.debugDescription)")
            XCTAssertTrue(app.buttons["wizard.back"].waitForExistence(timeout: 5))
            app.buttons["wizard.back"].tap()
            XCTAssertTrue(go.waitForExistence(timeout: 5), "上一步 from the key does not lead back to the list")
            // Zero computers: the import says why it cannot run.
            app.buttons["wizard.back"].tap()
            reveal(app, "wizard.pick.advanced")
            element(app, "wizard.pick.advanced").tap()
            app.swipeUp()
            XCTAssertTrue(text(app, "还没有电脑：先添加一台电脑").waitForExistence(timeout: 5), "\(app.debugDescription)")
            shot(app, "advanced-zero-\(scheme)")
            // Disabled: tapping it stays on the list.
            text(app, "还没有电脑：先添加一台电脑").tap()
            XCTAssertFalse(text(app, "选一台电脑").waitForExistence(timeout: 2), "the import opened with no computer")
            app.terminate()
        }
    }
}
