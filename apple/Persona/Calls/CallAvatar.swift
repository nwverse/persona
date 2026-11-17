#if os(iOS)
import SwiftUI

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

/// The assistant on the call screen: its own look as the app shows it elsewhere (`PersonaDot`: Mochi drawn, or a
/// package's or an uploaded picture's frames), moving with the call, and a thin ring around it following the voice. Only
/// the ring reads the levels, on its own clock, so a new level redraws the ring and nothing else (docs/apple/performance.md
/// P4, P5); the clock pauses whenever the ring has nothing to follow, with Reduce Motion and in Low Power Mode, where the
/// face holds still too. While the assistant speaks and `interrupt` is given it is a button, Interrupt Voice, as the voice
/// bar's orb; otherwise it is decoration (the name and the line under it say the state in words).
struct CallAvatar: View {
    let look: CallAvatarLook
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
                PersonaDot(size: face, state: look.state, still: look.still)
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
