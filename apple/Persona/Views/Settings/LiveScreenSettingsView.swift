#if os(iOS)
import SwiftUI

/// The live screen's preferences on this device (#6398, #6396): how a computer's screen gets to this phone. Settings ›
/// This Device › Live Screen. Its route part shows only while direct routes are offered (`ScreenRoutes.offered`);
/// the quality tiers add their own section here.
struct LiveScreenSettingsView: View {
    @StoredValue(ScreenRoutePrefs.automaticKey) private var automatic = true

    /// Whether Settings lists the page: while any of its sections has something to set.
    static var isShown: Bool { ScreenRoutes.offered }

    var body: some View {
        SettingsForm {
            if ScreenRoutes.offered {
                Section {
                    SettingsSwitchRow(title: String(localized: "Choose the Fastest Route Automatically"), symbol: Theme.Icon.screen,
                                      titleLines: 2, accessibilityID: "settings-live-screen-routes", isOn: $automatic)
                } footer: {
                    Text("On the same Wi-Fi or Tailscale as your computer, the screen comes straight from it, faster and steadier. Turned off, it always goes through your hub.")
                }
            }
        }
        .themedPage()
        .detailTitle(String(localized: "Live Screen"))
        .navigationBarTitleDisplayMode(.inline) // the centered title of Settings' pages (as Knowledge Selection)
        .onChange(of: automatic) { _, on in
            if !on { ScreenRoutes.turnedOff() }
        }
    }
}

/// The route switch as it is kept on this device: on unless turned off.
enum ScreenRoutePrefs {
    nonisolated static let automaticKey = "screen.routes.automatic"

    nonisolated static var automatic: Bool { UserDefaults.standard.object(forKey: automaticKey) as? Bool ?? true }
}
#endif
