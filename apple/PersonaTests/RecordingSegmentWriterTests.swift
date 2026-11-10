import AVFoundation
import Foundation
import Testing
@testable import Persona

struct RecordingSegmentWriterTests {
    private func silence(seconds: Double) throws -> AVAudioPCMBuffer {
        let format = try #require(AVAudioFormat(commonFormat: .pcmFormatFloat32, sampleRate: RecordingAudioSpec.sampleRate,
                                                channels: 1, interleaved: false))
        let frames = AVAudioFrameCount(seconds * RecordingAudioSpec.sampleRate)
        let buffer = try #require(AVAudioPCMBuffer(pcmFormat: format, frameCapacity: frames))
        buffer.frameLength = frames
        buffer.floatChannelData?[0].update(repeating: 0, count: Int(frames))
        return buffer
    }

    /// Segment start times come from the audio, not from when a segment is reported, so a pause or a late callback
    /// doesn't shift them.
    @Test func segmentsCarryTheWallClockOfTheirFirstSample() throws {
        let dir = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        defer { try? FileManager.default.removeItem(at: dir) }
        try FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
        let writer = RecordingSegmentWriter(directory: dir)
        writer.segmentLength = 10
        let start = Date(timeIntervalSince1970: 1_800_000_000)
        writer.append(try silence(seconds: 25), startedAt: start)
        // Paused for a while: the third segment started before the pause and finishes after it.
        writer.append(try silence(seconds: 5), startedAt: start.addingTimeInterval(100))
        writer.append(try silence(seconds: 10), startedAt: start.addingTimeInterval(200))
        let segments = writer.finishedSegments
        #expect(segments.map(\.index) == [0, 1, 2, 3])
        #expect(segments.map(\.startedAt) == [0, 10, 20, 200].map { start.addingTimeInterval($0) })
        #expect(segments.map(\.start) == [0, 10, 20, 30])
        #expect(segments.allSatisfy { abs($0.duration - 10) < 0.001 })
    }
}
