import AVFoundation
import CoreMedia
import CoreVideo
import Observation
import SwiftUI

/// One Mac's VNC (#1343): the native client (RFBClient) over the hub's relay, signed in with the sign-in this device
/// keeps (ScreenCredentials, #1322) or the one typed in, and its frames for every layer that shows them (the page's,
/// the picture-in-picture's). One per Mac at a time (`VNC.session`); it goes when nothing shows it any more.
@MainActor @Observable final class RFBSession {
    enum Phase: Equatable {
        case connecting
        /// The Mac wants its account and password: none kept, or the kept one refused (failed says so).
        case signIn(failed: String?)
        case live
        case closed(String?)
    }

    let ref: MachineRef
    private(set) var phase: Phase = .connecting
    /// The Mac's screen, in its pixels.
    private(set) var size: CGSize = .zero
    /// The server's screens on it (ExtendedDesktopSize, #80); empty from a server that sends none (macOS's, as far as is known).
    /// Kept across reconnections until the next list, so a chosen display does not flash to all of them.
    private(set) var screens: [RFBClient.Screen] = []
    /// Where the pointer is, on the Mac's screen (the last one sent; the middle before any).
    private(set) var pointer: CGPoint = .zero
    private(set) var cursor: RFBClient.Cursor?
    /// What the Mac last put on its clipboard (ServerCutText).
    private(set) var macClipboard: String?
    /// Frames a second shown, and how long the last one took from the mock's Screen Sharing to here (its stamp).
    private(set) var fps: Double = 0
    private(set) var latency: Double?
    /// A frame of this connection has been shown: the small window shows it instead of its card (VNCPiPCard).
    private(set) var pictured = false
    /// When the last frame of this connection was copied out (not observed: read when it matters), and which
    /// connection this is (a new one each run): a check made on one connection is not answered by the next (#4380).
    @ObservationIgnored private(set) var lastFrame: Date?
    @ObservationIgnored private(set) var connection = 0

    @ObservationIgnored private var client: RFBClient?
    @ObservationIgnored private let sink = RFBFrameSink()
    @ObservationIgnored private var asked: CheckedContinuation<RFBClient.Credentials?, Never>?
    @ObservationIgnored private var typed: (RFBClient.Credentials, remember: Bool)?
    @ObservationIgnored private var refusedKept = false
    @ObservationIgnored private var buttons: UInt8 = 0
    /// Keys and pastes in the order they were made (#2374): a paste waits on the hub, so what follows waits behind it.
    @ObservationIgnored private var input: RFBInputQueue!

    init(ref: MachineRef) {
        self.ref = ref
        input = RFBInputQueue(send: { [weak self] e in self?.client?.key(e.keysym, down: e.down) },
                              write: { [weak self] text in
                                  // No connection: its ⌘V could not go out, so the paste counts as failed.
                                  guard let self, self.client != nil else { return false }
                                  return await self.setMacClipboard(text)
                              })
    }

    func start() {
        guard client == nil, let api = AppModel.shared.api(ref.group) else {
            if AppModel.shared.api(ref.group) == nil { phase = .closed(String(localized: "Can't reach the hub")) }
            return
        }
        run(RFBClient(transport: RFBWebSocket(api: api, machine: ref.id)) { [weak self] in await self?.credentials() })
    }

    /// Follows this client and starts it: start's over the hub's relay, or a test's over a scripted transport.
    func run(_ c: RFBClient) {
        c.onState = { [weak self] s in Task { @MainActor in self?.state(s) } }
        c.onCutText = { [weak self] text in Task { @MainActor in self?.macClipboard = text } }
        c.onCursor = { [weak self] cursor in Task { @MainActor in self?.cursor = cursor } }
        c.onResize = { [weak self] w, h in Task { @MainActor in self?.resized(width: w, height: h) } }
        c.onScreens = { [weak self] screens in Task { @MainActor in self?.screens = screens } }
        sink.attach(c) { [weak self] fps, latency in
            // Stamped here, when the frame was copied: a report still queued from before a suspension keeps its time.
            let at = Date.now
            Task { @MainActor in
                self?.fps = fps
                self?.latency = latency
                self?.lastFrame = at
                if self?.pictured == false { self?.pictured = true }
            }
        }
        client = c
        phase = .connecting
        pictured = false
        lastFrame = nil
        connection += 1
        c.start()
    }

