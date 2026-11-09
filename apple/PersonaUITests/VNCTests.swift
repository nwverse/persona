import XCTest

/// The app's own VNC (#1343) against scripts/mock/mock_hub.py with MOCK_COMPUTER=ready MOCK_SCREEN=fake (its Screen
/// Sharing: ARD, me / mock-pass, ZRLE; it records the pointer, clicks, keys and the clipboard), MOCK_DEVICE_TOKEN
/// (TEST_RUNNER_PERSONA_SCREEN_HUB, TEST_RUNNER_PERSONA_FAKE_SCREEN=1): the small window (picture in picture) from VNC,
/// over the home screen, back to the whole page and down again; and on the page, the two modes, the clipboard both
/// ways, the keyboard and Help. TEST_RUNNER_PERSONA_VNC_PAUSE slows it down for a screen recording.
final class VNCTests: XCTestCase {
    private var hub: String? { ProcessInfo.processInfo.environment["PERSONA_SCREEN_HUB"] }
    private var pause: UInt32 { UInt32(ProcessInfo.processInfo.environment["PERSONA_VNC_PAUSE"] ?? "") ?? 0 }

    @MainActor private func launch(_ args: [String]) throws -> XCUIApplication {
        guard let hub, ProcessInfo.processInfo.environment["PERSONA_FAKE_SCREEN"] == "1" else {
            throw XCTSkip("needs the mock's own Screen Sharing: MOCK_SCREEN=fake (PERSONA_FAKE_SCREEN=1)")
        }
        continueAfterFailure = false
        try mock(hub, "/mock/screen-password?p=mock-pass&clear=1")
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=" + UITestToken.device
        app.launchArguments = ["-pairURL", pair] + args
        app.launchPinned()
        return app
    }

    /// The machine page's VNC, signed in once on the whole page (kept), then straight into the small window.
    @MainActor private func signedInMachinePage(_ more: [String] = []) throws -> (XCUIApplication, XCUIElement) {
        let app = try launch(["-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook", "-openScreenFresh", "YES"] + more)
        let vnc = app.buttons["computer-screen"].firstMatch
        XCTAssertTrue(vnc.waitForExistence(timeout: 30), "no VNC:\n\(app.debugDescription)")
        for _ in 0..<6 where !vnc.isHittable { app.swipeUp() }
        vnc.tap() // nothing kept: the whole page, to sign in
        let password = app.secureTextFields["vnc-password"].firstMatch
        XCTAssertTrue(password.waitForExistence(timeout: 30), "the sign-in:\n\(app.debugDescription)")
        shot(app, "vnc-signin")
        password.tap()
        password.typeText("mock-pass")
        app.buttons["vnc-connect"].tap()
        XCTAssertTrue(live(app), "signed in")
        rest()
        return (app, vnc)
    }

