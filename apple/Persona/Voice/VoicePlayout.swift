import Foundation

/// The voice's jitter buffer (#4430). The extension relays the upstream's audio as it comes, and a piece that reaches the
/// player after the one before it finished leaves a gap: scheduled the moment it arrived, the voice played a couple of
/// syllables, stopped, and went on. So when nothing is playing, the pieces are held until a lead of them has gathered,
/// or the first has waited long enough (a short reply, the tail of one), and go to the player together; while it plays,
/// each goes straight behind the last. A service that sends faster than it plays fills the lead almost at once.
///
/// The lead grows when the voice stalls (#12922). The measured service sends a reply in 384 ms pieces, about four times
/// faster than it plays, so `lead` costs nothing on a link that keeps up; but a single piece is more than the lead, so
/// on a link that carries a little less than the voice every piece played on its own with a gap after it. A player that
/// runs dry mid-reply (the next piece comes within `stallWindow` of the last one finishing) raises the lead for the rest
/// of the call, step by step (`raisedLeads`): a few longer pauses instead of one per piece, and the next replies start
/// with that much in hand. While a raised lead gathers, what is held plays once nothing more has come for `quiet`, so a
/// short reply or the tail of one does not wait out the whole lead.
nonisolated struct VoicePlayout<Piece> {
    /// How much to gather before playing, until the voice stalls.
    static var lead: TimeInterval { 0.2 }
    /// The longest the first held piece waits for the rest of `lead`; a raised lead waits as much longer.
    static var wait: TimeInterval { 0.3 }
    /// The leads after the first stall, the second and the third or more.
    static var raisedLeads: [TimeInterval] { [0.6, 1.2, 2.0] }
    /// A piece that comes this soon after the player ran dry is the same reply arriving late, not the next one.
    static var stallWindow: TimeInterval { 1.5 }
    /// While a raised lead gathers, what is held plays once nothing more has come for this long.
    static var quiet: TimeInterval { 1.0 }

    /// The voice's rate in frames per second.
    let rate: Int
    /// How often the voice stalled, at most the number of `raisedLeads`: the lead in use.
    private(set) var level: Int
    /// How often the voice stalled in all, and the last gap: for the call's log.
    private(set) var stalls = 0
    private(set) var lastGap: TimeInterval = 0
    private(set) var held: [Piece] = []
    private var heldFrames = 0
    private var firstHeldAt: TimeInterval = 0
    private var lastHeldAt: TimeInterval = 0
    /// When the player last played out everything it was given (nil: not since it started, was cleared or got more).
    private var dryAt: TimeInterval?

    /// A new buffer at `rate`, at `level` (a new player for the same call keeps how far its voice stalled).
    init(rate: Int, level: Int = 0) {
        self.rate = rate
        self.level = min(max(level, 0), Self.raisedLeads.count)
    }

    /// How much to gather before playing now.
    var leadNow: TimeInterval { level == 0 ? Self.lead : Self.raisedLeads[level - 1] }

    /// `leadNow` in frames at the voice's rate.
    var leadFrames: Int { max(1, Int(Double(rate) * leadNow)) }

    /// The longest the first held piece waits for the rest of `leadNow`.
    var waitNow: TimeInterval { level == 0 ? Self.wait : Self.wait * leadNow / Self.lead }

    /// Something is held, waiting for the lead or for `due`.
    var isHolding: Bool { !held.isEmpty }

    /// When what is held is to play, lead or not (nil: nothing is held): once the first has waited `waitNow`, and while a
    /// raised lead gathers, once nothing more has come for `quiet`.
    var due: TimeInterval? {
        guard !held.isEmpty else { return nil }
        let waited = firstHeldAt + waitNow
        return level == 0 ? waited : min(waited, lastHeldAt + Self.quiet)
    }

    /// A piece of the voice, `frames` long, arrived at `now` while the player had some of it queued (`playing`) or
    /// none: what to schedule now, in order (empty while it is held).
    mutating func add(_ piece: Piece, frames: Int, playing: Bool, at now: TimeInterval) -> [Piece] {
        if playing, held.isEmpty {
            dryAt = nil
            return [piece]
        }
        if held.isEmpty {
            if !playing, let dryAt, now - dryAt < Self.stallWindow {
                stalls += 1
                lastGap = max(0, now - dryAt)
                level = min(level + 1, Self.raisedLeads.count)
            }
            dryAt = nil
            firstHeldAt = now
        }
        held.append(piece)
        heldFrames += frames
        lastHeldAt = now
        return heldFrames >= leadFrames ? release() : []
    }

    /// The player played out everything it was given, at `now`.
    mutating func ranDry(at now: TimeInterval) {
        dryAt = now
    }

    /// What is held, to play now (`due` came); nothing is held afterwards.
    mutating func release() -> [Piece] {
        defer {
            held = []
            heldFrames = 0
        }
        return held
    }

    /// Drops what is held (`clear`, a new player): the next piece is not taken for one that came late.
    mutating func clear() {
        held = []
        heldFrames = 0
        dryAt = nil
    }
}
