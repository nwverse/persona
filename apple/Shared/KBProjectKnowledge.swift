import Foundation

/// The Hub's project receipts use byte offsets in log.md and opaque revisions for other evidence.
nonisolated struct KBProjectFreshness: Codable, Hashable, Sendable {
    var needsReview: Bool
    var timelineEnd: Int64
    var evidence: [Evidence]?
    var fields: [String: Field]

    enum CodingKeys: String, CodingKey {
        case fields, evidence
        case needsReview = "needs_review", timelineEnd = "timeline_end"
    }

    nonisolated struct Evidence: Codable, Hashable, Sendable {
        var ref: String
        var position: String
        var sourceID: String?
        enum CodingKeys: String, CodingKey { case ref, position, sourceID = "source_id" }
    }

    nonisolated struct Cutoff: Codable, Hashable, Sendable {
        var timelineFrom: Int64
        var timelineThrough: Int64
        var timelineTruncated: Bool?
        var sources: [Evidence]?

        enum CodingKeys: String, CodingKey {
            case sources, timelineFrom = "timeline_from", timelineThrough = "timeline_through", timelineTruncated = "timeline_truncated"
        }
    }

    nonisolated struct Field: Codable, Hashable, Sendable {
        var writer: String?
        var source: String?
        var cutoff: Cutoff?
        var lockReason: String?
        var reviewBy: String?
        var needsReview: Bool
        var reviewReason: String?
        var uncoveredEvidenceCount: Int?

        enum CodingKeys: String, CodingKey {
            case writer, source, cutoff, lockReason = "lock_reason", reviewBy = "review_by", needsReview = "needs_review"
            case reviewReason = "review_reason", uncoveredEvidenceCount = "uncovered_evidence_count"
        }
    }
}

nonisolated struct KBProjectSource: Codable, Identifiable, Hashable, Sendable {
    var id: String
    var url: String?
    var refs: [String]?
    var deliveryState: String?
    var deliveryAt: Date?
    var environmentVerification: String?

    enum CodingKeys: String, CodingKey {
        case id, url, refs, deliveryState = "delivery_state", deliveryAt = "delivery_at", environmentVerification = "environment_verification"
    }
}

nonisolated enum KBProjectPolicy: String, Sendable {
    case full
    case referenceOnly = "reference-only"
}

extension KBDetail {
    nonisolated var hasProjectEvidence: Bool {
        kind == KBKind.projects.rawValue && (projectMode != nil || freshness != nil || sources != nil)
    }

    nonisolated var projectPolicy: KBProjectPolicy? {
        guard hasProjectEvidence else { return nil }
        return KBProjectPolicy(rawValue: projectMode ?? KBProjectPolicy.full.rawValue)
    }
}

/// Replace only explicit owner URLs. Linked chats and canonical delivery URLs are derived by the Hub.
nonisolated struct KBProjectStorageDraft {
    private let savedPolicy: KBProjectPolicy
    private let savedURLs: [String]
    var policy: KBProjectPolicy
    var references: String

    init?(_ detail: KBDetail) {
        guard let policy = detail.projectPolicy else { return nil }
        let urls = detail.meta?["source_urls"]?.array?.compactMap(\.string) ?? []
        savedPolicy = policy
        savedURLs = urls
        self.policy = policy
        references = urls.joined(separator: "\n")
    }

    private var urls: [String] {
        references.components(separatedBy: .newlines).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    static func webURL(_ value: String) -> URL? {
        guard value.utf8.count <= 2048, value.rangeOfCharacter(from: .whitespacesAndNewlines.union(.controlCharacters)) == nil,
              let parts = URLComponents(string: value), ["http", "https"].contains(parts.scheme?.lowercased()),
              parts.host?.isEmpty == false, parts.user == nil, parts.password == nil else { return nil }
        if value.contains("%"), value.range(of: "%(?![0-9A-Fa-f]{2})", options: .regularExpression) != nil { return nil }
        // Foundation percent-encodes Unicode for opening. Leave the owner's spelling intact in source_urls.
        return parts.url
    }

    var validURLs: Bool {
        let values = urls
        return values == savedURLs || values.count <= 30 && values.allSatisfy { Self.webURL($0) != nil }
    }

    var update: KBUpdate? {
        let values = urls
        guard validURLs, policy != savedPolicy || values != savedURLs else { return nil }
        return KBUpdate(projectMode: policy == savedPolicy ? nil : policy.rawValue, sourceURLs: values == savedURLs ? nil : values)
    }
}

extension APIClient {
    /// Use the client that read the project, including its credentials and manager, only while this page still owns it.
    func saveProjectStorage(_ draft: KBProjectStorageDraft, id: String, current: APIClient?) async throws {
        guard sameSettingsScope(as: current), !Task.isCancelled else { throw CancellationError() }
        if let update = draft.update { try await kbUpdate(KBKind.projects.rawValue, id: id, update) }
    }
}
