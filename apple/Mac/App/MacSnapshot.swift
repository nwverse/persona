#if DEBUG
import AppKit
import SwiftUI

/// Screenshots without the screen (docs/apple/mac.md「Build, run, screenshot」): this Mac is often locked, and `screencapture` then only gets
/// the lock screen. `-macSnapshot <png>` renders the main window's own views into a PNG after a while
/// (`-macSnapshotDelay <s>`, default 4), then quits with `-macSnapshotQuit YES`. `-macWindowSize 1280x820` sizes the
/// window first; `-macAppearance dark|light` picks the appearance (`-AppleInterfaceStyle Dark` also works);
/// `-macSnapshotScroll <points>` scrolls the window's lists down that far before the shot (what moves under the title
/// bar's row, #1243). `scripts/app/mac_shot.sh` drives it.
@MainActor enum MacSnapshot {
    /// Before any window exists.
    static func applyAppearanceIfAsked() {
        if isRichBatch { NSApplication.shared.setActivationPolicy(.prohibited) }
        applyAppearance(UserDefaults.standard.string(forKey: "macAppearance"))
    }

    static var isRichBatch: Bool { UserDefaults.standard.string(forKey: "macSnapshot")?.hasSuffix(".json") == true }

    /// `-macAppearance` for SwiftUI too: `NSApp.appearance` alone left List rows in the light scheme's colors.
    static var colorScheme: ColorScheme? {
        if isRichBatch { return MacRichSnapshotBatch.shared.scheme }
        return switch UserDefaults.standard.string(forKey: "macAppearance")?.lowercased() {
        case "dark": .dark
        case "light": .light
        default: nil
        }
    }