    func close() {
        sink.detach()
        client?.close()
        client = nil
        asked?.resume(returning: nil)
        asked = nil
        if case .closed = phase {} else { phase = .closed(nil) }
    }

    /// Shown in this layer from now on (until it goes).
    func show(in layer: AVSampleBufferDisplayLayer, timed: Bool = false) { sink.add(layer, timed: timed) }
    func hide(from layer: AVSampleBufferDisplayLayer) { sink.remove(layer) }

    // MARK: Signing in

    /// What the client asks when the Mac wants a sign-in: the kept one first, else wait for the form.
    private func credentials() async -> RFBClient.Credentials? {
        if !refusedKept, let kept = ScreenCredentials.load(ref) {
            typed = nil
            return .init(account: kept.account, password: kept.password)
        }
        if let t = typed { return t.0 }
        return await withCheckedContinuation { k in
            asked = k
            phase = .signIn(failed: refusedKept ? String(localized: "The saved password no longer works (the Mac's password may have changed). Enter it again; it replaces the saved one once connected.") : nil)
        }
    }

    /// The form's account and password; `remember`: kept on this device once the Mac takes them.
    func signIn(account: String, password: String, remember: Bool) {
        let c = RFBClient.Credentials(account: account, password: password)
        typed = (c, remember)
        phase = .connecting
        if let k = asked {
            asked = nil
            k.resume(returning: c)
        } else {
            restart()
        }
    }

    /// The whole screen asked for again: a live connection answers with a frame (#4380).
    func refresh() { client?.refresh() }

    /// A new connection in place of this one, even while it still says live (it went quiet, #4380).
    func reconnect() { restart() }

    private func restart() {
        sink.detach()
        client?.close()
        client = nil
        start()
    }

    private func state(_ s: RFBClient.State) {
        switch s {
        case .connecting, .signingIn:
            if case .signIn = phase {} else { phase = .connecting }
        case let .live(width, height, _):
            size = CGSize(width: width, height: height)
            if pointer == .zero { pointer = CGPoint(x: width / 2, y: height / 2) }
            phase = .live
            if let t = typed {
                if t.remember { ScreenCredentials.save(.init(account: t.0.account, password: t.0.password), for: ref) } else { ScreenCredentials.forget(ref) }
                typed = nil
            }
            refusedKept = false
        case .refused(let why):
            if typed == nil, ScreenCredentials.load(ref) != nil, !refusedKept {
                refusedKept = true // the kept one: ask for a new one, on a new connection
                restart()
                return
            }
            typed = nil
            phase = .signIn(failed: why.isEmpty || why.contains("Authentication") ? String(localized: "Wrong account or password") : String(localized: "Couldn't sign in: \(why)"))
            client = nil
        case .failed(let why):
            if case .closed = phase { return }
            phase = .closed(why)
            client = nil
        case .closed:
            break
        }
    }

    /// The Mac's screen took a new size mid-session (DesktopSize, #2372): the geometry follows it and the pointer is
    /// kept on it, without sending anything (the next move sends where it is).
    private func resized(width: Int, height: Int) {
        size = CGSize(width: width, height: height)
        pointer = Self.clamped(pointer, to: size)
    }

    // MARK: Pointer, keys, clipboard

    /// p kept on a screen of this size (its last pixel at most).
    nonisolated static func clamped(_ p: CGPoint, to size: CGSize) -> CGPoint {
        CGPoint(x: min(max(0, p.x), max(0, size.width - 1)), y: min(max(0, p.y), max(0, size.height - 1)))
    }

    /// The pointer to p on the Mac's screen, with these buttons down.
    func move(to p: CGPoint, buttons: UInt8? = nil) {
        let q = Self.clamped(p, to: size)
        pointer = q
        if let buttons { self.buttons = buttons }
        client?.pointer(x: Int(q.x), y: Int(q.y), buttons: self.buttons)
    }

    func click(at p: CGPoint, button: UInt8 = 1) {
        move(to: p, buttons: 0)
        move(to: p, buttons: button)
        move(to: p, buttons: 0)
    }

