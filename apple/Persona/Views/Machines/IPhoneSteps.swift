import Foundation

/// What the assistant's iphone_do does, in words (docs/extensions/iphone.md): "Tap item 3 → Type “hello” → Press Home".
nonisolated enum IPhoneSteps {
    static func describe(_ input: JSONValue) -> String? {
        guard let steps = input["steps"]?.array, !steps.isEmpty else { return nil }
        return steps.compactMap(step).joined(separator: " → ")
    }

    /// One step: {"action": "tap", "target": 3}, {"action": "type", "text": "…"}…; an action this app does not know
    /// reads as its name.
    static func step(_ step: JSONValue) -> String? {
        guard let action = step["action"]?.string, !action.isEmpty else { return nil }
        let target = targetName(step)
        switch action {
        case "tap": return String(localized: "Tap \(target)", comment: "An iPhone step: tap an element or a spot")
        case "double_tap": return String(localized: "Double-tap \(target)")
        case "long_press": return String(localized: "Long-press \(target)")
        case "swipe":
            switch step["direction"]?.string {
            case "up": return String(localized: "Swipe up")
            case "down": return String(localized: "Swipe down")
            case "left": return String(localized: "Swipe left")
            case "right": return String(localized: "Swipe right")
            default: return String(localized: "Swipe")
            }
        case "type":
            let text = ComputerSteps.short(step["text"]?.string ?? "")
            return String(localized: "Type “\(text)”")
        case "press":
            switch step["button"]?.string {
            case "home": return String(localized: "Press Home")
            case "lock": return String(localized: "Press the side button")
            case "volume_up": return String(localized: "Turn the volume up")
            case "volume_down": return String(localized: "Turn the volume down")
            default: return String(localized: "Press a button")
            }
        case "launch":
            let app = step["app"]?.string ?? "App"
            return String(localized: "Open \(app) [Git Hub Thread View eb8679]", comment: "A computer tool step: launch an app")
        case "wait": return String(localized: "Wait")
        default: return action
        }
    }

    /// What a step acts on: the numbered element it names, else a spot on the screen.
    static func targetName(_ step: JSONValue) -> String {
        if let n = step["target"]?.int { return String(localized: "item \(n)", comment: "An iPhone step's target: the numbered element of the last look") }
        if let s = step["target"]?.string, !s.isEmpty { return ComputerSteps.short(s) }
        return String(localized: "a spot", comment: "An unnamed spot on screen that a click acts on")
    }
}
