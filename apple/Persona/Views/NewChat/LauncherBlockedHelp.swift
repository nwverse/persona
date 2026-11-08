import SwiftUI

/// How to fix a launcher New Agent grays out (#1643): why it can't start there, what to do about it, the hub's own words
/// under Details, and the ways on: add it to Persona as a model provider (for one the terminal can't run), or Try Anyway
/// once it is fixed (a start with it that works clears the record).
struct LauncherBlockedHelp: View {
    @Environment(\.dismiss) private var dismiss
    var launcher: Launcher
    var block: LauncherBlock
    /// Picks it after all: the help closes, then the list.
    var tryAnyway: () -> Void
    @State private var addingProvider = false
    @State private var showDetail = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: Theme.Space.l) {
                    HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
                        ThemedIcon(Theme.Icon.error).foregroundStyle(Theme.cautionText)
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(launcher.name).font(Theme.Font.heading.monospaced()).lineLimit(1).truncationMode(.middle)
                            Text(block.reason).font(Theme.Font.secondary).foregroundStyle(Theme.cautionText)
                                .fixedSize(horizontal: false, vertical: true)
                        }
                    }
                    if block.notInTerminal {
                        para(String(localized: "Persona found “\(launcher.name)” in this computer's login shell, but the shell new terminal tabs run doesn't have it: another shell, a container, or a startup file that stops early. New agents start in those tabs."))
                    } else {
                        para(String(localized: "Its latest check on this computer failed, and automatic repair didn't fix it."))
                    }
                    VStack(alignment: .leading, spacing: Theme.Space.s) {
                        Text("How to Fix").font(Theme.Font.heading)
                        if block.notInTerminal {
                            bullet(String(localized: "Define it in a startup file the terminal's shell reads (for bash, ~/.bashrc), then start again."))
                            bullet(String(localized: "Or add it to Persona as a model provider: those work in any terminal."))
                        } else {
                            bullet(String(localized: "Fix its key, gateway or CLI on that computer, or pick another launcher."))
                        }
                    }
                    if let detail = block.detail {
                        DisclosureGroup(isExpanded: $showDetail) {
                            Text(detail).font(Theme.Font.code).foregroundStyle(.secondary).textSelection(.enabled)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.top, Theme.Space.xs)
                        } label: {
                            Text("Details [Readiness Card 979a33]").font(Theme.Font.secondaryStrong).lineLimit(1)
                        }
                        .tint(.secondary)
                        .accessibilityIdentifier("launcherHelp.details")
                    }
                    para(block.notInTerminal ? String(localized: "A start with it that works clears this; otherwise it clears by itself after 6 hours.")
                                             : String(localized: "A check or start with it that works clears this."), secondary: true)
                    VStack(spacing: Theme.Space.s) {
                        if block.notInTerminal {
                            Button { addingProvider = true } label: {
                                Text("Add Provider").font(Theme.Font.heading).lineLimit(1).minimumScaleFactor(0.8)
                                    .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                            }
                            .themedButton(prominent: true)
                            .accessibilityIdentifier("launcherHelp.addProvider")
                        }
                        Button(action: tryAnyway) {
                            Text("Try Anyway").font(Theme.Font.heading).lineLimit(1).minimumScaleFactor(0.8)
                                .scaledFrame(maxWidth: .infinity, minHeight: Theme.Size.wellS)
                        }
                        .themedButton()
                        .accessibilityIdentifier("launcherHelp.tryAnyway")
                    }
                    .padding(.top, Theme.Space.s)
                }
                .padding(Theme.Space.page)
                .frame(maxWidth: ChatView.readableWidth, alignment: .leading)
                .frame(maxWidth: .infinity)
            }
            .themedGroupedPage()
            .navigationTitle(block.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button { dismiss() } label: { Text("Done [Extension System Presentation c0b3fb]").lineLimit(1) }
                }
            }
        }
        .sheet(isPresented: $addingProvider) { ProviderWizard(start: .pick) }
        #if os(macOS)
        .macSheet()
        #endif
        .accessibilityIdentifier("launcherHelp")
    }

    private func para(_ s: String, secondary: Bool = false) -> some View {
        Text(s).font(Theme.Font.body).lineHeight(.body).foregroundStyle(secondary ? .secondary : .primary)
            .fixedSize(horizontal: false, vertical: true)
    }

    private func bullet(_ s: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            ThemedIcon(Theme.Icon.done).font(Theme.Font.label).foregroundStyle(Theme.accent)
            para(s)
        }
    }
}
