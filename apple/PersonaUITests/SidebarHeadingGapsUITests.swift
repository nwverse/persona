import XCTest

/// The Sidebar layout's three section headings (#10551): Pages, Persona Chats and Agents are as tall and as far apart as
/// each other, folded and unfolded, in the iPad's and the open iPhone Duo's column and the iPhone's drawer. The Agents
/// heading carries its ⋯, Personal Agents and + and sits in the Agents list's own section; it once stood taller and
/// further down than the other two. Against scripts/mock/mock_hub.py (PERSONA_MOCK_HUB), on any simulator.
/// PERSONA_SHOTS=<dir> keeps the screens (PERSONA_SHOTS_DEVICE names the device, PERSONA_SHOTS_APPEARANCE the look;
/// PERSONA_SHOTS_EXTERNAL=1 asks `shotwatch.sh` for a `simctl` screenshot, which the iPhone Duo needs).
final class SidebarHeadingGapsUITests: XCTestCase {
    private var app: XCUIApplication!
    private var hub = ""
    /// How far two measures may differ: half a point, under one pixel on every screen.
    private let tolerance: CGFloat = 0.5

    override func setUpWithError() throws {
        continueAfterFailure = false
        guard let hub = ProcessInfo.processInfo.environment["PERSONA_MOCK_HUB"] else {
            throw XCTSkip("needs scripts/mock/mock_hub.py (PERSONA_MOCK_HUB)")
        }
        self.hub = hub
    }

    /// Both Persona sections folded: the three headings follow each other, one heading's height apart.
    @MainActor func testTheHeadingsAreEvenlySpacedFolded() {
        launch(folded: true)
        let (places, chats, agents) = headings()
        reach(agents, below: 0, "the Agents heading")
        let a = places.frame, b = chats.frame, c = agents.frame
        report("folded", [("places", a), ("chats", b), ("agents", c)])
        XCTAssertEqual(a.height, b.height, accuracy: tolerance, "Pages and Persona Chats are not as tall")
        XCTAssertEqual(b.height, c.height, accuracy: tolerance, "Persona Chats and Agents are not as tall")
        XCTAssertEqual(b.minY - a.minY, c.minY - b.minY, accuracy: tolerance,
                       "Pages → Persona Chats is \(b.minY - a.minY) pt, Persona Chats → Agents \(c.minY - b.minY) pt")
        XCTAssertEqual(b.minY - a.maxY, c.minY - b.maxY, accuracy: tolerance, "the gaps between the headings differ")
        shot("folded")
    }

    /// Both unfolded: each heading stands as far below the row before it, and its first row as far below it.
    @MainActor func testTheHeadingsAreEvenlySpacedUnfolded() {
        launch(folded: false)
        let (places, chats, agents) = headings()
        // Pages' first row, then Persona Chats' last place above its heading and first chat below it.
        let pagesBelow = below(places)
        reach(chats, below: 60, "the Persona Chats heading")
        let chatsAbove = above(chats), chatsBelow = below(chats)
        let chatsHeight = chats.frame.height
        shot("unfolded-chats")
        // Agents: the last of Persona's chats above it, the filters below it.
        reach(agents, below: 60, "the Agents heading")
        let agentsAbove = above(agents), agentsBelow = below(agents)
        print("SIDEBAR-HEADING-GAPS unfolded above: chats \(chatsAbove) agents \(agentsAbove); below: places \(pagesBelow) "
              + "chats \(chatsBelow) agents \(agentsBelow); heights: places \(places.frame.height) chats \(chatsHeight) "
              + "agents \(agents.frame.height)")
        XCTAssertEqual(chatsHeight, agents.frame.height, accuracy: tolerance, "Persona Chats and Agents are not as tall")
        XCTAssertEqual(chatsAbove, agentsAbove, accuracy: tolerance,
                       "above Persona Chats \(chatsAbove) pt, above Agents \(agentsAbove) pt")
        XCTAssertEqual(pagesBelow, chatsBelow, accuracy: tolerance, "below Pages \(pagesBelow) pt, below Persona Chats \(chatsBelow) pt")
        XCTAssertEqual(chatsBelow, agentsBelow, accuracy: tolerance, "below Persona Chats \(chatsBelow) pt, below Agents \(agentsBelow) pt")
        shot("unfolded-agents")
    }

    // MARK: Helpers

