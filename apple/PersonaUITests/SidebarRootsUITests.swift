import XCTest

/// The Sidebar layout's page (#8359, docs/design/sidebar-navigation.md option 3): every sidebar item is a root of its own.
/// A pick replaces the page with no push and no Back; only a drill-down inside a page pushes, with Back; each root keeps
/// its drill-down while the user is elsewhere; the root in front picked again goes back to it; at a root an edge swipe
/// reaches no other item. Read from the app's own probe (`probe.sidebar-page`: the root, the path, and how many animated
/// navigation transitions UIKit has run). Demo data in the Merged layout: an iPad, the open iPhone Duo (upright, where the
/// column lies over the page, and on its side) and an iPhone (the drawer).
final class SidebarRootsUITests: XCTestCase {
    private var app: XCUIApplication!

    private struct Page: Decodable, CustomStringConvertible {
        var root: String
        var path: Int
        var transitions: Int
        var depth: Int
        var description: String { "root \(root), path \(path), transitions \(transitions), depth \(depth)" }
    }

    override func setUpWithError() throws {
        continueAfterFailure = false
    }

    @MainActor private func launch() {
        app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-newAssistant", "YES", "-tab", "assistant", "-dev.openChat", "",
                               "-app.navigation", "sidebar", "-probes", "YES"]
        app.launchPinned(navigation: false)
    }

    @MainActor private func element(_ id: String) -> XCUIElement { app.descendants(matching: .any)[id].firstMatch }

    /// The sidebar's scrolling column: beside the page, or a phone's drawer (whose host names it as the Separate layout's).
    @MainActor private var column: XCUIElement {
        let split = element("unified-sidebar")
        return split.exists ? split : element("assistant-sidebar")
    }

    @MainActor private func page() -> Page? { app.probe("sidebar-page", as: Page.self) }

    /// The page once `root` is in front and nothing has moved for a moment (a push, had there been one, has run).
    @MainActor private func settled(_ root: String, _ step: String) -> Page {
        var last: Page?
        var stableSince = Date.now
        let deadline = Date.now.addingTimeInterval(20) // a page with live charts (Machines) slows every query
        while Date.now < deadline {
            let now = page()
            if now?.root != last?.root || now?.path != last?.path || now?.transitions != last?.transitions { stableSince = .now }
            last = now
            if now?.root == root, Date.now.timeIntervalSince(stableSince) > 1.0 { return now! }
            Thread.sleep(forTimeInterval: 0.2)
        }
        XCTFail("\(step): the page did not settle on \(root): \(last.map(\.description) ?? "no probe")\n\(app.debugDescription)")
        return last ?? Page(root: "", path: 0, transitions: 0, depth: 0)
    }

    /// UIKit's own back button on the page in front: in the navigation bar, or in the system's side bars (the open Duo on
    /// its side).
    @MainActor private var systemBack: Bool {
        app.buttons.matching(identifier: "BackButton").allElementsBoundByIndex.contains { $0.exists && $0.isHittable }
    }

    /// The sidebar can be tapped: beside the page it always is; laid over the page (the open Duo upright) or in a phone's
    /// drawer it is brought out first.
    @MainActor private func reveal(_ target: XCUIElement) {
        if target.exists && target.isHittable { return }
        let systemToggle = app.buttons.matching(NSPredicate(format: "identifier == 'ToggleSidebar' OR label IN %@",
                                                            ["Show Sidebar", "显示边栏", "显示侧边栏"])).firstMatch
        for opener in [element("assistant-sidebar-button"), element("sidebar-drawer-button"), element("chevron.left"), systemToggle]
        where opener.exists && opener.isHittable {
            opener.tap()
            if target.waitForExistence(timeout: 3), target.isHittable { return }
        }
        // A phone's drawer from anywhere on a root: a drag to the right.
        let window = app.windows.firstMatch
        window.coordinate(withNormalizedOffset: CGVector(dx: 0.3, dy: 0.5))
            .press(forDuration: 0.05, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: 0.95, dy: 0.5)))
        XCTAssertTrue(target.waitForExistence(timeout: 3) && target.isHittable,
                      "the sidebar could not be brought out; the bar's buttons: \(app.navigationBars.buttons.debugDescription)\n\(app.debugDescription)")
    }

    /// Picks a sidebar row by its identifier, scrolling the column to it (and unfolding Pages if it is folded).
    @MainActor private func pick(_ id: String, _ step: String) {
        let target = element(id)
        let main = element("sidebar-main")
        reveal(main)
        if !target.exists, element("sidebar-fold-places").exists, id.hasPrefix("sidebar-") {
            element("sidebar-fold-places").tap()
        }
        // Above or below where the column was left scrolled.
        for _ in 0..<6 where !(target.exists && target.isHittable) { column.swipeDown(velocity: .fast) }
        for _ in 0..<8 where !(target.exists && target.isHittable) { column.swipeUp(velocity: .slow) }
        XCTAssertTrue(target.exists && target.isHittable, "\(step): \(id) not reachable in the sidebar:\n\(app.debugDescription)")
        target.tap()
    }

    @MainActor private func assertSwitched(_ before: Page, _ after: Page, _ step: String) {
        XCTAssertEqual(after.transitions, before.transitions, "\(step): the page was pushed or popped (\(before) → \(after))")
        XCTAssertFalse(systemBack, "\(step): a Back on a root:\n\(app.debugDescription)")
    }

    @MainActor private func shot(_ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        let look = ProcessInfo.processInfo.environment["PERSONA_SHOTS_APPEARANCE"] ?? "system"
        let device = UIDevice.current.userInterfaceIdiom == .pad ? "ipad" : "phone"
        try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appendingPathComponent("sidebar-roots-\(device)-\(look)-\(name).png"))
    }

    /// From the main conversation, Recordings and an Agent replace the root (no push, no Back); Machines → a machine pushes
    /// with Back; the conversation and back to Machines brings the machine's page back with no push; Machines again goes
    /// back to its root; an edge swipe at a root stays on it.
    @MainActor func testPicksReplaceTheRootAndOnlyDrillDownsPush() {
        launch()
        var now = settled("conversation", "launch")
        shot("0-conversation")

        pick("sidebar-meeting-recording", "Recordings")
        var next = settled("recordings", "Recordings from the conversation")
        XCTAssertEqual(next.path, 0, "Recordings is not the root")
        assertSwitched(now, next, "Recordings from the conversation")
        shot("1-recordings")
        now = next

        let row = app.buttons.matching(identifier: "agents.row").firstMatch
        reveal(element("sidebar-main"))
        for _ in 0..<10 where !(row.exists && row.isHittable) { column.swipeUp(velocity: .slow) }
        XCTAssertTrue(row.exists && row.isHittable, "no Agent's row:\n\(app.debugDescription)")
        row.tap()
        next = settled("agent", "an Agent from Recordings")
        XCTAssertEqual(next.path, 0, "the Agent's chat is not the root")
        assertSwitched(now, next, "an Agent")
        shot("2-agent")

        pick("sidebar-main", "the conversation")
        now = settled("conversation", "the conversation from the Agent")
        pick("sidebar-meeting-recording", "Recordings again")
        next = settled("recordings", "Recordings from the conversation")
        assertSwitched(now, next, "Recordings again from the conversation")
        pick("sidebar-main", "the conversation")
        now = settled("conversation", "the conversation from Recordings")
        reveal(element("sidebar-main"))
        for _ in 0..<10 where !(row.exists && row.isHittable) { column.swipeUp(velocity: .slow) }
        row.tap()
        next = settled("agent", "an Agent from the conversation")
        assertSwitched(now, next, "an Agent from the conversation")

        // A drill-down pushes, with Back.
        pick("sidebar-machines", "Machines")
        now = settled("machines", "Machines")
        // The machine's card on the page, not a row in the column that names the same computer.
        let cards = app.buttons.matching(NSPredicate(format: "label BEGINSWITH 'MacBook'"))
        _ = cards.firstMatch.waitForExistence(timeout: 10)
        let pageStart = column.exists && column.isHittable ? column.frame.maxX : 0
        func onPage() -> XCUIElement? {
            cards.allElementsBoundByIndex.first { $0.exists && $0.isHittable && $0.frame.minX >= pageStart }
        }
        for _ in 0..<6 where onPage() == nil {
            app.windows.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.8, dy: 0.7))
                .press(forDuration: 0.05, thenDragTo: app.windows.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.8, dy: 0.4)))
        }
        guard let card = onPage() else { return XCTFail("no MacBook card on the page:\n\(app.debugDescription)") }
        card.tap()
        next = settled("machines", "a machine")
        XCTAssertEqual(next.path, 1, "the machine's page was not pushed: \(next)")
        XCTAssertEqual(next.transitions, now.transitions + 1, "the drill-down did not push: \(now) → \(next)")
        XCTAssertTrue(systemBack, "no Back on the machine's page:\n\(app.debugDescription)")
        shot("3-machine")

        // A phone's drawer: a sub-page has only Back, the drawer opens from a root (docs/design/sidebar-navigation.md,
        // "One button on each side"). The rest needs the column beside or over the page.
        if app.windows.firstMatch.frame.width < 600 { return }

        // Machines keeps its drill-down while the conversation is in front.
        pick("sidebar-main", "the conversation")
        now = settled("conversation", "the conversation from the machine")
        XCTAssertEqual(now.path, 0)
        assertSwitched(next, now, "the conversation from a drilled Machines")
        pick("sidebar-machines", "Machines again")
        next = settled("machines", "Machines again")
        XCTAssertEqual(next.path, 1, "Machines did not come back at the machine's page: \(next)")
        XCTAssertEqual(next.transitions, now.transitions, "coming back to Machines pushed: \(now) → \(next)")
        XCTAssertTrue(systemBack, "the machine's page came back without its Back")

        // Machines in front picked again: back to its root.
        pick("sidebar-machines", "Machines in front")
        next = settled("machines", "Machines in front again")
        XCTAssertEqual(next.path, 0, "picking Machines in front did not go to its root: \(next)")

        // An edge swipe at a root reaches no other item.
        let window = app.windows.firstMatch
        let edge = UIDevice.current.userInterfaceIdiom == .pad || element("sidebar-main").isHittable
            ? (column.frame.maxX + 2) / window.frame.width : 0.005
        window.coordinate(withNormalizedOffset: CGVector(dx: edge, dy: 0.5))
            .press(forDuration: 0.05, thenDragTo: window.coordinate(withNormalizedOffset: CGVector(dx: min(edge + 0.5, 0.95), dy: 0.5)))
        let afterSwipe = settled("machines", "an edge swipe at Machines' root")
        XCTAssertEqual(afterSwipe.path, 0)
    }

    /// Where the column's head and its list start: the assistant's name in the head, the fold button, the main
    /// conversation held over the list, and the first heading (Pages), once the column is at its top.
    @MainActor private func head(_ step: String) -> [String: CGRect] {
        let main = element("sidebar-main"), fold = element("sidebar-fold-column"), pages = element("sidebar-fold-places")
        reveal(main)
        for _ in 0..<4 where !(pages.exists && pages.isHittable) { column.swipeDown(velocity: .fast) }
        // A list let go after a swipe settles a moment later.
        Thread.sleep(forTimeInterval: 1.0)
        // The head's title is the assistant's name, which the main conversation's row starts with.
        let name = main.label.components(separatedBy: CharacterSet(charactersIn: ",，")).first ?? ""
        let title = app.staticTexts.matching(NSPredicate(format: "label == %@", name)).allElementsBoundByIndex
            .first { $0.exists && $0.frame.maxY <= main.frame.minY && $0.frame.minX < main.frame.maxX }
        XCTAssertTrue(title != nil && pages.exists, "\(step): no head title (\(name)) or Pages heading:\n\(app.debugDescription)")
        return ["the head's title": title?.frame ?? .zero, "the fold button": fold.exists ? fold.frame : .zero,
                "the main conversation": main.frame, "the Pages heading": pages.frame]
    }

    @MainActor private func assertSame(_ a: CGRect, _ b: CGRect, _ what: String, _ step: String) {
        let off = max(abs(a.minX - b.minX), abs(a.minY - b.minY), abs(a.width - b.width), abs(a.height - b.height))
        XCTAssertLessThanOrEqual(off, 1, "\(step): \(what) moved: \(a) on the main conversation, \(b) here")
    }

    /// The column's head and where its list starts do not depend on the root beside it (#8359): the persona's face, the
    /// fold button and the Pages heading are where they are on the main conversation, on Recordings, Inbox and an
    /// Agent's chat, within 1 pt.
    @MainActor func testTheSidebarHeadStaysPutOnEveryRoot() {
        continueAfterFailure = true // every root's offsets, not only the first
        launch()
        _ = settled("conversation", "launch")
        let main = head("the main conversation")
        shot("head-0-conversation")
        func check(_ root: String) {
            let here = head(root)
            shot("head-\(root)")
            for (what, frame) in main.sorted(by: { $0.key < $1.key }) { assertSame(frame, here[what] ?? .zero, what, root) }
        }
        for (id, root) in [("sidebar-meeting-recording", "recordings"), ("sidebar-inbox", "inbox")] {
            pick(id, root)
            _ = settled(root, root)
            check(root)
        }
        let row = app.buttons.matching(identifier: "agents.row").firstMatch
        reveal(element("sidebar-main"))
        for _ in 0..<10 where !(row.exists && row.isHittable) { column.swipeUp(velocity: .slow) }
        XCTAssertTrue(row.exists && row.isHittable, "no Agent's row:\n\(app.debugDescription)")
        row.tap()
        _ = settled("agent", "an Agent")
        check("agent")
    }
}
