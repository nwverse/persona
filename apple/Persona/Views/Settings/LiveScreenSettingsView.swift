#if os(iOS)
import SwiftUI

/// Settings › Live Screen (#6396, #6398): how the live screen of a computer reaches this iPhone. One page, two sections:
/// the route (#6398's switch, added by that work) and the picture's quality on mobile data (`ScreenMobileData`). Both are
/// this iPhone's, for every computer.
struct LiveScreenSettingsView: View {
    @AppStorage(ScreenMobileData.key) private var mobileData: String?

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
#endif
