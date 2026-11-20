#if os(iOS)
import SwiftUI

@Observable final class TeamPanelUI {
    var open = false
    var requested: SessionKey?
    var previous: Int?
    var lead: SessionKey?

    init(requested: SessionKey? = nil) { self.requested = requested }

    func present(_ show: Bool, for key: SessionKey) {
        open = show
        if !show { requested = nil }
        TeamPanelMemory.setClosed(!show, for: key)
    }
}

/// A lead owns a trailing inspector on iPad and presents the same worker chat in a sheet at compact width.
struct TeamPresentation: ViewModifier {
    let lead: SessionKey
    let enabled: Bool
    let ui: TeamPanelUI
    @Environment(AppModel.self) private var model
    @Environment(\.horizontalSizeClass) private var width
    @Environment(\.tabIsActive) private var active

    struct Update: Equatable {
        var lead: SessionKey
        var count: Int
        var landing: TeamLanding?
        var active: Bool
    }

    @ViewBuilder func body(content: Content) -> some View {
        if enabled {
            let update = currentUpdate
            presented(content).task(id: update) { await refresh(update) }
        } else {
            content
        }
    }

    private var currentUpdate: Update {
        var landing = model.teamLanding
        if landing?.lead != lead { landing = nil }
        return Update(lead: lead, count: model.teamCount(lead), landing: landing, active: active)
    }

    private var isOpen: Binding<Bool> {
        Binding(get: { active && ui.open }, set: { if active { ui.present($0, for: lead) } })
    }

    private var panel: some View { TeamPanel(lead: lead, rows: model.team(of: lead), ui: ui) }

    @ViewBuilder private func presented(_ content: Content) -> some View {
        if width == .regular {
            content.inspector(isPresented: isOpen) {
                panel.inspectorColumnWidth(min: Theme.Size.box320, ideal: Theme.Size.box380, max: Theme.Size.readable)
            }
        } else {
            content.sheet(isPresented: isOpen) { panel.presentationDetents([.large]) }
        }
    }

    private func refresh(_ update: Update) async {
        guard update.active else { ui.previous = nil; return }
        if ui.lead != lead {
            if ui.lead != nil { ui.open = false; ui.requested = nil }
            ui.lead = lead
            ui.previous = nil
        }
        if let landing = update.landing {
            ui.requested = landing.worker
            model.teamLanding = nil
        }
        // A paged window or reconnect can momentarily show no children. Only a settled zero is a new team boundary.
        if update.count == 0 {
            do { try await Task.sleep(for: .seconds(4)) } catch { return }
        }
        let reveal = TeamWorkers.reveal(count: update.count, previous: ui.previous, wide: width == .regular,
                                        manuallyClosed: TeamPanelMemory.closed(lead), requested: ui.requested != nil)
        ui.previous = update.count
        switch reveal {
        case .open: ui.present(true, for: lead)
        case .close: ui.open = false; ui.requested = nil
        case nil: break
        }
    }
}

struct TeamEntry: View {
    let count: Int
    let waiting: Bool
    let open: () -> Void

    var body: some View {
        Button(action: open) {
            HStack(spacing: Theme.Space.xs) {
                ThemedIcon(Theme.Icon.subagent)
                Text("Team")
                if waiting { StatusDot(state: .needsYou, size: Theme.Size.dotS).accessibilityHidden(true) }
            }
            .font(Theme.Font.labelStrong)
            .foregroundStyle(.primary)
            .padding(.horizontal, Theme.Space.m)
            .scaledFrame(height: Theme.Size.wellS)
            .background(Theme.Fill.chip, in: .capsule)
            .frame(minHeight: Theme.Size.tapTarget)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .accessibilityLabel(Text("Team"))
        .accessibilityValue(waiting ? String(localized: "A worker needs you") : String(localized: "Workers: \(count)"))
        .accessibilityIdentifier("team-entry")
    }
}

private struct TeamPanel: View {
    let lead: SessionKey
    let rows: [Session]
    let ui: TeamPanelUI
    @Environment(AppModel.self) private var model
    @Environment(\.dynamicTypeSize) private var type
    @FocusState private var focused: SessionKey?

