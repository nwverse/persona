import SwiftUI

/// Settings › Claude Code › Live Replies (`LiveRepliesModel`): the switch for the computer picked at the top of the page,
/// with what it does and what it costs. A section of the page's form; the page drops it on a hub without the switch.
struct LiveRepliesSection: View {
    let model: LiveRepliesModel
    /// The other online computers a change goes to too (Apply to All Online Machines).
    var others: [MachineRef] = []

    var body: some View {
        Section {
            if let on = model.on {
                Toggle(isOn: Binding(get: { on }, set: { next in Task { await model.set(next, others: others) } })) {
                    VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                        Text("Stream Replies as Claude Writes").lineLimit(1).minimumScaleFactor(0.8)
                        Text(on ? String(localized: "Replies arrive line by line, seconds sooner")
                                : String(localized: "Replies show up once Claude saves them"))
                            .font(Theme.Font.label).foregroundStyle(.secondary).lineLimit(1).minimumScaleFactor(0.8)
                    }
                }
                .disabled(model.saving)
                .accessibilityIdentifier("live-replies")
                if let error = model.error {
                    Text(error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                }
            } else if let error = model.loadError {
                Text(error).font(Theme.Font.caption).foregroundStyle(Theme.danger)
                Button { Task { await model.load() } } label: {
                    Label("Retry [Components b8784c]", systemImage: Theme.Icon.retry).lineLimit(1)
                }
                .formButton()
            } else {
                ProgressView().frame(maxWidth: .infinity)
            }
        } header: {
            Text("Live Replies")
        } footer: {
            Text("When on, the Claude Code sessions Persona starts on this computer send their replies while Claude writes them, so this app and the web show the text sooner. The cost: in this computer's terminal, replies appear a few lines at a time instead of word by word. The hub keeps this switch, not ~/.claude/settings.json; with “Apply to All Online Machines” on above, the other online computers change too. A change applies to sessions started after it; a session that is already running changes once Claude restarts.")
        }
    }
}
