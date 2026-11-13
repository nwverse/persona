import Foundation
import Testing
import UniformTypeIdentifiers
@testable import Persona

/// Files pasted or dropped into a terminal the app drives (docs/integrations/terminal.md「Web clipboard: paste images/files, copy, OSC 52」): the same escaping, names and
/// guards as the web's `lib/terminal-clipboard.ts`.
struct TerminalFilesTests {
    /// Every path is one shell word: the same characters as the web's `escapePastePath` get a backslash.
    @Test func escapesPathsAsShellWords() {
        #expect(TerminalPaste.escape("/tmp/a-b_c.d/x@y%z+1=2:3,4.png") == "/tmp/a-b_c.d/x@y%z+1=2:3,4.png")
        #expect(TerminalPaste.escape("/tmp/my shot (1).png") == "/tmp/my\\ shot\\ \\(1\\).png")
        #expect(TerminalPaste.escape("/tmp/it's $HOME\"`") == "/tmp/it\\'s\\ \\$HOME\\\"\\`")
        #expect(TerminalPaste.escape("/tmp/截图 1.png") == "/tmp/截图\\ 1.png")
        #expect(TerminalPaste.escape("a;b&c|d\\e") == "a\\;b\\&c\\|d\\\\e")
    }

    /// The paths go in as one bracketed paste, space-separated with a space after the last, and no return.
    @Test func typesPathsAsOnePaste() {
        let typed = TerminalPaste.typed(["/u/a.png", "/u/b c.pdf"])
        #expect(typed == "\u{1B}[200~/u/a.png /u/b\\ c.pdf \u{1B}[201~")
        #expect(!typed.contains("\r") && !typed.contains("\n"))
    }

    @Test func refusesEmptyAndTooLargeFiles() {
        #expect(TerminalPaste.refusal(name: "a.png", size: 0) == "「a.png」是空的")
        #expect(TerminalPaste.refusal(name: "big.mov", size: TerminalPaste.limit + 1) == "「big.mov」超过 50 MB，没有上传")
        #expect(TerminalPaste.refusal(name: "ok.png", size: TerminalPaste.limit) == nil)
    }

    /// Only into the pane the files were meant for, and only while still driving it.
    @Test func pastesOnlyWhereItWasMeantTo() {
        #expect(TerminalPaste.verdict(left: false, driving: true) == .ok)
        #expect(TerminalPaste.verdict(left: false, driving: false) == .watching)
        #expect(TerminalPaste.verdict(left: true, driving: true) == .changed)
    }

    @Test func namesWhatIsOnItsWay() {
        #expect(TerminalPaste.uploadingText(["shot.png"]) == "正在上传 shot.png…")
        #expect(TerminalPaste.uploadingText(["shot.png", "b", "c"]) == "正在上传 shot.png 等 3 个文件…")
    }

    /// Pictures without a name are named by their type; HEIC goes as JPEG (agents read PNG, JPEG, GIF, WebP).
    @Test func namesPhotosAndPictures() {
        #expect(TerminalPaste.pictureName(.png) == "image.png")
        #expect(TerminalPaste.photoName(.heic) == "image.heic")
        #expect(TerminalPaste.photoName(.quickTimeMovie) == "video.mov")
        #expect(TerminalPaste.photoName(nil) == "image.jpg")
        #expect(TerminalPaste.needsJPEG(.heic))
        #expect(TerminalPaste.needsJPEG(.tiff))
        #expect(!TerminalPaste.needsJPEG(.png))
        #expect(!TerminalPaste.needsJPEG(.jpeg))
        #expect(!TerminalPaste.needsJPEG(.quickTimeMovie))
        #expect(!TerminalPaste.needsJPEG(nil))
    }

    /// Folders are counted, not uploaded; links that are not files are ignored.
    @Test func leavesFoldersOut() {
        let split = TerminalPaste.files([URL(fileURLWithPath: "/a/project", isDirectory: true), URL(fileURLWithPath: "/a/x.txt"),
                                         URL(string: "https://example.com/a.png")!])
        #expect(split.files.map(\.lastPathComponent) == ["x.txt"])
        #expect(split.folders == 1)
    }
}
