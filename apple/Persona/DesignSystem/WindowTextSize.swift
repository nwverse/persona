#if os(iOS)
import SwiftUI
import UIKit

/// The interface's cap (`Theme.interfaceLargestTypeSize`) on the windows' own text size. The environment's cap does not
/// reach a sheet, a full screen cover or a popover: each takes its size from its own trait collection, which is the
/// system's (Settings was drawn at AX5 under the capped page). A trait override on the window reaches everything shown
/// in it. Reading content lifts the cap in SwiftUI with `.readingContent()` and in UIKit by overriding its own view's
/// trait with `readingCategory`.
enum WindowTextSize {
    /// The system's size, not the window's (which this overrides).
    @MainActor static var system: DynamicTypeSize { DynamicTypeSize(UIApplication.shared.preferredContentSizeCategory) ?? .large }

    /// What the reader chose (the system's, moved by Small / Standard / Large): reading content draws at it.
    @MainActor static var readingCategory: UIContentSizeCategory {
        UIContentSizeCategory(AppModel.shared.textSize.applied(to: system))
    }

    /// On every window of every scene; RootView runs it when it shows and when the system's size changes.
    @MainActor static func apply() {
        let capped = min(system, Theme.interfaceLargestTypeSize)
        for case let scene as UIWindowScene in UIApplication.shared.connectedScenes {
            for w in scene.windows {
                if capped < system {
                    w.traitOverrides.preferredContentSizeCategory = UIContentSizeCategory(capped)
                } else {
                    w.traitOverrides.remove(UITraitPreferredContentSizeCategory.self)
                }
            }
        }
    }
}

#if DEBUG
extension WindowTextSize {
    /// `-ax.probe YES`: what the system's size, the window's and the Large Content Viewer say, in the log (`ax.probe`),
    /// and a system alert over the page: it must stay at the system's size, not the interface's cap.
    @MainActor static func probe() async {
        guard UserDefaults.standard.bool(forKey: "ax.probe") else { return }
        try? await Task.sleep(for: .seconds(3))
        let window = UIApplication.shared.connectedScenes.compactMap { ($0 as? UIWindowScene)?.windows.first { $0.isKeyWindow } }.first
        NSLog("ax.probe system=%@ window=%@ largeContentViewer=%d", UIApplication.shared.preferredContentSizeCategory.rawValue,
              window?.traitCollection.preferredContentSizeCategory.rawValue ?? "none", UILargeContentViewerInteraction.isEnabled ? 1 : 0)
        guard UserDefaults.standard.bool(forKey: "ax.probeAlert"), var top = window?.rootViewController else { return }
        while let next = top.presentedViewController { top = next }
        let alert = UIAlertController(title: "Probe alert", message: "Does this follow the system's text size?", preferredStyle: .alert)
        alert.addAction(UIAlertAction(title: "OK", style: .default))
        top.present(alert, animated: false)
    }
}
#endif
#endif
