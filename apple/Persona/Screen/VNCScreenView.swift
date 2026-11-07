import AVFoundation
import SwiftUI

/// How touches on the Mac's screen work (#1343), kept per Mac on this device: Mouse Mode (the default) points where
/// the finger is, Trackpad Mode moves the pointer like a trackpad.
enum VNCMode: String, CaseIterable {
    case mouse, trackpad

    private static func key(_ ref: MachineRef) -> String { "vnc.mode." + ScreenCredentials.key(ref) }
    static func of(_ ref: MachineRef) -> VNCMode { VNCMode(rawValue: UserDefaults.standard.string(forKey: key(ref)) ?? "") ?? .mouse }
    static func set(_ mode: VNCMode, for ref: MachineRef) { UserDefaults.standard.set(mode.rawValue, forKey: key(ref)) }
}

#if os(iOS) || os(visionOS)
/// The Mac's screen in a layer, aspect-fit and zoomable, its pointer drawn over it, and, when interactive, touches turned
/// into the Mac's pointer as the mode says:
/// - Mouse Mode: a tap clicks there; one finger drags; two scroll (or, zoomed in, move the picture); two tapping or one
///   held down is the right button; pinching zooms.
/// - Trackpad Mode: one finger moves the pointer by as much; a tap clicks where it is; a tap then a drag drags; two scroll.
/// Two fingers scroll as VNCWheel says: a notch every few points, coasting after a flick.
/// With `region` set (one of the Mac's displays, #80) only that part of the framebuffer is shown, zoomed and touched;
/// what is sent still carries whole-framebuffer pixels.
final class VNCScreenUIView: UIView, UIGestureRecognizerDelegate {
    let display = AVSampleBufferDisplayLayer()
    /// Holds the picture and cuts it to the part shown.
    private let clip = CALayer()
    private let pointerLayer = CALayer()
    weak var session: RFBSession?
    var mode: VNCMode = .mouse
    var interactive = false { didSet { gestures.forEach { $0.isEnabled = interactive } } }
    /// The part of the framebuffer shown, in pixels; nil: all of it. A new one starts unzoomed, with the pointer on it
    /// (moved to its middle when it was elsewhere): Trackpad Mode clicks and drags where the pointer is.
    var region: CGRect? {
        didSet {
            guard region != oldValue else { return }
            shown = nil
            stopCoast()
            if let s = session, s.phase == .live, !visible.contains(s.pointer) { s.move(to: CGPoint(x: visible.midX, y: visible.midY)) }
        }
    }
    private var gestures: [UIGestureRecognizer] = []
    /// Where the Mac's screen is drawn, in this view; nil: fitted (not zoomed).
    private var shown: CGRect?
    /// Two fingers turned into the wheel, and where it goes (a coast keeps going there after they let go).
    private var wheel = VNCWheel()
    private var wheelAt = CGPoint.zero
    private var coast: CADisplayLink?
    private var lastTap = Date.distantPast
    private var dragging = false
    private var lastPan = CGPoint.zero

