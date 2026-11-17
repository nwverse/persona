#if os(iOS)
import SwiftUI
import UIKit

/// The conversation a call talks in when it is not the main one (#9887 one button), as its pages show it, taken when the
/// call is made: its title (what the call screen, the bar and the question elsewhere name), a line saying whose it is
/// (the assistant's name for another assistant conversation; the agent and its machine for an Agent session), and an
/// Agent session's mark (its runtime's, `AgentKind`). The main conversation has none: the assistant's look and name
/// stand for it.
nonisolated struct CallConversation: Equatable, Sendable {
    var title: String
    var detail: String?
    /// An Agent session's mark; nil: an assistant conversation, which wears the assistant's look.
    var agent: AgentKind?

    /// `key`'s conversation as the app has it now; nil for the main conversation, or one the app does not list (it is
    /// shown as the assistant's).
    @MainActor static func make(_ key: SessionKey, model: AppModel) -> CallConversation? {
        make(session: model.session(key), main: model.assistantMain == key, assistant: model.assistantShownName,
             machine: model.machines.first { $0.id == key.machineID }?.name)
    }

    /// The same from what the app has: the session, whether it is the main conversation, the assistant's name and the
    /// session's machine's.
    static func make(session: Session?, main: Bool, assistant: String, machine: String?) -> CallConversation? {
        guard !main, let session else { return nil }
        if session.assistant {
            let title = session.title.trimmingCharacters(in: .whitespacesAndNewlines)
            return CallConversation(title: title.isEmpty ? assistant : title, detail: assistant, agent: nil)
        }
        let detail = [session.agentLabel, machine].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ") // i18n-ignore: a separator
        return CallConversation(title: session.displayTitle, detail: detail.isEmpty ? nil : detail, agent: AgentKind(session))
    }
}

/// Who a call is with, as its pages show them (#9290 follow-up): the assistant's look and its name, taken together when
/// the call is made and once more just before the system's Start (a cold launch's look may have come by then:
/// `PersonaCalls.place`), then fixed for the call, as the system's handle is. Choosing another avatar, or another hub's,
/// changes neither the face nor the name of a call that is on. A package's pictures are read and decoded off the main
/// actor (`prepareFrames`, `prepareFace`) and kept here for the call; until they are ready, and for a built-in, the face
/// is the built-in drawing in the look's design. The pages only arrange what is here (docs/apple/performance.md P1, P4,
/// P8).
@MainActor @Observable final class CallAssistant {
    private(set) var persona: Persona
    /// The package's dot frames for the states the call screen shows (`states`), once decoded.
    private(set) var frames: [PersonaState: PersonaFrames] = [:]
    /// The package's face for the return bar and the urgent call's header, once decoded.
    private(set) var face: UIImage?
    /// The pixel width the face was read for: a larger one is read again (the text grew).
    @ObservationIgnored private var faceWidth: CGFloat = 0
    @ObservationIgnored private var loadingFrames = false

    /// The states the call screen shows (`CallAvatarLook.state`).
    static let states: [PersonaState] = [.idle, .working, .offline]

    /// The name the system's call UI, the call screen and the bar say (`CallHandle.name`).
    var name: String { CallHandle.name(persona.name) }

    init(_ persona: Persona) {
        self.persona = persona
    }

    /// The look and name taken once more (just before the system's Start), together; its pictures go with the old look.
    func retake(_ persona: Persona) {
        guard persona != self.persona else { return }
        self.persona = persona
        frames = [:]
        face = nil
        faceWidth = 0
    }

    /// Reads and decodes the package's frames for the call's states off the main actor, once. Nothing for a built-in.
    func prepareFrames(_ store: PersonaStore) async {
        let look = persona
        guard !look.isBuiltin, frames.isEmpty, !loadingFrames else { return }
        loadingFrames = true
        let loaded = await store.decodedFrames(look, Self.states)
        loadingFrames = false
        guard persona == look else { return }
        frames = loaded
    }

