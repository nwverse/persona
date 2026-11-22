import Foundation

// Commenting in the Feishu document reader (#1290): reply to a thread, comment on the whole document or on a passage,
// resolve and reopen, @ someone. The Feishu plugin serves the routes (feature `lark_doc_comments`); they are the
// user's own writes from their device, so no gate asks about them (as with replies in the inbox, #1203).

/// What Send writes: a comment's words with the people it @s (the text holds each `@name`, as in a chat), and for a
/// new comment on a passage the text it quotes and the block it is in. `client_id` keeps a retry from posting twice.
nonisolated struct FeishuDocsCommentWrite: Encodable, Sendable {
    var url: String
    var content: String
    var mentions: [LarkMention]
    var quote: String?
    var blockID: String?
    var clientID: String

    enum CodingKeys: String, CodingKey {
        case url, content, mentions, quote
        case blockID = "block_id", clientID = "client_id"
    }
}

/// The routes the comments use; `APIClient` is the real one, tests give their own.
protocol FeishuDocsCommentsAPI: Sendable {
    func docComments(url: String) async throws -> FeishuDocsComments
    func docComment(_ c: FeishuDocsCommentWrite) async throws
    func docReply(thread: String, _ c: FeishuDocsCommentWrite) async throws
    func docResolve(thread: String, url: String, resolved: Bool) async throws
    func docPeople(url: String, query: String) async throws -> [LarkMember]
}

extension APIClient: FeishuDocsCommentsAPI {
    func docComments(url: String) async throws -> FeishuDocsComments { try await feishuDocsGet("documents/comments", query: ["url": url]) }

    func docComment(_ c: FeishuDocsCommentWrite) async throws { _ = try await post("/api/v1/lark/documents/comments", body: c) }

    func docReply(thread: String, _ c: FeishuDocsCommentWrite) async throws {
        _ = try await post("/api/v1/lark/documents/comments/\(Self.segment(thread))/replies", body: c)
    }

    func docResolve(thread: String, url: String, resolved: Bool) async throws {
        struct Body: Encodable { var url: String; var resolved: Bool }
        _ = try await post("/api/v1/lark/documents/comments/\(Self.segment(thread))/resolve", body: Body(url: url, resolved: resolved))
    }

    /// People to @ in a document's comment, by name (the plugin's search; no chat to take members from).
    func docPeople(url: String, query: String) async throws -> [LarkMember] {
        struct R: Decodable { var members: [LarkMember] }
        return try await get("/api/v1/lark/documents/people", query: ["url": url, "q": query], as: R.self).members
    }

    private static func segment(_ s: String) -> String { s.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(["/"])) ?? s }
}

/// A document's comment threads and what the user is writing (MVVM, docs/apple/ios.md「Data flow」): loading them, Send to
/// where `target` says, resolving and reopening. After a write the threads are read again: what Feishu made of it
/// (the reply's id, a resolved thread moving tabs) shows as Feishu has it.
@Observable @MainActor
final class FeishuDocsCommentsModel {
    /// Where Send goes: a new comment on the whole document, a reply to a thread, or a new comment on a passage.
    enum Target: Equatable, Sendable {
        case document
        case thread(id: String, name: String, text: String)
        case passage(quote: String, blockID: String?)
    }

    let url: String
    private let api: any FeishuDocsCommentsAPI
    private(set) var threads: [FeishuDocsComments.Thread]?
    private(set) var loadError: String?
    var draft = ""
    var mentions: [LarkMention] = []
    var target: Target = .document
    private(set) var sending = false
    /// Threads being resolved or reopened.
    private(set) var resolving: Set<String> = []
    /// What went wrong with the last write, for the page to say; the draft stays.
    var writeError: String?
    /// The id of the send being tried: a retry of the same words to the same place reuses it.
    private var pending: (key: String, id: String)?

    init(url: String, api: any FeishuDocsCommentsAPI, target: Target = .document) {
        self.url = url
        self.api = api
        self.target = target
    }

    func threads(resolved: Bool) -> [FeishuDocsComments.Thread] { (threads ?? []).filter { $0.resolved == resolved } }

    func load() async {
        do {
            threads = feishuDocsSortedThreads(try await api.docComments(url: url).threads)
            loadError = nil
        } catch {
            if let m = error.feishuDocsMessage { loadError = m }
        }
    }

    var canSend: Bool { !sending && LarkComposer.canSend(draft) }

    /// Writes the draft where `target` says; on success the box empties and goes back to commenting on the document.
    func send() async {
        guard canSend else { return }
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        let key = "\(target)|\(text)"
        let id = pending?.key == key ? pending!.id : UUID().uuidString
        pending = (key, id)
        var write = FeishuDocsCommentWrite(url: url, content: text, mentions: LarkMentions.mentions(in: text, picked: mentions),
                                           quote: nil, blockID: nil, clientID: id)
        sending = true
        defer { sending = false }
        do {
            switch target {
            case .document:
                try await api.docComment(write)
            case .thread(let thread, _, _):
                try await api.docReply(thread: thread, write)
            case .passage(let quote, let block):
                write.quote = quote
                write.blockID = block
                try await api.docComment(write)
            }
            pending = nil
            draft = ""
            mentions = []
            target = .document
            writeError = nil
            await load()
        } catch {
            guard !error.isCancellation else { return }
            writeError = String(localized: "Couldn't send: \(Self.reason(error))")
        }
    }

    /// Resolves an open thread or reopens a resolved one.
    func setResolved(_ thread: FeishuDocsComments.Thread, _ resolved: Bool) async {
        guard !resolving.contains(thread.id) else { return }
        resolving.insert(thread.id)
        defer { resolving.remove(thread.id) }
        do {
            try await api.docResolve(thread: thread.id, url: url, resolved: resolved)
            writeError = nil
            await load()
        } catch {
            guard !error.isCancellation else { return }
            writeError = resolved ? String(localized: "Couldn't resolve: \(Self.reason(error))") : String(localized: "Couldn't reopen: \(Self.reason(error))")
        }
    }

    /// Replying to a thread: the box says to whom, the draft is kept.
    func reply(to thread: FeishuDocsComments.Thread) {
        let last = thread.replies.last
        target = .thread(id: thread.id, name: last?.author.name ?? String(localized: "Comments"), text: last?.text ?? thread.quote)
    }

    func people(_ query: String) async throws -> [LarkMember] { try await api.docPeople(url: url, query: query) }

    /// What the hub said (the plugin's words: 「…未登录飞书」…), else what the docs pages say for the error.
    private static func reason(_ error: Error) -> String {
        if let e = error as? APIError, !e.message.isEmpty { return e.message }
        return error.feishuDocsMessage ?? error.localizedDescription
    }
}