    override init(frame: CGRect) {
        super.init(frame: frame)
        backgroundColor = .black
        clipsToBounds = true
        display.videoGravity = .resize
        clip.masksToBounds = true
        clip.addSublayer(display)
        layer.addSublayer(clip)
        pointerLayer.contentsGravity = .resizeAspect
        layer.addSublayer(pointerLayer)
        let tap = UITapGestureRecognizer(target: self, action: #selector(tapped(_:)))
        let twoTap = UITapGestureRecognizer(target: self, action: #selector(rightTapped(_:)))
        twoTap.numberOfTouchesRequired = 2
        let pan = UIPanGestureRecognizer(target: self, action: #selector(panned(_:)))
        pan.maximumNumberOfTouches = 1
        let twoPan = UIPanGestureRecognizer(target: self, action: #selector(twoPanned(_:)))
        twoPan.minimumNumberOfTouches = 2
        twoPan.maximumNumberOfTouches = 2
        let hold = UILongPressGestureRecognizer(target: self, action: #selector(held(_:)))
        hold.minimumPressDuration = 0.5
        let pinch = UIPinchGestureRecognizer(target: self, action: #selector(pinched(_:)))
        gestures = [tap, twoTap, pan, twoPan, hold, pinch]
        for g in gestures {
            g.delegate = self
            g.isEnabled = interactive
            addGestureRecognizer(g)
        }
        isAccessibilityElement = true
        accessibilityLabel = String(localized: "Mac's screen")
        accessibilityIdentifier = "vnc-screen"
    }

    required init?(coder: NSCoder) { fatalError() }

    func gestureRecognizer(_ g: UIGestureRecognizer, shouldRecognizeSimultaneouslyWith other: UIGestureRecognizer) -> Bool {
        (g is UIPinchGestureRecognizer && other is UIPanGestureRecognizer) || (other is UIPinchGestureRecognizer && g is UIPanGestureRecognizer)
    }

    // MARK: Where things are

    private var screenSize: CGSize {
        let s = session?.size ?? .zero
        return s.width > 0 ? s : CGSize(width: 16, height: 10)
    }

    /// The part of the framebuffer shown: the region while it lies on the framebuffer, else all of it.
    private var visible: CGRect {
        let whole = CGRect(origin: .zero, size: screenSize)
        guard let region, !region.isEmpty, whole.contains(region) else { return whole }
        return region
    }

    private var fitted: CGRect { AVMakeRect(aspectRatio: visible.size, insideRect: bounds) }
    private var rect: CGRect { shown ?? fitted }
    private var viewport: VNCViewport { VNCViewport(visible: visible, rect: rect) }

    /// A point of this view on the Mac's screen, kept on the part shown.
    func toScreen(_ p: CGPoint) -> CGPoint { viewport.clamped(viewport.toScreen(p)) }

    func toView(_ q: CGPoint) -> CGPoint { viewport.toView(q) }

    /// Where Trackpad Mode acts: the pointer, kept on the part shown.
    private func pointer(_ s: RFBSession) -> CGPoint { viewport.clamped(s.pointer) }

    override func layoutSubviews() {
        super.layoutSubviews()
        place()
    }

    /// The picture and the pointer where they go now.
    func place() {
        CATransaction.begin()
        CATransaction.setDisableActions(true)
        let port = viewport
        clip.frame = port.rect
        display.frame = port.framebufferFrame(screenSize)
        if let s = session {
            let at = toView(s.pointer)
            if let c = s.cursor, c.width > 0, let image = Self.image(c) {
                let scale = 1 / port.pixelsPerPoint
                pointerLayer.contents = image
                pointerLayer.frame = CGRect(x: at.x - CGFloat(c.hotX) * scale, y: at.y - CGFloat(c.hotY) * scale,
                                            width: CGFloat(c.width) * scale, height: CGFloat(c.height) * scale)
            } else {
                let side = Theme.Size.icon
                pointerLayer.contents = Self.arrow
                pointerLayer.frame = CGRect(x: at.x - side * 0.3, y: at.y - side * 0.15, width: side, height: side)
            }
            pointerLayer.isHidden = s.phase != .live || !port.visible.contains(s.pointer)
            #if DEBUG
            accessibilityValue = String(format: "fps=%.0f;latency=%.0f;mode=%@", s.fps, s.latency ?? -1, mode.rawValue) // UI tests measure
            #endif
        }
        CATransaction.commit()
    }

    private static let arrow: CGImage? = UIImage(systemName: Theme.Icon.pointer)?
        .withTintColor(.white, renderingMode: .alwaysOriginal)
        .applyingSymbolConfiguration(.init(pointSize: Theme.Size.icon, weight: .bold))?.cgImage

    private static func image(_ c: RFBClient.Cursor) -> CGImage? {
        guard let provider = CGDataProvider(data: Data(c.pixels) as CFData) else { return nil }
        return CGImage(width: c.width, height: c.height, bitsPerComponent: 8, bitsPerPixel: 32, bytesPerRow: c.width * 4,
                       space: CGColorSpaceCreateDeviceRGB(),
                       bitmapInfo: CGBitmapInfo(rawValue: CGImageAlphaInfo.premultipliedFirst.rawValue | CGBitmapInfo.byteOrder32Little.rawValue),
                       provider: provider, decode: nil, shouldInterpolate: false, intent: .defaultIntent)
    }

    // MARK: Touches

    @objc private func tapped(_ g: UITapGestureRecognizer) {
        guard let s = session else { return }
        switch mode {
        case .mouse: s.click(at: toScreen(g.location(in: self)))
        case .trackpad: s.click(at: pointer(s))
        }
        lastTap = Date()
    }

    @objc private func rightTapped(_ g: UITapGestureRecognizer) {
        guard let s = session else { return }
        s.click(at: mode == .mouse ? toScreen(g.location(in: self)) : pointer(s), button: 4)
    }

    @objc private func held(_ g: UILongPressGestureRecognizer) {
        guard g.state == .began, mode == .mouse, let s = session else { return }
        s.click(at: toScreen(g.location(in: self)), button: 4)
    }

    @objc private func panned(_ g: UIPanGestureRecognizer) {
        guard let s = session else { return }
        let p = g.location(in: self)
        switch (mode, g.state) {
        case (.mouse, .began):
            s.move(to: toScreen(p), buttons: 0)
            s.move(to: toScreen(p), buttons: 1)
        case (.mouse, .changed):
            s.move(to: toScreen(p), buttons: 1)
        case (.mouse, _):
            s.move(to: toScreen(p), buttons: 0)
        case (.trackpad, .began):
            lastPan = .zero
            dragging = Date().timeIntervalSince(lastTap) < 0.35 // a tap, then a drag: dragging
            if dragging { s.move(to: pointer(s), buttons: 1) }
        case (.trackpad, .changed):
            let t = g.translation(in: self)
            let port = viewport, speed = port.pixelsPerPoint * 1.4
            s.move(to: port.clamped(CGPoint(x: s.pointer.x + (t.x - lastPan.x) * speed, y: s.pointer.y + (t.y - lastPan.y) * speed)),
                   buttons: dragging ? 1 : 0)
            lastPan = t
        case (.trackpad, _):
            if dragging { s.move(to: s.pointer, buttons: 0) }
            dragging = false
        }
        place()
    }

    @objc private func twoPanned(_ g: UIPanGestureRecognizer) {
        guard let s = session else { return }
        let t = g.translation(in: self)
        if shown != nil, mode == .mouse { // zoomed in: two fingers move the picture
            var r = rect
            r.origin.x += t.x
            r.origin.y += t.y
            shown = clamp(r)
            g.setTranslation(.zero, in: self)
            place()
            return
        }
        let zoom = rect.width / fitted.width
        wheelAt = mode == .mouse ? toScreen(g.location(in: self)) : pointer(s)
        if g.state == .began { wheel.began() }
        let notches = wheel.moved(t.y, zoom: zoom)
        g.setTranslation(.zero, in: self)
        if notches != 0 { s.scroll(notches: notches, at: wheelAt) }
        if g.state == .ended {
            wheel.released(velocity: g.velocity(in: self).y, zoom: zoom)
            if wheel.coasting { startCoast() }
        }
    }

    private func startCoast() {
        coast?.invalidate()
        let link = CADisplayLink(target: self, selector: #selector(coasted(_:)))
        link.add(to: .main, forMode: .common)
        coast = link
    }

    @objc private func coasted(_ link: CADisplayLink) {
        let notches = wheel.tick(link.targetTimestamp - link.timestamp)
        if notches != 0 { session?.scroll(notches: notches, at: wheelAt) }
        if !wheel.coasting { stopCoast() }
    }

    /// A touch, or the view going away, stops a coast where it is.
    private func stopCoast() {
        wheel.stop()
        coast?.invalidate()
        coast = nil
    }

    override func touchesBegan(_ touches: Set<UITouch>, with event: UIEvent?) {
        stopCoast()
        super.touchesBegan(touches, with: event)
    }

    override func willMove(toWindow newWindow: UIWindow?) {
        super.willMove(toWindow: newWindow)
        if newWindow == nil { stopCoast() }
    }

    @objc private func pinched(_ g: UIPinchGestureRecognizer) {
        let anchor = g.location(in: self)
        let r = rect, base = fitted
        let width = min(max(r.width * g.scale, base.width), base.width * 5)
        g.scale = 1
        guard width > base.width * 1.01 else {
            shown = nil
            place()
            return
        }
        let k = width / r.width
        shown = clamp(CGRect(x: anchor.x - (anchor.x - r.minX) * k, y: anchor.y - (anchor.y - r.minY) * k,
                             width: width, height: r.height * k))
        place()
    }

    /// A zoomed picture kept over the view (centered on an axis it is narrower on).
    private func clamp(_ r: CGRect) -> CGRect {
        var r = r
        r.origin.x = r.width <= bounds.width ? (bounds.width - r.width) / 2 : min(0, max(bounds.width - r.width, r.minX))
        r.origin.y = r.height <= bounds.height ? (bounds.height - r.height) / 2 : min(0, max(bounds.height - r.height, r.minY))
        return r
    }

    /// Center Pointer.
    func centerPointer() {
        guard let s = session else { return }
        s.move(to: CGPoint(x: visible.midX, y: visible.midY))
        place()
    }
}

/// The Mac's screen in SwiftUI: its layer shown while this is up, touches when interactive.
struct VNCScreen: UIViewRepresentable {
    let session: RFBSession
    var mode: VNCMode = .mouse
    var interactive = false
    /// One of the Mac's displays to show alone (its frame on the framebuffer); nil: all of them.
    var region: CGRect? = nil
    /// The view, for the page's Center Pointer.
    var onView: (VNCScreenUIView) -> Void = { _ in }

    func makeUIView(context: Context) -> VNCScreenUIView {
        let v = VNCScreenUIView()
        v.session = session
        session.show(in: v.display)
        onView(v)
        return v
    }

    func updateUIView(_ v: VNCScreenUIView, context: Context) {
        v.mode = mode
        v.interactive = interactive
        v.region = region
        _ = (session.pointer, session.size, session.cursor?.width, session.phase, session.fps) // redrawn as they change
        v.place()
    }

    static func dismantleUIView(_ v: VNCScreenUIView, coordinator: ()) {
        v.session?.hide(from: v.display)
    }
}
#else
/// The Mac's screen on the Mac: its layer, aspect-fit; clicks and keys go to it when interactive (a click there, a
/// drag drags, the wheel scrolls).
final class VNCScreenNSView: NSView {
    let display = AVSampleBufferDisplayLayer()
    weak var session: RFBSession?
    var interactive = false
    var onClick: (() -> Void)?

    override init(frame: NSRect) {
        super.init(frame: frame)
        wantsLayer = true
        layer?.backgroundColor = NSColor.black.cgColor
        display.videoGravity = .resizeAspect
        layer?.addSublayer(display)
        setAccessibilityIdentifier("vnc-screen")
    }

    required init?(coder: NSCoder) { fatalError() }

    override func layout() {
        super.layout()
        display.frame = bounds
    }

    private var screenSize: CGSize { session?.size ?? CGSize(width: 16, height: 10) }

    private func toScreen(_ e: NSEvent) -> CGPoint {
        let p = convert(e.locationInWindow, from: nil)
        let r = AVMakeRect(aspectRatio: screenSize, insideRect: bounds)
        return CGPoint(x: (p.x - r.minX) / r.width * screenSize.width, y: (r.maxY - p.y) / r.height * screenSize.height)
    }

    override func mouseDown(with e: NSEvent) {
        guard interactive else { onClick?(); return }
        session?.move(to: toScreen(e), buttons: 1)
    }
    override func mouseDragged(with e: NSEvent) { if interactive { session?.move(to: toScreen(e), buttons: 1) } }
    override func mouseUp(with e: NSEvent) { if interactive { session?.move(to: toScreen(e), buttons: 0) } }
    override func mouseMoved(with e: NSEvent) { if interactive { session?.move(to: toScreen(e), buttons: 0) } }
    override func rightMouseDown(with e: NSEvent) { if interactive { session?.click(at: toScreen(e), button: 4) } }
    override func scrollWheel(with e: NSEvent) {
        guard interactive, abs(e.scrollingDeltaY) > 2 else { return }
        session?.scroll(notches: e.scrollingDeltaY > 0 ? 1 : -1, at: toScreen(e))
    }
    override var acceptsFirstResponder: Bool { interactive }
    override func keyDown(with e: NSEvent) {
        guard interactive, let s = session else { return }
        if let special = VNCKeys.keysym(macKeyCode: e.keyCode) { s.press(special, with: VNCKeys.modifiers(e.modifierFlags)) }
        else if let text = e.charactersIgnoringModifiers, !text.isEmpty {
            if e.modifierFlags.intersection([.command, .control, .option]).isEmpty { s.type(e.characters ?? text) }
            else { for ch in text.unicodeScalars { s.press(RFBSession.keysym(ch), with: VNCKeys.modifiers(e.modifierFlags)) } }
        }
    }

    override func updateTrackingAreas() {
        super.updateTrackingAreas()
        trackingAreas.forEach(removeTrackingArea)
        addTrackingArea(NSTrackingArea(rect: bounds, options: [.mouseMoved, .activeInKeyWindow, .inVisibleRect], owner: self))
    }
}

struct VNCScreen: NSViewRepresentable {
    let session: RFBSession
    var mode: VNCMode = .mouse
    var interactive = false
    var onClick: (() -> Void)? = nil

    func makeNSView(context: Context) -> VNCScreenNSView {
        let v = VNCScreenNSView()
        v.session = session
        session.show(in: v.display)
        return v
    }

    func updateNSView(_ v: VNCScreenNSView, context: Context) {
        v.interactive = interactive
        v.onClick = onClick
        if interactive { DispatchQueue.main.async { v.window?.makeFirstResponder(v) } }
    }

    static func dismantleNSView(_ v: VNCScreenNSView, coordinator: ()) { v.session?.hide(from: v.display) }
}
#endif

/// Keys by name, for the shortcut row and the Mac's keyboard.
enum VNCKeys {
    static let escape: UInt32 = 0xff1b, tab: UInt32 = 0xff09, enter: UInt32 = 0xff0d, backspace: UInt32 = 0xff08
    static let left: UInt32 = 0xff51, up: UInt32 = 0xff52, right: UInt32 = 0xff53, down: UInt32 = 0xff54

    #if os(macOS)
    static func keysym(macKeyCode code: UInt16) -> UInt32? {
        switch code {
        case 53: escape
        case 48: tab
        case 36, 76: enter
        case 51: backspace
        case 123: left
        case 124: right
        case 125: down
        case 126: up
        default: nil
        }
    }

    static func modifiers(_ f: NSEvent.ModifierFlags) -> [UInt32] {
        var out: [UInt32] = []
        if f.contains(.command) { out.append(RFBSession.command) }
        if f.contains(.option) { out.append(RFBSession.option) }
        if f.contains(.control) { out.append(RFBSession.control) }
        if f.contains(.shift) { out.append(RFBSession.shift) }
        return out
    }
    #endif
}
