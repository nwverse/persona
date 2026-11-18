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

    /// What the bar and the banner were drawn in.
    @Observable final class Looks {
        var bar: ColorScheme?
        var banner: ColorScheme?
        /// What the page's environment says: dark against a light window on purpose, a copy as stale as the one an
        /// Agent chat opened for the first time kept (#9829).
        var page: ColorScheme = .dark
    }

    private struct LookProbe: View {
        let seen: (ColorScheme) -> Void
        @Environment(\.colorScheme) private var scheme

        var body: some View {
            Color.clear.onChange(of: scheme, initial: true) { seen(scheme) }
        }
    }

    /// A bar and its banner that report their look, on a page whose environment says `Looks.page`.
    private struct LookSample: View {
        let looks: Looks

        var body: some View {
            VStack(spacing: 0) {
                LookProbe { looks.bar = $0 }
                    .frame(height: 60)
                    .screenshotBanner { Color.clear } banner: { LookProbe { looks.banner = $0 } }
                Spacer(minLength: 0)
            }
            .environment(\.colorScheme, looks.page)
            .ignoresSafeArea()
        }
    }

    private func window(_ view: some View, style: UIUserInterfaceStyle = .unspecified) async throws -> UIWindow {
        let scene = UIApplication.shared.connectedScenes.compactMap { $0 as? UIWindowScene }.first
        let window = scene.map { UIWindow(windowScene: $0) } ?? UIWindow()
        window.frame = CGRect(origin: .zero, size: Self.size)
        window.windowLevel = .alert + 1
        window.overrideUserInterfaceStyle = style
        window.rootViewController = UIHostingController(rootView: view.environment(AppModel.shared))
        window.makeKeyAndVisible()
        try await settle(window)
        return window
    }

    private func settle(_ window: UIWindow) async throws {
        for _ in 0..<10 {
            try await Task.sleep(for: .milliseconds(50))
            window.layoutIfNeeded()
        }
    }

    private func seen(_ picture: UIImage) throws -> Seen {
        let cg = try #require(picture.cgImage)
        var px = [UInt8](repeating: 0, count: cg.width * cg.height * 4)
        let ctx = try #require(CGContext(data: &px, width: cg.width, height: cg.height, bitsPerComponent: 8, bytesPerRow: cg.width * 4,
                                         space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue))
        ctx.draw(cg, in: CGRect(x: 0, y: 0, width: cg.width, height: cg.height))
        // The bar's middle (30 pt down), in the picture's own pixels (the buffer's first row is the picture's top).
        let x = cg.width / 2, y = Int(30 * CGFloat(cg.height) / Self.size.height)
        let i = (y * cg.width + x) * 4
        let (r, g, b) = (px[i], px[i + 1], px[i + 2])
        // The system's red and blue (255, 66, 69 and 0, 145, 255 here).
        if r > 200 && g < 120 && b < 120 { return .bar }
        if b > 200 && r < 80 { return .banner }
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

    /// The banner's row (#10172): the face and the name at the left, GitHub's mark and the repository at the right.
    /// Where an iPhone's bar has room the repository stays whole and a long name is cut; a short name stays whole with
    /// it; where not even the name's first characters fit beside it (the narrowest iPad split), the mark alone. The
    /// banner draws what the row it picks draws, to the pixel, as `FirstFittingTests` checks `ViewThatFits`.
    @Test func theRepositoryStaysWholeWhileTheNameIsCut() throws {
        let long = "家庭生活、研究资料与日程整理全能助理", short = "助手"
        // An iPhone 17 Pro's bar and a narrow iPhone's between the page margins; an iPad split's narrowest column.
        for width: CGFloat in [370, 343] {
            let banner = try png(PersonaBrandLockup(size: .banner, name: long), width: width)
            #expect(banner == (try png(PersonaBannerRow(name: long, slim: false, showsRepository: true), width: width)),
                    "\(width) pt: a long name and the repository")
            #expect(banner != (try png(PersonaBannerRow(name: long, slim: false, showsRepository: false), width: width)))
            #expect((try png(PersonaBrandLockup(size: .banner, name: short), width: width))
                    == (try png(PersonaBannerRow(name: short, slim: false, showsRepository: true), width: width)),
                    "\(width) pt: a short name and the repository")
        }
        let narrow: CGFloat = 288
        #expect((try png(PersonaBrandLockup(size: .banner, name: long), width: narrow))
                == (try png(PersonaBannerRow(name: long, slim: false, showsRepository: false), width: narrow)),
                "\(narrow) pt: the mark alone")
        // The bar's largest text size (xxxLarge, where the slim row stands in the bar): the repository still stays.
        let large = try png(PersonaBrandLockup(size: .slim, name: long), width: 370, typeSize: .xxxLarge)
        #expect(large == (try png(PersonaBannerRow(name: long, slim: true, showsRepository: true), width: 370, typeSize: .xxxLarge)),
                "xxxLarge: a long name and the repository")
        save(large, "banner-row-slim-xxxLarge-370")
        save(try png(PersonaBrandLockup(size: .banner, name: long), width: narrow), "banner-row-288")
        save(try png(PersonaBrandLockup(size: .banner, name: long), width: 343), "banner-row-343")
        #expect(PersonaBrandLockup.repository == "nwverse/persona")
    }

    private func png(_ v: some View, width: CGFloat, typeSize: DynamicTypeSize = .large) throws -> Data {
        let r = ImageRenderer(content: v.frame(width: width, alignment: .leading).background(Color.white)
            .environment(AppModel.shared).dynamicTypeSize(typeSize))
        r.scale = 2
        return try #require(r.uiImage?.pngData())
    }

    /// TEST_RUNNER_PERSONA_SHOTS=<dir> keeps the rows drawn here, for a look.
    private func save(_ png: Data, _ name: String) {
        guard let dir = ProcessInfo.processInfo.environment["PERSONA_SHOTS"] else { return }
        try? FileManager.default.createDirectory(atPath: dir, withIntermediateDirectories: true)
        try? png.write(to: URL(fileURLWithPath: dir).appendingPathComponent("\(name).png"))
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

    /// The iPad's sidebar drag takes its pictures right after revealing, after the screen updates while a bar is in the
    /// layer (`hasLayers`; a picture of the frame already drawn still leaves it out): they have the bar, not the banner
    /// (laid over the window, the picture covers the bar's own layer, captured again).
    @Test func aSnapshotRightAfterRevealingHasTheBar() async throws {
        let w = try await window(Sample())
        defer { w.isHidden = true }
        let root = try #require(w.rootViewController?.view)
        #expect(ScreenshotBanner.hasLayers)
        ScreenshotBanner.beginReveal()
        let snap = try #require(root.snapshotView(afterScreenUpdates: ScreenshotBanner.hasLayers))
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

    /// The bar and the banner take the look of the window they are drawn in (#9829), as the drawer's sidebar does (#4496,
    /// `DrawerSidebarLookTests`): their hosting controllers get a copy of the page's environment, only as new as the
    /// last update SwiftUI sent, and an Agent chat opened for the first time drew its header and banner light above the
    /// dark chat.
    @Test func theBarAndTheBannerFollowTheirWindow() async throws {
        let looks = Looks()
        let w = try await window(LookSample(looks: looks), style: .light)
        defer { w.isHidden = true }
        #expect(looks.bar == .light && looks.banner == .light,
                "a light window, the page's environment dark: the bar drew \(String(describing: looks.bar)), the banner \(String(describing: looks.banner))")

        // The window changes look (the system's, or Settings › Appearance › Light & Dark): both follow it.
        w.overrideUserInterfaceStyle = .dark
        try await settle(w)
        #expect(looks.bar == .dark && looks.banner == .dark,
                "the window went dark: the bar drew \(String(describing: looks.bar)), the banner \(String(describing: looks.banner))")

        // An update from the page in the other look does not take them with it.
        looks.page = .light
        try await settle(w)
        #expect(looks.bar == .dark && looks.banner == .dark,
                "the page updated in the other look: the bar drew \(String(describing: looks.bar)), the banner \(String(describing: looks.banner))")
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
