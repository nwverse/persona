import XCTest

extension XCUIApplication {
    /// Agents starts with its search drawer folded. Reveal it with the same pull a user makes before typing.
    @MainActor @discardableResult func revealAgentsSearch() -> XCUIElement {
        let field = searchFields.firstMatch
        if field.exists && field.isHittable { return field }
        let list = collectionViews.firstMatch
        XCTAssertTrue(list.waitForExistence(timeout: 15), "the Agents list did not open")
        list.swipeDown(velocity: .slow)
        let visible = NSPredicate(format: "exists == true AND hittable == true")
        XCTAssertEqual(XCTWaiter.wait(for: [XCTNSPredicateExpectation(predicate: visible, object: field)], timeout: 5), .completed,
                       "the pull did not reveal Agents search")
        return field
    }
}

extension XCUIApplication {
    /// Scrolls the list until `element` is clear of the home indicator, so a swipe or tap lands on it.
    /// A row not built yet (the cards that need you fill the first screen, and the lazy list builds only what is
    /// near it, #836) is scrolled to first.
    @MainActor func bringAboveTabBar(_ element: XCUIElement, file: StaticString = #filePath, line: UInt = #line) {
        let bottom = windows.firstMatch.frame.maxY - 40
        // The window, not the first collection view: in the shell that may be the other page's list, out of sight.
        let from = windows.firstMatch.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.6))
        for _ in 0..<12 where !element.exists || element.frame.maxY > bottom - 8 {
            // A short, slow drag from the middle of the list: far enough to lift a row, never past the top.
            from.press(forDuration: 0.05, thenDragTo: from.withOffset(CGVector(dx: 0, dy: -160)), withVelocity: .slow, thenHoldForDuration: 0.3)
        }
        XCTAssertTrue(element.exists, "\(element) is not in the list", file: file, line: line)
        XCTAssertLessThanOrEqual(element.frame.maxY, bottom - 8, "\(element) is still under the home indicator", file: file, line: line)
    }
}
