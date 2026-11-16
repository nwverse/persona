#if os(iOS)
import AVFoundation
import Foundation
import os
import Speech

/// The urgent call's voice, one turn at a time (docs/assistant/urgent-call.md "Flow"): Persona speaks, then listens.
/// It speaks in the hub's speech voice when an extension offers one (`MeetingVoiceRoutes`, the meeting answers' way),
/// else in the phone's own voice; it listens with the phone's own recognizer, on the device when it can, so the
/// owner's words leave the phone only as the text of their answer. It plays and records on the call's audio session,
/// which LiveCommunicationKit activated (`.playAndRecord`, `.voiceChat`): it never sets a category of its own.
/// What the call's turns speak and hear with: `UrgentCallVoice` on a call, a written-out one in the tests.
@MainActor protocol UrgentCallSpeech: AnyObject {
    /// Says `text` and returns once it was said.
    func say(_ text: String) async
    /// What the owner said once they stopped for a moment; nil when nothing was heard.
    func listen() async -> String?
    func stop()
    /// Whether it can hear at all (the microphone and speech recognition allowed); false: the call works by taps.
    var canHear: Bool { get }
}

@MainActor final class UrgentCallVoice: UrgentCallSpeech {
    private static let log = Logger(subsystem: "persona", category: "urgent-call")
    private let device = MeetingDeviceVoice()
    private var player: MeetingVoicePlayer?
    private var route: MeetingVoiceRoute?
    private var routeRead = false
    private var hearing: Hearing?

    /// The language Persona speaks and hears in: the app's.
    let language: String

    init(language: String = LanguageStore.shared.launched) {
        self.language = language
    }

    /// Says `text` and returns once it was said (or cut by `stop`).
    func say(_ text: String) async {
        if !routeRead {
            routeRead = true
            route = await MeetingVoiceRoutes.shared.route()
        }
        if let route, let speech = route.speech, let api = route.api, await sayAloud(text, api: api, extensionID: route.extensionID, speech: speech) {
            return
        }
        let voice = MeetingSpeaker.voice(lang: nil, text: text, fallback: Self.speechLanguage(language))
        await withCheckedContinuation { (done: CheckedContinuation<Void, Never>) in
            device.speak(text, voice: voice) { done.resume() }
        }
    }

    /// The speech extension's voice; false when it could not speak, so the phone's voice says it instead.
    private func sayAloud(_ text: String, api: APIClient, extensionID: String, speech: String) async -> Bool {
        do {
            let audio = try await api.speechSpeak(extensionID, contribution: speech, text: text, lang: Self.speechLanguage(language))
            let player = MeetingVoicePlayer()
            self.player = player
            return try await withCheckedThrowingContinuation { (done: CheckedContinuation<Bool, Error>) in
                var resumed = false
                let finish: @MainActor (Bool) -> Void = { ok in
                    guard !resumed else { return }
                    resumed = true
                    done.resume(returning: ok)
                }
                do {
                    try player.start(rate: audio.rate, started: {}, ended: { finish(true) })
                } catch {
                    finish(false)
                    return
                }
                Task { @MainActor in
                    do {
                        for try await chunk in audio.audio { player.play(chunk) }
                        player.finish()
                    } catch {
                        player.stop()
                        finish(false)
                    }
                }
            }
        } catch {
            Self.log.notice("urgent call: the speech voice did not speak: \(error.localizedDescription, privacy: .public)")
            return false
        }
    }

    func listen() async -> String? { await hear(quiet: 1.6, longest: 20) }

    var canHear: Bool { Self.allowed }

    /// What the owner said, once they stopped for `quiet` seconds; nil when nothing was heard (or listening is not allowed).
    func hear(quiet: TimeInterval, longest: TimeInterval) async -> String? {
        guard Self.allowed, let recognizer = SFSpeechRecognizer(locale: Locale(identifier: Self.speechLanguage(language))),
              recognizer.isAvailable else { return nil }
        let hearing = Hearing()
        self.hearing = hearing
        defer { self.hearing = nil }
        do {
            try hearing.start(recognizer)
        } catch {
            Self.log.notice("urgent call: could not listen: \(error.localizedDescription, privacy: .public)")
            return nil
        }
        let began = Date.now
        while !Task.isCancelled {
            try? await Task.sleep(for: .milliseconds(200))
            let (text, changed) = hearing.heard
            let now = Date.now
            if !text.isEmpty, now.timeIntervalSince(changed) >= quiet { break }
            if text.isEmpty, now.timeIntervalSince(began) >= 8 { break } // nothing at all for a while
            if now.timeIntervalSince(began) >= longest || hearing.over { break }
        }
        hearing.stop()
        let text = hearing.heard.text.trimmingCharacters(in: .whitespacesAndNewlines)
        return text.isEmpty ? nil : text
    }

    /// Stops whatever it is saying or hearing (the call ended).
    func stop() {
        device.stop()
        player?.stop()
        player = nil
        hearing?.stop()
    }

    /// Whether the owner allowed the microphone and speech recognition (asked when urgent calls are turned on: on a
    /// locked phone mid-call the prompt cannot show).
    static var allowed: Bool {
        SFSpeechRecognizer.authorizationStatus() == .authorized && AVAudioApplication.shared.recordPermission == .granted
    }

    /// Asks for both, as turning urgent calls on does; true when both are allowed.
    static func requestAccess() async -> Bool {
        let speech = await withCheckedContinuation { (done: CheckedContinuation<Bool, Never>) in
            SFSpeechRecognizer.requestAuthorization { done.resume(returning: $0 == .authorized) }
        }
        let mic = await AVAudioApplication.requestRecordPermission()
        return speech && mic
    }

    /// The BCP 47 language for the app's language: what the speech recognizer and voices take.
    nonisolated static func speechLanguage(_ app: String) -> String {
        let language = DictationLanguage.language(of: app) // "zh-Hans", "ja", "en"
        if language.hasPrefix("zh") { return "zh-CN" }
        return language == "ja" ? "ja-JP" : "en-US"
    }
}

/// One turn of listening: the recognizer's request fed from the microphone, and the best words so far.
@MainActor private final class Hearing {
    private let engine = AVAudioEngine()
    private var task: SFSpeechRecognitionTask?
    private var request: SFSpeechAudioBufferRecognitionRequest?
    private(set) var heard: (text: String, changed: Date) = ("", .now)
    private(set) var over = false

    func start(_ recognizer: SFSpeechRecognizer) throws {
        let request = SFSpeechAudioBufferRecognitionRequest()
        request.shouldReportPartialResults = true
        if recognizer.supportsOnDeviceRecognition { request.requiresOnDeviceRecognition = true }
        self.request = request
        let input = engine.inputNode
        nonisolated(unsafe) let feed = request
        input.installTap(onBus: 0, bufferSize: 1024, format: input.outputFormat(forBus: 0)) { @Sendable buffer, _ in
            feed.append(buffer)
        }
        engine.prepare()
        try engine.start()
        task = recognizer.recognitionTask(with: request) { @Sendable [weak self] result, error in
            let text = result?.bestTranscription.formattedString
            let final = result?.isFinal == true || error != nil
            Task { @MainActor in
                guard let self else { return }
                if let text, text != self.heard.text { self.heard = (text, .now) }
                if final { self.over = true }
            }
        }
    }

    func stop() {
        if engine.isRunning {
            engine.stop()
            engine.inputNode.removeTap(onBus: 0)
        }
        request?.endAudio()
        task?.cancel()
        task = nil
        request = nil
    }
}
#endif