    var body: some View {
        let shown = TeamWorkers.shown(in: rows, requested: ui.requested, last: model.lastTeamWorker[lead])
        NavigationStack {
            VStack(alignment: .leading, spacing: 0) {
                Text("Team")
                    .font(Theme.Font.heading)
                    .accessibilityAddTraits(.isHeader)
                    .padding(.horizontal, Theme.Size.tapTarget + Theme.Space.l)
                    .frame(maxWidth: .infinity)
                    .frame(minHeight: Theme.Size.tapTarget)
                    .overlay(alignment: .leading) {
                        SheetClose(id: "team-close") { ui.present(false, for: lead) }.control
                            .buttonStyle(.bordered)
                            .buttonBorderShape(.circle)
                            .controlSize(.large)
                            .padding(.leading, Theme.Space.l)
                    }
                    .padding(.vertical, Theme.Space.s)
                if !rows.isEmpty {
                    let running = rows.filter { $0.state == .working }.count
                    Text("Workers: \(rows.count) · Running: \(running)")
                        .font(Theme.Font.secondary)
                        .foregroundStyle(Theme.Reply.secondaryInk)
                        .padding(.horizontal, Theme.Space.l)
                        .accessibilityIdentifier("team-summary")
                    strip(shown?.key)
                    Divider()
                }
                if let shown {
                    // Each worker keeps its own chat store, composer, subscriptions and permission answers.
                    AnyView(ChatView(key: shown.key, teamPage: true).id(shown.key))
                        .accessibilityElement(children: .contain)
                        .accessibilityIdentifier("team-worker-chat")
                } else {
                    ContentUnavailableView {
                        Label("Waiting for worker", systemImage: Theme.Icon.subagent)
                    }
                }
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            .background(Theme.pageBackground)
            .toolbarVisibility(.hidden, for: .navigationBar)
        }
        .task(id: lead) { await model.refreshOpenChildren(lead, hint: nil) }
    }

    private func pick(_ worker: SessionKey) {
        ui.requested = worker
        model.lastTeamWorker[lead] = worker
    }

    private func strip(_ selected: SessionKey?) -> some View {
        ScrollViewReader { scroll in
            ScrollView(.horizontal) {
                HStack(spacing: Theme.Space.s) {
                    ForEach(rows, id: \.key) { worker in
                        let active = selected == worker.key
                        Button { pick(worker.key) } label: {
                            HStack(spacing: Theme.Space.s) {
                                StatusDot(state: worker.state, size: Theme.Size.dotS, waiting: worker.waitsInBackground)
                                Text(worker.displayTitle).lineLimit(type.isAccessibilitySize ? 2 : 1)
                            }
                            .font(Theme.Font.labelStrong)
                            .padding(.horizontal, Theme.Space.m)
                            .padding(.vertical, Theme.Space.s)
                            .foregroundStyle(active ? Theme.Shell.inkText : Color.primary)
                            .background(active ? Theme.Shell.ink : Theme.Fill.well, in: .capsule)
                            .overlay { if !active { Capsule().strokeBorder(.quaternary, lineWidth: Theme.Size.hairline) } }
                            .frame(minHeight: Theme.Size.tapTarget)
                            .contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                        .focused($focused, equals: worker.key)
                        .onKeyPress(keys: [.leftArrow, .rightArrow, .home, .end]) { event in
                            guard let index = rows.firstIndex(where: { $0.key == worker.key }) else { return .ignored }
                            let next: Int
                            switch event.key {
                            case .leftArrow: next = (index + rows.count - 1) % rows.count
                            case .rightArrow: next = (index + 1) % rows.count
                            case .home: next = 0
                            case .end: next = rows.count - 1
                            default: return .ignored
                            }
                            pick(rows[next].key)
                            focused = rows[next].key
                            return .handled
                        }
                        .accessibilityLabel(worker.displayTitle)
                        .accessibilityValue(worker.stateLabel)
                        .accessibilityAddTraits(active ? .isSelected : [])
                        .accessibilityIdentifier("team-worker")
                        .id(worker.key)
                    }
                }
                .padding(.horizontal, Theme.Space.l)
            }
            .scrollIndicators(.hidden)
            .onChange(of: selected, initial: true) { _, next in
                if let next { scroll.scrollTo(next, anchor: .center) }
            }
        }
    }
}
#endif
