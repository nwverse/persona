import AppKit
import SwiftUI

/// The sections' pages, each built the first time its section is shown and kept in the window after that: a switch
/// hides one page and shows another (#390). Built anew on every switch (a `switch` in the shell), Agents took about
/// 240 ms on the main thread and Inbox about 190 (its list, its split, its rows' heights, its toolbar items all over
/// again); kept, a page comes back in about one frame (docs/apple/mac.md「Section switching: persistent pages」).
///
/// Each page is in a hosting view of its own (`MacPageHostingView`), not in a SwiftUI `ZStack`: a hidden view in the
/// same hierarchy is still laid out with it. What a kept page must do behind another one is up to the page, told by
/// `tabIsActive` (false while hidden): its toolbar items and search field go (`pageToolbar`, `pageSearchable`: two
/// search fields in the window's toolbar are a crash), its lists hold still (the Agents list's `Hold`), its chat is no
/// longer the one on screen. See docs/apple/mac.md for what is kept from the shell's environment.
struct MacPageStack: NSViewRepresentable {
    /// nil: every page behind (a plugin's page is over them, `MacShell.page`), none of them built or dropped.
    let shown: MacSection?
    let chrome: MacPageChrome
    let make: (MacSection) -> AnyView

    func makeNSView(context: Context) -> MacPageStackView { MacPageStackView() }

    func updateNSView(_ view: MacPageStackView, context: Context) { view.show(shown, chrome: chrome, make: make) }

    /// Whatever it is offered: measured the default way, SwiftUI asked Auto Layout to fit every kept page into it
    /// (`systemLayoutSizeFitting`) each time the shell was drawn, about half of a push's time with every page kept.
    func sizeThatFits(_ proposal: ProposedViewSize, nsView: MacPageStackView, context: Context) -> CGSize? {
        proposal.replacingUnspecifiedDimensions()
    }
}

/// What the shell gives every kept page that a hosting view of its own does not get from the window's SwiftUI tree:
/// set by the shell, read by the pages' roots.
@Observable final class MacPageChrome {
    /// The sections column is folded away: the title row starts past the traffic lights and the toolbar's buttons.
    var sidebarHidden = false
    /// The window scene's `openWindow` (「在新窗口打开」 in a page's menus).
    @ObservationIgnored var openWindow: OpenWindowAction?
    /// The section whose toolbar items the window shows: the one in front, a turn of the run loop after it came there.
    var toolbarSection: MacSection?
    /// The window scene's phase: the machines page polls only while active, a chat remembers where it was read.
    var scenePhase: ScenePhase = .active
}

/// Whether a kept page is the one in front.
@Observable final class MacPageActivity {
    var inFront = true
}

/// A kept page's root: the environment the shell's tree gave it before it had a hosting view of its own, and the
/// section's title in the title bar's row (#830), which the page can take for its own (`MacTitleRowTaken`).
struct MacPageRoot: View {
    let section: MacSection
    let activity: MacPageActivity
    let chrome: MacPageChrome
    let content: AnyView

    var body: some View {
        content
            .overlayPreferenceValue(MacTitleRowTaken.self, alignment: .topLeading) { taken in
                // Persona's page puts its own header there.
                if section != .assistant, !taken {
                    MacPageTitle(title: section.title, leading: chrome.sidebarHidden ? Theme.Size.macTitleAfterControls : Theme.Space.l)
                }
            }
            .environment(\.macTitleRowLeading, chrome.sidebarHidden ? Theme.Size.macTitleAfterControls : 0)
            .environment(\.shellOpenWindow, chrome.openWindow)
            .environment(\.scenePhase, chrome.scenePhase)
            .environment(\.tabIsActive, activity.inFront)
            .markdownInlineImageProvider(MathInlineImages()).markdownImageProvider(WebMarkdownImages()) // inline formulas (MathMarkdown), as the window's root sets
            .markdownCodeSyntaxHighlighter(PersonaCodeHighlighter()) // fences colored by language, as on the web (#2684)
    }
}

/// A kept page's hosting view. Hidden, it skips layout (its lists' rows, its splits): what SwiftUI changes on a page
/// behind another is laid out once, when it shows again.
final class MacPageHostingView: NSHostingView<MacPageRoot> {
    override func layout() {
        guard !isHidden else { return }
        super.layout()
    }
}

final class MacPageStackView: NSView {
    private var pages: [MacSection: (host: MacPageHostingView, activity: MacPageActivity)] = [:]
    private var front: MacSection?

    func show(_ section: MacSection?, chrome: MacPageChrome, make: (MacSection) -> AnyView) {
        if let section, pages[section] == nil {
            let activity = MacPageActivity()
            // In front at once only as the first page; else on the turn below, with the others' toolbar items going.
            activity.inFront = front == nil
            let host = MacPageHostingView(rootView: MacPageRoot(section: section, activity: activity, chrome: chrome, content: make(section)))
            // The page fills this view; it does not size the window (the shell's frame does).
            host.sizingOptions = []
            host.frame = bounds
            host.autoresizingMask = [.width, .height]
            addSubview(host)
            pages[section] = (host, activity)
        }
        guard section != front else { return }
        let leaving = front.flatMap { pages[$0] }
        front = section
        // The keyboard leaves with the page: a field on the hidden page kept typing into it.
        if let leaving, let responder = window?.firstResponder as? NSView, responder.isDescendant(of: leaving.host) {
            window?.makeFirstResponder(nil)
        }
        leaving?.host.isHidden = true
        if let section, let shown = pages[section] {
            shown.host.isHidden = false
            shown.host.needsLayout = true
        }
        // The pages' toolbar items and search fields (and what else follows `tabIsActive`) change once this turn's frame
        // is committed (after Core Animation's commit, as the run loop goes to sleep), on the turn after: the page shows
        // in the first frame, and the window's toolbar, which AppKit lays out item by item again for every change (about
        // half of a switch's time), is redone in the one after. A plain `DispatchQueue.main.async` sometimes ran before
        // the commit (Agents back in 13 ms, or one time in three in 135).
        let observer = CFRunLoopObserverCreateWithHandler(nil, CFRunLoopActivity.beforeWaiting.rawValue, false, CFIndex.max) { _, _ in
            DispatchQueue.main.async { [weak self] in
                guard let self, self.front == section else { return }
                for (s, page) in self.pages where page.activity.inFront != (s == section) { page.activity.inFront = s == section }
                chrome.toolbarSection = section
            }
        }
        CFRunLoopAddObserver(CFRunLoopGetMain(), observer, .commonModes)
    }
}

/// A section's title in the title bar's row, over the page's leading edge (#830): a label, so the row still drags the
/// window.
struct MacPageTitle: View {
    let title: String
    let leading: CGFloat

    var body: some View {
        Text(title)
            .font(Theme.Font.title)
            .foregroundStyle(Theme.macLabel)
            .lineLimit(1)
            .padding(.leading, leading)
            .frame(height: Theme.Size.macToolbarHeight)
            .ignoresSafeArea(.container, edges: .top)
            .allowsHitTesting(false)
            .accessibilityAddTraits(.isHeader)
    }
}

extension EnvironmentValues {
    /// The window's `openWindow` for a page in a hosting view of its own (`MacPageChrome`); nil elsewhere.
    @Entry var shellOpenWindow: OpenWindowAction? = nil
}
