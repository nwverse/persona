#if os(iOS)
import CoreGraphics
import Foundation
import ImageIO
import SwiftUI
import UniformTypeIdentifiers

/// The system call UI's icon (`ConversationManager.Configuration.iconTemplateImageData`): the assistant's mark as a
/// template, an alpha silhouette the system tints itself, `Theme.Size.callIconTemplate` points at 3× as Apple's
/// guidance for a call app's template asks. Mochi's body (the app icon's character), its outline included and its eyes
/// cut out, so it reads as Mochi even that small. Mochi stands for every look: a package's or an uploaded picture has no
/// outline of its own to cut. Plain Core Graphics, so it can be made anywhere; it is made once, with the manager.
nonisolated enum CallIcon {
    /// The icon's side in pixels.
    static var pixels: Int { Int(Theme.Size.callIconTemplate * Theme.Size.callIconTemplateScale) }

    /// The PNG, or nil if it could not be drawn (the call UI then shows no icon, as before).
    static func templatePNG() -> Data? {
        guard let image = template() else { return nil }
        let data = NSMutableData()
        guard let out = CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil) else { return nil }
        CGImageDestinationAddImage(out, image, nil)
        guard CGImageDestinationFinalize(out) else { return nil }
        return data as Data
    }

    /// The silhouette: black where Mochi is, clear everywhere else (the eyes too).
    static func template() -> CGImage? {
        let side = pixels
        guard let space = CGColorSpace(name: CGColorSpace.sRGB),
              let ctx = CGContext(data: nil, width: side, height: side, bitsPerComponent: 8, bytesPerRow: 0, space: space,
                                  bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue) else { return nil }
        let w = CGFloat(side)
        // Mochi's geometry has its origin at the top left, as UIKit's; Core Graphics' is at the bottom left.
        ctx.translateBy(x: 0, y: w)
        ctx.scaleBy(x: 1, y: -1)
        let body = Mochi.body(w).cgPath
        ctx.setFillColor(CGColor(gray: 0, alpha: 1))
        ctx.setStrokeColor(CGColor(gray: 0, alpha: 1))
        ctx.addPath(body)
        ctx.fillPath()
        ctx.setLineWidth(max(Mochi.strokeMin, w * Mochi.stroke))
        ctx.setLineJoin(.round)
        ctx.setLineCap(.round)
        ctx.addPath(body)
        ctx.strokePath()
        // The eyes, cut out: compact features, as the face draws them at a small size.
        ctx.setBlendMode(.clear)
        let r = Mochi.eyeRadius * Mochi.compactFeatures * w
        for eye in Mochi.eyes {
            ctx.fillEllipse(in: CGRect(x: eye.x * w - r, y: eye.y * w - r, width: 2 * r, height: 2 * r))
        }
        return ctx.makeImage()
    }
}
#endif
