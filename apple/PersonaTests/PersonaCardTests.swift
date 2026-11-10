import Foundation
import Testing
@testable import Persona

/// The assistant's cards (docs/extensions/extensions.md「Assistant cards: `persona-view` and `persona-card`」): a ```persona-card (#886) becomes the components every app
/// turns it into (`schema/extension/examples/persona-card/`: a card and the View it becomes, null when it stays a code
/// block); a ```persona-view is a View or one component; what the app cannot draw stays a code block.
@MainActor
struct PersonaCardTests {
    private static let examples = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent().appending(path: "schema/extension/examples/persona-card")

    /// Each case of the shared examples: the card's View is the one written there, field for field.
    @Test func everySharedCaseBecomesItsView() throws {
        let files = try FileManager.default.contentsOfDirectory(at: Self.examples, includingPropertiesForKeys: nil)
            .filter { $0.pathExtension == "json" }
        #expect(files.count >= 9, "the cases are at \(Self.examples.path)")
        for file in files {
            let json = try #require(try JSONSerialization.jsonObject(with: Data(contentsOf: file)) as? [String: Any])
            let card = try JSONSerialization.data(withJSONObject: try #require(json["card"]))
            let got = AssistantCardBlock.parse(language: "persona-card", String(decoding: card, as: UTF8.self))
            if json["view"] is NSNull {
                #expect(got == nil, "\(file.lastPathComponent): stays a code block")
            } else {
                let want = try JSONDecoder().decode(ExtensionView.self, from: JSONSerialization.data(withJSONObject: try #require(json["view"])))
                #expect(got == want, "\(file.lastPathComponent)")
            }
        }
    }

    @Test func whatStaysACodeBlock() {
        let notCards = [
            "",
            "{\"type\":\"stacked_bar\",\"segments\":[{\"label\":\"早餐\"", // still streaming
            #"{"type":"pie","segments":[{"label":"a","value":1}]}"#, // a type the app does not know
            #"{"type":"stat","title":"no value"}"#,
            #"{"type":"stacked_bar","segments":[]}"#,
            #"{"type":"stacked_bar","segments":[{"label":"a","value":0}]}"#, // nothing to share out
            #"{"type":"stacked_bar","segments":[{"label":"a","value":5},{"label":"b","value":-1}]}"#,
            #"{"type":"line","points":[{"label":"周一","value":1}]}"#, // a line needs two points
            #"{"type":"stat","value":"2250"}"#, // numbers are numbers
        ]
        for text in notCards { #expect(AssistantCardBlock.parse(language: "persona-card", text) == nil, "\(text)") }
        #expect(AssistantCardBlock.parse(language: "swift", #"{"type":"stat","value":1}"#) == nil, "another language is code")
    }

    @Test func numbersAreGroupedTheSameEverywhere() {
        #expect(ExtensionPersonaCard.number(2250) == "2,250")
        #expect(ExtensionPersonaCard.number(7.25) == "7.3" || ExtensionPersonaCard.number(7.25) == "7.2")
        #expect(ExtensionPersonaCard.number(45) == "45" && ExtensionPersonaCard.number(-1200.34) == "-1,200.3")
        #expect(ExtensionPersonaCard.number(-0.04) == "0" && ExtensionPersonaCard.number(9.96) == "10")
        #expect(ExtensionPersonaCard.amount(8432, "步") == "8,432 步" && ExtensionPersonaCard.amount(1, "") == "1")
    }

    /// A ```persona-view: a View, or one component; one with nothing the app knows stays a code block.
    @Test func viewBlocks() throws {
        let v = try #require(AssistantCardBlock.parse(language: "persona-view",
            #"{"sections":[{"title":"本周","items":[{"type":"metric","label":"会议","value":"12"},{"type":"hologram"}]}]}"#))
        #expect(v.sections[0].title == "本周" && v.sections[0].items.map(\.type) == ["metric", "hologram"])
        let one = try #require(AssistantCardBlock.parse(language: "persona-view", #"{"type":"markdown","text":"**好**"}"#))
        #expect(one.sections.count == 1 && one.sections[0].items.map(\.type) == ["markdown"])
        #expect(AssistantCardBlock.parse(language: "persona-view", #"{"sections":[{"items":[{"type":"hologram"}]}]}"#) == nil)
        #expect(AssistantCardBlock.parse(language: "persona-view", #"{"sections":[{"items":[{"type":"metric""#) == nil)
        #expect(AssistantCardBlock.parse(language: "persona-view", #"[1,2]"#) == nil)
        #expect(AssistantCardBlock.parse(language: "persona-view", #"{"type":"button","title":"运行","action":{"id":"run"}}"#) == nil,
                "只含交互组件时保留代码块，不画空白卡片")
        #expect(AssistantCardBlock.parse(language: "persona-view", #"{"type":"html","html":"<script>ignored()</script>"}"#) == nil,
                "助手专用 HTML 不通过只读扩展渲染器执行")
        let mixed = try #require(AssistantCardBlock.parse(language: "persona-view",
            #"{"sections":[{"items":[{"type":"html","html":"<b>ignored</b>","key":"html"},{"type":"metric","label":"数值","value":"1"}]}]}"#))
        #expect(ExtensionParts.of(mixed).flatMap(\.items).map { $0.component.type } == ["metric"])
        #expect(AssistantCardBlock.parse(language: "persona-view", #"{"sections":[{"items":[{"type":"field","name":"q","label":"搜索"},{"type":"divider"}]}]}"#) == nil)
    }
}
