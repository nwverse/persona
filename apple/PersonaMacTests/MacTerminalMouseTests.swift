import AppKit
import Testing
@testable import Persona

/// The pointer on a terminal the Mac drives (docs/integrations/terminal.md「Mouse: click, drag, buttons and modifiers」): the same routing, buttons and bits as the
/// web's `lib/terminal-mouse.ts`.
struct MacTerminalMouseTests {
    @Test func buttonsAreHerdrs() {
        #expect(MacTerminalMouse.button(0) == .left)
        #expect(MacTerminalMouse.button(1) == .right)
        #expect(MacTerminalMouse.button(2) == .middle)
        #expect(MacTerminalMouse.button(3) == nil)
        #expect(MacTerminalMouse.button(type: .rightMouseDown, number: 0) == .right)
        #expect(MacTerminalMouse.button(type: .leftMouseDragged, number: 1) == .left)
        #expect(MacTerminalMouse.button(type: .otherMouseUp, number: 2) == .middle)
    }

    /// Shift flips the route in both modes; ⌘ always stays here (⌘-click opens a link).
    @Test func shiftAndCommandStayHere() {
        #expect(MacTerminalMouse.toProgram(.program, flags: []))
        #expect(MacTerminalMouse.toProgram(.program, flags: [.control, .option]))
        #expect(!MacTerminalMouse.toProgram(.program, flags: .shift))
        #expect(!MacTerminalMouse.toProgram(.program, flags: .command))
        #expect(!MacTerminalMouse.toProgram(.local, flags: []))
        #expect(MacTerminalMouse.toProgram(.local, flags: .shift))
        #expect(!MacTerminalMouse.toProgram(.local, flags: [.shift, .command]))
    }

    /// ctrl 2, alt 4 (measured on herdr 0.9.5); Shift chose the route and is not sent, ⌘ has no bit.
    @Test func modifierBits() {
        #expect(MacTerminalMouse.modifiers([]) == 0)
        #expect(MacTerminalMouse.modifiers(.control) == 2)
        #expect(MacTerminalMouse.modifiers(.option) == 4)
        #expect(MacTerminalMouse.modifiers([.control, .option, .shift, .command]) == 6)
    }

    @Test func modeDefaultsToTheProgram() {
        #expect(MacTerminalMouse.mode(nil) == .program)
        #expect(MacTerminalMouse.mode("") == .program)
        #expect(MacTerminalMouse.mode("local") == .local)
    }

    /// A drag sends once per cell.
    @Test func oneEventPerCell() {
        var t = MacTerminalMouse.CellThrottle((3, 4))
        let steps = [(3, 4), (4, 4), (4, 4), (4, 5), (3, 4)].map { t.step($0) }
        #expect(steps == [false, true, false, true, true])
    }

    /// A press outside the screen is not the program's; a drag that left it points at the nearest edge cell.
    @Test func cellsOutsideTheScreen() {
        #expect(MacTerminalMouse.cell(col: 5, row: 2, cols: 80, rows: 24, clamp: false)! == (5, 2))
        #expect(MacTerminalMouse.cell(col: 80, row: 2, cols: 80, rows: 24, clamp: false) == nil)
        #expect(MacTerminalMouse.cell(col: 5, row: -1, cols: 80, rows: 24, clamp: false) == nil)
        #expect(MacTerminalMouse.cell(col: 5, row: 24, cols: 80, rows: 24, clamp: false) == nil)
        #expect(MacTerminalMouse.cell(col: -3, row: 30, cols: 80, rows: 24, clamp: true)! == (0, 23))
        #expect(MacTerminalMouse.cell(col: 200, row: -5, cols: 80, rows: 24, clamp: true)! == (79, 0))
        #expect(MacTerminalMouse.cell(col: 9000, row: 9000, cols: 9000, rows: 9000, clamp: true)! == (4095, 4095))
    }
}

/// The terminal view itself, fed AppKit events: what reaches the program and what stays a selection.
@MainActor struct MacTerminalMouseViewTests {
    private let size: CGFloat = 13

    private func view() -> (TerminalScrollView, NSWindow) {
        let v = TerminalScrollView(frame: NSRect(x: 0, y: 0, width: 800, height: 400))
        let window = NSWindow(contentRect: v.frame, styleMask: [.titled], backing: .buffered, defer: false)
        window.contentView = v
        let row: TermRow = Array(repeating: TermCell(text: "a", width: 1, style: TermStyle()), count: 40)
        v.update(content: TerminalContent(rows: Array(repeating: row, count: 10), cols: 40), wrap: false, fontSize: size,
                 font: .jetbrains, theme: .ink, dark: true)
        window.layoutIfNeeded()
        return (v, window)
    }

