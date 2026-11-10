// What the runner does for the bridge, with public XCUIAutomation API only. Points are screen points, origin
// top-left, as on the wire. Runs on the main thread.

import XCTest

enum Ops {
    static let springboardID = "com.apple.springboard"
    static let springboard = XCUIApplication(bundleIdentifier: springboardID)

    /// The last issue XCTest recorded while an operation ran (Serve keeps the test from failing on it).
    static var issue: String?

    static func run(_ req: [String: Any]) -> [String: Any] {
        issue = nil
        do {
            let result = try perform(req)
            if let issue {
                return failure("internal", issue)
            }
            return ["ok": true, "result": result]
        } catch let e as OpError {
            return failure(e.code, e.message)
        } catch {
            return failure("internal", "\(error)")
        }
    }

    private static func failure(_ code: String, _ message: String) -> [String: Any] {
        ["ok": false, "error": ["code": code, "message": message]]
    }

    private static func perform(_ req: [String: Any]) throws -> Any {
        let op = req["op"] as? String ?? ""
        switch op {
        case "state":
            return ["orientation": orientation(), "app": describe(foreground(req))]
        case "ax_tree":
            return try axTree(req)
        case "tap":
            let at = try point(req)
            let count = req["count"] as? Int ?? 1
            switch count {
            case 2: at.doubleTap()
            default:
                for i in 0..<max(1, count) {
                    if i > 0 { Thread.sleep(forTimeInterval: 0.08) }
                    at.tap()
                }
            }
        case "long_press":
            try point(req).press(forDuration: seconds(req["duration_ms"], default: 800))
        case "drag":
            guard let from = req["from"] as? [String: Any], let to = req["to"] as? [String: Any] else {
                throw OpError("bad_request", "from and to are required")
            }
            let a = try point(from), b = try point(to)
            let duration = seconds(req["duration_ms"], default: 500)
            let hold = max(seconds(req["hold_ms"], default: 0), 0.05)
            let distance = hypot(num(to["x"]) - num(from["x"]), num(to["y"]) - num(from["y"]))
            let velocity = XCUIGestureVelocity(max(distance / max(duration, 0.05), 1))
            a.press(forDuration: hold, thenDragTo: b, withVelocity: velocity, thenHoldForDuration: 0)
        case "type":
            let app = try focused(req)
            app.typeText(req["text"] as? String ?? "")
        case "key":
            let app = try focused(req)
            let key = try keyText(req["key"] as? String ?? "")
            for _ in 0..<max(1, req["repeat"] as? Int ?? 1) {
                app.typeText(key)
            }
        case "home":
            XCUIDevice.shared.press(.home)
            // Some simulator runtimes ignore the synthesized Home press; bring the home screen up directly then.
            let front = foreground(req)
            if front.id != springboardID {
                Thread.sleep(forTimeInterval: 0.5)
                if foreground(req).id != springboardID {
                    springboard.activate()
                }
            }
        default:
            throw OpError("bad_request", "unknown op \(op)")
        }
        return [String: Any]()
    }

    // MARK: Apps

    /// The app in front: the first of the bridge's candidates (apps running on the simulator) that is, or
    /// SpringBoard when none is (the home screen).
    static func foreground(_ req: [String: Any]) -> (app: XCUIApplication, id: String) {
        for id in req["candidates"] as? [String] ?? [] where id != springboardID {
            let app = XCUIApplication(bundleIdentifier: id)
            if app.state == .runningForeground { return (app, id) }
        }
        return (springboard, springboardID)
    }

    private static func describe(_ front: (app: XCUIApplication, id: String)) -> [String: Any] {
        var d: [String: Any] = ["bundle_id": front.id]
        let name = front.app.label.trimmed
        if !name.isEmpty { d["name"] = name }
        return d
    }

    /// The app that has a focused text input, for typing.
    private static func focused(_ req: [String: Any]) throws -> XCUIApplication {
        let app = foreground(req).app
        let focus = app.descendants(matching: .any).matching(NSPredicate(format: "hasKeyboardFocus == YES")).firstMatch
        guard focus.exists else {
            throw OpError("bad_request", "nothing has keyboard focus: tap a text field first")
        }
        return app
    }

    private static func keyText(_ name: String) throws -> String {
        switch name {
        case "return": return XCUIKeyboardKey.return.rawValue
        case "backspace": return XCUIKeyboardKey.delete.rawValue
        case "delete": return XCUIKeyboardKey.forwardDelete.rawValue
        case "tab": return XCUIKeyboardKey.tab.rawValue
        case "escape": return XCUIKeyboardKey.escape.rawValue
        case "space": return XCUIKeyboardKey.space.rawValue
        case "up": return XCUIKeyboardKey.upArrow.rawValue
        case "down": return XCUIKeyboardKey.downArrow.rawValue
        case "left": return XCUIKeyboardKey.leftArrow.rawValue
        case "right": return XCUIKeyboardKey.rightArrow.rawValue
        default: throw OpError("bad_request", "unknown key \(name)")
        }
    }

    // MARK: Geometry

    private static func num(_ v: Any?) -> Double {
        (v as? NSNumber)?.doubleValue ?? .nan
    }

    private static func seconds(_ v: Any?, default ms: Double) -> TimeInterval {
        ((v as? NSNumber)?.doubleValue ?? ms) / 1000
    }

