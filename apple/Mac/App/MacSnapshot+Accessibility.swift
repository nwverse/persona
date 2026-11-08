#if DEBUG
import AppKit

extension MacSnapshot {
    struct AccessibilityEntry {
        var identifier: String?
        var label: String?
        var value: String?
        var frame: NSRect?
    }

    /// Read the actual window's accessibility tree to wait for content, not just a fixed capture delay.
    static func accessibilityEntries(_ root: NSObject) -> [AccessibilityEntry] {
        var visited: Set<ObjectIdentifier> = []
        var result: [AccessibilityEntry] = []
        func read(_ object: NSObject, _ selector: String) -> AnyObject? {
            let selector = NSSelectorFromString(selector)
            guard object.responds(to: selector) else { return nil }
            return object.perform(selector)?.takeUnretainedValue()
        }
        func visit(_ object: NSObject, depth: Int) {
            guard depth < 40, visited.insert(ObjectIdentifier(object)).inserted else { return }
            let frame = object.responds(to: NSSelectorFromString("accessibilityFrame"))
                ? (object.value(forKey: "accessibilityFrame") as? NSValue)?.rectValue : nil
            result.append(AccessibilityEntry(identifier: read(object, "accessibilityIdentifier") as? String,
                                             label: read(object, "accessibilityLabel") as? String,
                                             value: (read(object, "accessibilityValue") as? String) ?? (read(object, "accessibilityValueDescription") as? String), frame: frame))
            for child in read(object, "accessibilityChildren") as? [NSObject] ?? [] { visit(child, depth: depth + 1) }
        }
        visit(root, depth: 0)
        return result
    }
}
#endif
