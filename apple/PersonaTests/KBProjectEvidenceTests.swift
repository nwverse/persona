import Foundation
import PersonaWire
import Testing
@testable import Persona

private final class ProjectEvidenceBundle {}

@MainActor struct KBProjectEvidenceTests {
    private func fixture(_ name: String) throws -> Data {
        let url = try #require(Bundle(for: ProjectEvidenceBundle.self).url(forResource: "garden-project-" + name, withExtension: "json"))
        return try Data(contentsOf: url)
    }

    private func project(_ fixtureName: String = "detail") throws -> KBDetail {
        try JSON.decoder.decode(KBDetailResponse.self, from: fixture(fixtureName)).detail
    }

    private func body(_ draft: KBProjectStorageDraft) throws -> [String: Any] {
        let update = try #require(draft.update)
        return try #require(JSONSerialization.jsonObject(with: JSONEncoder().encode(update)) as? [String: Any])
    }

    @Test func realListAndDetailRetainBothReceiptsAndSharedDeliveryAcrossProjects() throws {
        let rows = try JSON.decoder.decode(KBList.self, from: fixture("list")).items
        let row = try #require(rows.first { $0.id == "community-garden" })
        let garden = try project()
        let receipt = try #require(row.freshness)
        #expect(receipt == garden.freshness && receipt.timelineEnd == 208)
        #expect(receipt.fields["summary"]?.cutoff?.timelineFrom == 13 && receipt.fields["summary"]?.cutoff?.timelineThrough == 87)
        #expect(receipt.fields["summary"]?.uncoveredEvidenceCount == 3 && receipt.fields["summary"]?.lockReason == "field_locked")
        #expect(receipt.fields["summary"]?.writer == "persona" && receipt.fields["summary"]?.reviewBy == "user")
        #expect(receipt.fields["summary"]?.source == "kb.project" && receipt.fields["summary"]?.reviewReason == "new_evidence")
        #expect(receipt.fields["one_line"]?.cutoff?.timelineThrough == 208 && receipt.fields["one_line"]?.uncoveredEvidenceCount == 0)
        let used = try #require(receipt.fields["summary"]?.cutoff?.sources?.last)
        let latest = try #require(receipt.evidence?.last)
        #expect(used.ref == latest.ref && used.sourceID == latest.sourceID && used.position != latest.position)
        let delivery = try #require(garden.sources?.last)
        #expect(delivery.id == used.sourceID && delivery.deliveryState == "merged" && delivery.environmentVerification == "unverified")
        #expect(delivery.deliveryAt == JSON.parseDate("2026-11-18T11:00:00Z"))
        #expect(delivery.refs == ["chats/garden-review"])
        let other = try project("detail-verification")
        #expect(other.sources?.first?.id == delivery.id && other.sources?.first?.url == delivery.url)
        #expect(other.freshness?.fields["summary"]?.cutoff == nil && other.freshness?.fields["summary"]?.writer == nil)
        #expect(other.freshness?.fields["summary"]?.uncoveredEvidenceCount == 1)
        #expect(try #require(KBProjectStorageDraft(other)).references == "https://github.com/example/garden/issues/4#issuecomment-1")
    }