    /// A screen point as a coordinate on SpringBoard, which always runs and covers the screen.
    private static func point(_ d: [String: Any]) throws -> XCUICoordinate {
        let x = num(d["x"]), y = num(d["y"])
        guard x.isFinite, y.isFinite else { throw OpError("bad_request", "x and y are required") }
        return springboard.coordinate(withNormalizedOffset: .zero).withOffset(CGVector(dx: x, dy: y))
    }

    static func orientation() -> String {
        switch XCUIDevice.shared.orientation {
        case .portraitUpsideDown: return "portrait_upside_down"
        // The device turned left puts the Home side on the right.
        case .landscapeLeft: return "landscape_right"
        case .landscapeRight: return "landscape_left"
        default: return "portrait"
        }
    }

    // MARK: Accessibility tree

    private static func axTree(_ req: [String: Any]) throws -> [String: Any] {
        let front = foreground(req)
        let snapshot = try front.app.snapshot()
        let maxDepth = req["max_depth"] as? Int ?? 0
        let maxNodes = req["max_nodes"] as? Int ?? 2000
        let offscreen = req["include_offscreen"] as? Bool ?? false
        let screen = snapshot.frame
        var walk = Walk(maxDepth: maxDepth, maxNodes: maxNodes, offscreen: offscreen, screen: screen)
        var root = walk.node(snapshot, depth: 0, keep: true).first ?? [:]
        // A system alert (a permission prompt) belongs to SpringBoard, floats over the app and takes every
        // touch and the Home button until answered: add it after the app's own elements.
        if front.id != springboardID {
            let alerts = springboard.alerts
            for i in 0..<alerts.count {
                guard let s = try? alerts.element(boundBy: i).snapshot() else { continue }
                var children = root["children"] as? [[String: Any]] ?? []
                children += walk.node(s, depth: 1, keep: true)
                root["children"] = children
            }
        }
        return [
            "app": describe(front),
            "orientation": orientation(),
            "point_width": screen.width,
            "point_height": screen.height,
            "truncated": walk.truncated,
            "root": root,
        ]
    }

    struct Walk {
        let maxDepth: Int
        let maxNodes: Int
        let offscreen: Bool
        let screen: CGRect
        var count = 0
        var truncated = false

        /// The node, or its children in its place when it says nothing (an unlabelled container).
        mutating func node(_ s: XCUIElementSnapshot, depth: Int, keep: Bool = false) -> [[String: Any]] {
            if count >= maxNodes {
                truncated = true
                return []
            }
            if !offscreen && !s.frame.intersects(screen) && !keep {
                return []
            }
            var children: [[String: Any]] = []
            if maxDepth == 0 || depth < maxDepth {
                for c in s.children {
                    children += node(c, depth: depth + 1)
                }
            } else if !s.children.isEmpty {
                truncated = true
            }
            let value = ((s.value as? String) ?? (s.value as? NSNumber)?.stringValue ?? "").trimmed
            let label = s.label.trimmed
            let says = !label.isEmpty || !s.identifier.isEmpty || !value.isEmpty
            if !keep && (s.frame.isEmpty || (!says && s.elementType == .other)) {
                // Nothing to see or say here: an empty frame (an icon on another page) or a bare container.
                return children
            }
            count += 1
            var d: [String: Any] = [
                "id": "e\(count)",
                "role": role(s.elementType),
                "frame": [s.frame.minX, s.frame.minY, s.frame.width, s.frame.height],
                "children": children,
            ]
            if !label.isEmpty { d["label"] = label }
            if !value.isEmpty { d["value"] = value }
            if !s.identifier.isEmpty { d["identifier"] = s.identifier }
            if let p = s.placeholderValue, !p.isEmpty { d["hint"] = p }
            if !s.isEnabled { d["enabled"] = false }
            if s.isSelected { d["selected"] = true }
            return [d]
        }
    }

    static func role(_ t: XCUIElement.ElementType) -> String {
        switch t {
        case .application: return "application"
        case .window: return "window"
        case .button: return "button"
        case .staticText: return "staticText"
        case .textField: return "textField"
        case .secureTextField: return "secureTextField"
        case .searchField: return "searchField"
        case .textView: return "textView"
        case .cell: return "cell"
        case .image: return "image"
        case .icon: return "icon"
        case .switch: return "switch"
        case .toggle: return "toggle"
        case .slider: return "slider"
        case .link: return "link"
        case .key: return "keyboardKey"
        case .keyboard: return "keyboard"
        case .navigationBar: return "navigationBar"
        case .tabBar: return "tabBar"
        case .toolbar: return "toolbar"
        case .table: return "table"
        case .collectionView: return "collectionView"
        case .scrollView: return "scrollView"
        case .alert: return "alert"
        case .sheet: return "sheet"
        case .segmentedControl: return "segmentedControl"
        case .picker: return "picker"
        case .pickerWheel: return "pickerWheel"
        case .menu: return "menu"
        case .menuItem: return "menuItem"
        case .webView: return "webView"
        case .progressIndicator: return "progressIndicator"
        case .activityIndicator: return "activityIndicator"
        case .pageIndicator: return "pageIndicator"
        case .stepper: return "stepper"
        default: return "other"
        }
    }
}

struct OpError: Error {
    let code: String
    let message: String
    init(_ code: String, _ message: String) {
        self.code = code
        self.message = message
    }
}

private extension String {
    var trimmed: String { trimmingCharacters(in: .whitespacesAndNewlines) }
}
