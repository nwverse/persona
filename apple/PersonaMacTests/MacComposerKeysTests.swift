import AppKit
import Testing
@testable import Persona

/// ⏎ and ⇧⏎ in a composer (docs/apple/mac.md「Details」, #785): ⏎ sends, ⇧⏎ starts a new line, an input method's ⏎ confirms
/// its candidate, and the other ⏎ combinations stay with the field (⌥⏎ a new line) and the send button (⌘⏎).
struct MacComposerKeysTests {
    private func action(_ modifiers: NSEvent.ModifierFlags = [], key: UInt16 = MacComposerKeys.returnKey,
                        composing: Bool = false, canSend: Bool = true) -> MacComposerKeys.Action {
        MacComposerKeys.action(keyCode: key, modifiers: modifiers, composing: composing, canSend: canSend)
    }

    @Test func returnSends() {
        #expect(action() == .send)
        #expect(action(key: MacComposerKeys.keypadEnter) == .send)
    }

    /// The field on its own selected all the text, and the next key typed replaced it.
    @Test func returnWithNothingToSendDoesNothing() {
        #expect(action(canSend: false) == .ignore)
    }

    @Test func shiftReturnIsANewLine() {
        #expect(action(.shift) == .newline)
        #expect(action(.shift, canSend: false) == .newline)
    }

    /// Caps Lock, the function key and the keypad's own flag ride along without changing what ⏎ means.
    @Test func lockFlagsDoNotCount() {
        #expect(action([.capsLock]) == .send)
        #expect(action([.numericPad], key: MacComposerKeys.keypadEnter) == .send)
        #expect(action([.shift, .capsLock, .function]) == .newline)
    }

    /// Pinyin's ⏎ puts the letters typed so far in as they are; it must not send them.
    @Test func anInputMethodsReturnIsItsOwn() {
        #expect(action(composing: true) == .pass)
        #expect(action(.shift, composing: true) == .pass)
    }

    @Test func otherCombinationsStayWithTheFieldAndTheButton() {
        #expect(action(.option) == .pass)
        #expect(action(.command) == .pass)
        #expect(action(.control) == .pass)
        #expect(action([.shift, .command]) == .pass)
    }

    @Test func otherKeysPass() {
        #expect(action(key: 0) == .pass) // A
        #expect(action(.shift, key: 48) == .pass) // ⇧⇥
    }
}

/// The wheel over a composer taller than its six lines (#785): it moves the text within its first and last lines, and
/// at either end leaves the wheel to the page.
struct MacComposerWheelTests {
    @Test func wheelUpShowsEarlierLines() {
        #expect(MacComposerWheel.offset(from: 60, delta: 20, top: 0, bottom: 60) == 40)
    }

    @Test func stopsAtTheFirstAndLastLines() {
        #expect(MacComposerWheel.offset(from: 10, delta: 50, top: -1, bottom: 60) == -1)
        #expect(MacComposerWheel.offset(from: 50, delta: -50, top: -1, bottom: 60) == 60)
    }

    @Test func atAnEndTheWheelGoesToThePage() {
        #expect(MacComposerWheel.offset(from: 0, delta: 10, top: 0, bottom: 60) == nil)
        #expect(MacComposerWheel.offset(from: 60, delta: -10, top: 0, bottom: 60) == nil)
    }

    @Test func textThatFitsDoesNotScroll() {
        #expect(MacComposerWheel.offset(from: 0, delta: 10, top: 0, bottom: 0) == nil)
    }
}
