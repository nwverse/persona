import Foundation

/// Text made safe to go inside a `<persona-context>` block: every literal opening or closing context tag has its `<`
/// written as `&lt;` so nothing inside the body can close the block, trigger snapshot stripping, or be rewritten by
/// compatibility adapters, while readers can restore the original text losslessly. The same output as the hub's
/// `prompts.ContextBody`, the web's `contextBody`, and Android's `contextBody`.
nonisolated enum ContextText {
    /// What a reader may take as the end of a block: any case, spaces around the slash and before `>`.
    static let closingTag = try! NSRegularExpression(
        pattern: #"<(\s*/\s*"# + HubNaming.contextPattern + #"\s*>)"#, options: [.caseInsensitive])

    /// Any literal opening or closing context tag prefix (either generation, any case, optional spaces around `/`).
    static let contextTag = try! NSRegularExpression(
        pattern: #"<(\s*/?\s*"# + HubNaming.contextPattern + #"\b)"#, options: [.caseInsensitive])

    /// Entity-escaped form written by `inBlock`.
    static let escapedContextTag = try! NSRegularExpression(
        pattern: #"&lt;(\s*/?\s*"# + HubNaming.contextPattern + #"\b)"#, options: [.caseInsensitive])

    /// Escapes every literal opening or closing context tag's `<` as `&lt;`. Replacing `<` with `&lt;` never joins
    /// surrounding fragments into a new tag, so one linear pass suffices.
    static func inBlock(_ text: String) -> String {
        guard text.contains("<") else { return text }
        return contextTag.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: "&lt;$1")
    }

    /// Restores literal context tags escaped by `inBlock`.
    static func fromBlock(_ text: String) -> String {
        guard text.contains("&") else { return text }
        return escapedContextTag.stringByReplacingMatches(in: text, range: NSRange(text.startIndex..., in: text), withTemplate: "<$1")
    }

    /// Whether `text` has anything a reader would take as the end of a block.
    static func closes(_ text: String) -> Bool {
        closingTag.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) != nil
    }

    /// Whether `text` contains any unescaped literal opening or closing context tag.
    static func containsTag(_ text: String) -> Bool {
        contextTag.firstMatch(in: text, range: NSRange(text.startIndex..., in: text)) != nil
    }
}
