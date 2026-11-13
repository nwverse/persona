// The test runner needs a host app; it is never shown to anyone and does nothing.
import SwiftUI

@main
struct HostApp: App {
    var body: some Scene {
        WindowGroup { EmptyView() }
    }
}
