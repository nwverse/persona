import AppKit
import Testing
@testable import Persona

/// A secondary click opens the message action bar where it happened (#501): the recognizer has to report the
/// click's spot, which SwiftUI's coordinate converter reads through `location(in:)`.
@MainActor
struct MacSecondaryClickTests {
    private final class ActionRecorder: NSObject {
        var states: [NSGestureRecognizer.State] = []

        @objc func record(_ recognizer: NSGestureRecognizer) {
            states.append(recognizer.state)
        }
    }

    private func setUp() -> (NSWindow, NSView, SecondaryClickRecognizer, ActionRecorder) {
        let window = NSWindow(contentRect: NSRect(x: 0, y: 0, width: 400, height: 300), styleMask: [.titled],
                              backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false
        let view = NSView(frame: NSRect(x: 50, y: 40, width: 200, height: 150))
        window.contentView?.addSubview(view)
        // SwiftUI installs a target/action on the app's recognizer; keep that same contract in this fixture.
        let recorder = ActionRecorder()
        let recognizer = SecondaryClickRecognizer(target: recorder, action: #selector(ActionRecorder.record(_:)))
        view.addGestureRecognizer(recognizer)
        return (window, view, recognizer, recorder)
    }

    private func event(_ type: NSEvent.EventType, at point: NSPoint, in window: NSWindow,
                       flags: NSEvent.ModifierFlags = []) throws -> NSEvent {
        try #require(NSEvent.mouseEvent(with: type, location: point, modifierFlags: flags, timestamp: 0,
                                        windowNumber: window.windowNumber, context: nil, eventNumber: 0,
                                        clickCount: 1, pressure: 1))
    }

    @Test func reportsWhereTheRightClickHappened() throws {
        let (window, view, recognizer, recorder) = setUp()
        defer { window.close() }
        let down = try event(.rightMouseDown, at: NSPoint(x: 120, y: 90), in: window)
        let up = try event(.rightMouseUp, at: NSPoint(x: 120, y: 90), in: window)
        // AppKit must deliver the event and recognize its state change, not just run the callback directly.
        withExtendedLifetime(recorder) {
            // A complete click keeps AppKit's shared gesture tracking from capturing later windows' mouse events.
            defer { window.sendEvent(up) }
            window.sendEvent(down)
            #expect(recognizer.state == .began)
            #expect(recognizer.location(in: nil) == NSPoint(x: 120, y: 90))
            #expect(recognizer.location(in: view) == NSPoint(x: 70, y: 50))
            #expect(recorder.states == [.began])
        }
        #expect(recorder.states == [.began, .ended])
    }

    @Test func completeClicksDoNotCaptureLaterWindows() throws {
        // AppKit's gesture tracking is shared across windows: an unmatched down captures subsequent clicks.
        for _ in 0..<3 { try reportsWhereTheRightClickHappened() }
    }
}
