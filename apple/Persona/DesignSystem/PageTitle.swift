import SwiftUI

extension View {
    /// A pushed page's title, and the line under it where it has one (Knowledge's day: the chat). The phone shows the
    /// title in its navigation bar as before (`navigationTitle`; its own principal item says the rest). The Mac's
    /// window has no title in its bar (#830: it ran over the sections column), so a pushed page draws its title in the
    /// title bar's row over its own leading edge instead, the shell's section name giving way to it (#855).
    @ViewBuilder func pageTitle(_ title: String, subtitle: String? = nil) -> some View {
        navigationTitle(title)
    }
}

extension View {
    /// A page's title in a split's detail column (Settings' pages, #1224 B2). The phone shows it in its navigation bar as
    /// before (`navigationTitle`); the Mac's window has no title in its bar (#830), so the page hands it to the column's
    /// title row (`MacDetailBack` draws it after the ‹, where there is one), without taking the shell's section name
    /// away as `pageTitle` does: the list beside still shows its section's name.
    @ViewBuilder func detailTitle(_ title: String) -> some View {
        navigationTitle(title)
    }
}

extension View {
    /// A page pushed in a section's own stack: on the Mac its ‹ and title are in the title bar's row (`MacPushedPage`);
    /// on the phone nothing changes.
    @ViewBuilder func sectionPushedPage() -> some View {
        self
    }
}