    /// An event over the middle of a cell of the screen (0-based), as AppKit would send it.
    private func event(_ type: NSEvent.EventType, _ col: CGFloat, _ row: CGFloat, in v: TerminalScrollView, window: NSWindow,
                       flags: NSEvent.ModifierFlags = []) -> NSEvent {
        let cell = CellMetrics(size: size).cell
        let p = v.screen.convert(NSPoint(x: 10 + (col + 0.5) * cell.width, y: 8 + (row + 0.5) * cell.height), to: nil)
        return NSEvent.mouseEvent(with: type, location: p, modifierFlags: flags, timestamp: 0, windowNumber: window.windowNumber,
                                  context: nil, eventNumber: 0, clickCount: 1, pressure: 1)!
    }

    @Test func pressDragReleaseGoToTheProgram() {
        let (v, w) = view()
        var got: [MacTerminalMouse.Event] = []
        v.setMouse({ got.append($0) }, mode: .program)
        v.screen.mouseDown(with: event(.leftMouseDown, 2, 1, in: v, window: w, flags: .control))
        v.screen.mouseDragged(with: event(.leftMouseDragged, 2, 1, in: v, window: w)) // same cell: nothing
        v.screen.mouseDragged(with: event(.leftMouseDragged, 5, 3, in: v, window: w))
        v.screen.mouseDragged(with: event(.leftMouseDragged, 99, 3, in: v, window: w)) // past the right edge: clamped
        v.screen.mouseUp(with: event(.leftMouseUp, 99, 3, in: v, window: w))
        #expect(got == [
            .init(action: .down, button: .left, column: 2, row: 1, mods: 2),
            .init(action: .drag, button: .left, column: 5, row: 3, mods: 0),
            .init(action: .drag, button: .left, column: 39, row: 3, mods: 0),
            .init(action: .up, button: .left, column: 39, row: 3, mods: 0),
        ])
        #expect(!v.hasSelection)

        got = []
        v.screen.rightMouseDown(with: event(.rightMouseDown, 7, 0, in: v, window: w, flags: .option))
        v.screen.rightMouseUp(with: event(.rightMouseUp, 7, 0, in: v, window: w))
        #expect(got == [.init(action: .down, button: .right, column: 7, row: 0, mods: 4),
                        .init(action: .up, button: .right, column: 7, row: 0, mods: 0)])
    }

    /// Shift keeps the pointer here: a drag selects and nothing goes out. Select Locally is the other way round.
    @Test func shiftSelectsHere() {
        let (v, w) = view()
        var got: [MacTerminalMouse.Event] = []
        v.setMouse({ got.append($0) }, mode: .program)
        v.screen.mouseDown(with: event(.leftMouseDown, 1, 1, in: v, window: w, flags: .shift))
        v.screen.mouseDragged(with: event(.leftMouseDragged, 6, 1, in: v, window: w, flags: .shift))
        v.screen.mouseUp(with: event(.leftMouseUp, 6, 1, in: v, window: w, flags: .shift))
        #expect(got.isEmpty)
        #expect(v.hasSelection)

        v.setMouse({ got.append($0) }, mode: .local)
        v.screen.mouseDown(with: event(.leftMouseDown, 1, 2, in: v, window: w))
        v.screen.mouseUp(with: event(.leftMouseUp, 1, 2, in: v, window: w))
        #expect(got.isEmpty)
        v.screen.mouseDown(with: event(.leftMouseDown, 1, 2, in: v, window: w, flags: .shift))
        v.screen.mouseUp(with: event(.leftMouseUp, 1, 2, in: v, window: w, flags: .shift))
        #expect(got.map(\.action) == [.down, .up])
        #expect(got.first?.mods == 0) // Shift chose the route: not sent
    }

    /// Not forwarding (watching, an older hub): a click is the older click at the cell, a drag selects.
    @Test func withoutForwardingClicksAsBefore() {
        let (v, w) = view()
        var clicks: [[Int]] = []
        v.onClick = { clicks.append([$0, $1]) }
        v.screen.mouseDown(with: event(.leftMouseDown, 3, 2, in: v, window: w))
        v.screen.mouseUp(with: event(.leftMouseUp, 3, 2, in: v, window: w))
        #expect(clicks == [[3, 2]])
    }
}