    /// The Sidebar layout pinned, Persona's two sections folded or not (their remembered values), on the conversation.
    @MainActor private func launch(folded: Bool) {
        app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        let fold = folded ? "YES" : "NO"
        app.launchArguments = ["-pairURL", pair, "-dev.openChat", "", "-tab", "assistant", "-app.navigation", "sidebar",
                               "-sidebar.fold.places", fold, "-sidebar.fold.chats", fold]
        app.launchPinned(navigation: true)
        // The iPad's and the open Duo's column is out; a phone's drawer opens from the persona's face.
        let places = element("sidebar-fold-places"), face = element("assistant-sidebar-button")
        let shown = NSPredicate { _, _ in (places.exists && places.isHittable) || face.exists }
        XCTAssertEqual(XCTWaiter().wait(for: [XCTNSPredicateExpectation(predicate: shown, object: NSNull())], timeout: 25), .completed,
                       "no sidebar and no face:\n\(app.debugDescription)")
        if !(places.exists && places.isHittable) {
            face.tap()
            XCTAssertTrue(places.waitForExistence(timeout: 10), "the drawer did not open:\n\(app.debugDescription)")
        }
        usleep(1_000_000) // the column's slide and the list's first layout
    }

    @MainActor private func headings() -> (XCUIElement, XCUIElement, XCUIElement) {
        let places = element("sidebar-fold-places"), chats = element("sidebar-fold-chats")
        // Before #10551 the Agents heading's words were its only element: measured too, so the test reports the old gaps.
        let named = element("sidebar-agents-heading")
        let agents = named.waitForExistence(timeout: 3) ? named : column.staticTexts["Agents"].firstMatch
        XCTAssertTrue(places.waitForExistence(timeout: 10) && chats.exists, "no Pages or Persona Chats heading:\n\(app.debugDescription)")
        return (places, chats, agents)
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// The list the headings are in: the column beside the page, or the phone's drawer.
    @MainActor private var column: XCUIElement {
        let side = element("unified-sidebar")
        return side.exists ? side : element("assistant-sidebar")
    }

    /// Scrolls the sidebar until `target` and `room` points under it are on screen (the next row's top with it).
    @MainActor private func reach(_ target: XCUIElement, below room: CGFloat, _ step: String) {
        let list = column
        for _ in 0..<12 {
            if target.exists, target.isHittable, target.frame.maxY + room < list.frame.maxY - 80 { break }
            list.swipeUp(velocity: .slow)
        }
        usleep(800_000) // the scroll's deceleration
        XCTAssertTrue(target.exists && target.isHittable, "\(step): not reached in the sidebar:\n\(app.debugDescription)")
    }

    /// The list's rows (cells) on screen in the sidebar, top to bottom.
    @MainActor private func cells() -> [CGRect] {
        column.cells.allElementsBoundByIndex.map(\.frame).filter { !$0.isEmpty }.sorted { $0.minY < $1.minY }
    }

    /// From the bottom of the row before `heading`'s to the heading's top.
    @MainActor private func above(_ heading: XCUIElement) -> CGFloat {
        let h = heading.frame, rows = cells()
        guard let own = rows.first(where: { $0.contains(CGPoint(x: h.midX, y: h.midY)) }),
              let previous = rows.last(where: { $0.maxY <= own.minY + tolerance && $0.height > 1 }) else {
            XCTFail("no row above \(heading.identifier): \(rows)")
            return .nan
        }
        return h.minY - previous.maxY
    }

    /// From `heading`'s bottom to the top of the row after its own.
    @MainActor private func below(_ heading: XCUIElement) -> CGFloat {
        let h = heading.frame, rows = cells()
        guard let own = rows.first(where: { $0.contains(CGPoint(x: h.midX, y: h.midY)) }),
              let next = rows.first(where: { $0.minY >= own.maxY - tolerance && $0.height > 1 }) else {
            XCTFail("no row below \(heading.identifier): \(rows)")
            return .nan
        }
        return next.minY - h.maxY
    }

    private func report(_ state: String, _ frames: [(String, CGRect)]) {
        let parts = frames.map { "\($0.0) y \($0.1.minY) h \($0.1.height)" }
        print("SIDEBAR-HEADING-GAPS \(state): " + parts.joined(separator: ", "))
    }

    @MainActor private func shot(_ name: String) {
        let env = ProcessInfo.processInfo.environment
        guard let dir = env["PERSONA_SHOTS"] else { return }
        let device = env["PERSONA_SHOTS_DEVICE"] ?? (UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "iphone")
        let appearance = env["PERSONA_SHOTS_APPEARANCE"] ?? "light"
        let file = "headings-\(device)-\(appearance)-\(name).png"
        let folder = URL(fileURLWithPath: dir)
        sleep(1)
        guard env["PERSONA_SHOTS_EXTERNAL"] != nil else {
            try? app.screenshot().pngRepresentation.write(to: folder.appendingPathComponent(file))
            return
        }
        try? file.write(to: folder.appendingPathComponent("want"), atomically: true, encoding: .utf8)
        let target = folder.appendingPathComponent(file)
        let deadline = Date.now.addingTimeInterval(15)
        while Date.now < deadline, !FileManager.default.fileExists(atPath: target.path) { Thread.sleep(forTimeInterval: 0.2) }
    }
}
