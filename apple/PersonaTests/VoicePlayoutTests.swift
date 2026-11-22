import Foundation
import Testing
@testable import Persona

/// The voice's jitter buffer (#4430): nothing plays until the lead has gathered or the wait ran out, then everything held
/// goes at once; while the voice plays, each piece goes straight behind it; a player that ran dry gathers again, and a
/// player that ran dry mid-reply gathers more from then on (#12922).
struct VoicePlayoutTests {
    /// 24 kHz: the lead is 4800 frames, a 40 ms piece 960.
    private let piece = 960
    /// The measured service's piece: 384 ms at 16 kHz.
    private let servicePiece = 6144

    @Test func theLeadIsTwoHundredMillisecondsAtTheVoicesRate() {
        #expect(VoicePlayout<Int>(rate: 24000).leadFrames == 4800)
        #expect(VoicePlayout<Int>(rate: 16000).leadFrames == 3200)
        #expect(VoicePlayout<Int>(rate: 16000).waitNow == VoicePlayout<Int>.wait)
        #expect(VoicePlayout<Int>.wait > VoicePlayout<Int>.lead, "a voice sent as fast as it plays fills the lead before the wait runs out")
        #expect(VoicePlayout<Int>.raisedLeads == VoicePlayout<Int>.raisedLeads.sorted())
        #expect(VoicePlayout<Int>.raisedLeads.first! > VoicePlayout<Int>.lead)
    }

    @Test func piecesAreHeldUntilTheLeadThenPlayTogetherInOrder() {
        var playout = VoicePlayout<Int>(rate: 24000)
        for n in 1...4 {
            #expect(playout.add(n, frames: piece, playing: false, at: 0).isEmpty)
            #expect(playout.isHolding)
        }
        #expect(playout.add(5, frames: piece, playing: false, at: 0) == [1, 2, 3, 4, 5])
        #expect(!playout.isHolding)
    }

