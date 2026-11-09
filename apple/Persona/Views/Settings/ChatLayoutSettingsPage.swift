import SwiftUI

/// Settings › Appearance › Layout › New Conversation Layout (#1666, `ChatLayout`): the switch, on by default, and how much of what
/// Persona and its tasks say to each other shows before Expand.
struct ChatLayoutSettingsPage: View {
    @Bindable private var settings = ChatLayoutSettings.shared

    var body: some View {
        SettingsForm {
            Section {
                Toggle(isOn: $settings.on) {
                    Text("New Conversation Layout").lineLimit(1)
                }
                .accessibilityIdentifier("chat-layout-toggle")
            } footer: {
                Text("Persona's conversation shows who is talking to whom (“Persona → @task”), puts every message in a box, folds what Persona and its tasks say to each other, and adds a navigator on the right edge: slide along it to go to any message you sent. Turn it off to go back to the previous look.")
            }
            if settings.on {
                Section {
                    Picker(selection: $settings.fold) {
                        ForEach(ChatFold.allCases) { fold in
                            Text(title(fold)).lineLimit(1).tag(fold)
                        }
                    } label: {
                        Text("Messages Between Persona and Tasks").lineLimit(1)
                    }
                    .pickerStyle(.inline)
                    .labelsHidden() // the section's header names it
                    .accessibilityIdentifier("chat-layout-fold")
                    if settings.fold == .lines {
                        Stepper(value: $settings.lines, in: ChatLayoutSettings.lineRange) {
                            Text("Lines Shown: \(settings.lines)").lineLimit(1)
                        }
                        .accessibilityIdentifier("chat-layout-lines")
                    }
                } header: {
                    Text("Messages Between Persona and Tasks")
                } footer: {
                    Text("How much of what Persona and its tasks say to each other shows before you tap Expand.")
                }
            }
        }
        .themedGroupedPage()
        .detailTitle(String(localized: "New Conversation Layout"))
        #if os(iOS) || os(visionOS)
        .navigationBarTitleDisplayMode(.inline)
        #endif
    }

    private func title(_ fold: ChatFold) -> String {
        switch fold {
        case .folded: String(localized: "Folded [Chat Layout]")
        case .lines: String(localized: "Show \(settings.lines) Lines")
        case .expanded: String(localized: "Expand All")
        }
    }
}
