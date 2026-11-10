import Foundation
import NaturalLanguage
import Observation

// Settings › Translation (docs/apple/ios.md「Translation」): when it is on, content not in the target language is translated by the hub
// (its small-task model). The scope is mail (on by default) and the Feishu chats picked in settings (none by default:
// there are too many messages). Whether a text needs it is decided here, on the device, with NaturalLanguage.

/// The translation settings, as `@AppStorage` keys and plain reads for code outside views.
nonisolated enum TranslationPrefs {
    static let enabledKey = "translate.enabled"
    static let targetKey = "translate.target"
    static let mailKey = "translate.mail"
    static let chatsKey = "translate.larkChats"
    static let defaultTarget = "zh-Hans"

    struct Language: Identifiable, Hashable {
        var code: String
        var name: String
        var id: String { code }
    }

    /// The targets in `server/internal/assistantwire/inbox.go` (`TranslateLanguages`).
    static let languages: [Language] = [
        .init(code: "zh-Hans", name: "简体中文"), .init(code: "zh-Hant", name: "繁體中文"), .init(code: "en", name: "English"), // i18n-ignore: endonyms
        .init(code: "ja", name: "日本語"), .init(code: "ko", name: "한국어"), .init(code: "fr", name: "Français"), // i18n-ignore: endonyms
        .init(code: "de", name: "Deutsch"), .init(code: "es", name: "Español"), .init(code: "ru", name: "Русский"),
        .init(code: "pt", name: "Português"),
    ]

    static func name(of code: String) -> String { languages.first { $0.code == code }?.name ?? code }

    /// A Feishu chat whose messages are translated (its name kept for the settings list).
    struct Chat: Codable, Hashable, Identifiable, Sendable {
        var id: String
        var name: String
    }

    static func chats(_ raw: String) -> [Chat] {
        (try? JSONDecoder().decode([Chat].self, from: Data(raw.utf8))) ?? []
    }

    static func encode(_ chats: [Chat]) -> String {
        (try? JSONEncoder().encode(chats)).map { String(decoding: $0, as: UTF8.self) } ?? ""
    }

    private static var defaults: UserDefaults { .standard }
    static var enabled: Bool { defaults.bool(forKey: enabledKey) }
    static var target: String { defaults.string(forKey: targetKey) ?? defaultTarget }
    static var mail: Bool { defaults.object(forKey: mailKey) as? Bool ?? true }
    static var chatIDs: Set<String> { Set(chats(defaults.string(forKey: chatsKey) ?? "").map(\.id)) }

    /// Mail gets translated now.
    static var translatesMail: Bool { enabled && mail }
    /// Messages of this Feishu chat get translated now.
    static func translates(chat feedID: String?) -> Bool {
        guard enabled, let feedID else { return false }
        return chatIDs.contains(feedID)
    }

    /// Whether `text` is in another language than `target`. Too little text to tell (an emoji, "OK", a number) is not;
    /// the two Chinese scripts count as the same language (the recognizer mixes them up on short text).
    static func isForeign(_ text: String, target: String) -> Bool {
        let sample = String(text.prefix(1500))
        let letters = sample.unicodeScalars.filter { CharacterSet.letters.contains($0) }.count
        guard letters >= 6 else { return false }
        let r = NLLanguageRecognizer()
        r.processString(sample)
        guard let lang = r.dominantLanguage, lang != .undetermined else { return false }
        return family(lang.rawValue) != family(target)
    }

    static func family(_ code: String) -> String { String(code.split(separator: "-").first ?? Substring(code)) }
}

extension TranslationPrefs {
    /// What the hub keeps for this device, so it translates new mail as it comes in.
    static var body: TranslatePrefsBody { .init(enabled: enabled, target: target, mail: mail) }
}

/// A mail's translation (POST /api/v1/translate/mail).
nonisolated struct MailTranslation: Codable, Hashable, Sendable {
    var subject: String
    var text: String
    var clipped: Bool?
}

extension APIClient {
    private nonisolated struct TranslateBody: Encodable { var texts: [String]; var target: String }
    private nonisolated struct TranslateReply: Decodable { var texts: [String] }
    private nonisolated struct TranslateMailBody: Encodable {
        var accountID: String, uid: UInt32, mailbox: String, target: String
        enum CodingKeys: String, CodingKey { case uid, mailbox, target; case accountID = "account_id" }
    }

