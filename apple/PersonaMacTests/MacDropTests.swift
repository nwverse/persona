import Foundation
import Testing
@testable import Persona

/// Files dragged onto a chat's composer (docs/apple/mac.md「Details」): pictures go into the message as images, everything else
/// is uploaded to the session's machine and its path typed in, as the + menu's Photos / Files do.
struct MacDropTests {
    @Test func picturesBecomeImagesTheRestUploads() {
        let urls = ["/a/shot.png", "/a/photo.JPG", "/a/live.heic", "/a/scan.tiff", "/a/notes.md", "/a/data.zip", "/a/dir"]
            .map { URL(fileURLWithPath: $0) }
        let split = MacDrop.split(urls)
        #expect(split.images.map(\.lastPathComponent) == ["shot.png", "photo.JPG", "live.heic", "scan.tiff"])
        #expect(split.files.map(\.lastPathComponent) == ["notes.md", "data.zip", "dir"])
    }

    /// A message takes at most four pictures (the + menu's limit); the rest are uploaded as files.
    @Test func atMostFourPictures() {
        let urls = (1...6).map { URL(fileURLWithPath: "/a/\($0).png") }
        let split = MacDrop.split(urls, already: 1)
        #expect(split.images.count == 3)
        #expect(split.files.count == 3)
    }

    /// Folders are not uploaded (the upload sends one file); the drop says so instead of failing halfway.
    @Test func foldersAreLeftOut() {
        let split = MacDrop.split([URL(fileURLWithPath: "/a/project", isDirectory: true), URL(fileURLWithPath: "/a/x.txt")])
        #expect(split.files.map(\.lastPathComponent) == ["x.txt"])
        #expect(split.folders == 1)
    }

    @Test func webLinksAreNotFiles() {
        let split = MacDrop.split([URL(string: "https://example.com/a.png")!])
        #expect(split.images.isEmpty && split.files.isEmpty)
    }
}

/// ⌘V in a composer: copied files (Finder ⌘C) go the way a drop does, a copied picture (a screenshot to the clipboard)
/// is attached, and anything else is left to the text field.
struct MacPasteTests {
    @Test func copiedFilesGoTheDropWay() {
        let urls = [URL(fileURLWithPath: "/a/x.png")]
        #expect(MacPaste.plan(fileURLs: urls, hasImage: true, hasText: true) == .files(urls))
    }

    @Test func aCopiedPictureIsAttached() {
        #expect(MacPaste.plan(fileURLs: [], hasImage: true, hasText: false) == .image)
    }

    /// Numbers, Excel, a PDF viewer copy text with a picture of it: the text is what was meant.
    @Test func textWinsOverItsPicture() {
        #expect(MacPaste.plan(fileURLs: [], hasImage: true, hasText: true) == .text)
    }

    @Test func textIsTheFieldsJob() {
        #expect(MacPaste.plan(fileURLs: [], hasImage: false, hasText: true) == .text)
    }
}
