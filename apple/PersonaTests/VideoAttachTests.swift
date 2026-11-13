import AVFoundation
import CoreMedia
import Foundation
import Testing
@testable import Persona

/// Videos sent as files (VideoAttach.swift, docs/protocol/protocol.md「Sending videos」).
struct VideoAttachTests {
    private let clip = AttachedFile(path: "/tmp/IMG_0001-20261103-150102.MOV", name: "IMG_0001.MOV", size: 25_270_000, duration: 12.4)

    @Test func theTagGoesAfterWhatWasTypedAndComesBack() {
        let text = AttachedFile.message("看看这段", files: [clip])
        #expect(text == "看看这段\n\n<persona-file kind=\"video\" path=\"/tmp/IMG_0001-20261103-150102.MOV\" name=\"IMG_0001.MOV\" size=\"25270000\" duration=\"12.4\"/>")
        let parsed = AttachedFile.parse(text)
        #expect(parsed.files == [clip])
        #expect(parsed.rest == "看看这段")
        #expect(AttachedFile.message("看看这段", files: []) == "看看这段")
    }

    @Test func oddNamesAndMissingFieldsSurvive() {
        let odd = AttachedFile(path: "/tmp/a \"b\" <c>&d.mp4", name: "\"我的\" <视频> & 1.mp4", size: 0, duration: nil)
        let parsed = AttachedFile.parse(AttachedFile.message("x", files: [odd, clip]))
        #expect(parsed.files == [odd, clip])
        // Written by hand (or by an older build): no name, a closing tag, no duration; one without a path is dropped.
        let loose = AttachedFile.parse("<persona-file path=\"/tmp/v.mp4\" size=\"12\"></persona-file>\n<persona-file kind=\"video\"/>\n好")
        #expect(loose.files == [AttachedFile(path: "/tmp/v.mp4", name: "v.mp4", size: 12, duration: nil)])
        #expect(loose.rest == "好")
        #expect(AttachedFile.parse("没有附件").files.isEmpty)
    }

    @Test func theCardSaysSizeAndLength() {
        #expect(clip.detail.hasSuffix(" · 0:12"))
        #expect(clip.detail.contains("MB"))
        #expect(AttachedFile(path: "/a", name: "a", size: 1000, duration: 3725).detail.hasSuffix(" · 1:02:05"))
        #expect(!AttachedFile(path: "/a", name: "a", size: 1000, duration: nil).detail.contains("·"))
    }

    @Test func theBubbleShowsTheTextAndACardNotTheTag() {
        let raw = "<persona-context kind=\"context\" title=\"T\" url=\"\">x</persona-context>\n\n看看 /tmp/notes.md\n\n" + clip.xml
        let row = UserRowText(raw)
        #expect(row.shown == "看看 /tmp/notes.md")
        #expect(row.attached == [clip])
        #expect(row.files == ["/tmp/notes.md"], "the video's path is its card, not a chip too")
        #expect(UserBubble.display(AttachedFile.message("请看视频", files: [clip])) == "请看视频")
    }

    @Test func theWatchSaysVideo() {
        #expect(WatchText.shown(AttachedFile.message("请看视频", files: [clip, clip])) == "请看视频\n〔视频〕")
        #expect(WatchText.shown("<persona-file kind=\"doc\" path=\"/tmp/a.pdf\"/>") == "〔文件〕")
    }

    @Test func compressionTriesTheSizesThatFit() {
        let limit: Int64 = 200 << 20
        let est: [(preset: String, bytes: Int64)] = [("1080", 400 << 20), ("720", 185 << 20), ("540", 120 << 20)]
        #expect(VideoPrep.candidates(est, limit: limit) == ["540"], "720p's estimate is within 10 % of the cap: too close")
        #expect(VideoPrep.candidates([("1080", 150 << 20), ("720", 90 << 20)], limit: limit) == ["1080", "720"])
        #expect(VideoPrep.candidates([("1080", 0), ("720", 500 << 20), ("540", 0)], limit: limit) == ["1080", "540"], "no estimate: try it")
        #expect(VideoPrep.candidates([("540", 300 << 20)], limit: limit).isEmpty)
        #expect(VideoPrep.presets == [AVAssetExportPreset1920x1080, AVAssetExportPreset1280x720, AVAssetExportPreset960x540])
    }

