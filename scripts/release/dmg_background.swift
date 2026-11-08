// The background of the Mac app's disk image window (scripts/release/mac_dmg.sh): warm paper from the app icon and a
// hand-drawn arrow from the app to Applications. No words: one picture can't follow the reader's language.
//
//   swift scripts/release/dmg_background.swift <out dir> <width> <height> <app x> <Applications x> <icon y>
//
// Sizes and positions are in points with the origin at the top left, as Finder lays out icons; the arrow runs between
// the two icon centers on row <icon y>. Writes background.png (1x) and background@2x.png into <out dir>.
import AppKit

let args = CommandLine.arguments.dropFirst().map { $0 }
guard args.count == 6, let width = Double(args[1]), let height = Double(args[2]), let appX = Double(args[3]),
      let linkX = Double(args[4]), let rowY = Double(args[5]) else {
    FileHandle.standardError.write("usage: dmg_background.swift <out dir> <width> <height> <app x> <Applications x> <icon y>\n".data(using: .utf8)!)
    exit(2)
}
let outDir = URL(fileURLWithPath: args[0], isDirectory: true)

// The app icon's palette (apple/Persona/Resources/Assets.xcassets/AppIcon.appiconset/icon.png): its paper, lighter at
// the top, and its ink, softened so the arrow guides without shouting.
func srgb(_ hex: UInt32, _ alpha: CGFloat = 1) -> NSColor {
    NSColor(srgbRed: CGFloat((hex >> 16) & 0xff) / 255, green: CGFloat((hex >> 8) & 0xff) / 255,
            blue: CGFloat(hex & 0xff) / 255, alpha: alpha)
}
let paperTop = srgb(0xF8F3EC)
let paperBottom = srgb(0xEFE5D9)
let ink = srgb(0x3A302A, 0.55)

// Clear of both 128 pt icons, with room on each side.
let iconHalf = 64.0, gap = 30.0
let start = CGPoint(x: appX + iconHalf + gap, y: rowY)
let end = CGPoint(x: linkX - iconHalf - gap, y: rowY)
let control = CGPoint(x: (start.x + end.x) / 2, y: rowY - 20)

func draw(scale: Int) -> Data {
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: Int(width) * scale, pixelsHigh: Int(height) * scale,
                               bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true, isPlanar: false,
                               colorSpaceName: .deviceRGB, bytesPerRow: 0, bitsPerPixel: 0)!
    rep.size = NSSize(width: width, height: height)  // points: 72 dpi at 1x, 144 at 2x
    NSGraphicsContext.saveGraphicsState()
    let context = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = context
    // Top-left origin, as Finder positions icons.
    context.cgContext.translateBy(x: 0, y: height)
    context.cgContext.scaleBy(x: 1, y: -1)

    NSGradient(starting: paperTop, ending: paperBottom)!
        .draw(in: NSRect(x: 0, y: 0, width: width, height: height), angle: 90)

    // A shallow arc, then an open head along the arc's final direction, like the strokes in the icon.
    let shaft = NSBezierPath()
    shaft.move(to: start)
    shaft.curve(to: end, controlPoint1: control, controlPoint2: control)
    let direction = atan2(end.y - control.y, end.x - control.x)
    let head = NSBezierPath()
    for side in [-1.0, 1.0] {
        let angle = direction + .pi - side * .pi / 5
        head.move(to: end)
        head.line(to: CGPoint(x: end.x + 15 * cos(angle), y: end.y + 15 * sin(angle)))
    }
    for path in [shaft, head] {
        path.lineWidth = 4
        path.lineCapStyle = .round
        path.lineJoinStyle = .round
    }
    ink.setStroke()
    shaft.stroke()
    head.stroke()

    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

try FileManager.default.createDirectory(at: outDir, withIntermediateDirectories: true)
try draw(scale: 1).write(to: outDir.appendingPathComponent("background.png"))
try draw(scale: 2).write(to: outDir.appendingPathComponent("background@2x.png"))
