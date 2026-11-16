import SwiftUI
import Testing
import UIKit
@testable import Persona

/// The screenshot banner (#9508, docs/apple/ios.md 「Screenshot banner」): a view wrapped with `screenshotBanner` is on
/// screen but out of every capture, the banner under it shows instead; `beginReveal` puts the view back into pictures
/// the app takes of its own window (the iPad's sidebar drag, DEBUG screenshots); the Settings switch off draws the view
/// as it is. In-app pictures (`drawHierarchy`, `snapshotView`) leave the capture layer out as the system's screenshot
/// does, so they stand in for one here.
@MainActor @Suite(.serialized) struct ScreenshotBannerTests {
    /// What a picture shows at the bar's middle.
    enum Seen: Equatable { case bar, banner, other(UInt8, UInt8, UInt8) }

    private static let size = CGSize(width: 240, height: 160)

    /// A red bar 60 pt tall at the top over a white page, the banner blue.
    private struct Sample: View {
        var body: some View {
            VStack(spacing: 0) {
                Color.red
                    .frame(height: 60)
                    .screenshotBanner { Color.white } banner: { Color.blue }
                Spacer(minLength: 0)
            }
            .background(Color.white)
            // The window lies under the status bar: the bar at its very top, where `seen` looks.
            .ignoresSafeArea()
        }
    }

    private func window(_ view: some View) async throws -> UIWindow {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        let window = scene.map { UIWindow(windowScene: $0) } ?? UIWindow()
        window.frame = CGRect(origin: .zero, size: Self.size)
        window.windowLevel = .alert + 1
        window.rootViewController = UIHostingController(rootView: view.environment(AppModel.shared))
        window.makeKeyAndVisible()
        for _ in 0..<10 {
            try await Task.sleep(for: .milliseconds(50))
            window.layoutIfNeeded()
        }
        return window
    }

    private func seen(_ picture: UIImage) throws -> Seen {
        let cg = try #require(picture.cgImage)
        var px = [UInt8](repeating: 0, count: cg.width * cg.height * 4)
        let ctx = try #require(CGContext(data: &px, width: cg.width, height: cg.height, bitsPerComponent: 8, bytesPerRow: cg.width * 4,
                                         space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: cg.width, height: cg.height))
        // The bar's middle (30 pt down), in the picture's own pixels; the context's rows run bottom up.
        let x = cg.width / 2, y = cg.height - 1 - Int(30 * CGFloat(cg.height) / Self.size.height)
        let i = (y * cg.width + x) * 4
        let (r, g, b) = (px[i], px[i + 1], px[i + 2])
        if r > 200 && g < 60 && b < 60 { return .bar }
        if b > 200 && r < 60 && g < 60 { return .banner }
        return .other(r, g, b)
    }

    private func drawn(_ window: UIWindow, afterScreenUpdates: Bool = true) throws -> Seen {
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        let image = UIGraphicsImageRenderer(size: Self.size, format: format).image { _ in
            _ = window.drawHierarchy(in: window.bounds, afterScreenUpdates: afterScreenUpdates)
        }
        return try seen(image)
    }

    @Test func thisSystemHasTheCaptureLayer() {
        #expect(ScreenshotBanner.isAvailable)
        #expect(ScreenshotBannerPrefs.enabledByDefault)
    }

    @Test func theNameIsNotSaidTwice() {
        #expect(!PersonaBrandLockup.showsProduct(after: "Persona"))
        #expect(!PersonaBrandLockup.showsProduct(after: "persona "))
        #expect(PersonaBrandLockup.showsProduct(after: "小白"))
        #expect(PersonaBrandLockup.showsProduct(after: "Persona Two"))
    }

    /// A picture of the window shows the banner where the bar is; revealed, the bar; after, the banner again.
    @Test func picturesShowTheBannerUnlessRevealed() async throws {
        let w = try await window(Sample())
        defer { w.isHidden = true }
        let captured = try drawn(w)
        #expect(captured == .banner, "a picture showed \(captured)")
        ScreenshotBanner.beginReveal()
        let revealed = try drawn(w)
        #expect(revealed == .bar, "revealed, a picture showed \(revealed)")
        // Nested: still revealed until the last end.
        ScreenshotBanner.beginReveal()
        ScreenshotBanner.endReveal()
        let nested = try drawn(w)
        #expect(nested == .bar, "after a nested reveal ended, a picture showed \(nested)")
        ScreenshotBanner.endReveal()
        let after = try drawn(w)
        #expect(after == .banner, "after the reveal, a picture showed \(after)")
    }

    /// The iPad's sidebar drag takes its pictures with `afterScreenUpdates: false` right after revealing: they have
    /// the bar, not the banner (laid over the window, the picture covers the bar's own layer, captured again).
    @Test func aSnapshotRightAfterRevealingHasTheBar() async throws {
        let w = try await window(Sample())
        defer { w.isHidden = true }
        let root = try #require(w.rootViewController?.view)
        ScreenshotBanner.beginReveal()
        let snap = try #require(root.snapshotView(afterScreenUpdates: false))
        ScreenshotBanner.endReveal()
        snap.frame = w.bounds
        w.addSubview(snap)
        defer { snap.removeFromSuperview() }
        let pictured = try drawn(w)
        #expect(pictured == .bar, "the snapshot showed \(pictured)")
    }

    /// Off in Settings: the view is drawn as it is, so pictures (and screenshots) show it.
    @Test func switchedOffThePicturesShowTheBar() async throws {
        let defaults = UserDefaults.standard
        let before = defaults.object(forKey: ScreenshotBannerPrefs.enabledKey)
        defaults.set(false, forKey: ScreenshotBannerPrefs.enabledKey)
        defer {
            if let before { defaults.set(before, forKey: ScreenshotBannerPrefs.enabledKey) } else { defaults.removeObject(forKey: ScreenshotBannerPrefs.enabledKey) }
        }
        let w = try await window(Sample())
        defer { w.isHidden = true }
        let pictured = try drawn(w)
        #expect(pictured == .bar, "switched off, a picture showed \(pictured)")
    }

    /// The banner is for pictures only: VoiceOver never reaches it, and it takes no touches.
    @Test func theBannerIsOutOfReach() async throws {
        let w = try await window(Sample())
        defer { w.isHidden = true }
        let hidden = allViews(w).filter { $0.accessibilityElementsHidden }
        #expect(hidden.contains { !$0.isUserInteractionEnabled })
    }

    private func allViews(_ v: UIView) -> [UIView] { [v] + v.subviews.flatMap(allViews) }
}
