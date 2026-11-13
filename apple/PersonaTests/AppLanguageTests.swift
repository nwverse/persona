import Foundation
import Testing
@testable import Persona

/// docs/protocol/i18n.md「Choosing a language」: the system's first language decides, by its language subtag only.
struct AppLanguageTests {
    @Test(arguments: [
        ("zh", "zh-Hans"), ("zh-Hans", "zh-Hans"), ("zh-Hans-CN", "zh-Hans"), ("zh-CN", "zh-Hans"), ("zh_SG", "zh-Hans"),
        ("zh-Hant", "zh-Hans"), ("zh-Hant-TW", "zh-Hans"), ("zh-HK", "zh-Hans"), ("ZH-tw", "zh-Hans"), ("zh-MO", "zh-Hans"),
        ("ja", "ja"), ("ja-JP", "ja"), ("JA_jp", "ja"),
        ("en", "en"), ("en-GB", "en"), ("fr", "en"), ("ko-KR", "en"), ("de", "en"), ("", "en"),
    ])
    func resolvesTheSystemLanguage(tag: String, code: String) {
        #expect(AppLanguage.resolve(tag) == code)
    }

    @Test func nothingIsEnglish() {
        #expect(AppLanguage.resolve(nil) == "en")
    }

    @Test func aChoiceWinsOverTheSystem() {
        #expect(AppLanguage.system.resolved(system: "ja-JP") == "ja")
        #expect(AppLanguage.en.resolved(system: "zh-Hans") == "en")
        #expect(AppLanguage.zhHans.resolved(system: "fr") == "zh-Hans")
        #expect(AppLanguage.ja.resolved(system: nil) == "ja")
    }

    @Test func languageNamesAreTheirOwn() {
        #expect(AppLanguage.zhHans.title == "简体中文")
        #expect(AppLanguage.en.title == "English")
        #expect(AppLanguage.ja.title == "日本語")
        #expect(AppLanguage.codes == ["zh-Hans", "en", "ja"])
    }

    /// The choice is written where Foundation reads it at launch, and where the extensions read it.
    @Test func applyWritesTheResolvedLanguage() throws {
        let name = "test.language.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: name))
        let groupName = name + ".group"
        let group = try #require(UserDefaults(suiteName: groupName))
        defer {
            defaults.removePersistentDomain(forName: name)
            group.removePersistentDomain(forName: groupName)
        }
        let store = LanguageStore(defaults: defaults, group: group, argument: nil,
                                  appValue: { defaults.persistentDomain(forName: name)?[LanguageStore.appleLanguages] },
                                  systemValue: { ["fr-FR"] })
        // What was written to the app's own domain (lookups would also see the test run's -AppleLanguages argument).
        func saved() -> [String]? { defaults.persistentDomain(forName: name)?[LanguageStore.appleLanguages] as? [String] }
        #expect(store.current == "en") // Match System: French is English
        store.choice = .ja
        #expect(saved() == ["ja"])
        #expect(group.string(forKey: LanguageStore.resolvedKey) == "ja")
        #expect(store.current == "ja")
        store.choice = .en
        #expect(saved() == ["en"])
        #expect(group.string(forKey: LanguageStore.resolvedKey) == "en")
        // iOS's per-app setting changed it: the app adopts it as the choice.
        defaults.set(["ja-JP"], forKey: LanguageStore.appleLanguages) // as iOS's Settings › Persona › Language would
        #expect(store.systemFirst == "ja-JP")
        #expect(store.apply() == "ja")
        #expect(store.choice == .ja)
    }

    @Test func aLaunchArgumentWinsAndIsNotKept() throws {
        let name = "test.language.\(UUID().uuidString)"
        let defaults = try #require(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        let store = LanguageStore(defaults: defaults, group: nil, argument: "(ja)", appValue: { nil }, systemValue: { ["en"] })
        #expect(store.systemFirst == "ja")
        #expect(store.apply() == "ja")
        #expect(defaults.persistentDomain(forName: name)?[LanguageStore.appleLanguages] == nil)
    }

    /// The tests run in Chinese (the scheme's language): their expectations are the zh-Hans UI.
    @Test func testsRunInChinese() {
        #expect(LanguageStore.shared.launched == "zh-Hans")
        #expect(String(localized: "取消") == "取消")
    }
}
