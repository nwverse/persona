import UIKit
import XCTest

/// New from Photo end to end (#2405) against mock_hub.py with MOCK_LOOK_UPLOADS=demo: the crop sheet opened with a
/// portrait picture (`-showAvatarCrop`), dragged, uploaded and chosen; then the avatar made deleted from its card's menu.
/// Real touches: the crop sits in a Form, which scrolls, and a drag on the picture must move the picture, not the sheet.
/// `PERSONA_MOCK_HUB_NO_FILES` (a second mock with MOCK_LOOK_UPLOAD_FILES=fail): made but not downloaded, Try Again.
/// `PERSONA_LOOK_APPEARANCE` light / dark, `PERSONA_LOOK_LANGUAGE` (en, zh-Hans, ja), `PERSONA_LOOK_SHOTS` a folder for
/// the screenshots.
final class AvatarUploadUITests: XCTestCase {
    private var env: [String: String] { ProcessInfo.processInfo.environment }

    override func setUp() {
        continueAfterFailure = false
    }

    private func hub(_ key: String) throws -> String {
        guard let hub = env[key] else { throw XCTSkip("needs \(key): mock_hub.py's address") }
        return hub
    }

    /// A portrait picture (taller than wide, so it moves up and down at the first zoom), striped so a move shows.
    private func portrait() throws -> String {
        let size = CGSize(width: 600, height: 1200)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let png = UIGraphicsImageRenderer(size: size, format: format).pngData { ctx in
            for (i, y) in stride(from: 0, to: size.height, by: 100).enumerated() {
                UIColor(hue: Double(i) / 12, saturation: 0.7, brightness: 0.9, alpha: 1).setFill()
                ctx.fill(CGRect(x: 0, y: y, width: size.width, height: 100))
            }
        }
        let url = FileManager.default.temporaryDirectory.appending(path: "avatar-upload-portrait.png")
        try png.write(to: url)
        return url.path(percentEncoded: false)
    }

    @MainActor private func launch(_ hub: String, _ image: String) -> XCUIApplication {
        let app = XCUIApplication()
        let pair = "persona://pair?url=" + hub.addingPercentEncoding(withAllowedCharacters: .alphanumerics)! + "&token=mock"
        app.launchArguments = ["-pairURL", pair, "-tab", "settings", "-openAppearance", "persona", "-openPersona", "persona",
                               "-showAvatarCrop", image, "-showAvatarCropName", "Sunny",
                               "-ui.scheme", env["PERSONA_LOOK_APPEARANCE"] ?? "light"]
        if let language = env["PERSONA_LOOK_LANGUAGE"] { app.launchArguments += ["-AppleLanguages", "(\(language))"] }
        app.launchPinned()
        let board = XCUIApplication(bundleIdentifier: "com.apple.springboard")
        let deny = board.alerts.buttons.matching(NSPredicate(format: "label IN %@", ["不允许", "Don’t Allow", "Don't Allow"])).firstMatch
        if deny.waitForExistence(timeout: 3) { deny.tap() }
        return app
    }

    @MainActor private func shot(_ app: XCUIApplication, _ name: String) {
        let attachment = XCTAttachment(screenshot: app.screenshot())
        attachment.name = name
        attachment.lifetime = .keepAlways
        add(attachment)
        if let dir = env["PERSONA_LOOK_SHOTS"] {
            let tag = [env["PERSONA_LOOK_LANGUAGE"], env["PERSONA_LOOK_APPEARANCE"] ?? "light"].compactMap { $0 }.joined(separator: "-")
            try? app.screenshot().pngRepresentation.write(to: URL(fileURLWithPath: dir).appending(path: "\(name)-\(tag).png"))
        }
    }

