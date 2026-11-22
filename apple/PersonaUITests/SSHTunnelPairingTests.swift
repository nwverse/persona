import XCTest

/// Pairing a hub this device cannot reach directly, through an SSH tunnel set up on the pairing page
/// (docs/operations/network.md「Connecting to hub through SSH」): the server's fingerprint is shown in an alert and only trusted when tapped,
/// then pairing goes through the tunnel.
///
/// Needs a real sshd that takes the key passed in and forwards to a hub (the lane's end-to-end setup, see
/// docs/operations/testing.md「SSH tunnels」); skipped without one. Environment (TEST_RUNNER_ prefix when run by xcodebuild):
/// PERSONA_SSH_HOST, PERSONA_SSH_PORT, PERSONA_SSH_USER, PERSONA_SSH_TARGET_HOST, PERSONA_SSH_TARGET_PORT,
/// PERSONA_SSH_FP (the server's `SHA256:…`), PERSONA_SSH_KEY (base64 seed of the key the server takes),
/// PERSONA_SSH_HUB_URL (an address of the hub this simulator cannot reach), PERSONA_SSH_HUB_TOKEN.
final class SSHTunnelPairingTests: XCTestCase {
    private var app: XCUIApplication!
    private var env: [String: String] = [:]

    override func setUpWithError() throws {
        continueAfterFailure = false
        let all = ProcessInfo.processInfo.environment
        for k in ["HOST", "PORT", "USER", "TARGET_HOST", "TARGET_PORT", "FP", "KEY", "HUB_URL", "HUB_TOKEN"] {
            guard let v = all["PERSONA_SSH_" + k], !v.isEmpty else { throw XCTSkip("no SSH test server (PERSONA_SSH_\(k))") }
            env[k] = v
        }
    }

    @MainActor func testPairThroughTheTunnelAfterTrustingTheServer() throws {
        app = XCUIApplication()
        app.launchArguments = ["-openWelcome", "YES", "-pairURL", "", "-seedSSHKey", env["KEY"]!]
        app.launchPinned()
        let pair = app.buttons["welcome.pair"]
        XCTAssertTrue(pair.waitForExistence(timeout: 10))
        pair.tap()

        let address = app.textFields["服务器地址"]
        XCTAssertTrue(address.waitForExistence(timeout: 10))
        address.tap()
        address.typeText(env["HUB_URL"]!)
        let token = app.secureTextFields["pair.token"]
        token.tap()
        token.typeText(env["HUB_TOKEN"]!)

        app.buttons["pair.ssh"].tap()
        // Should iOS still offer to save the token as a password, it is not one.
        let notNow = app.buttons["Not Now"]
        if notNow.waitForExistence(timeout: 2) { notNow.tap() }
        fill("ssh.host", env["HOST"]!)
        fill("ssh.port", env["PORT"]!)
        fill("ssh.user", env["USER"]!)
        fill("ssh.targetHost", env["TARGET_HOST"]!)
        fill("ssh.targetPort", env["TARGET_PORT"]!)
        let save = app.buttons["ssh.save"]
        if !save.isHittable { app.swipeUp() }
        save.tap()

        // First connection: the fingerprint is shown, nothing is trusted until the user says so.
        let alert = app.alerts["确认 SSH 服务器的指纹"]
        XCTAssertTrue(alert.waitForExistence(timeout: 20), "no fingerprint to confirm")
        XCTAssertTrue(alert.staticTexts.allElementsBoundByIndex.contains { $0.label.contains(env["FP"]!) }, "the alert shows another fingerprint")
        try shot(app, "ssh-confirm")
        alert.buttons["信任并保存"].tap()

        // Saved: back on the pairing page, which says it will try the tunnel when the address does not answer.
        let ssh = app.buttons["pair.ssh"]
        XCTAssertTrue(ssh.waitForExistence(timeout: 20))
        XCTAssertTrue(ssh.label.contains(env["USER"]! + "@" + env["HOST"]!), ssh.label)
        try shot(app, "ssh-pair")
        app.buttons["连接"].tap()

        // Paired through the tunnel (the address itself does not answer): the engine step if the hub asks, then the app.
        let engine = app.buttons["engine.save"]
        let root = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(engine.waitForExistence(timeout: 30) || root.waitForExistence(timeout: 5), "not paired")
        if engine.exists { engine.tap() }
        XCTAssertTrue(root.waitForExistence(timeout: 15))
    }

    /// Types into an empty field (the defaults are its prompt), scrolling it between the navigation bar and the
    /// keyboard first.
    @MainActor private func fill(_ id: String, _ text: String) {
        let field = app.textFields[id]
        XCTAssertTrue(field.waitForExistence(timeout: 10), id)
        for _ in 0..<6 {
            let keyboard = app.keyboards.firstMatch
            let bottom = keyboard.exists ? keyboard.frame.minY : app.frame.maxY - 40
            let middle = app.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.35))
            if field.frame.minY < 140 {
                middle.press(forDuration: 0.05, thenDragTo: middle.withOffset(CGVector(dx: 0, dy: 160)))
            } else if field.frame.maxY > bottom - 20 {
                middle.press(forDuration: 0.05, thenDragTo: middle.withOffset(CGVector(dx: 0, dy: -160)))
            }
            field.tap()
            if (field.value(forKey: "hasKeyboardFocus") as? Bool) == true { break }
            Thread.sleep(forTimeInterval: 0.4)
        }
        field.typeText(text)
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) throws {
        let screenshot = app.screenshot()
        let attachment = XCTAttachment(screenshot: screenshot)
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let style = ProcessInfo.processInfo.environment["PERSONA_STYLE"] ?? "light"
            try screenshot.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name)-\(style).png"))
        }
    }
}