    /// Reads the package's face `points` wide at `scale` off the main actor, prepared for display. Nothing for a built-in,
    /// nor when the one here is large enough.
    func prepareFace(_ store: PersonaStore, points: CGFloat, scale: CGFloat) async {
        let look = persona, width = points * scale
        guard !look.isBuiltin, width > faceWidth else { return }
        faceWidth = width
        let image = await store.decodedFace(look, points: points, scale: scale)
        guard persona == look else { return }
        if let image { face = image } else { faceWidth = 0 }
    }
}

/// How the assistant looks on the call screen in each of the voice's phases (pure, for the tests): which of its states
/// it shows (`PersonaState`, as the Persona tab's dot does), whether it holds still, how the ring around it looks, and
/// the mark on it. It moves while it speaks or works and idles (blinking) while it listens; on hold, muted and over it
/// holds still, the ring and a mark saying why.
nonisolated struct CallAvatarLook: Equatable, Sendable {
    nonisolated enum Ring: Equatable, Sendable {
        /// An arc lapping a faint track: the call connects (Calling…, Connecting…, Reconnecting…).
        case lap
        /// The persona's color, swelling with the voice's level (the microphone's, or the assistant's own voice).
        case voice
        /// On hold: still, in the caution color.
        case held
        /// Muted: still and gray.
        case quiet
        /// Over: faint gray.
        case ended
    }

    nonisolated enum Mark: Equatable, Sendable {
        case hold, muted
    }

    var state: PersonaState
    var still: Bool
    var ring: Ring
    var mark: Mark?

    static func make(_ phase: VoiceOrb.Phase) -> CallAvatarLook {
        switch phase {
        case .idle: CallAvatarLook(state: .idle, still: false, ring: .quiet, mark: nil)
        case .connecting: CallAvatarLook(state: .idle, still: false, ring: .lap, mark: nil)
        case .listening, .hearing: CallAvatarLook(state: .idle, still: false, ring: .voice, mark: nil)
        // Speaking is the assistant answering, as its dot shows a reply on its way; working, the conversation it handed
        // work to at work.
        case .speaking, .working: CallAvatarLook(state: .working, still: false, ring: .voice, mark: nil)
        case .paused: CallAvatarLook(state: .idle, still: true, ring: .held, mark: .hold)
        case .muted: CallAvatarLook(state: .idle, still: true, ring: .quiet, mark: .muted)
        case .ended: CallAvatarLook(state: .offline, still: true, ring: .ended, mark: nil)
        }
    }

    /// The ring follows a level (and so needs a clock) in this look.
    var followsLevel: Bool { ring == .voice }
    /// The ring moves by itself (a level, or the lap) while motion is allowed.
    var ringMoves: Bool { ring == .voice || ring == .lap }
}

/// The assistant on the call screen: the call's own look (`CallAssistant`, as the app's dot draws it: Mochi drawn, or a
/// package's or an uploaded picture's frames, decoded off the main actor), moving with the call, and a thin ring around
/// it following the voice. Only
/// the ring reads the levels, on its own clock, so a new level redraws the ring and nothing else (docs/apple/performance.md
/// P4, P5); the clock pauses whenever the ring has nothing to follow, with Reduce Motion and in Low Power Mode, where the
/// face holds still too. While the assistant speaks and `interrupt` is given it is a button, Interrupt Voice, as the voice
/// bar's orb; otherwise it is decoration (the name and the line under it say the state in words).
struct CallAvatar: View {
    let look: CallAvatarLook
    /// Who the call is with: the look and name it was made with (never the one chosen since).
    let assistant: CallAssistant
    /// A call to an Agent session (#9887 one button): its mark in the assistant's place.
    var agent: AgentKind?
    /// 0…1, the voice's level, read only on the ring's clock.
    var level: () -> Double = { 0 }
    /// The box at the default text size; it grows with the text like every `Theme.Size`.
    var size: CGFloat = Theme.Size.callAvatar
    /// What a tap does while the assistant speaks: stop the voice. Given only then; without it this is never a button.
    var interrupt: (() -> Void)?

    @Environment(\.dynamicTypeSize) private var typeSize

