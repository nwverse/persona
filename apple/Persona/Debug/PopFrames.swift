#if DEBUG && (os(iOS) || os(visionOS))
import UIKit

/// `-showPopFrames YES`: every display frame, where the lists, navigation bars and search bars are (window points),
/// logged when anything changed ("POPF …", `log stream --predicate 'eventMessage CONTAINS "POPF"'`). For going into a
/// chat and back frame by frame (#340, docs/lessons/ios.md「Do not toggle per-page clearance on navigation containers」).
@MainActor final class PopFrames: NSObject {
    static let shared = PopFrames()
    private var link: CADisplayLink?
    private var frame = 0
    private var last = ""

    static func startIfAsked() {
        guard UserDefaults.standard.bool(forKey: "showPopFrames") else { return }
        shared.start()
    }

    private func start() {
        guard link == nil else { return }
        let l = CADisplayLink(target: self, selector: #selector(tick))
        l.add(to: .main, forMode: .common)
        link = l
    }

    @objc private func tick() {
        frame += 1
        guard let window = UIApplication.shared.connectedScenes.compactMap({ $0 as? UIWindowScene })
            .flatMap(\.windows).first(where: \.isKeyWindow) else { return }
        var parts: [String] = []
        var stack: [UIView] = [window]
        while let v = stack.popLast() {
            stack.append(contentsOf: v.subviews)
            guard !v.isHidden, v.alpha > 0.01 else { continue }
            if let c = v as? UICollectionView, c.bounds.height > 300 {
                let f = c.convert(c.bounds, to: window)
                let top = c.visibleCells.map { $0.convert($0.bounds, to: window) }.min { $0.minY < $1.minY }
                let ip = c.visibleCells.min { $0.frame.minY < $1.frame.minY }.flatMap { c.indexPath(for: $0) }
                parts.append(String(format: "list%x x=%.0f y=%.0f off=%.1f in=%.1f row=%@ rowY=%.1f",
                                    UInt(bitPattern: ObjectIdentifier(c).hashValue) & 0xfff, f.minX, f.minY,
                                    c.contentOffset.y, c.adjustedContentInset.top,
                                    ip.map { "\($0.section).\($0.item)" } ?? "-", top?.minY ?? -1))
            } else if let c = v as? UIScrollView, !(c is UITextView), c.bounds.height > 300 {
                let f = c.convert(c.bounds, to: window)
                parts.append(String(format: "scroll%x x=%.0f y=%.0f off=%.1f in=%.1f",
                                    UInt(bitPattern: ObjectIdentifier(c).hashValue) & 0xfff, f.minX, f.minY,
                                    c.contentOffset.y, c.adjustedContentInset.top))
            } else if let b = v as? UINavigationBar {
                let f = b.convert(b.bounds, to: window)
                parts.append(String(format: "nav x=%.0f y=%.0f h=%.0f a=%.2f", f.minX, f.minY, f.height, b.alpha))
                if let box = b.superview {
                    let g = box.convert(box.bounds, to: window)
                    parts.append(String(format: "navbox y=%.0f safe=%.0f add=%.0f", g.minY, box.safeAreaInsets.top,
                                        (box.next as? UIViewController)?.additionalSafeAreaInsets.top ?? -1))
                }
            } else if let s = v as? UISearchBar {
                let f = s.convert(s.bounds, to: window)
                parts.append(String(format: "search x=%.0f y=%.0f h=%.0f a=%.2f", f.minX, f.minY, f.height, s.alpha))
            }
        }
        let line = parts.joined(separator: " | ")
        guard line != last else { return }
        last = line
        NSLog("POPF %d %.3f %@", frame, CACurrentMediaTime(), line)
    }
}
#endif
