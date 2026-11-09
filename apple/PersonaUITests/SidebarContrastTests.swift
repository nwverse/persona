import XCTest

/// The drawer's rows stay readable (#4496): on an iPhone in light mode the sidebar came up now and then with its
/// titles and rows white on its near-white backdrop, while the glass buttons and badges in it were fine. The sidebar
/// lives in a hosting controller of its own; its text has to follow the same look as the window it is drawn in.
/// Measured on the screenshot: in the Inbox row the text has to stand apart from the row's own background.
/// Run once per look (`simctl ui <udid> appearance light|dark`) and per style (`TEST_RUNNER_PERSONA_UI_STYLE`).
final class SidebarContrastTests: XCTestCase {
    private var app: XCUIApplication!

    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor private func launch(_ extra: [String] = []) {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-newAssistant", "YES"] + extra
        // Settings › Appearance › Light & Dark set against the system's (`-ui.scheme light` on a dark simulator).
        if let scheme = ProcessInfo.processInfo.environment["PERSONA_SCHEME"] { app.launchArguments += ["-ui.scheme", scheme] }
        app.launchPinned()
    }

    @MainActor private func openDrawer(_ why: String) {
        let toggle = app.buttons["assistant-sidebar-button"].firstMatch
        XCTAssertTrue(toggle.waitForExistence(timeout: 15), "no ☰ (\(why))")
        toggle.tapCenter()
        XCTAssertTrue(app.buttons["sidebar-inbox"].firstMatch.waitForExistence(timeout: 5), "the drawer did not open (\(why))")
        sleep(1) // the settle's spring
    }

    @MainActor private func closeDrawer() {
        app.descendants(matching: .any)["assistant-drawer-close"].firstMatch.tap()
        _ = app.buttons["sidebar-inbox"].firstMatch.waitForNonExistence(timeout: 5)
    }

    /// The spread of luminance (0…1) over the Inbox row's left part: its text on the backdrop is over 0.4 in every style (the
    /// BeautifulUI style's gray rows 0.44, the glass 0.9); white text on the light backdrop is a few percent.
    @MainActor private func rowContrast() -> Double {
        let row = app.buttons["sidebar-inbox"].firstMatch
        let shot = app.windows.firstMatch.screenshot()
        guard let image = shot.image.cgImage else { XCTFail("no screenshot"); return 0 }
        let windowFrame = app.windows.firstMatch.frame
        let scale = CGFloat(image.width) / windowFrame.width
        // The icon and the title only: the count badge on the right is red whatever the text's color.
        var f = row.frame.offsetBy(dx: -windowFrame.minX, dy: -windowFrame.minY)
        f.size.width *= 0.55
        let rect = CGRect(x: f.minX * scale, y: f.minY * scale, width: f.width * scale, height: f.height * scale).integral
            .intersection(CGRect(x: 0, y: 0, width: image.width, height: image.height))
        guard let crop = image.cropping(to: rect) else { XCTFail("no crop of \(f)"); return 0 }
        let w = crop.width, h = crop.height
        var pixels = [UInt8](repeating: 0, count: w * h * 4)
        guard let ctx = CGContext(data: &pixels, width: w, height: h, bitsPerComponent: 8, bytesPerRow: w * 4,
                                  space: CGColorSpaceCreateDeviceRGB(),
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return 0 }
        ctx.draw(crop, in: CGRect(x: 0, y: 0, width: w, height: h))
        var lo = 1.0, hi = 0.0
        for i in stride(from: 0, to: pixels.count, by: 4) {
            let l = (0.2126 * Double(pixels[i]) + 0.7152 * Double(pixels[i + 1]) + 0.0722 * Double(pixels[i + 2])) / 255
            lo = min(lo, l)
            hi = max(hi, l)
        }
        return hi - lo
    }

    @MainActor private func assertReadable(_ when: String) {
        let c = rowContrast()
        print("SIDEBAR-CONTRAST \(when): \(String(format: "%.3f", c))")
        if c < 0.25 {
            let shot = XCTAttachment(screenshot: app.screenshot())
            shot.name = "faded sidebar \(when)"
            shot.lifetime = .keepAlways
            add(shot)
        }
        XCTAssertGreaterThan(c, 0.25, "the drawer's 收件箱 row is faded \(when)")
    }

