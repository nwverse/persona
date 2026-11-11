import Foundation
import Testing
@testable import Persona

/// Renaming, moving and trashing in the file browser (docs/protocol/protocol.md "Changing files", #315): what is sent, and
/// what the user is told.
@MainActor
struct FolderEditsTests {
    private func body(_ req: LocalRequest) throws -> [String: Any] {
        try #require(JSONSerialization.jsonObject(with: req.body ?? Data()) as? [String: Any])
    }

    @Test func sendsTheRequestsTheHubTakes() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files/rename", json: #"{"path": "~/proj/b.txt"}"#)
        hub.on("POST", "/api/v1/machines/m1/files/move", json: #"{"moved": [{"from": "~/proj/b.txt", "to": "~/d/b.txt"}]}"#)
        hub.on("POST", "/api/v1/machines/m1/files/trash", json: #"{"trashed": [{"path": "~/d/b.txt", "trash_id": "t1"}]}"#)
        hub.on("POST", "/api/v1/machines/m1/files/restore", json: #"{"restored": [{"path": "~/d/b.txt"}]}"#)
        let api = hub.client

        #expect(try await api.renameFile(machine: "m1", path: "~/proj/a.txt", cwd: "/w", name: "b.txt") == "~/proj/b.txt")
        let rename = try body(try #require(hub.sent("POST", "/api/v1/machines/m1/files/rename").first))
        #expect(rename["path"] as? String == "~/proj/a.txt" && rename["cwd"] as? String == "/w" && rename["name"] as? String == "b.txt")

        let moved = try await api.moveFiles(machine: "m1", paths: ["~/proj/b.txt"], cwd: "/w", dir: "~/d", conflict: "keep")
        #expect(moved.map(\.to) == ["~/d/b.txt"])
        let move = try body(try #require(hub.sent("POST", "/api/v1/machines/m1/files/move").first))
        #expect(move["paths"] as? [String] == ["~/proj/b.txt"] && move["dir"] as? String == "~/d" && move["conflict"] as? String == "keep")

        let trashed = try await api.trashFiles(machine: "m1", paths: ["~/d/b.txt"], cwd: "/w")
        #expect(trashed.map(\.trashID) == ["t1"])
        #expect(try await api.restoreFiles(machine: "m1", trashIDs: ["t1"]) == ["~/d/b.txt"])
        let restore = try body(try #require(hub.sent("POST", "/api/v1/machines/m1/files/restore").first))
        #expect(restore["trash_ids"] as? [String] == ["t1"])
    }

    @Test func movesWithoutAPolicyLeaveItOut() async throws {
        let hub = FakeHub()
        hub.on("POST", "/api/v1/machines/m1/files/move", json: #"{"moved": []}"#)
        _ = try await hub.client.moveFiles(machine: "m1", paths: ["a"], cwd: "", dir: "~")
        let move = try body(try #require(hub.sent("POST", "/api/v1/machines/m1/files/move").first))
        #expect(move["conflict"] == nil)
    }

    @Test func renamePreservesTheSelectedAndEnteredNames() async throws {
        let hub = FakeHub()
        let route = "/api/v1/machines/m1/files/rename"
        hub.on("POST", route, json: #"{"path":"~/proj/new "}"#)
        let folder = FileManager.default.temporaryDirectory.appending(path: "folder-edits-\(UUID().uuidString)")
        let model = AppModel(stateFolder: folder)
        model.apiForTests = hub.client
        let edits = FolderEdits()
        edits.rename("~/proj/old ", to: "old ", cwd: "/project", machine: MachineRef(id: "m1"), model: model) {}
        #expect(!edits.busy && hub.sent("POST", route).isEmpty)
        edits.rename("~/proj/old ", to: "new ", cwd: "/project", machine: MachineRef(id: "m1"), model: model) {}
        try await waitForRefreshTest { !edits.busy }
        let request = try body(try #require(hub.sent("POST", route).first))
        #expect(request["path"] as? String == "~/proj/old ")
        #expect(request["name"] as? String == "new ")
    }

    @Test func namesAreCheckedBeforeAsking() {
        #expect(FolderEdits.nameProblem("notes.txt") == nil)
        #expect(FolderEdits.nameProblem(".env") == nil)
        for bad in ["", "  ", ".", "..", "a/b"] {
            #expect(FolderEdits.nameProblem(bad) != nil, "\(bad)")
        }
    }

    @Test func whitespaceNamesHaveVisibleAndSpokenBoundaries() throws {
        let space = String(localized: "space [File Name Whitespace]")
        let nbsp = String(localized: "non-breaking space [File Name Whitespace]")
        #expect(FileNameDisplay.label("report") == "report")
        #expect(FileNameDisplay.spoken("report") == "report")
        #expect(FileNameDisplay.label("report ") == "⟦\"report\"[\(space)]⟧")
        #expect(FileNameDisplay.label("report\u{a0}") == "⟦\"report\"[\(nbsp)]⟧")
        #expect(FileNameDisplay.label("  report\t") == "⟦[\(space) ×2]\"report\"[\(String(localized: "tab [File Name Whitespace]"))]⟧")
        #expect(FileNameDisplay.label(" \u{a0}") == "⟦[\(space), \(nbsp)]\"\"⟧")
        #expect(FileNameDisplay.label("report\u{200b}") == "⟦\"report\"[U+200B]⟧")
        #expect(FileNameDisplay.label("a b") == "a b")
        let markedParts = try #require(FileNameDisplay.parts(" report "))
        #expect(markedParts.body == "report" && !markedParts.leading.isEmpty && !markedParts.trailing.isEmpty)
        #expect(FileNameDisplay.spoken("report ") != FileNameDisplay.spoken("report\u{a0}"))
        #expect(FileNameDisplay.spoken("report ") != FileNameDisplay.spoken("report"))
        let marked = FileNameDisplay.label("report ")
        #expect(marked != FileNameDisplay.label("report[space]"))
        #expect(marked != FileNameDisplay.label(marked))
        let spoken = FileNameDisplay.spoken("report ")
        #expect(spoken != FileNameDisplay.spoken("report, trailing whitespace [space]"))
        #expect(spoken != FileNameDisplay.spoken(spoken))
        #expect(FileNameDisplay.hint("report") == "")
        #expect(FileNameDisplay.hint("report ") == spoken)
        #expect(FileNameDisplay.label(marked).contains(String(localized: "literal [File Name Whitespace]")))
    }

    @Test func refusalsInTheUsersWords() {
        let readOnly = APIError(status: 409, message: "x is read-only", fields: ["code": "read_only"])
        #expect(FolderEdits.message(readOnly).contains("~/.persona"))
        let exists = APIError(status: 409, message: "taken", fields: ["code": "exists", "name": "a.txt"])
        #expect(FolderEdits.message(exists).contains("a.txt"))
        let outdated = APIError(status: 409, message: "personad on box is too old", fields: ["code": "node_outdated"])
        #expect(FolderEdits.message(outdated) == "personad on box is too old")
        let oldHub = APIError(status: 404, message: "404 page not found")
        #expect(FolderEdits.message(oldHub) != oldHub.message)
        let gone = APIError(status: 404, message: "~/a is not there", fields: ["code": "not_found"])
        #expect(FolderEdits.message(gone) != gone.message)
        let other = APIError(status: 502, message: "~/a is on another disk", fields: ["code": "internal"])
        #expect(FolderEdits.message(other).contains("HTTP 502"))
        #expect(!FolderEdits.message(other).contains("~/a"))
    }

    @Test func folderNames() {
        #expect(FolderEdits.folderName("~") == "~")
        #expect(FolderEdits.folderName("~/proj/") == "proj")
        #expect(FolderEdits.folderName("/") == "/")
        #expect(FolderEdits.folderName("/Users/me/Downloads") == "Downloads")
    }
}
