#if os(iOS)
import UIKit
import XCTest
@testable import Persona

/// The composer's picture offer (#4573): a copy made elsewhere or by Get Clipboard is offered once, and returning to
/// the app looks again. These use synthetic pictures only; the simulator's pasteboard is shared with the test runner.
@MainActor final class PasteboardImagesTests: XCTestCase {
    override func setUp() {
        super.setUp()
        UIPasteboard.general.items = []
        PasteboardImages.shared.dismiss()
    }

    override func tearDown() {
        UIPasteboard.general.items = []
        PasteboardImages.shared.dismiss()
        super.tearDown()
    }

    func testPictureCopiedElsewhereIsOffered() {
        UIPasteboard.general.image = Self.image()

        XCTAssertTrue(PasteboardImages.shared.offered)
    }

    func testOwnPictureWriteIsOfferedAndOwnTextWriteClearsIt() throws {
        let bytes = try XCTUnwrap(Self.image().pngData())
        PhonePasteboard.live.write(.image(ClipboardImage(mime: "image/png", bytes: bytes)))

        XCTAssertTrue(PasteboardImages.shared.offered)

        PhonePasteboard.live.write(.text("synthetic clipboard text"))

        XCTAssertFalse(PasteboardImages.shared.offered)
    }

    func testTakenOrDismissedCopyIsNotOfferedAgain() {
        UIPasteboard.general.image = Self.image()
        XCTAssertTrue(PasteboardImages.shared.offered)

        PasteboardImages.shared.dismiss()
        NotificationCenter.default.post(name: UIPasteboard.changedNotification, object: UIPasteboard.general)

        XCTAssertFalse(PasteboardImages.shared.offered)
    }

    func testBecomingActiveAsksAgainWithoutReofferingHandledCopy() {
        UIPasteboard.general.image = Self.image()
        PasteboardImages.shared.dismiss()

        NotificationCenter.default.post(name: UIApplication.didBecomeActiveNotification, object: UIApplication.shared)

        XCTAssertFalse(PasteboardImages.shared.offered)

        UIPasteboard.general.image = Self.image()
        NotificationCenter.default.post(name: UIApplication.didBecomeActiveNotification, object: UIApplication.shared)

        XCTAssertTrue(PasteboardImages.shared.offered)
    }

    private static func image() -> UIImage {
        UIGraphicsImageRenderer(size: CGSize(width: 2, height: 2)).image { context in
            UIColor.systemTeal.setFill()
            context.fill(CGRect(x: 0, y: 0, width: 2, height: 2))
        }
    }
}
#endif
