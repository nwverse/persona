import Foundation
import Testing
@testable import Persona

/// UI version 6's pictures on iOS (#827, docs/extensions/extensions.md "Pictures through the hub"): the device's
/// credentials go only to the hub's image route signed for this page's extension, a picture loads through the page,
/// and the viewer gets its type from its bytes.
@MainActor
struct ExtensionMediaTests {
    @Test func onlyTheHubsImageRouteForThisExtension() {
        #expect(ExtensionViewModel.isHubPicture("/api/v1/extensions/home/-/image/eyJlIjoi.c2ln-_", owner: "home"))
        #expect(!ExtensionViewModel.isHubPicture("/api/v1/extensions/other/-/image/abc", owner: "home"), "another extension's")
        #expect(!ExtensionViewModel.isHubPicture("/api/v1/extensions/home/-/image/", owner: "home"), "no token")
        #expect(!ExtensionViewModel.isHubPicture("/api/v1/extensions/home/-/image/a/b", owner: "home"), "another route under it")
        #expect(!ExtensionViewModel.isHubPicture("/api/v1/extensions/home/-/image/a?x=1", owner: "home"))
        #expect(!ExtensionViewModel.isHubPicture("/api/v1/extensions/home/-/image/%2e%2e", owner: "home"))
        #expect(!ExtensionViewModel.isHubPicture("https://example.com/api/v1/extensions/home/-/image/a", owner: "home"), "an address")
        #expect(!ExtensionViewModel.isHubPicture("/api/v1/extensions/home/-/image/a", owner: ""))
    }

    @Test func aPictureLoadsThroughThePage() async throws {
        let hub = FakeHub()
        let png = Data([0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A])
        hub.on("POST", "/api/v1/plugins/home/-/ui/page", json: #"{"view":{"sections":[]}}"#)
        hub.on("GET", "/api/v1/extensions/home/-/image/tok") { _ in png }
        let m = ExtensionViewModel(plugin: "home", ui: "page", deps: .init(api: { hub.client }))
        #expect(try await m.picture("/api/v1/extensions/home/-/image/tok", pixels: 600) == png)
        let sent = try #require(hub.sent("GET", "/api/v1/extensions/home/-/image/tok").last)
        #expect(sent.query["w"] == "600", "about as wide as it is drawn")
        await #expect(throws: (any Error).self) { try await m.picture("/api/v1/extensions/other/-/image/tok", pixels: 600) }
        #expect(hub.sent("GET", "/api/v1/extensions/other/-/image/tok").isEmpty, "never sent")
    }

    @Test func theViewerKnowsThePicturesType() {
        #expect(ExtensionImageItem.payload(Data([0x89, 0x50, 0x4E, 0x47, 1]))?.mime == "image/png")
        #expect(ExtensionImageItem.payload(Data([0xFF, 0xD8, 0xFF, 0xE0]))?.mime == "image/jpeg")
        #expect(ExtensionImageItem.payload(Data([0x47, 0x49, 0x46, 0x38]))?.mime == "image/gif")
        #expect(ExtensionImageItem.payload(Data("<svg".utf8)) == nil, "never SVG")
    }
}