    static func scheduleIfAsked() {
        let d = UserDefaults.standard
        if isRichBatch {
            if let path = d.string(forKey: "macSnapshot"), let plan = MacDemoSnapshotBatch.plan(at: path) {
                Task { await MacDemoSnapshotBatch.run(plan) }
            } else if let path = d.string(forKey: "macSnapshot"), let plan = MacExtensionSnapshotBatch.plan(at: path) {
                Task { await MacExtensionSnapshotBatch.run(plan) }
            } else {
                Task { await MacRichSnapshotBatch.shared.run() }
            }
            return
        }
        let size = d.string(forKey: "macWindowSize").flatMap(parseSize)
        if let size {
            // The window exists a moment after launch; size it as soon as it does.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { resize(to: size) }
        }
        guard let path = d.string(forKey: "macSnapshot"), !path.isEmpty else { return }
        let delay = d.object(forKey: "macSnapshotDelay") != nil ? d.double(forKey: "macSnapshotDelay") : 4
        DispatchQueue.main.asyncAfter(deadline: .now() + max(delay, 0.5)) {
            if let size { resize(to: size) }
            scroll(by: d.double(forKey: "macSnapshotScroll"))
            // One more turn so a resize or a scroll has laid out and drawn.
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.6) {
                let ok = capture(to: URL(fileURLWithPath: path))
                FileHandle.standardError.write(Data("macSnapshot \(ok ? "wrote" : "FAILED") \(path)\n".utf8))
                // A sheet is a window of its own, not drawn with the main one: it goes next to it as <name>-sheet.png.
                if let sheet = mainWindow?.attachedSheet {
                    let url = URL(fileURLWithPath: path)
                    let sheetURL = url.deletingPathExtension().appendingPathExtension("sheet").appendingPathExtension(url.pathExtension)
                    let wrote = capture(to: sheetURL, window: sheet)
                    FileHandle.standardError.write(Data("macSnapshot \(wrote ? "wrote" : "FAILED") \(sheetURL.path)\n".utf8))
                    // A sheet on that sheet (the terminal's Settings over the Terminal sheet): <name>.sheet2.png.
                    if let inner = sheet.attachedSheet {
                        let innerURL = url.deletingPathExtension().appendingPathExtension("sheet2").appendingPathExtension(url.pathExtension)
                        let wrote = capture(to: innerURL, window: inner)
                        FileHandle.standardError.write(Data("macSnapshot \(wrote ? "wrote" : "FAILED") \(innerURL.path)\n".utf8))
                    }
                }
                // A conversation opened in its own window (-openChatWindow): next to it as <name>.chat.png.
                if let chat = NSApp.windows.first(where: { $0.isVisible && $0 !== mainWindow && ($0.identifier?.rawValue.hasPrefix(MacChatWindow.id) == true || $0.identifier?.rawValue.hasPrefix(MacTerminalWindow.id) == true) }) {
                    let url = URL(fileURLWithPath: path)
                    let chatURL = url.deletingPathExtension().appendingPathExtension("chat").appendingPathExtension(url.pathExtension)
                    let wrote = capture(to: chatURL, window: chat)
                    FileHandle.standardError.write(Data("macSnapshot \(wrote ? "wrote" : "FAILED") \(chatURL.path)\n".utf8))
                }
                // A popover is a window of its own too (the composer's ⌃R search): <name>.popover.png.
                if let pop = NSApp.windows.first(where: { $0.isVisible && $0.className.contains("Popover") }) {
                    let url = URL(fileURLWithPath: path)
                    let popURL = url.deletingPathExtension().appendingPathExtension("popover").appendingPathExtension(url.pathExtension)
                    let wrote = capture(to: popURL, window: pop)
                    FileHandle.standardError.write(Data("macSnapshot \(wrote ? "wrote" : "FAILED") \(popURL.path)\n".utf8))
                }
                if d.bool(forKey: "macSnapshotQuit") {
                    NSApp.terminate(nil)
                    // A sheet with a text editor in it holds termination off: leave anyway.
                    DispatchQueue.main.asyncAfter(deadline: .now() + 1) { exit(0) }
                }
            }
        }
    }

    private static func applyAppearance(_ name: String?) {
        switch name?.lowercased() {
        case "dark": NSApp.appearance = NSAppearance(named: .darkAqua)
        case "light": NSApp.appearance = NSAppearance(named: .aqua)
        default: break
        }
    }

    private static func parseSize(_ s: String) -> CGSize? {
        let parts = s.lowercased().split(separator: "x").compactMap { Double($0) }
        guard parts.count == 2, parts[0] > 100, parts[1] > 100 else { return nil }
        return CGSize(width: parts[0], height: parts[1])
    }

    private static var mainWindow: NSWindow? {
        NSApp.windows.first { $0.isVisible && $0.contentView != nil && !($0 is NSPanel) && $0.className != "NSStatusBarWindow" }
    }

    private static func resize(to size: CGSize) {
        guard let w = mainWindow else { return }
        let frame = w.frameRect(forContentRect: NSRect(origin: .zero, size: size))
        w.setFrame(NSRect(origin: w.frame.origin, size: frame.size), display: true)
    }

    /// Every scroll view in the window that scrolls vertically (SwiftUI's `List`, `Form` and `ScrollView` are
    /// NSScrollViews with flipped document views) moved `points` further down its content, as far as it goes.
    static func scroll(by points: Double) {
        guard points > 0, let frameView = mainWindow?.contentView?.superview else { return }
        for view in descendants(of: frameView) {
            guard let scroll = view as? NSScrollView, isShown(scroll), let doc = scroll.documentView, doc.isFlipped else { continue }
            let clip = scroll.contentView
            let bottom = doc.frame.height - clip.bounds.height + scroll.contentInsets.bottom
            guard bottom > clip.bounds.origin.y + 1 else { continue }
            clip.scroll(to: NSPoint(x: clip.bounds.origin.x, y: min(clip.bounds.origin.y + points, bottom)))
            scroll.reflectScrolledClipView(clip)
        }
    }

    struct ScrollMetrics: Encodable {
        var offset: Double
        var viewport: Double
        var end: Double
    }

    private static func captureScrollView(in window: NSWindow) -> NSScrollView? {
        guard let root = window.contentView else { return nil }
        return descendants(of: root).compactMap { $0 as? NSScrollView }
            .filter { scroll in
                guard isShown(scroll), let doc = scroll.documentView, doc.isFlipped else { return false }
                return doc.frame.height > scroll.contentView.bounds.height + 1
            }
            .max { $0.contentView.bounds.width * $0.contentView.bounds.height < $1.contentView.bounds.width * $1.contentView.bounds.height }
    }

    static func captureScrollMetrics(in window: NSWindow) -> ScrollMetrics? {
        guard let scroll = captureScrollView(in: window), let doc = scroll.documentView else { return nil }
        let clip = scroll.contentView
        return ScrollMetrics(offset: clip.bounds.origin.y, viewport: clip.bounds.height,
                             end: max(0, doc.frame.height - clip.bounds.height + scroll.contentInsets.bottom))
    }

    static func advanceCapturePage(in window: NSWindow) -> Bool {
        guard let scroll = captureScrollView(in: window), let metrics = captureScrollMetrics(in: window),
              metrics.offset < metrics.end - 1 else { return false }
        let clip = scroll.contentView
        clip.scroll(to: NSPoint(x: clip.bounds.origin.x, y: min(metrics.offset + metrics.viewport * 0.75, metrics.end)))
        scroll.reflectScrolledClipView(clip)
        return clip.bounds.origin.y > metrics.offset + 1
    }

    /// The window's frame view (title bar and toolbar included) drawn into a bitmap at the screen's scale. Views
    /// draw themselves through `cacheDisplay`, which does not need the window server's picture of the screen.
    /// Materials (NSVisualEffectView, glass) render as the window server would composite them only on screen: here
    /// they draw their fallback fill, so the window background is painted underneath first.
    private static func isShown(_ v: NSView) -> Bool {
        var cur: NSView? = v
        while let c = cur { if c.isHidden || c.alphaValue < 0.01 { return false }; cur = c.superview }
        return v.bounds.width > 0 && v.bounds.height > 0
    }

    private static func paint(_ rep: NSBitmapImageRep, appearance: NSAppearance? = nil, _ draw: () -> Void) {
        guard let ctx = NSGraphicsContext(bitmapImageRep: rep) else { return }
        NSGraphicsContext.saveGraphicsState()
        NSGraphicsContext.current = ctx
        (appearance ?? NSApp.effectiveAppearance).performAsCurrentDrawingAppearance(draw)
        NSGraphicsContext.restoreGraphicsState()
    }

    private static func filteredLayers(_ l: CALayer) -> [CALayer] {
        (l.compositingFilter != nil || l.filters?.isEmpty == false ? [l] : []) + (l.sublayers ?? []).flatMap(filteredLayers)
    }

    private static func descendants(of v: NSView) -> [NSView] { v.subviews + v.subviews.flatMap(descendants) }

    /// The window drawn into a bitmap at the screen's scale, title bar and toolbar included, through `cacheDisplay`:
    /// views draw themselves and nothing needs the window server's picture of the screen, so a locked Mac works.
    ///
    /// What that misses, found by trying (2026-10-31, docs/apple/mac.md「Build, run, screenshot」): the rows of AppKit-backed lists
    /// (SwiftUI `List` is an NSTableView) come out empty when drawn as part of the window, so every row is drawn again
    /// on its own and laid over its place; a selected row's material draws black off screen, so it is painted in the
    /// inactive selection color. Glass and other materials draw no blur: the window background shows through.
    /// `layer.render(in:)` and `dataWithPDF` were worse (upside-down, or most text missing).
    static func capture(to url: URL, window: NSWindow? = nil) -> Bool {
        guard let window = window ?? mainWindow, let frameView = window.contentView?.superview ?? window.contentView else { return false }
        frameView.layoutSubtreeIfNeeded()
        frameView.displayIfNeeded()
        let bounds = frameView.bounds
        guard let rep = frameView.bitmapImageRepForCachingDisplay(in: bounds) else { return false }
        paint(rep) { NSColor.windowBackgroundColor.setFill(); bounds.fill() }
        // Vibrant text (in sidebars, over glass) is drawn with a blend mode or filter against what is behind it; off
        // screen there is nothing behind and it comes out dark. Drawn plainly for the shot, and put back.
        let blended = frameView.layer.map(filteredLayers) ?? []
        let saved = blended.map { ($0.compositingFilter, $0.filters) }
        blended.forEach { $0.compositingFilter = nil; $0.filters = nil }
        defer { zip(blended, saved).forEach { $0.compositingFilter = $1.0; $0.filters = $1.1 } }
        window.effectiveAppearance.performAsCurrentDrawingAppearance { frameView.cacheDisplay(in: bounds, to: rep) }
        // A split's sidebar column floats in a glass container whose content holder is a bare layer: drawn as part of
        // the window it comes out as an empty white card (the Persona sidebar, 2026-11-01). Each view held in one is
        // drawn again on its own, over the window background, and laid over its place.
        let glass = descendants(of: frameView).filter { String(describing: type(of: $0)).contains("ConcentricGlassEffectView") && isShown($0) }
        var heldRects: [CGRect] = []
        for g in glass {
            for held in g.subviews.flatMap({ $0.subviews }) where isShown(held) {
                guard let sub = held.bitmapImageRepForCachingDisplay(in: held.bounds) else { continue }
                paint(sub, appearance: held.effectiveAppearance) { NSColor.windowBackgroundColor.setFill(); held.bounds.fill() }
                held.effectiveAppearance.performAsCurrentDrawingAppearance { held.cacheDisplay(in: held.bounds, to: sub) }
                let full = held.convert(held.bounds, to: frameView)
                guard let cg = sub.cgImage, let ctx = NSGraphicsContext(bitmapImageRep: rep) else { continue }
                ctx.cgContext.draw(cg, in: full)
                heldRects.append(full)
                ctx.flushGraphics()
            }
        }
        // The held view reaches up under the toolbar: the toolbar's items go back on top, there only.
        if !heldRects.isEmpty, let bar = descendants(of: frameView).first(where: { String(describing: type(of: $0)) == "NSToolbarView" }),
           let sub = bar.bitmapImageRepForCachingDisplay(in: bar.bounds) {
            bar.effectiveAppearance.performAsCurrentDrawingAppearance { bar.cacheDisplay(in: bar.bounds, to: sub) }
            if let cg = sub.cgImage, let ctx = NSGraphicsContext(bitmapImageRep: rep) {
                ctx.cgContext.saveGState()
                ctx.cgContext.clip(to: heldRects)
                ctx.cgContext.draw(cg, in: bar.convert(bar.bounds, to: frameView))
                ctx.cgContext.restoreGState()
                ctx.flushGraphics()
            }
        }
        let rows = descendants(of: frameView).compactMap { $0 as? NSTableRowView }
        for row in rows where isShown(row) {
            guard let sub = row.bitmapImageRepForCachingDisplay(in: row.bounds) else { continue }
            let materials = descendants(of: row).compactMap { $0 as? NSVisualEffectView }.filter { !$0.isHidden }
            materials.forEach { $0.isHidden = true }
            if row.isSelected {
                paint(sub, appearance: row.effectiveAppearance) {
                    for m in materials {
                        let r = m.convert(m.bounds, to: row)
                        let box = row.isFlipped ? NSRect(x: r.minX, y: row.bounds.height - r.maxY, width: r.width, height: r.height) : r
                        let radius = m.layer?.cornerRadius ?? 0
                        NSColor.unemphasizedSelectedContentBackgroundColor.setFill()
                        NSBezierPath(roundedRect: box, xRadius: radius, yRadius: radius).fill()
                    }
                }
            }
            // Vibrant text (sidebar lists) is drawn with a blend mode against the material behind it; off screen
            // there is nothing behind and it comes out dark. Drawn plainly for the shot.
            row.effectiveAppearance.performAsCurrentDrawingAppearance { row.cacheDisplay(in: row.bounds, to: sub) }
            materials.forEach { $0.isHidden = false }
            // The window bitmap's context is in the frame view's points, origin at the bottom left, as `convert` gives;
            // clipped to what every clipping view above it shows: its scroll view's, and SwiftUI's `.clipped()` (a
            // list kept below the title bar's row stops there, `MacBelowTitleRow`, #1243).
            var r = row.convert(row.bounds, to: frameView)
            var above = row.superview
            while let v = above, v !== frameView {
                if v.clipsToBounds || v.layer?.masksToBounds == true { r = r.intersection(v.convert(v.bounds, to: frameView)) }
                above = v.superview
            }
            guard !r.isEmpty, let cg = sub.cgImage else { continue }
            let full = row.convert(row.bounds, to: frameView)
            if let ctx = NSGraphicsContext(bitmapImageRep: rep) {
                ctx.cgContext.saveGState()
                ctx.cgContext.clip(to: r)
                ctx.cgContext.draw(cg, in: full)
                ctx.cgContext.restoreGState()
                ctx.flushGraphics()
            }
        }
        guard let png = rep.representation(using: .png, properties: [:]) else { return false }
        do {
            try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
            try png.write(to: url)
            return true
        } catch {
            return false
        }
    }
}
#endif
