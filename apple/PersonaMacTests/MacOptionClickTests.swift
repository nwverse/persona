import AppKit
import SwiftUI
import Testing
@testable import Persona

/// ⌥-click on a list row opens it in a window of its own (docs/apple/mac.md「Separate chat windows」): only an option-click reaches the
/// catcher; every other click goes to what is under it.
@MainActor struct MacOptionClickTests {
    private func mouse(_ type: NSEvent.EventType, _ flags: NSEvent.ModifierFlags, at point: NSPoint = NSPoint(x: 50, y: 20),
                       window: NSWindow? = nil) -> NSEvent {
        NSEvent.mouseEvent(with: type, location: point, modifierFlags: flags, timestamp: 0,
                           windowNumber: window?.windowNumber ?? 0, context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    @Test func onlyAPlainOptionClick() {
        #expect(MacOptionClickCatcher.catches(mouse(.leftMouseDown, .option)))
        #expect(MacOptionClickCatcher.catches(mouse(.leftMouseUp, [.option, .capsLock])))
        #expect(!MacOptionClickCatcher.catches(mouse(.leftMouseDown, [])))
        #expect(!MacOptionClickCatcher.catches(mouse(.leftMouseDown, [.option, .command])))
        #expect(!MacOptionClickCatcher.catches(mouse(.leftMouseDown, [.option, .shift])))
        #expect(!MacOptionClickCatcher.catches(mouse(.rightMouseDown, .option)))
        #expect(!MacOptionClickCatcher.catches(nil))
    }

    @Test func theLayerIsHitOnlyByAnOptionClick() {
        let view = MacOptionClickCatcher.Catcher(action: {})
        view.frame = NSRect(x: 0, y: 0, width: 100, height: 40)
        view.currentEvent = { self.mouse(.leftMouseDown, []) }
        #expect(view.hitTest(NSPoint(x: 10, y: 10)) == nil)
        view.currentEvent = { self.mouse(.leftMouseDown, .option) }
        #expect(view.hitTest(NSPoint(x: 10, y: 10)) === view)
        view.currentEvent = { nil } // hover, the cursor: never the layer's
        #expect(view.hitTest(NSPoint(x: 10, y: 10)) == nil)
    }

    /// Through a real window: a plain click presses the button under the layer, an option-click runs the layer's
    /// action and the button never sees it.
    @Test func clicksThroughAWindow() throws {
        final class Counts { var button = 0; var option = 0 }
        let counts = Counts()
        let root = Button("打开") { counts.button += 1 }
            .buttonStyle(.plain)
            .frame(width: 200, height: 60)
            .contentShape(Rectangle())
            .macOptionClick { counts.option += 1 }
        let host = NSHostingView(rootView: root)
        host.frame = NSRect(x: 0, y: 0, width: 200, height: 60)
        let window = NSWindow(contentRect: host.frame, styleMask: [.borderless], backing: .buffered, defer: false)
        window.isReleasedWhenClosed = false // closed here, released by Swift
        window.contentView = host
        window.orderFrontRegardless()
        defer { window.close() }
        host.layoutSubtreeIfNeeded()
        let catcher = try #require(Self.find(MacOptionClickCatcher.Catcher.self, in: host))

        var dispatching: NSEvent?
        catcher.currentEvent = { dispatching }
        func click(_ flags: NSEvent.ModifierFlags) {
            for type in [NSEvent.EventType.leftMouseDown, .leftMouseUp] {
                let e = mouse(type, flags, at: NSPoint(x: 100, y: 30), window: window)
                dispatching = e
                window.sendEvent(e)
            }
            dispatching = nil
        }
        click(.option)
        #expect(counts.option == 1)
        #expect(counts.button == 0)
        click([])
        #expect(counts.option == 1)
        #expect(counts.button == 1)
    }

    private static func find<T: NSView>(_ type: T.Type, in view: NSView) -> T? {
        if let v = view as? T { return v }
        for sub in view.subviews { if let v = find(type, in: sub) { return v } }
        return nil
    }
}
