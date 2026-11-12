import SwiftUI

// The GitHub star prompts' views (#5761, `StarPromptModel`, docs/design/DESIGN_SYSTEM.md "GitHub star prompts"). Star on
// GitHub opens the project with the environment's `openURL`: the GitHub app or Safari, where the user's GitHub session
// is, never the app's link card.

/// Under the empty Agents list's Start Your First Session: Star on GitHub as a link badge, quieter than the empty state's
/// New Session (its one prominent button), and ✕ beside it to hide it for good on this device.
struct StarPromptPill: View {
    @Environment(\.openURL) private var openURL
    @Environment(\.dynamicTypeSize) private var typeSize

    var body: some View {
        Group {
            if typeSize.isAccessibilitySize {
                HStack(spacing: Theme.Space.xxs) {
                    star
                    hide
                }
            } else {
                // The badge centered under the empty state's button, ✕ just after it.
                star.overlay(alignment: .trailing) {
                    hide.alignmentGuide(.trailing) { $0[.leading] - Theme.Space.xxs }
                }
            }
        }
        .buttonStyle(.borderless) // in a list row: each button takes only its own taps
        .frame(maxWidth: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("agents.starPill")
    }

    private var star: some View {
        Button { StarPromptModel.shared.star(open: openURL) } label: {
            LinkBadge(symbol: Theme.Icon.gitHubStar, text: StarPromptText.star)
                .tapTargetHeight()
                .contentShape(.rect)
        }
        .accessibilityIdentifier("agents.starPill.star")
    }

    private var hide: some View {
        Button {
            withAnimation(Theme.Motion.standard) { StarPromptModel.shared.hidePill() }
        } label: {
            ThemedIcon(Theme.Icon.close)
                .font(Theme.Font.labelMedium)
                .foregroundStyle(.secondary)
                .tapTargetFrame(alignment: .leading)
                .contentShape(.rect)
        }
        .accessibilityLabel("Hide")
        .accessibilityIdentifier("agents.starPill.hide")
    }
}

/// A GitHub star in the Agents list's notice slot, once this device has started enough Agents: as quiet as the hub's
/// notice above it (`HubNoticeRow`), the words wrapping, Star on GitHub under them, and ✕ for Later (the card comes back
/// after twice the launches, and not before three days). The list shows it only while nothing needs the user and no
/// search is on, so it never pushes a waiting session down.
struct StarPromptNoticeRow: View {
    @Environment(\.openURL) private var openURL

    var body: some View {
        HStack(alignment: .top, spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.gitHubStar)
                .foregroundStyle(Theme.accent)
                .scaledFrame(width: Theme.Size.iconS)
            VStack(alignment: .leading, spacing: Theme.Space.flush) {
                Text("Enjoying Persona? It's open source, and a star on GitHub helps others find it.")
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
                Button { StarPromptModel.shared.star(open: openURL) } label: {
                    // A finger's height of its own: a touch area grown over the words above would star by mistake.
                    Text(StarPromptText.star)
                        .font(Theme.Font.secondaryStrong)
                        .foregroundStyle(Theme.accent)
                        .lineLimit(1)
                        .tapTargetHeight(alignment: .leading)
                        .contentShape(.rect)
                }
                .accessibilityIdentifier("agents.starPrompt.star")
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            Button {
                withAnimation(Theme.Motion.standard) { StarPromptModel.shared.later() }
            } label: {
                ThemedIcon(Theme.Icon.close)
                    .foregroundStyle(.secondary)
                    .tapTargetFrame(alignment: .top)
                    .contentShape(.rect)
            }
            .accessibilityLabel("Later [Readiness Card 9d2055]")
            .accessibilityIdentifier("agents.starPrompt.later")
        }
        .font(Theme.Font.secondary)
        .buttonStyle(.borderless) // in a list row: each button takes only its own taps
        .accessibilityElement(children: .contain) // or the row's id hides its buttons' (docs/lessons/ios.md)
        .accessibilityIdentifier("agents.starPrompt")
    }
}

/// The prompts' shared words.
enum StarPromptText {
    static var star: String { String(localized: "Star on GitHub") }
}

extension StarPromptModel {
    /// How long the onboarding toast stays: long enough to read it and reach its button.
    static let toastShown: Duration = .seconds(10)

    /// The toast after the checklist's Get started (`onboarded(demo:)` said it shows), with Star on GitHub on it.
    func welcomeToast(open: OpenURLAction) -> Toast {
        Toast(String(localized: "All set! If you like Persona, a star helps others find it."),
              style: .info,
              action: Toast.Action(title: StarPromptText.star, run: { [weak self] in self?.star(open: open) }),
              duration: Self.toastShown)
    }
}
