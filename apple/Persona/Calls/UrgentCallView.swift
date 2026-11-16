#if os(iOS)
import SwiftUI

/// The urgent call page (docs/assistant/urgent-call.md): over everything while a call is answered. What the call is
/// about and why Persona called, the card it needs answered with its options to tap, the read-back waiting for Confirm,
/// what was said, and Hang Up. The voice runs the call (`UrgentCallSession`); every choice can also be tapped.
struct UrgentCallView: View {
    let session: UrgentCallSession

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.section) {
            UrgentCallHeader(session: session)
            ScrollView {
                VStack(alignment: .leading, spacing: Theme.Space.section) {
                    UrgentCallMatter(session: session)
                    UrgentCallCard(session: session)
                    UrgentCallCaptions(session: session)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .scrollIndicators(.hidden)
            UrgentCallFooter(session: session)
        }
        .padding(Theme.Space.page)
        .themedPage()
        .accessibilityAction(.escape) { UrgentCallCenter.shared.hangUp() }
    }
}

/// "Urgent call", how long it has lasted, and the title the lock screen showed.
private struct UrgentCallHeader: View {
    let session: UrgentCallSession

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.xs) {
            HStack(spacing: Theme.Space.s) {
                ThemedIcon(Theme.Icon.voiceChannel).foregroundStyle(Theme.danger)
                Text("Urgent Call").font(Theme.Font.heading).foregroundStyle(Theme.danger)
                Spacer(minLength: Theme.Space.s)
                if let answered = session.answeredAt {
                    Text(answered, style: .timer).font(Theme.Font.secondary.monospacedDigit()).foregroundStyle(.secondary)
                }
            }
            Text(session.info?.title ?? session.ring.title).font(Theme.Font.title).lineLimit(3).fixedSize(horizontal: false, vertical: true)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Why Persona called: the rule it matched (or the owner's own request) and what is happening.
private struct UrgentCallMatter: View {
    let session: UrgentCallSession

    var body: some View {
        if let info = session.info {
            VStack(alignment: .leading, spacing: Theme.Space.s) {
                Text("Why Persona called").font(Theme.Font.caption).foregroundStyle(.secondary)
                // The rule in full, wrapped: a one-line chip of an owner's long rule pushed the page past the screen.
                if let rule = info.rule, !rule.text.isEmpty {
                    Text(rule.text).font(Theme.Font.caption).foregroundStyle(Theme.danger).fixedSize(horizontal: false, vertical: true)
                } else if info.why == "owner_asked" {
                    Text("You asked to be called").font(Theme.Font.caption).foregroundStyle(.secondary)
                }
                if let what = info.happening ?? info.body, !what.isEmpty {
                    Text(what).font(Theme.Font.body).fixedSize(horizontal: false, vertical: true)
                }
            }
            .padding(Theme.Space.l)
            .frame(maxWidth: .infinity, alignment: .leading)
            .surface(in: .rect(cornerRadius: Theme.Radius.card, style: .continuous))
        }
    }
}

/// The card the call is about: its question and options, or Approve / Reject; then the read-back's Confirm.
private struct UrgentCallCard: View {
    let session: UrgentCallSession

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.m) {
            if let pending = session.pending {
                Text("You chose: \(pending.said)").font(Theme.Font.bodyStrong)
                Button { session.confirmTapped() } label: {
                    Label("Confirm", systemImage: Theme.Icon.done).lineLimit(1).frame(maxWidth: .infinity)
                }
                .themedButton(prominent: true)
                .accessibilityIdentifier("urgentCall.confirm")
            } else if let card = session.card {
                switch card.kind {
                case .question:
                    if let q = card.questions?.first {
                        Text(q.question).font(Theme.Font.bodyStrong)
                        ForEach(q.options, id: \.label) { option in
                            Button { session.choose(option.label) } label: {
                                Text(QuestionOption.display(option.label).0).lineLimit(1).minimumScaleFactor(0.8).frame(maxWidth: .infinity)
                            }
                            .themedButton()
                        }
                    }
                case .permission, .plan:
                    if let summary = card.summary ?? card.toolDisplayName { Text(summary).font(Theme.Font.body).lineLimit(4) }
                    HStack(spacing: Theme.Space.m) {
                        Button { session.decide(allow: true) } label: {
                            Text("Approve").lineLimit(1).frame(maxWidth: .infinity)
                        }
                        .themedButton(prominent: true)
                        Button { session.decide(allow: false) } label: {
                            Text("Reject").lineLimit(1).frame(maxWidth: .infinity)
                        }
                        .themedButton()
                    }
                default:
                    Text("It's waiting on your phone.").font(Theme.Font.body).foregroundStyle(.secondary)
                }
            }
            if session.tapOnly {
                Label("Persona can't hear you: turn on the microphone and speech recognition for Persona in Settings.", systemImage: Theme.Icon.voiceMute)
                    .font(Theme.Font.caption).foregroundStyle(.secondary)
            }
        }
    }
}

/// What was said on the call, newest at the bottom.
private struct UrgentCallCaptions: View {
    let session: UrgentCallSession

    var body: some View {
        VStack(alignment: .leading, spacing: Theme.Space.s) {
            ForEach(session.lines) { line in
                VStack(alignment: .leading, spacing: Theme.Space.xs) {
                    Text(line.who == .persona ? "Persona" : String(localized: "You")).font(Theme.Font.label).foregroundStyle(.secondary)
                    Text(line.text).font(Theme.Font.secondary).fixedSize(horizontal: false, vertical: true)
                }
                .accessibilityElement(children: .combine)
            }
        }
    }
}

/// Whether Persona is speaking or listening, and Hang Up.
private struct UrgentCallFooter: View {
    let session: UrgentCallSession

    var body: some View {
        HStack(spacing: Theme.Space.m) {
            Label(status.text, systemImage: status.icon).font(Theme.Font.caption).foregroundStyle(.secondary).lineLimit(1)
            Spacer(minLength: Theme.Space.s)
            Button(role: .destructive) { UrgentCallCenter.shared.hangUp() } label: {
                Label("Hang Up", systemImage: Theme.Icon.voiceEnd).lineLimit(1)
            }
            .themedButton(prominent: true)
            .tint(Theme.danger)
            .accessibilityIdentifier("urgentCall.hangUp")
        }
    }

    private var status: (text: String, icon: String) {
        switch session.phase {
        case .connecting: (String(localized: "Connecting…"), Theme.Icon.voiceConnecting)
        case .speaking: (String(localized: "Persona is speaking"), Theme.Icon.voiceSpeaking)
        case .listening: (String(localized: "Listening"), Theme.Icon.voiceListening)
        case .confirming: (String(localized: "Waiting for “confirm”"), Theme.Icon.voiceListening)
        case .ended: (String(localized: "Call ended"), Theme.Icon.voiceEnded)
        }
    }
}

/// The answered call the page shows, as `UrgentCallCenter` sets it. The cover reads only this, so launching never makes
/// the call center (nor LiveCommunicationKit's manager, which the system calls back on its own queue).
@MainActor @Observable final class UrgentCallPresenter {
    static let shared = UrgentCallPresenter()
    var session: UrgentCallSession?
}

/// Puts the call page over the app while a call is answered.
struct UrgentCallCover: ViewModifier {
    @State private var presenter = UrgentCallPresenter.shared

    func body(content: Content) -> some View {
        content.fullScreenCover(item: Binding(get: { presenter.session }, set: { _ in })) { session in
            UrgentCallView(session: session)
        }
    }
}

extension View {
    func urgentCallCover() -> some View { modifier(UrgentCallCover()) }
}
#endif