    /// The wheel: up (positive) or down, a notch at a time.
    func scroll(notches: Int, at p: CGPoint) {
        let b: UInt8 = notches > 0 ? 8 : 16
        for _ in 0..<min(abs(notches), 20) {
            move(to: p, buttons: buttons | b)
            move(to: p, buttons: buttons & ~b)
        }
    }

    func key(_ keysym: UInt32, down: Bool) { input.keys([(keysym, down)]) }

    /// A key pressed and let go, with these modifiers held around it.
    func press(_ keysym: UInt32, with modifiers: [UInt32] = []) { input.press(keysym, with: modifiers) }

    /// Text typed: Latin-1 a key at a time; anything else (Chinese from the input method) by the Mac's clipboard and
    /// ⌘V, through its node (`computer/clipboard`); in the order typed (#2374). A paste whose clipboard write fails
    /// is left out (no ⌘V), with one toast for a run of failures.
    func type(_ text: String) {
        input.type(text) { [weak self] in
            guard let self else { return }
            let name = AppModel.shared.machine(self.ref)?.name ?? self.ref.id
            AppModel.shared.toast = Toast(String(localized: "Couldn't write to \(name)'s clipboard; some text wasn't typed"), style: .error)
        }
    }

    nonisolated static func keysym(_ ch: Unicode.Scalar) -> UInt32 {
        switch ch {
        case "\n", "\r": return 0xff0d
        case "\t": return 0xff09
        default: return ch.value < 256 ? ch.value : 0x0100_0000 + ch.value
        }
    }

    /// Text onto the Mac's clipboard, then ⌘V there, after the keys and pastes before it; false (and no ⌘V) when the
    /// clipboard did not take it.
    @discardableResult func paste(_ text: String) async -> Bool { await input.paste(text).value }

    /// Onto the Mac's clipboard: Latin-1 through RFB (sent, with no receipt: true means only that it went out in
    /// order); anything else through its node. False when it could not be: no connection, no hub, or the node failed.
    func setMacClipboard(_ text: String) async -> Bool {
        if text.unicodeScalars.allSatisfy({ $0.value < 256 }) {
            guard let client else { return false }
            client.cutText(text)
            return true
        }
        guard let api = AppModel.shared.api(ref.group) else { return false }
        do {
            try await api.setComputerClipboard(machine: ref.id, text)
            return true
        } catch {
            return false
        }
    }

    /// The Mac's clipboard now: through its node, else what it last sent.
    func readMacClipboard() async -> String? {
        if let text = try? await AppModel.shared.api(ref.group)?.computerClipboard(machine: ref.id), !text.isEmpty { return text }
        return macClipboard
    }

    /// The keysyms of the modifiers (macOS's Screen Sharing: ⌘ as Super, ⌥ as Alt).
    static let command: UInt32 = 0xffeb, option: UInt32 = 0xffe9, control: UInt32 = 0xffe3, shift: UInt32 = 0xffe1
}

/// Every Mac's VNC session (one each), and how many hold it (the page, the small window): closed a moment after the
/// last lets go, so passing it from one to the other keeps the connection.
@MainActor enum VNC {
    private static var sessions: [MachineRef: (session: RFBSession, holders: Int)] = [:]
    private static var closing: [MachineRef: Task<Void, Never>] = [:]

    static func hold(_ ref: MachineRef) -> RFBSession {
        closing.removeValue(forKey: ref)?.cancel()
        if let s = sessions[ref] {
            sessions[ref] = (s.session, s.holders + 1)
            if case .closed = s.session.phase { s.session.start() }
            return s.session
        }
        let s = RFBSession(ref: ref)
        sessions[ref] = (s, 1)
        s.start()
        return s
    }

    static func release(_ ref: MachineRef) {
        guard let s = sessions[ref] else { return }
        sessions[ref] = (s.session, max(0, s.holders - 1))
        guard s.holders <= 1 else { return }
        closing[ref] = Task {
            try? await Task.sleep(for: .seconds(2))
            guard !Task.isCancelled, let now = sessions[ref], now.holders == 0 else { return }
            sessions[ref] = nil
            closing[ref] = nil
            now.session.close()
        }
    }

    static func current(_ ref: MachineRef) -> RFBSession? { sessions[ref]?.session }
}

