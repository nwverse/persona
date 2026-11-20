import SwiftUI

/// Settings › Assistant › Exact Approval Replies. Every write is checked against both the PUT response and a fresh GET;
/// a stale or partial answer restores the previous switch instead of presenting a local opt-in as saved.
struct OwnerApprovalSettingsView: View {
    @Environment(AppModel.self) private var model
    @State private var config: APIClient.AssistantState?
    @State private var phraseText = ""
    @State private var loading = true
    @State private var saving = false
    @State private var failure: String?

    private enum Switch { case reply, persistent }

    private var phrases: [String] {
        phraseText.components(separatedBy: .newlines)
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    var body: some View {
        List {
            if let approval = config?.ownerReplyApproval,
               let persistent = config?.persistentOwnerActionCards,
               let confirmed = config?.ownerReplyApprovalPhrases {
                Section {
                    Toggle(String(localized: "Approve a Batch with an Exact Reply"), isOn: Binding(
                        get: { config?.ownerReplyApproval ?? approval },
                        set: { on in Task { await saveSwitch(.reply, on: on) } }
                    ))
                    .accessibilityIdentifier("owner-reply-approval")
                    Toggle(String(localized: "Keep Unanswered Proposals Until Answered"), isOn: Binding(
                        get: { config?.persistentOwnerActionCards ?? persistent },
                        set: { on in Task { await saveSwitch(.persistent, on: on) } }
                    ))
                    .accessibilityIdentifier("persistent-owner-action-cards")
                } footer: {
                    Text("Off by default. An exact reply approves only the displayed original text, recipients and actions, once. Edits need fresh approval; this never creates an Always rule.")
                }
                Section {
                    TextEditor(text: $phraseText)
                        .frame(minHeight: Theme.Space.xxl * 3)
                        .disabled(saving)
                        .accessibilityIdentifier("owner-approval-phrases")
                    Button(String(localized: "Save Phrases")) { Task { await savePhrases() } }
                        .disabled(saving || phrases == confirmed)
                } header: {
                    Text("Approval Phrases")
                } footer: {
                    Text("One exact phrase per line. The whole reply must match. An empty list disables reply approval; ordinary Allow and Deny still work.")
                }
            }
            if let failure {
                Section { Text(failure).foregroundStyle(.red) }
            }
        }
        .navigationTitle(String(localized: "Exact Approval Replies"))
        .disabled(saving)
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        guard let api = model.api else { loading = false; return }
        loading = true
        failure = nil
        do {
            let st = try await api.assistant()
            guard st.ownerReplyApproval != nil, st.persistentOwnerActionCards != nil, st.ownerReplyApprovalPhrases != nil else {
                config = nil
                loading = false
                return
            }
            config = st
            phraseText = st.ownerReplyApprovalPhrases?.joined(separator: "\n") ?? ""
            model.assistantSettingsSaved(st)
        } catch {
            failure = error.localizedDescription
        }
        loading = false
    }

    private func saveSwitch(_ field: Switch, on: Bool) async {
        guard !saving, let api = model.api, let before = config else { return }
        saving = true
        failure = nil
        var optimistic = before
        var body = APIClient.AssistantConfigBody()
        switch field {
        case .reply:
            optimistic.ownerReplyApproval = on
            body.ownerReplyApproval = on
        case .persistent:
            optimistic.persistentOwnerActionCards = on
            body.persistentOwnerActionCards = on
        }
        config = optimistic
        do {
            let answered = try await api.setAssistant(body)
            try verify(answered, field: field, on: on, manager: before.manager)
            let persisted = try await api.assistant()
            try verify(persisted, field: field, on: on, manager: before.manager)
            config = persisted
            phraseText = persisted.ownerReplyApprovalPhrases?.joined(separator: "\n") ?? phraseText
            model.assistantSettingsSaved(persisted)
        } catch {
            config = before
            failure = error.localizedDescription
        }
        saving = false
    }

    private func verify(_ st: APIClient.AssistantState, field: Switch, on: Bool, manager: String?) throws {
        let saved = switch field {
        case .reply: st.ownerReplyApproval
        case .persistent: st.persistentOwnerActionCards
        }
        guard st.manager == manager, saved == on else {
            throw APIError(status: 502, message: String(localized: "The hub did not confirm this approval setting."))
        }
    }

    private func savePhrases() async {
        guard !saving, let api = model.api, let before = config else { return }
        saving = true
        failure = nil
        let wanted = phrases
        var body = APIClient.AssistantConfigBody()
        body.ownerReplyApprovalPhrases = wanted
        do {
            let answered = try await api.setAssistant(body)
            guard answered.manager == before.manager, answered.ownerReplyApprovalPhrases == wanted else {
                throw APIError(status: 502, message: String(localized: "The hub did not confirm these approval phrases."))
            }
            let persisted = try await api.assistant()
            guard persisted.manager == before.manager, persisted.ownerReplyApprovalPhrases == wanted else {
                throw APIError(status: 502, message: String(localized: "The hub did not confirm these approval phrases."))
            }
            config = persisted
            phraseText = wanted.joined(separator: "\n")
            model.assistantSettingsSaved(persisted)
        } catch {
            config = before
            failure = error.localizedDescription
        }
        saving = false
    }
}
