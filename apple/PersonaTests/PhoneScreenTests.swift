import CoreGraphics
import Foundation
import Testing
@testable import Persona

/// Phone Screen on the phone (#9314): finding its display on a picture where displays have different scales, and the
/// session's start / windows / place / stop against a stand-in hub.
@MainActor
struct PhoneScreenTests {
    private let main = MachineDisplay(id: 1, x: 0, y: 0, width: 1920, height: 1080, main: true, scale: 1)

    private func phone(_ x: Double, _ y: Double, _ w: Double = 540, _ h: Double = 950) -> MachineDisplay {
        MachineDisplay(id: 23, x: x, y: y, width: w, height: h, scale: 2)
    }

    private func frame(_ ds: [VNCDisplay], _ id: String) -> CGRect? { ds.first { $0.id == id }?.frame }

    /// Measured on macOS 26.6 (docs/extensions/computer-use.md "Phone Screen"): a 2× display beside a 1× main one is
    /// drawn at its own pixels, the edge touching main at main's pixel coordinate. Every placement measured comes out at
    /// the framebuffer's size and puts the display where Screen Sharing drew it.
    @Test func displaysOfDifferentScalesLieWhereScreenSharingDrawsThem() {
        // Right of main, 540 × 1021 pt: framebuffer 3000 × 2042.
        var ds = VNCDisplays.layout(server: [], machine: [main, phone(1920, 0, 540, 1021)], framebuffer: CGSize(width: 3000, height: 2042))
        #expect(frame(ds, "display-23") == CGRect(x: 1920, y: 0, width: 1080, height: 2042))
        #expect(frame(ds, "display-1") == CGRect(x: 0, y: 0, width: 1920, height: 1080))
        // Left of main: 3000 × 1900, main moved right by the display's pixels.
        ds = VNCDisplays.layout(server: [], machine: [main, phone(-540, 0)], framebuffer: CGSize(width: 3000, height: 1900))
        #expect(frame(ds, "display-23") == CGRect(x: 0, y: 0, width: 1080, height: 1900))
        #expect(frame(ds, "display-1") == CGRect(x: 1080, y: 0, width: 1920, height: 1080))
        // Below: 1920 × 2980. Above: 1920 × 2980, main under it. Offset right by 130 pt: 3000 × 2030.
        ds = VNCDisplays.layout(server: [], machine: [main, phone(0, 1080)], framebuffer: CGSize(width: 1920, height: 2980))
        #expect(frame(ds, "display-23") == CGRect(x: 0, y: 1080, width: 1080, height: 1900))
        ds = VNCDisplays.layout(server: [], machine: [main, phone(0, -950)], framebuffer: CGSize(width: 1920, height: 2980))
        #expect(frame(ds, "display-23") == CGRect(x: 0, y: 0, width: 1080, height: 1900))
        #expect(frame(ds, "display-1") == CGRect(x: 0, y: 1900, width: 1920, height: 1080))
        ds = VNCDisplays.layout(server: [], machine: [main, phone(1920, 130)], framebuffer: CGSize(width: 3000, height: 2030))
        #expect(frame(ds, "display-23") == CGRect(x: 1920, y: 130, width: 1080, height: 1900))
    }

    /// The rule is trusted only when it explains the picture: another size (the display not drawn yet), or a display
    /// without a scale (an older node), offers nothing rather than a wrong crop.
    @Test func aLayoutThatDoesNotExplainThePictureOffersNothing() {
        #expect(VNCDisplays.layout(server: [], machine: [main, phone(1920, 0)], framebuffer: CGSize(width: 1920, height: 1080)).isEmpty)
        var old = phone(1920, 0)
        old.scale = 0
        #expect(VNCDisplays.anchored([main, old], framebuffer: CGSize(width: 3000, height: 1900)).isEmpty)
    }