    private nonisolated struct TranslatePrefsPut: Encodable { var translate: TranslatePrefsBody }

    /// Tells the hub this device's Settings › Translation (only that field: the rest of its prefs stay as they are).
    func setTranslatePrefs(device: String, _ prefs: TranslatePrefsBody) async throws {
        _ = try await request("PUT", "/api/v1/devices/\(device)/prefs", body: TranslatePrefsPut(translate: prefs))
    }

    /// Short texts translated in order ("" where the model gave nothing).
    func translate(_ texts: [String], to target: String) async throws -> [String] {
        try await post("/api/v1/translate", body: TranslateBody(texts: texts, target: target), timeout: 120, as: TranslateReply.self).texts
    }

    /// One mail's subject and body, translated by the hub from the message it reads itself.
    func translateMail(account: String, uid: UInt32, mailbox: String, to target: String) async throws -> MailTranslation {
        try await post("/api/v1/translate/mail", body: TranslateMailBody(accountID: account, uid: uid, mailbox: mailbox, target: target),
                       timeout: 180, as: MailTranslation.self)
    }
}

/// The translations the app has: Feishu messages asked for while they show (batched, 30 at a time), mails by
/// message. Kept for the session; the hub keeps its own copy, so a second launch is quick too.
@MainActor @Observable
final class Translator {
    static let shared = Translator()

    private var texts: [String: String] = [:]
    @ObservationIgnored private var foreign: [String: Bool] = [:]
    @ObservationIgnored private var failed: Set<String> = []
    @ObservationIgnored private var queued: [String: (text: String, target: String)] = [:]
    @ObservationIgnored private var flushing: Task<Void, Never>?
    @ObservationIgnored private var mails: [String: MailTranslation] = [:]

    private static let batch = 30

    private func key(_ text: String, _ target: String) -> String { target + "\u{0}" + text }

    /// Whether `text` needs translating into `target`, remembered per text (the recognizer is too slow for a row's body).
    func isForeign(_ text: String, target: String) -> Bool {
        let k = key(text, target)
        if let v = foreign[k] { return v }
        let v = TranslationPrefs.isForeign(text, target: target)
        foreign[k] = v
        return v
    }

    func translation(of text: String, to target: String) -> String? { texts[key(text, target)] }

    /// Asks for a text's translation; texts asked for within a moment go in one request.
    func request(_ text: String, to target: String) {
        let k = key(text, target)
        guard texts[k] == nil, queued[k] == nil, !failed.contains(k) else { return }
        queued[k] = (text, target)
        guard flushing == nil else { return }
        flushing = Task {
            try? await Task.sleep(for: .milliseconds(300))
            await flush()
            flushing = nil
            if !queued.isEmpty { request(queued.first!.value.text, to: queued.first!.value.target) }
        }
    }

    private func flush() async {
        guard let api = AppModel.shared.api else { return }
        while !queued.isEmpty {
            let target = queued.first!.value.target
            let picked = queued.filter { $0.value.target == target }.prefix(Self.batch)
            for (k, _) in picked { queued[k] = nil }
            let keys = picked.map(\.key), list = picked.map(\.value.text)
            do {
                let out = try await api.translate(list, to: target)
                for (i, k) in keys.enumerated() where i < out.count {
                    if out[i].isEmpty { failed.insert(k) } else { texts[k] = out[i] }
                }
            } catch {
                failed.formUnion(keys) // not again this session; the original stays readable
            }
        }
    }

    /// Sends Settings › Translation to the hub (on connect and when it changes) unless it already has it.
    func syncPrefs(hubHas: TranslatePrefsBody?) {
        let mine = TranslationPrefs.body
        guard hubHas != mine, let api = AppModel.shared.api else { return }
        Task { try? await api.setTranslatePrefs(device: PushRegistrar.deviceID, mine) }
    }

    /// A mail's translation, asked once per message and target.
    func mail(account: String, uid: UInt32, mailbox: String, to target: String) async throws -> MailTranslation {
        let k = [account, mailbox, String(uid), target].joined(separator: "\u{0}")
        if let m = mails[k] { return m }
        guard let api = AppModel.shared.api else { throw URLError(.notConnectedToInternet) }
        let m = try await api.translateMail(account: account, uid: uid, mailbox: mailbox, to: target)
        mails[k] = m
        return m
    }
}
