import UIKit
import XCTest

/// A picture on the clipboard is offered over the focused composer and attached with one tap (#4573, `PasteImageChip`);
/// the same copy is not offered again.
final class PasteImageTests: XCTestCase {
    override func setUp() {
        continueAfterFailure = false
    }

    @MainActor func testAClipboardPictureAttachesFromTheChip() {
        // The simulator's clipboard is shared with the app under test.
        UIPasteboard.general.image = UIGraphicsImageRenderer(size: CGSize(width: 40, height: 30)).image { ctx in
            UIColor.systemTeal.setFill()
            ctx.fill(CGRect(x: 0, y: 0, width: 40, height: 30))
        }
        let app = XCUIApplication()
        app.launchArguments = ["-demo.on", "YES", "-newAssistant", "YES", "-dev.openChat", "", "-tab", "assistant"]
        app.launchPinned()

        let field = app.descendants(matching: .any)["shell-composer-field"].firstMatch
        XCTAssertTrue(field.waitForExistence(timeout: 15), "no composer")
        // The system's PasteButton may not carry the identifier through; its title is 「粘贴」.
        let chip = app.buttons.matching(NSPredicate(format: "identifier == %@ OR label IN %@", "composer-paste-image", ["粘贴", "Paste"])).firstMatch
        XCTAssertFalse(chip.exists, "offered before the composer is focused")
        field.tap()
        XCTAssertTrue(chip.waitForExistence(timeout: 5), "no 粘贴 over the focused composer")
        chip.tap() // a PasteButton tap is the consent: no Allow Paste prompt
        let attached = app.descendants(matching: .any).matching(NSPredicate(format: "label BEGINSWITH %@", "图片 1")).firstMatch
        XCTAssertTrue(attached.waitForExistence(timeout: 5), "the picture is not in the composer")
        XCTAssertTrue(chip.waitForNonExistence(timeout: 3), "the same copy is offered again")
        let shot = XCTAttachment(screenshot: app.screenshot())
        shot.name = "pasted"; shot.lifetime = .keepAlways; add(shot)
    }
}