    @Test func pickerMadeUpNames() {
        #expect(VideoPrep.name("IMG_0001.MOV") == "IMG_0001.MOV")
        #expect(VideoPrep.name("海边 2.mov") == "海边 2.mov")
        #expect(VideoPrep.name("version=1&uuid=4F6C0E7A&mode=current.mov") == "视频.mov")
    }

    @Test func messages() {
        let long = VideoAttachments.message(VideoPrep.Failure.tooLong, name: "IMG_0002.MOV", duration: 754.2)
        #expect(long == "「IMG_0002.MOV」有 12:34，压缩到 540p 也超过 200 MB，发不了：先在「照片」里剪短再发")
        #expect(VideoAttachments.message(CancellationError(), name: "a.mov", duration: nil) == nil)
        #expect(VideoAttachments.message(URLError(.cancelled), name: "a.mov", duration: nil) == nil)
        #expect(VideoAttachments.message(CocoaError(.fileReadUnknown), name: "视频", duration: nil)?.contains("iCloud") == true)
        let old = APIError(status: 409, message: "MacBook 上的 personad 太旧，不能接收文件，需要重新部署", fields: ["code": "node_outdated"])
        #expect(VideoAttachments.message(old, name: "a.mov", duration: nil) == old.message)
    }

    @Test func aVideoThatFitsGoesAsItIs() async throws {
        let dir = try Self.scratch()
        defer { try? FileManager.default.removeItem(at: dir) }
        let src = try await Self.makeClip(in: dir)
        let size = VideoPrep.size(src)
        #expect(size > 0)
        #expect(try await VideoPrep.fit(src, limit: size) { _ in } == src)
        await #expect(throws: VideoPrep.Failure.tooLong) { try await VideoPrep.fit(src, limit: 64) { _ in } }
        #expect(!FileManager.default.fileExists(atPath: dir.appending(path: "compressed").path), "what did not fit is gone")
    }

    @Test func compressedIsH264MP4OfTheSameLength() async throws {
        let dir = try Self.scratch()
        defer { try? FileManager.default.removeItem(at: dir) }
        let src = try await Self.makeClip(in: dir)
        let out = dir.appending(path: "out.mp4")
        let progress = ProgressLog()
        try await VideoPrep.export(src, preset: AVAssetExportPreset960x540, to: out) { progress.append($0) }
        #expect(progress.values.last == 1)
        #expect(progress.values.allSatisfy { (0...1).contains($0) })
        let asset = AVURLAsset(url: out)
        let track = try #require(try await asset.loadTracks(withMediaType: .video).first)
        let formats = try await track.load(.formatDescriptions)
        #expect(formats.first.map(CMFormatDescriptionGetMediaSubType) == kCMVideoCodecType_H264)
        let seconds = try await asset.load(.duration).seconds
        #expect(abs(seconds - 1) < 0.2, "\(seconds)")
    }

    @Test func cancelledExportDoesNotStartOrReportProgress() async throws {
        let dir = try Self.scratch()
        defer { try? FileManager.default.removeItem(at: dir) }
        let src = try await Self.makeClip(in: dir)
        let out = dir.appending(path: "cancelled.mp4")
        let progress = ProgressLog()
        let job = Task {
            withUnsafeCurrentTask { $0?.cancel() }
            try await VideoPrep.export(src, preset: AVAssetExportPreset960x540, to: out) { progress.append($0) }
        }
        await #expect(throws: CancellationError.self) { try await job.value }
        #expect(progress.values.isEmpty)
        #expect(!FileManager.default.fileExists(atPath: out.path))
    }

    @Test func failedExportStopsReportingBeforeItThrows() async throws {
        let dir = try Self.scratch()
        defer { try? FileManager.default.removeItem(at: dir) }
        let src = try await Self.makeClip(in: dir)
        let progress = ProgressLog()
        do {
            // An existing directory is not an output movie file.
            try await VideoPrep.export(src, preset: AVAssetExportPreset960x540, to: dir) { progress.append($0) }
            Issue.record("Export to an existing directory unexpectedly succeeded")
        } catch {}
        let samples = progress.values
        try await Task.sleep(for: .milliseconds(300))
        #expect(progress.values == samples)
    }

