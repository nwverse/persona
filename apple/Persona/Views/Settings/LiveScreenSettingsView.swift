#if os(iOS)
import SwiftUI

/// Settings › Live Screen (#6396, #6398): how the live screen of a computer reaches this iPhone. One page, two sections:
/// the route (#6398's switch, added by that work) and the picture's quality on mobile data (`ScreenMobileData`). Both are
/// this iPhone's, for every computer.
struct LiveScreenSettingsView: View {
    @AppStorage(ScreenMobileData.key) private var mobileData: String?

    /// Settings shows the page only when it can change something: some computer offers the live screen's video
    /// channel (quality on mobile data applies to it). The route's section (#6398) adds its own condition here.
    static func isShown(machines: [Machine]) -> Bool {
        #if DEBUG
        if ProcessInfo.processInfo.arguments.contains("-showScreenVideoDemo") { return true }
        #endif
        return machines.contains(where: AppModel.advertisesVideo)
    }

    var body: some View {
        SettingsForm {
            Section {
                Picker(String(localized: "Quality on Mobile Data"), selection: Binding(
                    get: { mobileData.flatMap(ScreenMobileData.init(rawValue:)) ?? .saver },
                    set: { mobileData = $0.rawValue })) {
                    ForEach(ScreenMobileData.allCases, id: \.self) { m in
                        VStack(alignment: .leading, spacing: Theme.Space.xxs) {
                            Text(m.title)
                            Text(m.usage).font(Theme.Font.caption).foregroundStyle(.secondary)
                        }
                        .tag(m)
                    }
                }
                .pickerStyle(.inline)
                .labelsHidden()
                .accessibilityIdentifier("settings-live-screen-mobile-data")
            } header: {
                Text("Quality on Mobile Data")
            } footer: {
                Text("For watching a computer's screen on mobile data or a personal hotspot. On Wi-Fi the picture is always as clear as the connection allows, and with Low Data Mode on it always saves data.")
            }
        }
        .detailTitle(String(localized: "Live Screen"))
        .navigationBarTitleDisplayMode(.inline) // like the other pages of Settings
    }
}

/// Settings' row for the page, only while `LiveScreenSettingsView.isShown`. Its own view, so machines changing redraws
/// this row and not the whole of Settings (docs/apple/performance.md, P5).
struct LiveScreenSettingsRow: View {
    @Environment(AppModel.self) private var model

    var body: some View {
        if LiveScreenSettingsView.isShown(machines: model.machines) {
            NavigationLink(value: SettingsPage.liveScreen) {
                SettingsRow(title: String(localized: "Live Screen"), symbol: Theme.Icon.screen, tint: Theme.accent, look: .oneLine)
            }
            .settingsListRow(SettingsPage.liveScreen)
            .accessibilityIdentifier("settings-live-screen")
        }
    }
}
#endif