nonisolated private struct SampleBox: @unchecked Sendable { let sample: CMSampleBuffer }
nonisolated private struct LayersBox: @unchecked Sendable { let layers: [AVSampleBufferDisplayLayer] }

/// The frames out of the client into the layers that show them: a CVPixelBuffer copied from the framebuffer at most
/// 30 times a second (a last copy after a burst, so nothing is left out), as a sample buffer to display at once.
nonisolated final class RFBFrameSink: @unchecked Sendable {
    private let lock = NSLock()
    private let queue = DispatchQueue(label: "persona.vnc.frames")
    private weak var client: RFBClient?
    private var layers = NSHashTable<AVSampleBufferDisplayLayer>.weakObjects()
    /// Layers with a timebase of their own (the small window's source, VNCPiP): their samples carry a time on the host
    /// clock instead of DisplayImmediately, which AVFoundation advises against mixing with a timebase.
    private var timed = NSHashTable<AVSampleBufferDisplayLayer>.weakObjects()
    private var dirty = false, scheduled = false
    private var last = Date.distantPast
    private var pool: CVPixelBufferPool?
    private var poolSize = (0, 0)
    private var shown: [Date] = []
    private var report: (@Sendable (Double, Double?) -> Void)?
    private var latest: CVPixelBuffer?

    func attach(_ client: RFBClient, report: @escaping @Sendable (Double, Double?) -> Void) {
        lock.withLock {
            self.client = client
            self.report = report
        }
        client.onFrame = { [weak self] _, _ in self?.changed() }
    }

    func detach() { lock.withLock { client = nil } }

    func add(_ layer: AVSampleBufferDisplayLayer, timed isTimed: Bool = false) {
        let frame = lock.withLock { () -> CVPixelBuffer? in
            (isTimed ? timed : layers).add(layer)
            return latest
        }
        if let frame, let sample = Self.sample(frame, immediate: !isTimed) {
            let box = SampleBox(sample: sample), one = LayersBox(layers: [layer])
            Task { @MainActor in for l in one.layers { Self.enqueue(box.sample, in: l) } }
        }
    }

    func remove(_ layer: AVSampleBufferDisplayLayer) {
        lock.withLock {
            layers.remove(layer)
            timed.remove(layer)
        }
    }

    private func changed() {
        lock.withLock {
            dirty = true
            guard !scheduled else { return }
            scheduled = true
            let wait = max(0, 1.0 / 30 - Date().timeIntervalSince(last))
            queue.asyncAfter(deadline: .now() + wait) { [weak self] in self?.copy() }
        }
    }

    private func copy() {
        guard let client = lock.withLock({ () -> RFBClient? in
            scheduled = false
            dirty = false
            last = Date()
            return self.client
        }) else { return }
        var stamp: Double?
        let buffer: CVPixelBuffer? = client.withFramebuffer { fb in
            guard fb.width > 1, let pb = pixelBuffer(fb.width, fb.height) else { return nil }
            CVPixelBufferLockBaseAddress(pb, [])
            if let dst = CVPixelBufferGetBaseAddress(pb) {
                let row = CVPixelBufferGetBytesPerRow(pb)
                if row == fb.bytesPerRow {
                    memcpy(dst, fb.pixels, fb.bytesPerRow * fb.height)
                } else {
                    for y in 0..<fb.height { memcpy(dst + y * row, fb.pixels + y * fb.bytesPerRow, fb.bytesPerRow) }
                }
            }
            CVPixelBufferUnlockBaseAddress(pb, [])
            stamp = Self.stamp(fb)
            return pb
        }
        guard let buffer, let sample = Self.sample(buffer, immediate: true) else { return }
        let (targets, timedTargets, report, fps) = lock.withLock {
            () -> ([AVSampleBufferDisplayLayer], [AVSampleBufferDisplayLayer], (@Sendable (Double, Double?) -> Void)?, Double) in
            latest = buffer
            let now = Date()
            shown.append(now)
            shown.removeAll { now.timeIntervalSince($0) > 1 }
            return (layers.allObjects, timed.allObjects, self.report, Double(shown.count))
        }
        let latency = stamp.map { Date().timeIntervalSince1970 * 1000 - $0 }
        report?(fps, latency.flatMap { $0 >= 0 && $0 < 60_000 ? $0 : nil })
        let box = SampleBox(sample: sample), onScreen = LayersBox(layers: targets)
        Task { @MainActor in for l in onScreen.layers { Self.enqueue(box.sample, in: l) } }
        if !timedTargets.isEmpty, let atTime = Self.sample(buffer, immediate: false) {
            let box = SampleBox(sample: atTime), small = LayersBox(layers: timedTargets)
            Task { @MainActor in for l in small.layers { Self.enqueue(box.sample, in: l) } }
        }
        if lock.withLock({ dirty && !scheduled }) { changed() }
    }

    /// The mock's Screen Sharing stamps when it sent a frame (MOCK_SCREEN_ANIMATE) in its first two pixels: a marker
    /// (pure magenta) then the low 24 bits of its clock in milliseconds; nil on any other screen.
    private static func stamp(_ fb: RFBFramebuffer) -> Double? {
        let m = fb.pixel(0, 0)
        guard m[0] == 255, m[1] == 0, m[2] == 255 else { return nil }
        let t = fb.pixel(1, 0)
        let low = Double(Int(t[2]) << 16 | Int(t[1]) << 8 | Int(t[0]))
        let now = Date().timeIntervalSince1970 * 1000
        let base = now - now.truncatingRemainder(dividingBy: 16_777_216)
        let sent = base + low
        return sent > now ? sent - 16_777_216 : sent
    }

    private func pixelBuffer(_ w: Int, _ h: Int) -> CVPixelBuffer? {
        if pool == nil || poolSize != (w, h) {
            let attrs: [String: Any] = [kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA,
                                        kCVPixelBufferWidthKey as String: w, kCVPixelBufferHeightKey as String: h,
                                        kCVPixelBufferIOSurfacePropertiesKey as String: [:]]
            CVPixelBufferPoolCreate(nil, nil, attrs as CFDictionary, &pool)
            poolSize = (w, h)
        }
        var pb: CVPixelBuffer?
        guard let pool, CVPixelBufferPoolCreatePixelBuffer(nil, pool, &pb) == kCVReturnSuccess else { return nil }
        return pb
    }

    /// A sample of the frame at this moment on the host clock (rising from one to the next); `immediate`: shown at once
    /// whatever the layer's time (the page's), else when its timebase reaches it (the small window's, at once too).
    private static func sample(_ pb: CVPixelBuffer, immediate: Bool) -> CMSampleBuffer? {
        var format: CMVideoFormatDescription?
        guard CMVideoFormatDescriptionCreateForImageBuffer(allocator: nil, imageBuffer: pb, formatDescriptionOut: &format) == noErr,
              let format else { return nil }
        var timing = CMSampleTimingInfo(duration: CMTime(value: 1, timescale: 30), presentationTimeStamp: CMClockGetTime(CMClockGetHostTimeClock()),
                                        decodeTimeStamp: .invalid)
        var sample: CMSampleBuffer?
        guard CMSampleBufferCreateReadyWithImageBuffer(allocator: nil, imageBuffer: pb, formatDescription: format,
                                                       sampleTiming: &timing, sampleBufferOut: &sample) == noErr, let sample else { return nil }
        if immediate, let attachments = CMSampleBufferGetSampleAttachmentsArray(sample, createIfNecessary: true),
           CFArrayGetCount(attachments) > 0 {
            let dict = unsafeBitCast(CFArrayGetValueAtIndex(attachments, 0), to: CFMutableDictionary.self)
            CFDictionarySetValue(dict, Unmanaged.passUnretained(kCMSampleAttachmentKey_DisplayImmediately).toOpaque(),
                                 Unmanaged.passUnretained(kCFBooleanTrue).toOpaque())
        }
        return sample
    }

    /// A picture of the app's own into a timed layer (the small window's card, VNCPiPCard), at this moment on the host
    /// clock: the client's next frame, later, replaces it.
    @MainActor static func show(_ pb: CVPixelBuffer, in layer: AVSampleBufferDisplayLayer) {
        if let sample = sample(pb, immediate: false) { enqueue(sample, in: layer) }
    }

    @MainActor private static func enqueue(_ sample: CMSampleBuffer, in layer: AVSampleBufferDisplayLayer) {
        let r = layer.sampleBufferRenderer
        if r.status == .failed { r.flush() }
        r.enqueue(sample)
    }
}