    @Test func playedCopiesAreKeptWithinABudget() throws {
        let folder = try Self.scratch()
        defer { try? FileManager.default.removeItem(at: folder) }
        let mac = MachineRef(id: "macbook"), other = MachineRef(group: "work", id: "macbook")
        func file(_ name: String, _ bytes: Int) throws -> URL {
            let url = FileManager.default.temporaryDirectory.appending(path: UUID().uuidString + "-" + name)
            try Data(count: bytes).write(to: url)
            return url
        }
        let a = try #require(AttachedVideos.keep(try file("a.mp4", 600), mac, "/tmp/a.mp4", in: folder, budget: 1 << 20))
        #expect(AttachedVideos.local(mac, "/tmp/a.mp4", in: folder) == a)
        #expect(AttachedVideos.local(other, "/tmp/a.mp4", in: folder) == nil, "another group's machine of the same id")
        #expect(AttachedVideos.local(mac, "/tmp/b.mp4", in: folder) == nil)
        // A second copy over the budget pushes the older one out; the new one stays however big it is.
        try FileManager.default.setAttributes([.modificationDate: Date(timeIntervalSinceNow: -60)], ofItemAtPath: a.deletingLastPathComponent().path)
        let b = try #require(AttachedVideos.keep(try file("b.mp4", 600 << 10), mac, "/tmp/b.mp4", in: folder, budget: 300 << 10))
        #expect(AttachedVideos.local(mac, "/tmp/b.mp4", in: folder) == b)
        #expect(AttachedVideos.local(mac, "/tmp/a.mp4", in: folder) == nil)
    }

    // MARK: -

    private nonisolated final class ProgressLog: @unchecked Sendable {
        private let lock = NSLock()
        private var samples: [Double] = []

        func append(_ value: Double) { lock.withLock { samples.append(value) } }
        var values: [Double] { lock.withLock { samples } }
    }

    private static func scratch() throws -> URL {
        let dir = FileManager.default.temporaryDirectory.appending(path: "video-tests-" + UUID().uuidString)
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        return dir
    }

    /// A made-up second of H.264 (a grey frame that brightens), 320 × 240 at 30 fps.
    private static func makeClip(in dir: URL) async throws -> URL {
        let url = dir.appending(path: "clip.mov")
        let writer = try AVAssetWriter(outputURL: url, fileType: .mov)
        let input = AVAssetWriterInput(mediaType: .video, outputSettings: [
            AVVideoCodecKey: AVVideoCodecType.h264, AVVideoWidthKey: 320, AVVideoHeightKey: 240,
        ])
        input.expectsMediaDataInRealTime = false
        let adaptor = AVAssetWriterInputPixelBufferAdaptor(assetWriterInput: input, sourcePixelBufferAttributes: [
            kCVPixelBufferPixelFormatTypeKey as String: kCVPixelFormatType_32BGRA, kCVPixelBufferWidthKey as String: 320,
            kCVPixelBufferHeightKey as String: 240,
        ])
        writer.add(input)
        #expect(writer.startWriting())
        writer.startSession(atSourceTime: .zero)
        for frame in 0..<30 {
            while !input.isReadyForMoreMediaData { try await Task.sleep(for: .milliseconds(5)) }
            var buffer: CVPixelBuffer?
            CVPixelBufferPoolCreatePixelBuffer(nil, try #require(adaptor.pixelBufferPool), &buffer)
            let pixels = try #require(buffer)
            CVPixelBufferLockBaseAddress(pixels, [])
            memset(CVPixelBufferGetBaseAddress(pixels), Int32(60 + frame * 6), CVPixelBufferGetDataSize(pixels))
            CVPixelBufferUnlockBaseAddress(pixels, [])
            adaptor.append(pixels, withPresentationTime: CMTime(value: CMTimeValue(frame), timescale: 30))
        }
        input.markAsFinished()
        writer.endSession(atSourceTime: CMTime(value: 30, timescale: 30))
        await writer.finishWriting()
        #expect(writer.status == .completed, "\(String(describing: writer.error))")
        return url
    }
}
