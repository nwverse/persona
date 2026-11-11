#if os(iOS)
import AVFoundation
import NaturalLanguage
import Observation
import OSLog
import PersonaKit

/// Says Persona's answers out loud in a meeting it takes part in (#2981, docs/apple/recording.md「Persona in meetings」).
/// On the phone with an on-device voice for the answer's language, in the meeting recording's own audio session (so the
/// recording goes on), with the voice call's route rule for where it plays (`VoiceRoute.output`): a connected headset,
/// else the loudspeaker, never the earpiece; the recording keeps its microphone. Every stretch the phone speaks in is
/// marked on the recording (`RecordingAttendance.spoken`): a sentence at least half inside one is Persona's own voice and
/// stays out of the transcript and the live log (the contract requires it).
@MainActor @Observable
final class MeetingSpeaker: NSObject {
    static let shared = MeetingSpeaker()

    /// The exchange being said (`MeetingAsk.Exchange.id`), nil when quiet.
    private(set) var speaking: UUID?

    @ObservationIgnored private let synthesizer = AVSpeechSynthesizer()
    @ObservationIgnored private var recording: UUID?
    /// The utterance being said, held so its identity can't be reused while it plays: a late "cancelled" from the one
    /// before must not end the next.
    @ObservationIgnored private var utterance: AVSpeechUtterance?
    /// Where on the recording this answer began.
    @ObservationIgnored private var spanStart: Double?
    @ObservationIgnored private var overrodeOutput = false
    private static let log = Logger(subsystem: "persona", category: "meeting-speaker")

    override private init() {
        super.init()
        synthesizer.delegate = self
        // The recording's session, as it is: no category of its own, and it is never deactivated afterwards.
        synthesizer.usesApplicationAudioSession = true
    }

    /// The longest an answer can take aloud (the contract's 400 characters, slowly): a stretch never runs past it, even
    /// if the system never says the answer ended (an interruption that never ends).
    static let longestAnswer: Double = 150

    /// The stretch being said now, still open: a sentence transcribed meanwhile is checked against it.
    func openSpan(for id: UUID) -> RecordingSpan? {
        guard recording == id, let start = spanStart else { return nil }
        return RecordingSpan(start: start, end: min(max(RecordingCapture.shared.timeline, start), start + Self.longestAnswer))
    }

    /// Says `text` for the exchange `id` of `recording`, in `lang` (BCP 47, the hub's), else the language it is written
    /// in, else the recording's.
    func speak(_ text: String, id: UUID, recording: UUID, lang: String?, locale: String) {
        stop()
        let session = AVAudioSession.sharedInstance()
        let plan = VoiceRoute.output(PhoneVoiceAudio.snapshot(session))
        if plan.speaker {
            do {
                try session.overrideOutputAudioPort(.speaker)
                overrodeOutput = true
            } catch {
                Self.log.notice("meeting: could not move the answer to the speaker: \(error.localizedDescription, privacy: .public)")
            }
        }
        let next = AVSpeechUtterance(string: text)
        next.voice = Self.voice(lang: lang, text: text, fallback: locale)
        speaking = id
        self.recording = recording
        spanStart = RecordingCapture.shared.timeline
        utterance = next
        synthesizer.speak(next)
    }

    /// Stops at once (Stop Speaking, a new answer, the role no longer speaking, the recording ending).
    func stop() {
        guard speaking != nil else { return }
        synthesizer.stopSpeaking(at: .immediate)
        finished()
    }

    #if DEBUG
    /// Screenshots (`-showMeetingSample answered`): the exchange shows as being said, with no voice.
    func showSample(_ id: UUID) { speaking = id }
    #endif

    /// The answer ended or was cut: the stretch it took is marked on the recording, and the route goes back.
    private func finished() {
        if let id = recording, let span = openSpan(for: id) {
            RecordingLibrary.shared.change(id) { entry in entry.attendance?.spoken.append(span) }
        }
        if overrodeOutput {
            try? AVAudioSession.sharedInstance().overrideOutputAudioPort(.none)
            overrodeOutput = false
        }
        speaking = nil
        recording = nil
        spanStart = nil
        utterance = nil
    }

    private static func voice(lang: String?, text: String, fallback: String) -> AVSpeechSynthesisVoice? {
        if let lang, let voice = AVSpeechSynthesisVoice(language: lang) { return voice }
        return AVSpeechSynthesisVoice(language: language(of: text, fallback: fallback))
    }

    /// The BCP 47 language to say `text` in: the one it is written in, when the recognizer is sure, else `fallback`
    /// (the recording's language).
    nonisolated static func language(of text: String, fallback: String) -> String {
        let recognizer = NLLanguageRecognizer()
        recognizer.processString(text)
        switch recognizer.dominantLanguage {
        case .simplifiedChinese, .traditionalChinese: return "zh-CN"
        case .english: return "en-US"
        case .japanese: return "ja-JP"
        default: return fallback
        }
    }

    /// The answer the system says is over (finished, cancelled, or paused by an interruption, which cuts it): only the
    /// one being said, compared as the very object, so a late word about the one before never ends the next.
    fileprivate func over(_ said: AVSpeechUtterance, cut: Bool) {
        guard let utterance, utterance === said else { return }
        if cut { synthesizer.stopSpeaking(at: .immediate) }
        finished()
    }
}

extension MeetingSpeaker: AVSpeechSynthesizerDelegate {
    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didFinish utterance: AVSpeechUtterance) {
        nonisolated(unsafe) let said = utterance
        Task { @MainActor in self.over(said, cut: false) }
    }

    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didCancel utterance: AVSpeechUtterance) {
        nonisolated(unsafe) let said = utterance
        Task { @MainActor in self.over(said, cut: false) }
    }

    /// A call or Siri paused it: the answer is cut rather than finished later, out of place in the meeting.
    nonisolated func speechSynthesizer(_ synthesizer: AVSpeechSynthesizer, didPause utterance: AVSpeechUtterance) {
        nonisolated(unsafe) let said = utterance
        Task { @MainActor in self.over(said, cut: true) }
    }
}
#endif