    @Test func historicalAndReferenceOnlyReceiptsKeepUnknownDistinctFromZero() throws {
        let detail = try project("detail-reference-only")
        let summary = try #require(detail.freshness?.fields["summary"])
        #expect(detail.projectPolicy == .referenceOnly)
        #expect(!summary.needsReview && summary.reviewReason == "reference-only" && summary.uncoveredEvidenceCount == nil)
        #expect(summary.reviewBy == "user" && detail.freshness?.fields["one_line"]?.reviewBy == "persona")
        let unknown = try JSON.decoder.decode(KBProjectFreshness.Field.self, from: Data(#"{"needs_review":true,"review_reason":"coverage_unknown"}"#.utf8))
        #expect(unknown.writer == nil && unknown.reviewBy == nil && unknown.cutoff == nil && unknown.uncoveredEvidenceCount == nil)
    }

    @Test func anEmptyModernProjectCanSwitchModesWhileItsListAndDetailsHaveNoEvidence() throws {
        let row = try #require(JSON.decoder.decode(KBList.self, from: fixture("empty-list")).items.first)
        let empty = try project("empty-detail")
        #expect(row.id == empty.id && row.freshness == empty.freshness)
        #expect(empty.freshness?.fields.isEmpty == true && empty.freshness?.needsReview == false)
        #expect(empty.projectMode == nil && empty.sources == nil && empty.projectPolicy == .full)
        var draft = try #require(KBProjectStorageDraft(empty))
        #expect(draft.references.isEmpty && draft.update == nil)
        draft.policy = .referenceOnly
        #expect(try body(draft) as? [String: String] == ["project_mode": "reference-only"])
        var restore = try #require(KBProjectStorageDraft(project("empty-detail-reference-only")))
        restore.policy = .full
        #expect(try body(restore) as? [String: String] == ["project_mode": "full"])
    }

    @Test func oldHubAndOtherKindsNeverProduceAStorageEditor() throws {
        let old = try JSON.decoder.decode(KBDetail.self, from: Data(#"{"kind":"projects","id":"old","path":"projects/old","meta":{}}"#.utf8))
        #expect(!old.hasProjectEvidence && KBProjectStorageDraft(old) == nil)
        var detail = try project()
        #expect(detail.projectMode == nil && detail.projectPolicy == .full)
        detail.projectMode = "future"
        #expect(detail.hasProjectEvidence && KBProjectStorageDraft(detail) == nil)
        detail.kind = "chats"
        #expect(!detail.hasProjectEvidence && KBProjectStorageDraft(detail) == nil)
    }

    @Test func loadedDetailOverridesTheListsOlderReceiptIncludingWhenTheHubOmitsIt() throws {
        let rows = try JSON.decoder.decode(KBList.self, from: fixture("list")).items
        let row = try #require(rows.first { $0.id == "community-garden" })
        var detail = try project("detail-reference-only")
        #expect(KnowledgeDetailView.shown(row, detail: .init(detail: detail)).projectMode == "reference-only")
        detail.projectMode = nil
        detail.freshness = nil
        detail.sources = nil
        let old = KnowledgeDetailView.shown(row, detail: .init(detail: detail))
        #expect(old.projectMode == nil && old.freshness == nil && old.sources == nil)
        #expect(KnowledgeDetailView.shown(row, detail: nil).freshness == row.freshness)
    }

    @Test func onlyExplicitlyChangedOwnerSettingsAreWrittenAndAnEmptyArrayClears() throws {
        var draft = try #require(KBProjectStorageDraft(project()))
        #expect(draft.references == "https://example.org/garden/planting-plan" && draft.update == nil)
        draft.references = "  " + draft.references + "\n"
        #expect(draft.update == nil)
        draft.policy = .referenceOnly
        let policyOnly = try body(draft)
        #expect(Set(policyOnly.keys) == ["project_mode"] && policyOnly["project_mode"] as? String == "reference-only")
        draft.references = "\n"
        let both = try body(draft)
        #expect(Set(both.keys) == ["project_mode", "source_urls"] && both["source_urls"] as? [String] == [])
        draft.policy = .full
        #expect(Set(try body(draft).keys) == ["source_urls"])
        draft.references = "http://example.org/base\nhttps://example.org/notes#latest"
        #expect(try body(draft)["source_urls"] as? [String] == ["http://example.org/base", "https://example.org/notes#latest"])
        var discovered = try project()
        discovered.meta = nil
        var discoveryOnly = try #require(KBProjectStorageDraft(discovered))
        #expect(discoveryOnly.references.isEmpty && discoveryOnly.update == nil)
        discoveryOnly.policy = .referenceOnly
        #expect(try body(discoveryOnly)["source_urls"] == nil)
        var restore = try #require(KBProjectStorageDraft(project("detail-reference-only")))
        restore.policy = .full
        #expect(try body(restore)["project_mode"] as? String == "full")
    }

    @Test func unsupportedOrOversizedOwnerURLsCannotBeSubmitted() throws {
        var draft = try #require(KBProjectStorageDraft(project()))
        for value in ["chats/sample", "//example.org", "ftp://example.org/file", "https://", "https://name@example.org/",
                      "https://name:pass@example.org", "https://example.org/two words", "https://example.org/%zz", "https://example.org/" + String(repeating: "a", count: 2048),
                      Array(repeating: "https://example.org", count: 31).joined(separator: "\n")] {
            draft.references = value
            #expect(!draft.validURLs && draft.update == nil)
        }
        draft.references = Array(repeating: "https://example.org", count: 30).joined(separator: "\n")
        #expect(draft.validURLs && draft.update != nil)
        draft.references = "https://例え.テスト/庭"
        #expect(try body(draft)["source_urls"] as? [String] == ["https://例え.テスト/庭"])
    }

    @Test func sameSlugHasOneOwnerAcrossReadsSavesManagerAndCredentialChanges() async throws {
        let hub = FakeHub()
        let path = "/api/v1/kb/projects/community-garden"
        hub.on("GET", path, json: String(decoding: try fixture("detail"), as: UTF8.self))
        hub.on("PUT", path, json: #"{"id":"community-garden"}"#)
        for name: String? in [nil, "work"] {
            let current = hub.client.showing(name)
            let opened = current.currentAssistantSettings()
            let result = try await opened.kbDetail("projects", id: "community-garden")
            let id = result.detail.id
            var draft = try #require(KBProjectStorageDraft(result.detail))
            try await opened.saveProjectStorage(draft, id: id, current: current)
            draft.policy = .referenceOnly
            var changedCredential = current
            changedCredential.config.token = "other-fixture"
            for other in [hub.client.showing(name == nil ? "work" : nil), FakeHub(url: "https://second.invalid").client.showing(name),
                          changedCredential, nil] {
                await #expect(throws: CancellationError.self) {
                    try await opened.saveProjectStorage(draft, id: id, current: other)
                }
            }
            try await opened.saveProjectStorage(draft, id: id, current: current)
        }
        #expect(hub.sent("GET", path).map { $0.headers[APIClient.managerHeader] } == [nil, "work"])
        #expect(hub.sent("PUT", path).map { $0.headers[APIClient.managerHeader] } == [nil, "work"])
    }
}