    @MainActor func testDragMovesThePictureThenUploadChoosesItAndDeleteBringsTheDefaultBack() throws {
        let app = launch(try hub("PERSONA_MOCK_HUB"), try portrait())
        defer { app.terminate() }
        let crop = app.descendants(matching: .any)["avatar-crop"]
        XCTAssertTrue(crop.waitForExistence(timeout: 20), "the crop sheet did not open")
        let before = crop.screenshot().pngRepresentation
        let frame = crop.frame

        // Up the picture: the finger goes from the middle to near the top of the square.
        let start = crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.7))
        start.press(forDuration: 0.05, thenDragTo: crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2)))
        XCTAssertEqual(crop.frame, frame, "the sheet scrolled instead of the picture moving")
        XCTAssertNotEqual(crop.screenshot().pngRepresentation, before, "the picture did not follow the finger")
        let dragged = crop.screenshot().pngRepresentation

        // A quick flick, as people do: the same.
        crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.2))
            .press(forDuration: 0, thenDragTo: crop.coordinate(withNormalizedOffset: CGVector(dx: 0.5, dy: 0.8)),
                   withVelocity: .fast, thenHoldForDuration: 0)
        XCTAssertEqual(crop.frame, frame, "a flick scrolled the sheet")
        XCTAssertNotEqual(crop.screenshot().pngRepresentation, dragged, "a flick did not move the picture")
        shot(app, "crop-dragged")

        app.buttons["avatar-upload"].tap()
        XCTAssertTrue(crop.waitForNonExistence(timeout: 30), "the sheet did not close after the upload")
        let made = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'persona-u-' AND label CONTAINS 'Sunny'")).firstMatch
        XCTAssertTrue(made.waitForExistence(timeout: 10), "the avatar made is not listed")
        let selected = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: made)
        XCTAssertEqual(XCTWaiter.wait(for: [selected], timeout: 20), .completed, "the avatar made is not chosen here")
        shot(app, "after-upload")

        made.press(forDuration: 1.2)
        let delete = app.buttons.matching(NSPredicate(format: "label IN %@", ["Delete Avatar", "删除形象", "アバターを削除"])).firstMatch
        XCTAssertTrue(delete.waitForExistence(timeout: 5), "no Delete Avatar in the card's menu")
        delete.tap()
        let confirm = app.buttons["persona-delete-confirm"].firstMatch
        XCTAssertTrue(confirm.waitForExistence(timeout: 5), "no confirmation")
        shot(app, "delete-confirmation")
        confirm.tap()
        XCTAssertTrue(made.waitForNonExistence(timeout: 20), "the deleted avatar is still listed")
        let builtin = expectation(for: NSPredicate(format: "selected == true"), evaluatedWith: app.buttons["persona-persona"])
        XCTAssertEqual(XCTWaiter.wait(for: [builtin], timeout: 20), .completed, "the default did not come back")
        shot(app, "after-delete")
    }

    /// The hub made it but its files don't come: the sheet stays, says so, and its button turns into Try Again; closing
    /// it then asks nothing (the avatar is in the list, not chosen).
    @MainActor func testMadeButNotDownloadedSaysSoAndOffersTryAgain() throws {
        let app = launch(try hub("PERSONA_MOCK_HUB_NO_FILES"), try portrait())
        defer { app.terminate() }
        let crop = app.descendants(matching: .any)["avatar-crop"]
        XCTAssertTrue(crop.waitForExistence(timeout: 20), "the crop sheet did not open")
        let upload = app.buttons["avatar-upload"]
        upload.tap()
        XCTAssertTrue(app.descendants(matching: .any)["avatar-upload-error"].waitForExistence(timeout: 30), "no error after a failed download")
        XCTAssertTrue(crop.exists, "the sheet closed although the avatar is not on this device")
        let again = NSPredicate(format: "label IN %@ AND enabled == true", ["Try Again", "再试一次", "再試行"])
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: again, evaluatedWith: upload)], timeout: 10), .completed,
                       "the button is not Try Again: \(upload.label)")
        XCTAssertFalse(app.textFields["avatar-upload-name"].isEnabled, "the name can still be edited after the avatar was made")
        shot(app, "crop-made-not-downloaded")

        upload.tap() // still no files: the same, and the sheet stays
        XCTAssertEqual(XCTWaiter.wait(for: [expectation(for: again, evaluatedWith: upload)], timeout: 30), .completed)
        XCTAssertTrue(crop.exists)

        // Cancel: nothing to lose.
        app.buttons.matching(NSPredicate(format: "label IN %@", ["Cancel", "取消", "キャンセル"])).firstMatch.tap()
        XCTAssertTrue(crop.waitForNonExistence(timeout: 10), "Cancel did not close the sheet (or asked first)")
        let made = app.buttons.matching(NSPredicate(format: "identifier BEGINSWITH 'persona-u-' AND label CONTAINS 'Sunny'")).firstMatch
        XCTAssertTrue(made.waitForExistence(timeout: 10), "the avatar made is not listed")
        XCTAssertFalse(made.isSelected)
        XCTAssertTrue(app.buttons["persona-persona"].isSelected)
    }
}
