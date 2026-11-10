#if os(iOS)
import SwiftUI

/// Settings › Meetings (#2981, docs/assistant/meeting-minutes.md「Attendance fields」): the role every meeting recording
/// starts with, kept on the hub (`meeting_attendance_default`, Off until changed), and what Persona may say in front of
/// others. Each recording can still pick its own role and session (the Recordings page). Shown only by a hub that offers
/// attending.
struct MeetingSettingsView: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        SettingsForm {
            Section {
                Picker(String(localized: "Persona in Meetings"), selection: Binding(
                    get: { MeetingAttendees.defaultRole(model) },
                    set: { role in Task { await model.setMeetingAttendanceDefault(role) } })) {
                    ForEach(MeetingAttendance.allCases, id: \.self) { Text($0.title).tag($0) }
                }
                .pickerStyle(.inline)
                .labelsHidden()
                .accessibilityIdentifier("settings-meeting-attendance")
            } header: {
                Text("Persona in Meetings")
            } footer: {
                VStack(alignment: .leading, spacing: Theme.Space.s) {
                    Text(MeetingAttendees.defaultRole(model).detail)
                    Text("Each meeting recording starts with this. To change it for one meeting, pick another on the Recordings page before you start.")
                }
            }
            Section {
                Label("Answers only from meeting material: earlier minutes, project progress and who owns a to-do.", systemImage: Theme.Icon.meeting)
                Label("Never brings up private chats, mail, health or other personal data.", systemImage: Theme.Icon.privacy)
                Label("Speaks only when you tap Ask Persona, and only when it takes part.", systemImage: Theme.Icon.question)
            } header: {
                Text("In Front of Others")
            } footer: {
                Text("The hub holds Persona to these limits in a meeting, whatever it is asked.")
            }
            .font(Theme.Font.secondary)
        }
        .themedPage()
        .navigationTitle(String(localized: "Meetings"))
        .navigationBarTitleDisplayMode(.inline)
        .task { if model.assistantConfig == nil { await model.refreshAssistant() } }
    }
}
#endif