    private var isButton: Bool { interrupt != nil && !look.still }

    var body: some View {
        let box = size * Theme.sizeScale(typeSize)
        let face = box * Theme.Size.callAvatarFace
        Button { if isButton { interrupt?() } } label: {
            ZStack {
                CallAvatarRing(look: look, level: level, box: box)
                Group {
                    if let agent {
                        CallAgentFace(agent: agent, size: face)
                    } else {
                        CallAssistantDot(assistant: assistant, size: face, state: look.state, still: look.still)
                    }
                }
                .opacity(look.ring == .ended ? PersonaFace.offlineOpacity : 1)
                if let mark = look.mark {
                    CallAvatarMark(mark: mark, side: box * Theme.Size.callAvatarBadge)
                        .offset(x: face * 0.36, y: face * 0.36)
                        .transition(.opacity)
                }
            }
            .frame(width: box, height: box)
            .contentShape(.circle)
        }
        .buttonStyle(Press())
        .disabled(!isButton)
        .allowsHitTesting(isButton)
        .accessibilityHidden(!isButton)
        .accessibilityLabel("Interrupt Voice")
        .accessibilityHint("Stops only the voice; the work in progress carries on")
        .accessibilityIdentifier("call-avatar")
    }

    /// As it is, enabled or not (the picture says the state); pressed, it dims as a plain button does.
    private struct Press: ButtonStyle {
        func makeBody(configuration: Configuration) -> some View {
            configuration.label.opacity(configuration.isPressed ? 0.7 : 1)
        }
    }
}

/// The ring around the assistant: the only part that follows the voice's level, on a clock of its own that runs only
/// while it has something to follow and motion is allowed.
private struct CallAvatarRing: View {
    let look: CallAvatarLook
    let level: () -> Double
    let box: CGFloat
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var moving: Bool { look.ringMoves && !reduceMotion && !ProcessInfo.processInfo.isLowPowerModeEnabled }

    var body: some View {
        TimelineView(.animation(minimumInterval: Theme.Motion.voiceFrame, paused: !moving)) { context in
            let now = moving ? context.date.timeIntervalSinceReferenceDate : nil
            ring(lap: now.map { $0.truncatingRemainder(dividingBy: Theme.Motion.voiceSpin) / Theme.Motion.voiceSpin },
                 level: moving && look.followsLevel ? min(1, max(0, level())) : 0)
        }
        .frame(width: box, height: box)
        .accessibilityHidden(true)
    }

    @ViewBuilder private func ring(lap: Double?, level: Double) -> some View {
        let rest = box * Theme.Size.callAvatarRing
        switch look.ring {
        case .lap:
            ZStack {
                Circle().stroke(Theme.persona.opacity(0.2), lineWidth: Theme.Size.ruleWide)
                Circle()
                    .trim(from: 0, to: VoiceOrb.arc)
                    .stroke(Theme.persona, style: StrokeStyle(lineWidth: Theme.Size.ruleWide, lineCap: .round))
                    .rotationEffect(.degrees((lap ?? 0) * 360 - 90))
            }
            .frame(width: rest, height: rest)
        case .voice:
            // At rest a faint ring; a full voice swells it to the box's edge, brighter and a little wider.
            Circle()
                .stroke(Theme.persona.opacity(0.35 + 0.65 * level), lineWidth: Theme.Size.ruleWide * (1 + level))
                .frame(width: rest, height: rest)
                .scaleEffect(1 + level * (1 / Theme.Size.callAvatarRing - 1))
        case .held:
            Circle().stroke(Theme.caution, lineWidth: Theme.Size.ruleWide).frame(width: rest, height: rest)
        case .quiet:
            Circle().stroke(Theme.Hue.gray.opacity(0.6), lineWidth: Theme.Size.ruleWide).frame(width: rest, height: rest)
        case .ended:
            Circle().stroke(Theme.Hue.gray.opacity(0.3), lineWidth: Theme.Size.ruleWide).frame(width: rest, height: rest)
        }
    }
}

