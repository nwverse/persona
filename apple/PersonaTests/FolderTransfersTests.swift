import Foundation
import Testing
@testable import Persona

/// Files into a folder of the file browser (docs/protocol/protocol.md "Changing files", #4858): what is sent, and the "already exists" question.
@MainActor
struct FolderTransfersTests {
    private func file(_ name: String = "a.txt") throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appending(path: "folder-tests/" + UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let url = dir.appending(path: name)
        try Data("hello".utf8).write(to: url)
        return url
    }

    private static let uploaded = #"{"path": "/home/me/proj/a.txt", "name": "a.txt", "size": 5}"#

    @Test func sendsIntoTheFolderUnderItsName() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files", json: Self.uploaded)
        var asked = 0
        let out = try await FolderTransfers.send(try file(), name: "a.txt", dir: "~/proj", cwd: "/w", machine: "m1", api: hub.client,
                                                 ask: { _ in asked += 1; return .cancel }) { _ in }
        #expect(out?.path == "/home/me/proj/a.txt")
        #expect(asked == 0)
        let req = try #require(hub.sent("POST", "/api/v1/machines/m1/files").first)
        #expect(req.query == ["name": "a.txt", "dest": "dir", "dir": "~/proj", "cwd": "/w"])
        #expect(req.body == Data("hello".utf8))
    }

    /// A taken name asks once; the answer goes with the second try; Cancel sends nothing more.
    @Test func aTakenNameAsks() async throws {
        for (choice, policy) in [(FolderTransfers.Choice.keep, "keep"), (.replace, "replace")] {
            let hub = FakeHub()
            hub.on("POST", "/api/v1/machines/m1/files") { req in
                if req.query["conflict"] == nil { throw APIError(status: 409, message: "a.txt already exists", fields: ["code": "exists", "name": "a.txt"]) }
                return Data(Self.uploaded.utf8)
            }
            var asked: [String] = []
            var restarted = 0
            let out = try await FolderTransfers.send(try file(), name: "a.txt", dir: "~/proj", cwd: "", machine: "m1", api: hub.client,
                                                     ask: { asked.append($0); return choice }, restart: { restarted += 1 }) { _ in }
            #expect(out != nil)
            #expect(asked == ["a.txt"])
            #expect(restarted == 1)
            #expect(hub.sent("POST", "/api/v1/machines/m1/files").map { $0.query["conflict"] } == [nil, policy])
        }
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files") { _ in throw APIError(status: 409, message: "exists", fields: ["code": "exists"]) }
        let out = try await FolderTransfers.send(try file(), name: "a.txt", dir: "~", cwd: "", machine: "m1", api: hub.client,
                                                 ask: { _ in .cancel }) { _ in }
        #expect(out == nil)
        #expect(hub.sent("POST", "/api/v1/machines/m1/files").count == 1)
    }

    /// Exists again after the answer (a folder of that name) is an error, not another question.
    @Test func asksOnlyOnce() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files") { _ in throw APIError(status: 409, message: "d already exists", fields: ["code": "exists"]) }
        var asked = 0
        await #expect(throws: APIError.self) {
            _ = try await FolderTransfers.send(try file("d"), name: "d", dir: "~", cwd: "", machine: "m1", api: hub.client,
                                               ask: { _ in asked += 1; return .replace }) { _ in }
        }
        #expect(asked == 1)
    }

    private func waitUntil(_ condition: @escaping @MainActor () -> Bool) async {
        for _ in 0..<200 where !condition() { try? await Task.sleep(for: .milliseconds(20)) }
    }

    private func body(_ req: LocalRequest) throws -> [String: Any] {
        try #require(JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any])
    }

    @Test func quickCopyAndCompressCarryOperationsWithoutPolling() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files/copy", json: #"{"copied":[{"from":"~/a.txt","to":"~/a 2.txt"}]}"#)
        hub.on("POST", "/api/v1/machines/m1/files/zip", json: #"{"path":"~/a.zip","name":"a.zip","size":10,"files":1}"#)
        let model = AppModel()
        model.apiForTests = hub.client
        let transfers = FolderTransfers()
        let machine = MachineRef(group: "", id: "m1")
        var duplicated: [String] = []
        transfers.duplicate(["~/a.txt"], cwd: "~", machine: machine, model: model) { duplicated = $0 }
        await waitUntil { !duplicated.isEmpty }
        #expect(duplicated == ["a 2.txt"])
        var compressed = ""
        transfers.compress(["~/a.txt"], cwd: "~", machine: machine, model: model) { compressed = $0 }
        await waitUntil { !compressed.isEmpty }
        #expect(compressed == "a.zip")
        let copy = try body(try #require(hub.sent("POST", "/api/v1/machines/m1/files/copy").first))
        let zip = try body(try #require(hub.sent("POST", "/api/v1/machines/m1/files/zip").first))
        let copyID = try #require(copy["operation_id"] as? String)
        let zipID = try #require(zip["operation_id"] as? String)
        #expect(!copyID.isEmpty && !zipID.isEmpty && copyID != zipID)
        #expect(hub.requests.filter { $0.path.contains("/files/operations/") }.isEmpty)
        #expect(transfers.items.isEmpty)
    }

    @Test func slowCopyPollsProgressAndCancelsThePost() async throws {
        let hub = FakeHub()
        var cancelled = false
        hub.onAsync("POST", "/api/v1/machines/m1/files/copy") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any]
            let id = body?["operation_id"] as? String ?? "missing"
            hub.on("GET", "/api/v1/machines/m1/files/operations/\(id)", json: #"{"id":"\#(id)","kind":"copy","state":"running","files_done":1,"files_total":2,"bytes_done":32,"bytes_total":100}"#)
            for _ in 0..<500 {
                if Task.isCancelled {
                    cancelled = true
                    throw CancellationError()
                }
                try? await Task.sleep(for: .milliseconds(20))
            }
            return Data(#"{"copied":[]}"#.utf8)
        }
        let model = AppModel()
        model.apiForTests = hub.client
        let transfers = FolderTransfers()
        var made: [String] = []
        transfers.duplicate(["~/a.txt"], cwd: "~", machine: MachineRef(group: "", id: "m1"), model: model) { made = $0 }
        try await Task.sleep(for: .milliseconds(1250))
        let item = try #require(transfers.items.first)
        #expect(item.done == 32 && item.size == 100)
        #expect(!item.waiting)
        #expect(hub.requests.contains { $0.path.contains("/files/operations/") })
        transfers.stop()
        try await Task.sleep(for: .milliseconds(350))
        let stoppedReads = hub.requests.filter { $0.path.contains("/files/operations/") }.count
        try await Task.sleep(for: .milliseconds(300))
        #expect(hub.requests.filter { $0.path.contains("/files/operations/") }.count == stoppedReads)
        transfers.resumePolling()
        await waitUntil { hub.requests.filter { $0.path.contains("/files/operations/") }.count > stoppedReads }
        transfers.cancel(item.id)
        await waitUntil { cancelled }
        #expect(cancelled)
        #expect(transfers.items.isEmpty && made.isEmpty)
    }

    @Test func remoteCopyWithoutOperationLookupDoesNotOfferFalseCancel() async throws {
        let hub = FakeHub()
        hub.onAsync("POST", "/api/v1/machines/m1/files/copy") { _ in
            for _ in 0..<500 {
                if Task.isCancelled { throw CancellationError() }
                try? await Task.sleep(for: .milliseconds(20))
            }
            return Data(#"{"copied":[]}"#.utf8)
        }
        let model = AppModel()
        model.apiForTests = hub.client
        let transfers = FolderTransfers()
        defer { for item in transfers.items { transfers.cancel(item.id) } }
        transfers.duplicate(["~/a.txt"], cwd: "~", machine: MachineRef(group: "", id: "m1"), model: model) { _ in }
        try await Task.sleep(for: .milliseconds(1250))
        let item = try #require(transfers.items.first)
        #expect(!item.showsProgress && transfers.visibleItems.isEmpty)
        #expect(hub.requests.contains { $0.path.contains("/files/operations/") })
    }

    @Test func remoteCopyDoesNotWaitForAnUpload() async throws {
        let hub = FakeHub()
        var uploadCancelled = false
        hub.onAsync("POST", "/api/v1/machines/m1/files") { _ in
            for _ in 0..<500 {
                if Task.isCancelled { uploadCancelled = true; throw CancellationError() }
                try? await Task.sleep(for: .milliseconds(20))
            }
            return Data(Self.uploaded.utf8)
        }
        var copyCancelled = false
        hub.onAsync("POST", "/api/v1/machines/m1/files/copy") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any]
            let id = body?["operation_id"] as? String ?? "missing"
            hub.on("GET", "/api/v1/machines/m1/files/operations/\(id)", json: #"{"id":"\#(id)","kind":"copy","state":"running","files_done":1,"files_total":2,"bytes_done":32,"bytes_total":100}"#)
            for _ in 0..<500 {
                if Task.isCancelled { copyCancelled = true; throw CancellationError() }
                try? await Task.sleep(for: .milliseconds(20))
            }
            return Data(#"{"copied":[]}"#.utf8)
        }
        let model = AppModel()
        model.apiForTests = hub.client
        let transfers = FolderTransfers()
        defer { for item in transfers.items { transfers.cancel(item.id) } }
        let machine = MachineRef(group: "", id: "m1")
        transfers.upload([try file()], into: "~/dest", cwd: "~", machine: machine, model: model) { _ in }
        await waitUntil { hub.sent("POST", "/api/v1/machines/m1/files").count == 1 }
        transfers.duplicate(["~/a.txt"], cwd: "~", machine: machine, model: model) { _ in }
        try await Task.sleep(for: .milliseconds(1250))
        let item = try #require(transfers.items.first { $0.kind == .copy })
        #expect(!item.waiting && item.showsProgress)
        #expect(item.done == 32 && item.size == 100)
        #expect(hub.sent("POST", "/api/v1/machines/m1/files/copy").count == 1)
        transfers.cancel(item.id)
        await waitUntil { copyCancelled }
        #expect(copyCancelled && !uploadCancelled)
    }

    @Test func copyConflictRetriesWithANewOperation() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files/copy") { req in
            let body = try JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any]
            if body?["conflict"] == nil {
                throw APIError(status: 409, message: "taken", fields: ["code": "exists", "name": "a.txt"])
            }
            return Data(#"{"copied":[{"from":"~/a.txt","to":"~/dest/a 2.txt"}]}"#.utf8)
        }
        let model = AppModel()
        model.apiForTests = hub.client
        let transfers = FolderTransfers()
        var done = false
        transfers.copy(["~/a.txt"], into: "~/dest", cwd: "~", machine: MachineRef(group: "", id: "m1"), model: model) { done = true }
        await waitUntil { transfers.conflict != nil }
        #expect(transfers.conflict?.name == "a.txt")
        transfers.answer(.keep)
        await waitUntil { done }
        #expect(done)
        let requests = hub.sent("POST", "/api/v1/machines/m1/files/copy")
        #expect(requests.count == 2)
        let first = try body(try #require(requests.first))
        let second = try body(try #require(requests.last))
        let firstID = try #require(first["operation_id"] as? String)
        let secondID = try #require(second["operation_id"] as? String)
        #expect(firstID != secondID)
        #expect(second["conflict"] as? String == "keep")
    }

    @Test func failuresInWords() {
        let e = { (code: String) in APIError(status: 409, message: "raw", fields: ["code": code]) }
        #expect(FolderTransfers.message(e("read_only"), name: "a.txt")?.contains("home") == true)
        #expect(FolderTransfers.message(e("too_large"), name: "a.txt")?.contains("2 GB") == true)
        #expect(FolderTransfers.message(APIError(status: 409, message: "MacBook 上的 personad 太旧", fields: ["code": "node_outdated"]), name: "a") == "MacBook 上的 personad 太旧")
        #expect(FolderTransfers.message(APIError(status: 400, message: "unknown dest"), name: "a")?.contains("hub 版本太旧") == true)
        #expect(FolderTransfers.message(CancellationError(), name: "a") == nil)
    }

    /// One size format everywhere: an empty file reads in the same unit as a 30-byte one, never "Zero KB" / "0 KB".
    @Test func sizesShareOneFormat() {
        let unit = { (n: Int64) in n.storageText.filter { !$0.isNumber && !$0.isWhitespace && $0 != "," } }
        #expect(unit(0) == unit(30))
        #expect(!Int64(0).storageText.lowercased().contains("zero"))
        #expect(Int64(0).storageText.contains("0"))
        #expect(unit(2_048) != unit(30))
    }
}