    /// Scrolling on the phone display goes by its own points (2 pixels each), not the picture's average.
    @Test func theShownDisplaysScaleIsItsOwn() {
        let ds = [main, phone(1920, 0)], fb = CGSize(width: 3000, height: 1900)
        #expect(VNCDisplays.pixelsPerMacPoint(ds, framebuffer: fb, shown: "display-23") == 2)
        #expect(VNCDisplays.pixelsPerMacPoint(ds, framebuffer: fb, shown: "display-1") == 1)
        #expect(VNCDisplays.pixelsPerMacPoint(ds, framebuffer: fb) == 1)
    }

    @Test func displaysAndPhoneScreenDecode() throws {
        let json = #"{"enabled":true,"screen_sharing":true,"displays":[{"id":1,"x":0,"y":0,"width":1920,"height":1080,"main":true,"scale":1}],"phone_screen":{"version":1,"permission":"granted"}}"#
        let c = try JSONDecoder().decode(MachineComputer.self, from: Data(json.utf8))
        #expect(c.phoneScreen?.offered == true && c.phoneScreen?.granted == true)
        #expect(c.displays.first?.scale == 1)
        let old = try JSONDecoder().decode(MachineComputer.self, from: Data(#"{"enabled":true,"displays":[{"id":1,"x":0,"y":0,"width":10,"height":10}]}"#.utf8))
        #expect(old.phoneScreen == nil && old.displays.first?.scale == 0)
    }

    /// Start once per connection and shape, the window list and a placement, then stop; a new connection starts again.
    @Test func theSessionFollowsTheConnection() async throws {
        var asked: [PhoneScreenRequest] = []
        let display = phone(1920, 0, 540, 1021)
        let w = PhoneScreenWindow(pid: 10, window: 3, app: "Terminal", bundle: nil, title: "zsh", minimized: nil, placed: nil)
        let link = PhoneScreenLink(ref: MachineRef(group: "g", id: "mac")) { req in
            asked.append(req)
            switch req.op {
            case "start": return PhoneScreenReply(display: display, displays: [self.main, display])
            case "windows": return PhoneScreenReply(windows: [w])
            case "place": return PhoneScreenReply(display: display, placed: w)
            default: return PhoneScreenReply()
            }
        }
        link.start(pair: "P", size: CGSize(width: 402, height: 760))
        try await until { link.phase == .on }
        #expect(link.display == display && link.displays.count == 2)
        link.start(pair: "P", size: CGSize(width: 402, height: 760)) // the same again: nothing asked
        await link.loadWindows()
        #expect(link.windows == [w])
        #expect(await link.place(w))
        #expect(link.placed == w)
        link.start(pair: "R", size: CGSize(width: 402, height: 760)) // reconnected: the Mac ended the old one
        try await until { asked.filter { $0.op == "start" }.count == 2 && link.phase == .on }
        #expect(link.placed == nil)
        link.stop()
        try await until { asked.last?.op == "stop" }
        #expect(link.phase == .off)
        #expect(asked.map(\.op) == ["start", "windows", "place", "start", "stop"])
        #expect(asked[0].width == 402 && asked[0].height == 760 && asked[2].pid == 10 && asked[2].window == 3 && asked[4].pair == "R")
    }

    /// A refusal shows why in plain words and leaves the mode off on the Mac.
    @Test func aRefusalSaysWhy() async throws {
        let link = PhoneScreenLink(ref: MachineRef(group: "g", id: "mac")) { _ in
            throw APIError(status: 403, message: "no", fields: ["code": "permission"])
        }
        link.start(pair: "P", size: CGSize(width: 402, height: 760))
        try await until { if case .failed = link.phase { true } else { false } }
        #expect(link.phase == .failed(PhoneScreenLink.message(APIError(status: 403, message: "no", fields: ["code": "permission"]))))
    }

    private func until(_ ok: @escaping () -> Bool) async throws {
        for _ in 0..<200 where !ok() { try await Task.sleep(for: .milliseconds(10)) }
        #expect(ok())
    }
}