/// The call's assistant in a state, as `PersonaDot` draws one but from what `CallAssistant` has ready: a package's frames
/// once decoded (in a UIImageView, as the dot's), else the built-in drawing in the look's design. Nothing is read or
/// decoded here: the frames are asked for in a task, off the main actor. Holds still with Reduce Motion and in Low Power
/// Mode, and when the call says so.
struct CallAssistantDot: View {
    let assistant: CallAssistant
    let size: CGFloat
    let state: PersonaState
    var still = false
    @Environment(AppModel.self) private var model
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let look = assistant.persona
        face(look, still: still || reduceMotion || ProcessInfo.processInfo.isLowPowerModeEnabled)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
            .task(id: look) { await assistant.prepareFrames(model.personas) }
    }

    @ViewBuilder private func face(_ look: Persona, still: Bool) -> some View {
        if !look.isBuiltin, let frames = assistant.frames[state] {
            VisibleWork { eligible in
                PersonaFramesView(frames: frames, animating: !still, eligible: eligible)
            }
        } else {
            AssistantAvatar(size: size, design: look.avatarDesign, state: state, still: still)
        }
    }
}

/// The call's assistant's small face (the return bar, the urgent call's header), as `PersonaAvatar` draws one but from
/// what `CallAssistant` has ready: a package's picture once read off the main actor, else the built-in drawing.
struct CallAssistantFace: View {
    let assistant: CallAssistant
    let size: CGFloat
    @Environment(AppModel.self) private var model
    @Environment(\.displayScale) private var scale

    var body: some View {
        let look = assistant.persona
        face(look)
            .frame(width: size, height: size)
            .accessibilityHidden(true)
            .task(id: CallFaceRequest(look: look, side: size * scale)) {
                await assistant.prepareFace(model.personas, points: size, scale: scale)
            }
    }

    @ViewBuilder private func face(_ look: Persona) -> some View {
        if !look.isBuiltin, let image = assistant.face {
            Image(uiImage: image)
                .resizable()
                .interpolation(.high)
                .scaledToFill()
                .frame(width: size, height: size)
                .clipShape(.circle)
        } else {
            AssistantAvatar(size: size, design: look.avatarDesign, state: .idle)
        }
    }
}

/// An Agent session's face on a call to it (#9887 one button): its runtime's mark (Claude's, Codex's, TRAE's; else the
/// agent's letter), as the Agents page draws it (`AgentMark`), on the well's round disc, `size` points across (already
/// scaled with the text). Holds still: the ring around it says what the call does.
struct CallAgentFace: View {
    let agent: AgentKind
    let size: CGFloat

    var body: some View {
        ZStack {
            if let brand = agent.brand {
                BrandMark(brand, side: size * Theme.Size.callAgentMark, scaled: false).foregroundStyle(.secondary)
            } else {
                Text(verbatim: agent.letter)
                    .font(.system(size: size * Theme.Size.callAgentMark * 0.7, weight: .semibold, design: .rounded))
                    .foregroundStyle(.secondary)
            }
        }
        .frame(width: size, height: size)
        .background(Theme.Fill.well, in: .circle)
        .accessibilityHidden(true)
    }
}

/// What a face is read for: the look and its side in pixels.
private nonisolated struct CallFaceRequest: Hashable, Sendable {
    var look: Persona
    var side: CGFloat
}

/// The mark on a still face: a pause on hold (the caution color), a slashed microphone while muted (gray), as the
/// voice bar's orb wears them.
private struct CallAvatarMark: View {
    let mark: CallAvatarLook.Mark
    let side: CGFloat

    var body: some View {
        ThemedIcon(mark == .hold ? Theme.Icon.pause : Theme.Icon.voiceMute)
            .font(.system(size: side * 0.5, weight: .semibold))
            .foregroundStyle(Theme.onSolid)
            .frame(width: side, height: side)
            .background(mark == .hold ? Theme.caution : Theme.Hue.gray, in: .circle)
            .overlay { Circle().strokeBorder(Theme.pageBackground, lineWidth: Theme.Size.rule) }
            .accessibilityHidden(true)
    }
}
#endif
