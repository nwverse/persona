#if os(iOS)
import SwiftUI

/// One to-do on the V1 list (#10465; docs/design/proposals/todos-redesign.md 3.4, "Row anatomy"; DESIGN_SYSTEM 3.4
/// "The work-item row" with the tick in the source mark's slot): the tick; the title with its due day or age; line 2,
/// the mark and the hub's line in words; the door to what it asks; line 3, what the grouping does not already say.
/// It reads its own cell only (`TodosListModel.Row`), so a run's new line draws this row alone (P5). The tick acts from
/// the next step of #10465; until then the whole row opens the to-do.
struct TodoListRow: View {
    var row: TodosListModel.Row

    var body: some View {
        let c = row.content
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
            // The tick's 24-pt glyph on the title's first line.
            ThemedIcon(c.closed ? Theme.Icon.successFilled : Theme.Icon.unchecked)
                .font(Theme.Font.title)
                .foregroundStyle(c.closed ? AnyShapeStyle(Theme.accent) : AnyShapeStyle(.tertiary))
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                titleLine(c)
                if let said = c.said { lineTwo(said, mark: c.mark) }
                if let door = c.door { doorLine(door, asks: c.asks) }
                if let caption = c.caption {
                    Text(caption)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            .padding(.vertical, Theme.Space.s)
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel(spoken(c))
        .accessibilityIdentifier("todos.row." + row.todoID)
    }

    private func titleLine(_ c: TodoRowContent) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            Text(c.title)
                .font(Theme.Font.bodyStrong)
                .foregroundStyle(c.closed ? Color.secondary : Color.primary)
                .lineLimit(2)
            Spacer(minLength: Theme.Space.xs)
            if c.unseen {
                Circle().fill(StatusPalette.unread.dot)
                    .scaledFrame(width: Theme.Size.dotS, height: Theme.Size.dotS)
                    .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + $0.height }
            }
            if let trailing = c.trailing {
                Text(trailing)
                    .font(Theme.Font.secondary)
                    .foregroundStyle(c.overdue ? AnyShapeStyle(Theme.cautionText) : AnyShapeStyle(.secondary))
                    .monospacedDigit()
                    .lineLimit(1)
                    .fixedSize()
            }
        }
    }

    private func lineTwo(_ said: TodoLineText.Said, mark: TodoMark?) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            if let mark { TodoMarkView(mark: mark) }
            Text(Self.attributed(said))
                .lineLimit(said.lead == nil ? 1 : 2) // a plain line (a to-do's notes) is one line
        }
        .font(Theme.Font.secondary)
    }

    private func doorLine(_ door: TodoDoor, asks: Int) -> some View {
        HStack(spacing: Theme.Space.sm) {
            Chip(text: door == .reviewPlan ? String(localized: "Review Plan [To-dos Door]") : String(localized: "Review [To-dos Door]"),
                 look: .tint(Theme.accent))
            if asks > 1 {
                Text("\(asks) asks [To-dos Door]")
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }

    /// The lead in its tone, then the rest in the secondary color: one paragraph, so it wraps as one line of text.
    static func attributed(_ said: TodoLineText.Said) -> AttributedString {
        var out = AttributedString()
        let leadInk: Color = switch said.tone {
        case .needs: StatusPalette.needsYou.text
        case .danger: Theme.danger
        case .ready: StatusPalette.unread.text
        case .plain: .secondary
        }
        if let lead = said.lead {
            var part = AttributedString(lead)
            part.foregroundColor = leadInk
            out += part
        }
        if let rest = said.rest {
            var part = AttributedString(said.lead == nil ? rest : TodoTimeWords.separator + rest)
            part.foregroundColor = said.tone == .danger ? Theme.danger : .secondary
            out += part
        }
        return out
    }

    private func spoken(_ c: TodoRowContent) -> String {
        var parts = [c.title]
        if c.closed { parts.append(StateName.done.text) }
        if let said = c.said { parts.append(said.spoken) }
        if let door = c.door {
            parts.append(door == .reviewPlan ? String(localized: "Review Plan [To-dos Door]") : String(localized: "Review [To-dos Door]"))
        }
        if let caption = c.caption { parts.append(caption) }
        if let trailing = c.trailing { parts.append(trailing) }
        return parts.joined(separator: String(localized: ", ", comment: "Separator between parts of an accessibility label"))
    }
}
#endif