    /// Opened again and again, and with the app sent home and back (the system draws its switcher snapshots in the
    /// other look too, flipping the window's traits while the app is in the background).
    @MainActor func testRowsStayReadable() {
        launch()
        openDrawer("cold start")
        assertReadable("on the first open")
        if let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] {
            let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "look"
            try? app.screenshot().pngRepresentation
                .write(to: URL(fileURLWithPath: dir).appendingPathComponent("sidebar-\(UITestStyle.name)-\(look).png"))
        }
        for i in 1...4 {
            closeDrawer()
            openDrawer("trip \(i)")
            assertReadable("after closing and opening \(i)")
        }
        for i in 1...4 {
            XCUIDevice.shared.press(.home)
            sleep(2)
            app.activate()
            XCTAssertTrue(app.buttons["sidebar-inbox"].firstMatch.waitForExistence(timeout: 10), "the drawer after coming back")
            sleep(1)
            assertReadable("back from home with the drawer out \(i)")
        }
        for i in 1...4 {
            closeDrawer()
            XCUIDevice.shared.press(.home)
            sleep(2)
            app.activate()
            sleep(1)
            openDrawer("back from home \(i)")
            assertReadable("opened after coming back from home \(i)")
        }
    }

    /// The look changing while the app runs (the system's automatic light / dark at sunrise and sunset), with the
    /// drawer closed or out, the app in front or at home. `PERSONA_FLIP_LOOK=<udid>` names the simulator to flip:
    /// the test asks the host (`simctl ui … appearance`) through a file the runner script watches.
    @MainActor func testRowsFollowALookChange() throws {
        guard let flip = ProcessInfo.processInfo.environment["PERSONA_FLIP_LOOK"] else {
            throw XCTSkip("needs PERSONA_FLIP_LOOK=<a file the runner turns into simctl ui appearance>")
        }
        func look(_ l: String) {
            try? l.write(toFile: flip, atomically: true, encoding: .utf8)
            // The runner flips it and removes the file.
            for _ in 0..<30 where FileManager.default.fileExists(atPath: flip) { usleep(200_000) }
            sleep(2)
        }
        launch()
        let start = ProcessInfo.processInfo.environment["PERSONA_START_LOOK"] ?? "dark"
        let other = start == "dark" ? "light" : "dark"
        _ = app.buttons["assistant-sidebar-button"].firstMatch.waitForExistence(timeout: 15)
        sleep(2)
        // Closed while it flips, then opened.
        look(other)
        openDrawer("after the look flipped with the drawer closed")
        assertReadable("look flipped to \(other) while closed")
        // Out while it flips back.
        look(start)
        assertReadable("look flipped to \(start) while out")
        closeDrawer()
        // At home while it flips.
        XCUIDevice.shared.press(.home)
        sleep(1)
        look(other)
        app.activate()
        sleep(2)
        openDrawer("after the look flipped at home")
        assertReadable("look flipped to \(other) at home")
        closeDrawer()
        XCUIDevice.shared.press(.home)
        sleep(1)
        look(start)
        app.activate()
        sleep(2)
        openDrawer("after the look flipped back at home")
        assertReadable("look flipped to \(start) at home")
    }

    /// iOS draws the app-switcher snapshots in both looks as the app goes to the background: the window goes to the
    /// other look, lays out, and comes back (`-showLookBlink`, every 3 s here). The sidebar, closed or out, keeps its own.
    @MainActor func testRowsSurviveTheOtherLooksSnapshot() {
        launch(["-showLookBlink", "3"])
        openDrawer("cold start")
        for i in 1...4 {
            sleep(4)
            assertReadable("out through a snapshot in the other look \(i)")
        }
        for i in 1...4 {
            closeDrawer()
            sleep(4)
            openDrawer("after a snapshot in the other look while closed \(i)")
            assertReadable("opened after a snapshot in the other look \(i)")
        }
    }
}