    @Test func oneLongPieceFromAFastServicePlaysAtOnce() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: 24000, playing: false, at: 0) == [1])
        #expect(!playout.isHolding)
    }

    @Test func whileTheVoicePlaysEachPieceGoesStraightBehindIt() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: piece, playing: true, at: 0) == [1])
        #expect(playout.add(2, frames: piece, playing: true, at: 0) == [2])
        #expect(!playout.isHolding)
    }

    @Test func aPlayerThatRanDryGathersAgain() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: 4800, playing: false, at: 0) == [1])
        #expect(playout.add(2, frames: piece, playing: true, at: 0) == [2])
        // The player caught up with the stream: the next piece waits for a lead of its own.
        #expect(playout.add(3, frames: piece, playing: false, at: 0).isEmpty)
        #expect(playout.add(4, frames: 3840, playing: false, at: 0) == [3, 4])
    }

    @Test func aShortReplyPlaysWhenTheWaitRunsOut() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.due == nil)
        #expect(playout.add(1, frames: piece, playing: false, at: 10).isEmpty)
        #expect(playout.add(2, frames: piece, playing: false, at: 10.1).isEmpty)
        #expect(playout.due == 10 + VoicePlayout<Int>.wait, "from the first held piece, at the first lead")
        #expect(playout.release() == [1, 2])
        #expect(!playout.isHolding)
        #expect(playout.due == nil)
        #expect(playout.release().isEmpty)
        // The count starts over: the next reply gathers a whole lead again.
        #expect(playout.add(3, frames: 3840, playing: false, at: 20).isEmpty)
    }

    @Test func clearDropsWhatIsHeld() {
        var playout = VoicePlayout<Int>(rate: 24000)
        #expect(playout.add(1, frames: 3840, playing: false, at: 0).isEmpty)
        playout.clear()
        #expect(!playout.isHolding)
        #expect(playout.add(2, frames: piece, playing: false, at: 0).isEmpty, "what was cleared does not count toward the lead")
    }

    // MARK: The lead grows when the voice stalls (#12922)

    @Test func aPlayerThatRanDryMidReplyGathersALongerLead() {
        var playout = VoicePlayout<Int>(rate: 16000)
        // One of the service's pieces is more than the first lead: it plays at once.
        #expect(playout.add(1, frames: servicePiece, playing: false, at: 0) == [1])
        playout.ranDry(at: 0.384)
        // The next piece came 116 ms after the player ran dry: the link falls behind the voice.
        #expect(playout.add(2, frames: servicePiece, playing: false, at: 0.5).isEmpty, "held: one piece is less than the raised lead")
        #expect(playout.level == 1)
        #expect(playout.stalls == 1)
        #expect(abs(playout.lastGap - 0.116) < 0.000_1)
        #expect(playout.leadNow == VoicePlayout<Int>.raisedLeads[0])
        #expect(playout.leadFrames == Int(16000 * VoicePlayout<Int>.raisedLeads[0]))
        #expect(playout.add(3, frames: servicePiece, playing: false, at: 0.9) == [2, 3])
    }

    @Test func theNextReplyIsNotAStall() {
        var playout = VoicePlayout<Int>(rate: 16000)
        #expect(playout.add(1, frames: servicePiece, playing: false, at: 0) == [1])
        playout.ranDry(at: 0.384)
        // The next reply came after the user spoke: the lead stays as it was.
        #expect(playout.add(2, frames: servicePiece, playing: false, at: 0.384 + VoicePlayout<Int>.stallWindow + 0.01) == [2])
        #expect(playout.level == 0)
        #expect(playout.stalls == 0)
    }

    @Test func morePlayingForgetsThatThePlayerRanDry() {
        var playout = VoicePlayout<Int>(rate: 16000)
        #expect(playout.add(1, frames: servicePiece, playing: false, at: 0) == [1])
        playout.ranDry(at: 0.384)
        #expect(playout.add(2, frames: servicePiece, playing: true, at: 0.4) == [2], "a cue or a late completion: it still plays")
        #expect(playout.add(3, frames: servicePiece, playing: false, at: 0.5) == [3])
        #expect(playout.level == 0)
    }

    @Test func clearIsNotAStall() {
        var playout = VoicePlayout<Int>(rate: 16000)
        #expect(playout.add(1, frames: servicePiece, playing: false, at: 0) == [1])
        playout.ranDry(at: 0.384)
        playout.clear() // the user cut in: the next piece is the next reply
        #expect(playout.add(2, frames: servicePiece, playing: false, at: 0.5) == [2])
        #expect(playout.level == 0)
    }

    @Test func theLeadStopsGrowingAtTheLastStep() {
        var playout = VoicePlayout<Int>(rate: 16000)
        var now: TimeInterval = 0
        for _ in 0..<(VoicePlayout<Int>.raisedLeads.count + 2) {
            _ = playout.add(0, frames: 16000 * 3, playing: false, at: now)
            now += 3
            playout.ranDry(at: now)
            now += 0.1
        }
        #expect(playout.level == VoicePlayout<Int>.raisedLeads.count)
        #expect(playout.leadNow == VoicePlayout<Int>.raisedLeads.last)
        #expect(playout.stalls == VoicePlayout<Int>.raisedLeads.count + 1)
        #expect(VoicePlayout<Int>(rate: 16000, level: 99).level == VoicePlayout<Int>.raisedLeads.count)
        #expect(VoicePlayout<Int>(rate: 16000, level: -1).level == 0)
    }

    @Test func aRaisedLeadPlaysWhatIsHeldOnceTheStreamIsQuiet() {
        var playout = VoicePlayout<Int>(rate: 16000, level: VoicePlayout<Int>.raisedLeads.count)
        let wait = VoicePlayout<Int>.wait * VoicePlayout<Int>.raisedLeads.last! / VoicePlayout<Int>.lead
        #expect(playout.waitNow == wait)
        #expect(playout.add(1, frames: piece, playing: false, at: 0).isEmpty)
        #expect(playout.due == VoicePlayout<Int>.quiet, "a short reply waits for the stream to go quiet, not the whole lead")
        #expect(playout.add(2, frames: piece, playing: false, at: 0.5).isEmpty)
        #expect(playout.due == 0.5 + VoicePlayout<Int>.quiet)
        // Pieces that keep coming are gathered for the lead, but no longer than the wait from the first.
        var now = 0.5
        while now + 0.9 < wait {
            now += 0.9
            #expect(playout.add(3, frames: piece, playing: false, at: now).isEmpty)
        }
        #expect(playout.due == min(wait, now + VoicePlayout<Int>.quiet))
    }

    @Test func aNewPlayerForTheSameCallKeepsTheRaisedLead() {
        let playout = VoicePlayout<Int>(rate: 24000, level: 2)
        #expect(playout.leadNow == VoicePlayout<Int>.raisedLeads[1])
        #expect(playout.leadFrames == Int(24000 * VoicePlayout<Int>.raisedLeads[1]))
        #expect(!playout.isHolding)
    }

    // MARK: Replays of a link that falls behind the voice

    /// A link that carries 94 % of the voice's rate, from the first piece: with the fixed lead every piece played on its
    /// own with a gap after it (the reported "a syllable or two at a time"); with the growing lead, a couple of pauses.
    @Test func aLinkSlightlySlowerThanTheVoicePausesTwiceNotOncePerPiece() {
        let step: TimeInterval = Double(servicePiece) / 16000 / 0.94
        let arrivals: [TimeInterval] = (0..<40).map { Double($0) * step }
        let fixed = replay(arrivals, frames: servicePiece, rate: 16000, tellsDry: false)
        #expect(fixed.gaps.count == arrivals.count - 1)
        let growing = replay(arrivals, frames: servicePiece, rate: 16000)
        #expect(growing.gaps.count <= 2)
        #expect(growing.level == 2)
    }

    /// The first second and a half comes at once, then the link carries 60 % of the voice: smooth at first, then a gap
    /// after every piece; with the growing lead, a few longer pauses.
    @Test func aLinkThatFallsFarBehindAfterTheFirstBurstPausesAFewTimes() {
        let step: TimeInterval = Double(servicePiece) / 16000 / 0.6
        let burst: [TimeInterval] = (0..<5).map { Double($0) * 0.06 }
        let behind: [TimeInterval] = (1...30).map { (n: Int) -> TimeInterval in step * Double(n) + 0.3 }
        let arrivals = burst + behind
        let fixed = replay(arrivals, frames: servicePiece, rate: 16000, tellsDry: false)
        #expect(fixed.gaps.count >= 25)
        let growing = replay(arrivals, frames: servicePiece, rate: 16000)
        #expect(growing.gaps.count <= 4)
        #expect(growing.level == VoicePlayout<Int>.raisedLeads.count)
    }

    /// The measured service sends about four times faster than it plays: no gap, and the lead never grows.
    @Test func aLinkThatKeepsUpNeverGrowsTheLead() {
        let step: TimeInterval = Double(servicePiece) / 16000 / 4
        let arrivals: [TimeInterval] = (0..<40).map { Double($0) * step }
        let replayed = replay(arrivals, frames: servicePiece, rate: 16000)
        #expect(replayed.gaps.isEmpty)
        #expect(replayed.level == 0)
    }

    /// Plays pieces of `frames` arriving at `arrivals` through a playout and a player that plays what it is given back to
    /// back: the gaps where the player had run dry, and the playout's level at the end. Without `tellsDry` the playout is
    /// never told the player ran dry, so its lead never grows (the playout before #12922).
    private func replay(_ arrivals: [TimeInterval], frames: Int, rate: Int, tellsDry: Bool = true) -> (gaps: [TimeInterval], level: Int) {
        var playout = VoicePlayout<Int>(rate: rate)
        let length = Double(frames) / Double(rate)
        var playEnd: TimeInterval?
        var gaps: [TimeInterval] = []
        func schedule(_ pieces: [Int], at now: TimeInterval) {
            guard !pieces.isEmpty else { return }
            if let end = playEnd, end < now { gaps.append(now - end) }
            playEnd = max(now, playEnd ?? now) + Double(pieces.count) * length
        }
        for (n, now) in arrivals.enumerated() {
            if let due = playout.due, due <= now { schedule(playout.release(), at: due) }
            let playing = (playEnd ?? -1) > now
            if tellsDry, !playing, !playout.isHolding, let end = playEnd { playout.ranDry(at: end) }
            schedule(playout.add(n, frames: frames, playing: playing, at: now), at: now)
        }
        if let due = playout.due { schedule(playout.release(), at: due) }
        return (gaps, playout.level)
    }
}