    @MainActor func testTheSmallWindow() throws {
        let (app, vnc) = try signedInMachinePage()
        app.buttons["screen-close"].tap()
        XCTAssertTrue(vnc.waitForExistence(timeout: 10))
        rest()

        vnc.tap() // kept: the small window straight away
        let on = app.descendants(matching: .any)["vnc-pip-on"].firstMatch
        let page = app.descendants(matching: .any)["screen-drawer"].firstMatch
        let deadline = Date.now.addingTimeInterval(20)
        while Date.now < deadline, !on.exists, !page.exists { usleep(250_000) }
        if !on.exists, page.exists {
            // The simulators have no picture in picture (isPictureInPictureSupported is false): the whole page instead.
            XCTAssertTrue(live(app), "the page instead, live")
            throw XCTSkip("no picture in picture on this device: VNC opened the whole page instead")
        }
        XCTAssertTrue(on.exists, "the small window:\n\(app.debugDescription)")
        rest(3)
        shot(app, "vnc-pip-in-app")

        XCUIDevice.shared.press(.home) // to the home screen: it stays up
        rest(3)
        let springboard = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let pip = springboard.otherElements.matching(NSPredicate(format: "identifier CONTAINS[c] 'pictureinpicture' OR label CONTAINS[c] '画中画' OR label CONTAINS[c] 'Picture in Picture'")).firstMatch
        let screen = XCUIScreen.main.screenshot()
        let a = XCTAttachment(screenshot: screen)
        a.name = "vnc-pip-home"
        a.lifetime = .keepAlways
        add(a)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? screen.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("vnc-pip-home.png"))
        }
        if !pip.exists { print("SPRINGBOARD:\n\(springboard.debugDescription)") }
        XCTAssertTrue(pip.waitForExistence(timeout: 5), "the small window over the home screen:\n\(springboard.debugDescription)")

        // Its Restore: the whole page in the app.
        pip.tap()
        rest()
        let restore = springboard.buttons.matching(NSPredicate(format: "label CONTAINS[c] 'restore' OR label CONTAINS[c] '还原' OR identifier CONTAINS[c] 'restore'")).firstMatch
        if restore.waitForExistence(timeout: 3) { restore.tap() } else { pip.doubleTap() }
        XCTAssertTrue(app.wait(for: .runningForeground, timeout: 15), "back in the app")
        XCTAssertTrue(live(app), "the whole page, live:\n\(app.debugDescription)")
        rest(2)
        shot(app, "vnc-page")

        // Picture in Picture again from the page's …
        app.buttons["vnc-more"].tap()
        app.buttons["小窗"].firstMatch.tap()
        XCTAssertTrue(on.waitForExistence(timeout: 20), "the small window again")
        rest(3)
    }

    /// The small window comes up in the tap (#1839), Connecting… until the Mac's picture, then the picture; when the Mac
    /// hangs up, why. The simulators have no picture in picture: DEBUG -showPiPSource lays its source over the app.
    @MainActor func testTheSmallWindowConnectsInIt() throws {
        let (app, vnc) = try signedInMachinePage(["-showPiPSource", "YES"])
        let hub = try XCTUnwrap(self.hub)
        defer { try? mock(hub, "/mock/screen-password?slow=0&down=0") }
        app.buttons["screen-close"].tap()
        XCTAssertTrue(vnc.waitForExistence(timeout: 10))
        sleep(3) // the page's connection goes 2 s after it lets go (VNC.release): VNC makes a new one

        try mock(hub, "/mock/screen-password?slow=3") // the Mac answers late
        vnc.tap()
        let source = app.descendants(matching: .any)["vnc-pip-source"].firstMatch
        XCTAssertTrue(source.waitForExistence(timeout: 1.5), "the small window at once:\n\(app.debugDescription)")
        XCTAssertEqual(source.label, "小窗：连接中")
        XCTAssertFalse(app.descendants(matching: .any)["screen-drawer"].exists, "not the page")
        shot(app, "vnc-pip-card-connecting")
        let live = NSPredicate(format: "label == '小窗：实时画面'")
        XCTAssertEqual(XCTWaiter().wait(for: [expectation(for: live, evaluatedWith: source)], timeout: 15), .completed, source.label)
        rest(2)
        shot(app, "vnc-pip-card-live")

        try mock(hub, "/mock/screen-password?down=1") // the Mac hangs up
        let failed = NSPredicate(format: "label == '小窗：没连上：连接断了'")
        XCTAssertEqual(XCTWaiter().wait(for: [expectation(for: failed, evaluatedWith: source)], timeout: 15), .completed, source.label)
        rest(2)
        shot(app, "vnc-pip-card-failed")
    }

    /// On the page: the two modes (the mock's Screen Sharing says where it was clicked), the clipboard both ways, the
    /// keyboard, Help.
    @MainActor func testThePage() throws {
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        XCTAssertTrue(screen.waitForExistence(timeout: 10))

        // Mouse Mode: a tap clicks there.
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5)).tap()
        rest()
        var seen = try mockState()
        let clicks = seen["clicks"] as? [[String: Any]] ?? []
        XCTAssertTrue(clicks.contains { $0["down"] as? Bool == true }, "a click: \(clicks)")
        // A drag: down, moved, up.
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.4))
            .press(forDuration: 0.1, thenDragTo: screen.coordinate(withNormalizedOffset: CGVector(dx: 0.7, dy: 0.6)))
        rest()
        shot(app, "vnc-mouse")

        // Trackpad Mode, from …
        app.buttons["vnc-more"].tap()
        app.buttons["触控板模式"].firstMatch.tap()
        XCTAssertTrue(screen.value as? String ?? "" == "" || (screen.value as? String ?? "").contains("mode=trackpad"), "\(String(describing: screen.value))")
        screen.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.5))
            .press(forDuration: 0.05, thenDragTo: screen.coordinate(withNormalizedOffset: CGVector(dx: 0.6, dy: 0.55)))
        rest()
        app.buttons["vnc-more"].tap()
        app.buttons["指针居中"].firstMatch.tap()
        rest()
        seen = try mockState()
        let pointer = seen["pointer"] as? [Int] ?? []
        XCTAssertEqual(pointer.prefix(2), [240, 150], "指针居中: the middle of the Mac's 480×300")
        shot(app, "vnc-trackpad")
        app.buttons["vnc-more"].tap()
        app.buttons["鼠标模式"].firstMatch.tap()

        // Copy to iPhone: the Mac copies, the phone gets it; then Copy to Mac's Clipboard Only gives it back (the phone's own
        // clipboard, so no Allow Paste to answer), which shows the phone had it.
        try mock(hub!, "/mock/screen-password?clear=1&clip=" + "copied on the Mac".addingPercentEncoding(withAllowedCharacters: .alphanumerics)!)
        rest()
        app.buttons["vnc-clipboard"].tap()
        app.buttons["复制到 iPhone"].firstMatch.tap()
        XCTAssertTrue(app.staticTexts["已复制 MacBook 的剪贴板"].waitForExistence(timeout: 5), "the toast:\n\(app.debugDescription)")
        shot(app, "vnc-clipboard")
        app.buttons["vnc-clipboard"].tap()
        app.buttons["只放进 Mac 的剪贴板"].firstMatch.tap()
        allowPaste(app)
        rest()
        seen = try mockState()
        XCTAssertEqual(seen["clipboard"] as? String, "copied on the Mac", "back on the Mac:\n\(app.debugDescription)")

        // The keyboard: typing goes to the Mac; ⌘ from the row.
        try mock(hub!, "/mock/screen-password?clear=1")
        app.buttons["vnc-keyboard"].tap()
        XCTAssertTrue(app.keyboards.firstMatch.waitForExistence(timeout: 5), "the keyboard:\n\(app.debugDescription)")
        app.typeText("hello") // to what has the keyboard (the page's catcher: a tap on it could land on I'm Done)
        rest()
        XCTAssertTrue(app.buttons["vnc-key-cmd"].exists && app.buttons["vnc-key-esc"].exists, "the row of the Mac's keys")
        shot(app, "vnc-keyboard")
        seen = try mockState()
        XCTAssertEqual(seen["typed"] as? String, "hello")
        app.buttons["vnc-keyboard"].tap()

        // Picture in Picture Diagnostics (#1839): what this device can and what the small window did, step by step.
        app.buttons["vnc-more"].tap()
        app.buttons["小窗诊断"].firstMatch.tap()
        XCTAssertTrue(app.descendants(matching: .any)["vnc-pip-diagnostics"].firstMatch.waitForExistence(timeout: 5), "小窗诊断")
        XCTAssertTrue(app.staticTexts.matching(NSPredicate(format: "label CONTAINS %@", "点了 VNC")).firstMatch.exists,
                      "the steps:\n\(app.debugDescription)")
        rest()
        shot(app, "vnc-pip-diagnostics")
        app.buttons["关闭"].firstMatch.tap()
        XCTAssertTrue(app.buttons["vnc-help"].waitForExistence(timeout: 5))

        // Help.
        app.buttons["vnc-help"].tap()
        XCTAssertTrue(app.descendants(matching: .any)["vnc-help-page"].firstMatch.waitForExistence(timeout: 5))
        XCTAssertTrue(app.staticTexts["触控板模式"].exists, "\(app.debugDescription)")
        rest(2)
        shot(app, "vnc-help")
        app.descendants(matching: .any)["vnc-help-page"].firstMatch.swipeUp()
        XCTAssertTrue(app.staticTexts["我完成了"].waitForExistence(timeout: 5), "its last part:\n\(app.debugDescription)")
        rest()
        app.buttons["vnc-help-close"].tap()
        rest()
    }

    /// The top bar's round buttons share one shape: Help and More (⋯) as tall and as wide as Close.
    @MainActor func testTopBarButtonsKeepTheirShape() throws {
        let (app, _) = try signedInMachinePage()
        let close = app.buttons["screen-close"].firstMatch, help = app.buttons["vnc-help"].firstMatch, more = app.buttons["vnc-more"].firstMatch
        XCTAssertTrue(more.waitForExistence(timeout: 10), "\(app.debugDescription)")
        XCTAssertTrue(close.exists && help.exists, "\(app.debugDescription)")
        rest()
        shot(app, "vnc-top-bar")
        let c = close.frame
        for (name, b) in [("Help", help.frame), ("More", more.frame)] {
            XCTAssertEqual(b.height, c.height, accuracy: 1, "\(name) as tall as Close: \(b) vs \(c)")
            XCTAssertEqual(b.width, c.width, accuracy: 1, "\(name) as wide as Close: \(b) vs \(c)")
        }
    }

    /// One display of a Mac with two (#80), against the mock started with MOCK_SCREEN_DISPLAYS=2 (its Screen Sharing lists
    /// both, ExtendedDesktopSize) or MOCK_SCREEN_DISPLAYS=2:node (MacBook's status lists them, as a node reads them), and
    /// TEST_RUNNER_PERSONA_SCREEN_DISPLAYS=2: All Displays shows both side by side; Display 2 shows only the green one,
    /// a tap in its middle clicks the middle of the second display's pixels and Center Pointer centers on it; Display 1
    /// brings the pointer onto it, where Trackpad Mode clicks; the choice is kept for the Mac across a relaunch.
    @MainActor func testOneDisplay() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_SCREEN_DISPLAYS"] == "2" else {
            throw XCTSkip("needs the mock with two displays: MOCK_SCREEN_DISPLAYS=2 (TEST_RUNNER_PERSONA_SCREEN_DISPLAYS=2)")
        }
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        XCTAssertTrue(screen.waitForExistence(timeout: 10))
        app.buttons["vnc-more"].tap()
        app.buttons["鼠标模式"].firstMatch.tap() // a tap clicks where it lands
        choose(app, "全部显示器")
        rest(2)
        // All Displays: 960 × 300, display 1 (blue) on the left, display 2 (green) on the right; sampled on their
        // backgrounds, clear of the windows and the pointer in the middle.
        let both = pictureRect(screen.frame, CGSize(width: 960, height: 300))
        XCTAssertTrue(isBlue(color(app, both, 24, 150, of: CGSize(width: 960, height: 300))), "display 1 on the left")
        XCTAssertTrue(isGreen(color(app, both, 936, 150, of: CGSize(width: 960, height: 300))), "display 2 on the right")
        shot(app, "vnc-displays-all")

        app.buttons["vnc-more"].tap()
        app.buttons["显示器"].firstMatch.tap()
        XCTAssertTrue(app.buttons["显示器 2"].firstMatch.waitForExistence(timeout: 5), "the choice:\n\(app.debugDescription)")
        shot(app, "vnc-displays-menu")
        app.buttons["显示器 2"].firstMatch.tap()
        rest(2)
        try displayTwo(app, screen)
        shot(app, "vnc-displays-one")

        // Display 1 in Trackpad Mode: choosing it brings the pointer over from display 2, and a tap clicks where it is.
        choose(app, "显示器 1")
        rest()
        XCTAssertEqual((try mockState()["pointer"] as? [Int] ?? []).prefix(2), [240, 150], "the pointer onto display 1")
        app.buttons["vnc-more"].tap()
        app.buttons["触控板模式"].firstMatch.tap()
        try mock(hub!, "/mock/screen-password?clear=1")
        screen.tap()
        rest()
        let clicks = try mockState()["clicks"] as? [[String: Any]] ?? []
        XCTAssertEqual(clicks.first { $0["down"] as? Bool == true }?["x"] as? Int, 240, "Trackpad Mode clicks on display 1: \(clicks)")
        app.buttons["vnc-more"].tap()
        app.buttons["鼠标模式"].firstMatch.tap()
        choose(app, "显示器 2")
        rest()

        // Kept for this Mac: back on Display 2 after a relaunch (the sign-in is kept too).
        app.terminate()
        let again = try launch(["-tab", "settings", "-openMachines", "YES", "-openMachineDetail", "macbook"])
        let vnc = again.buttons["computer-screen"].firstMatch
        XCTAssertTrue(vnc.waitForExistence(timeout: 30))
        for _ in 0..<6 where !vnc.isHittable { again.swipeUp() }
        vnc.tap()
        XCTAssertTrue(live(again), "the page again, live")
        rest(2)
        try displayTwo(again, again.descendants(matching: .any)["vnc-screen"].firstMatch)
        choose(again, "全部显示器")
    }

    /// Display 2 alone: green across the picture (no blue from display 1), a tap in the middle clicks (720, 150), and
    /// Center Pointer puts the pointer there.
    @MainActor private func displayTwo(_ app: XCUIApplication, _ screen: XCUIElement) throws {
        let one = CGSize(width: 480, height: 300), r = pictureRect(screen.frame, one)
        // Below its window, from edge to edge.
        for x in [6.0, 240, 474] { XCTAssertTrue(isGreen(color(app, r, x, 280, of: one)), "display 2 at x \(x)") }
        try mock(hub!, "/mock/screen-password?clear=1")
        app.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: r.midX, dy: r.midY)).tap()
        rest()
        let clicks = try mockState()["clicks"] as? [[String: Any]] ?? []
        let down = clicks.first { $0["down"] as? Bool == true }
        XCTAssertEqual(Double(down?["x"] as? Int ?? -1), 720, accuracy: 3, "the middle of display 2: \(clicks)")
        XCTAssertEqual(Double(down?["y"] as? Int ?? -1), 150, accuracy: 3, "\(clicks)")
        app.buttons["vnc-more"].tap()
        app.buttons["指针居中"].firstMatch.tap()
        rest()
        XCTAssertEqual((try mockState()["pointer"] as? [Int] ?? []).prefix(2), [720, 150], "Center Pointer: the middle of display 2")
    }

    /// … › Displays › `label`.
    @MainActor private func choose(_ app: XCUIApplication, _ label: String) {
        app.buttons["vnc-more"].tap()
        app.buttons["显示器"].firstMatch.tap()
        let item = app.buttons[label].firstMatch
        XCTAssertTrue(item.waitForExistence(timeout: 5), "\(label):\n\(app.debugDescription)")
        item.tap()
    }

    /// Where a picture of this size is drawn in the screen's frame (aspect-fit, not zoomed).
    private func pictureRect(_ frame: CGRect, _ size: CGSize) -> CGRect {
        let k = min(frame.width / size.width, frame.height / size.height)
        let w = size.width * k, h = size.height * k
        return CGRect(x: frame.midX - w / 2, y: frame.midY - h / 2, width: w, height: h)
    }

    /// The screenshot's color at pixel (x, y) of a picture of this size drawn in `rect`.
    @MainActor private func color(_ app: XCUIApplication, _ rect: CGRect, _ x: CGFloat, _ y: CGFloat, of size: CGSize) -> (r: Int, g: Int, b: Int) {
        let image = app.screenshot().image
        guard let cg = image.cgImage else { return (0, 0, 0) }
        let scale = CGFloat(cg.width) / image.size.width
        let p = CGPoint(x: (rect.minX + x / size.width * rect.width) * scale, y: (rect.minY + y / size.height * rect.height) * scale)
        var px = [UInt8](repeating: 0, count: 4)
        let ctx = CGContext(data: &px, width: 1, height: 1, bitsPerComponent: 8, bytesPerRow: 4, space: CGColorSpaceCreateDeviceRGB(),
                            bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue)
        ctx?.draw(cg, in: CGRect(x: -p.x.rounded(.down), y: p.y.rounded(.down) - CGFloat(cg.height) + 1, width: CGFloat(cg.width), height: CGFloat(cg.height)))
        return (Int(px[0]), Int(px[1]), Int(px[2]))
    }

    /// The mock's display 1 is a blue gradient, display 2 a green one.
    private func isBlue(_ c: (r: Int, g: Int, b: Int)) -> Bool { c.b > c.g + 30 && c.b > c.r + 30 }
    private func isGreen(_ c: (r: Int, g: Int, b: Int)) -> Bool { c.g > c.b + 30 && c.g > c.r + 30 }

    /// The frame rate and latency the page measures (MOCK_SCREEN_ANIMATE=1: a box moving at ~30 frames a second, its
    /// frames stamped when sent): printed, for the record.
    @MainActor func testMeasure() throws {
        guard ProcessInfo.processInfo.environment["PERSONA_VNC_MEASURE"] == "1" else { throw XCTSkip("PERSONA_VNC_MEASURE=1 with MOCK_SCREEN_ANIMATE=1") }
        let (app, _) = try signedInMachinePage()
        let screen = app.descendants(matching: .any)["vnc-screen"].firstMatch
        var samples: [String] = []
        for _ in 0..<10 {
            sleep(1)
            samples.append(screen.value as? String ?? "")
        }
        print("VNC-MEASURE " + samples.joined(separator: " | "))
    }

    // MARK: Helpers

    @MainActor private func live(_ app: XCUIApplication) -> Bool {
        let phase = app.descendants(matching: .any)["screen-phase"].firstMatch
        let deadline = Date.now.addingTimeInterval(30)
        while Date.now < deadline { if phase.exists, phase.label.contains("已连接") { return true }; usleep(250_000) }
        return false
    }

    /// iOS asks before the app reads what another app (here the test runner) copied: Allow Paste.
    @MainActor private func allowPaste(_ app: XCUIApplication) {
        let allow = NSPredicate(format: "label IN %@", ["允许粘贴", "Allow Paste"])
        for owner in [app, XCUIApplication(bundleIdentifier: "com.apple.springboard")] {
            let b = owner.buttons.matching(allow).firstMatch
            if b.waitForExistence(timeout: 3) { b.tap(); return }
        }
    }

    private func rest(_ seconds: UInt32 = 1) { sleep(pause > 0 ? seconds * pause : (seconds > 1 ? 1 : 0)); usleep(300_000) }

    private func mock(_ hub: String, _ path: String) throws {
        let done = expectation(description: path)
        URLSession.shared.dataTask(with: URL(string: hub + path)!) { _, _, _ in done.fulfill() }.resume()
        wait(for: [done], timeout: 10)
    }

    private func mockState() throws -> [String: Any] {
        var out: [String: Any] = [:]
        let done = expectation(description: "state")
        URLSession.shared.dataTask(with: URL(string: hub! + "/mock/screen-password")!) { data, _, _ in
            out = (data.flatMap { try? JSONSerialization.jsonObject(with: $0) as? [String: Any] }) ?? [:]
            done.fulfill()
        }.resume()
        wait(for: [done], timeout: 10)
        return out
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let look = ProcessInfo.processInfo.environment["PERSONA_LOOK"].map { "-" + $0 } ?? ""
        let full = "\(name)-\(UITestStyle.name)\(look)"
        let png = app.screenshot()
        let attachment = XCTAttachment(screenshot: png)
        attachment.name = full
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            try? png.pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent(full + ".png"))
        }
    }
}
