import AppKit
import Testing
@testable import Persona

/// A secondary click opens the message action bar where it happened (#501): the recognizer has to report the
/// click's spot, which SwiftUI's coordinate converter reads through `location(in:)`.
@MainActor
struct MacSecondaryClickTests {
    private func setUp() -> (NSWindow, NSView, SecondaryClickRecognizer) {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled],
                              backing: .buffered, defer: false)
        let view = NSView(frame: NSRect(x: 50, y: 40, width: 200, height: 150))
        window.contentView?.addSubview(view)
        let recognizer = SecondaryClickRecognizer()
        view.addGestureRecognizer(recognizer)
        return (window, view, recognizer)
    }

    private func event(_ type: NSEvent.EventType, at point: NSPoint, in window: NSWindow,
                       flags: NSEvent.ModifierFlags = []) throws -> NSEvent {
        try #require(NSEvent.mouseEvent(with: type, location: point, modifierFlags: flags, timestamp: 0,
                                        windowNumber: window.windowNumber, context: nil, eventNumber: 0,
                                        clickCount: 1, pressure: 1))
    }

    @Test func reportsWhereTheRightClickHappened() throws {
        let (window, view, recognizer) = setUp()
        // Through the window, as AppKit delivers it: a recognizer only changes state while it handles an event.
        window.sendEvent(try event(.rightMouseDown, at: NSPoint(x: 120, y: 90), in: window))
        #expect(recognizer.state == .began)
        #expect(recognizer.location(in: nil) == NSPoint(x: 120, y: 90))
        #expect(recognizer.location(in: view) == NSPoint(x: 70, y: 50))
    }
}
