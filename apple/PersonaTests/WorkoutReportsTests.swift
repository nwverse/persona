#if os(iOS)
import Foundation
import Testing
@testable import Persona

/// Records which reports reached the fake hub, in order.
private actor Uploads {
    private(set) var ids: [String] = []
    func add(_ id: String) { ids.append(id) }
}

@MainActor
struct WorkoutReportsTests {
    private static func report(_ id: String = UUID().uuidString) -> WorkoutReports.Report {
        let start = Date(timeIntervalSince1970: 1_700_000_000)
        return WorkoutReports.Report(id: id, title: "Workout report", activity: "Running", source: "Test", start: start,
            end: start.addingTimeInterval(600), enabled_at: start, duration: 600, distance: nil, energy: nil, heart_rate: [])
    }

    /// An opted-in state whose outbox holds `pending`, and a hub that answers `status` for `failing` and accepts the rest.
    private static func outbox(_ pending: [WorkoutReports.Report], failing: String, status: Int)
        throws -> (WorkoutReports, APIClient, Uploads, URL) {
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let state = WorkoutReports.State(enabledAt: pending[0].start, group: "test-hub", pending: pending)
        try JSON.encoder.encode(state).write(to: folder.appending(path: "state.json"))
        let uploads = Uploads()
        let api = APIClient(config: ServerConfig(url: URL(string: "https://hub.invalid")!, token: "t"), transport: { request in
            let id = try JSON.decoder.decode(WorkoutReports.Report.self, from: request.body ?? Data()).id
            await uploads.add(id)
            if id == failing { throw APIError(status: status, message: "invalid workout") }
            return Data(#"{"accepted":true}"#.utf8)
        })
        return (WorkoutReports(folder: folder), api, uploads, folder)
    }

    private static func pending(in folder: URL) throws -> [String] {
        try JSON.decoder.decode(WorkoutReports.State.self, from: Data(contentsOf: folder.appending(path: "state.json")))
            .pending.map(\.id)
    }

    @Test(arguments: [400, 413, 422])
    func rejectedReportDoesNotBlockLaterOnes(status: Int) async throws {
        let bad = Self.report(), good = Self.report()
        let (reports, api, uploads, folder) = try Self.outbox([bad, good], failing: bad.id, status: status)
        defer { try? FileManager.default.removeItem(at: folder) }
        #expect(try await reports.upload(api: api) == 1)
        #expect(await uploads.ids == [bad.id, good.id])
        #expect(try Self.pending(in: folder).isEmpty)
    }

    @Test(arguments: [401, 403, 404, 429, 500, 503])
    func otherFailuresKeepTheOutbox(status: Int) async throws {
        let first = Self.report(), second = Self.report()
        let (reports, api, uploads, folder) = try Self.outbox([first, second], failing: first.id, status: status)
        defer { try? FileManager.default.removeItem(at: folder) }
        do {
            _ = try await reports.upload(api: api)
            Issue.record("HTTP \(status) emptied the outbox")
        } catch let error as APIError {
            #expect(error.status == status)
        }
        #expect(await uploads.ids == [first.id])
        #expect(try Self.pending(in: folder) == [first.id, second.id])
    }

    @Test func newInstallDoesNotOptIn() throws {
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        let reports = WorkoutReports(folder: folder)
        #expect(!reports.enabled)
        #expect(reports.error == nil)
    }

    @Test func unreadableConsentFailsClosed() throws {
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        try Data("broken state".utf8).write(to: folder.appending(path: "state.json"))
        let reports = WorkoutReports(folder: folder)
        #expect(!reports.enabled)
        #expect(reports.error != nil)
    }

    @Test func restartPreservesConsentAndPendingMeasurements() throws {
        let folder = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: folder) }
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let start = Date(timeIntervalSince1970: 1_700_000_000)
        let report = WorkoutReports.Report(id: UUID().uuidString, title: "Workout report", activity: "Running",
            source: "Test", start: start, end: start.addingTimeInterval(600), enabled_at: start,
            duration: 600, distance: nil, energy: nil, heart_rate: [])
        let state = WorkoutReports.State(enabledAt: start, group: "test-hub", anchor: Data([1, 2]), pending: [report])
        let file = folder.appending(path: "state.json")
        try JSON.encoder.encode(state).write(to: file)
        #expect(WorkoutReports(folder: folder).enabled)
        let restored = try JSON.decoder.decode(WorkoutReports.State.self, from: Data(contentsOf: file))
        #expect(restored.enabledAt == start)
        #expect(restored.pending.first?.id == report.id)
        #expect(restored.pending.first?.distance == nil)
        #expect(restored.anchor == Data([1, 2]))
    }
}
#endif
