#if os(iOS)
import SwiftUI

/// One to-do on the V1 list (#10465; docs/design/proposals/todos-redesign.md 3.4, "Row anatomy"; DESIGN_SYSTEM 3.4
/// "The work-item row" with the tick in the source mark's slot): the tick; the title with its due day or age; line 2,
/// the mark and the hub's line in words; the door to what it asks; line 3, what the grouping does not already say.
/// It reads its own cell only (`TodosListModel.Row`), so a run's new line draws this row alone (P5). The tick is a
/// button of its own (a 24-pt glyph hit as a finger's square, spoken "Complete, ‹title›" with its value, HIG-006 and
/// HIG-007); the rest of the row opens the to-do and is spoken as one element.
struct TodoListRow: View {
    var row: TodosListModel.Row
    /// The tick: closes, takes back a tick of a moment ago, or reopens (`TodosListModel.tick(of:)`).
    var tick: () -> Void = {}
    /// Mark Done at the end of a finished, seen to-do's line.
    var markDone: () -> Void = {}

    var body: some View {
        let c = row.content
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.m) {
            tickButton(c)
            VStack(alignment: .leading, spacing: Theme.Space.xs) {
                titleLine(c)
                if let said = c.said { lineTwo(said, mark: c.mark, saving: c.saving, markDone: c.markDone) }
                if let door = c.door { doorLine(door, asks: c.asks) }
                if let caption = c.caption {
                    Text(caption)
                        .font(Theme.Font.caption)
                        .foregroundStyle(.secondary)
                        .lineLimit(1)
                }
            }
            .padding(.vertical, Theme.Space.s)
            .accessibilityElement(children: .ignore)
            .accessibilityLabel(c.spoken)
            .accessibilityAddTraits(.isButton)
            .accessibilityActions {
                if c.markDone { Button(String(localized: "Mark Done [To-dos Actions]"), action: markDone) }
            }
            .accessibilityIdentifier("todos.row." + row.todoID)
        }
        // A re-filing put it in another group or fold (proposal 3.9): a new view there, drawn hidden from its first
        // frame, which fades it in where it now is as it appears, so nothing slides across the list. With Reduce Motion
        // the model hides nothing.
        .opacity(row.entering ? 0 : 1)
        .onAppear { if row.entering { withAnimation(Theme.Motion.conceal) { row.entered() } } }
        .accessibilityElement(children: .contain)
    }

    /// The tick, hit well past its glyph without moving the row (`tapTargetOutset`); not on a row still being saved.
    private func tickButton(_ c: TodoRowContent) -> some View {
        Button(action: tick) {
            TodoTickGlyph(closed: c.closed)
                .tapTargetOutset(.all)
        }
        .buttonStyle(.plain)
        .disabled(c.saving)
        .accessibilityLabel(c.tickLabel)
        .accessibilityValue(c.tickValue)
        .accessibilityAddTraits(c.closed ? .isSelected : [])
        .accessibilityIdentifier("todos.tick." + row.todoID)
    }

    private func titleLine(_ c: TodoRowContent) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.s) {
            // A tick of a moment ago is struck through in the secondary color until the hub has it closed; a closed to-do
            // keeps its title as it was (the frames draw it so), its tick saying it is closed.
            Text(c.title)
                .font(Theme.Font.bodyStrong)
                .strikethrough(c.struck)
                .foregroundStyle(c.struck ? Color.secondary : Color.primary)
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

    /// Line 2: the mark and the line; "Saving…" in the tertiary color while a new to-do is not on the hub yet; "· Mark Done"
    /// at the end of a finished one the owner has seen (proposal 3.4, `finished_seen`).
    private func lineTwo(_ said: TodoLineText.Said, mark: TodoMark?, saving: Bool, markDone: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: Theme.Space.sm) {
            if let mark { TodoMarkView(mark: mark) }
            if saving {
                Text(said.rest ?? "").foregroundStyle(.tertiary).lineLimit(1)
            } else if markDone {
                HStack(alignment: .firstTextBaseline, spacing: Theme.Space.flush) {
                    Text(Self.attributed(said, then: TodoTimeWords.separator)).lineLimit(1)
                    Button(action: self.markDone) {
                        Text("Mark Done [To-dos Actions]")
                            .foregroundStyle(Theme.accent)
                            .lineLimit(1)
                            .fixedSize()
                            .tapTargetOutset()
                    }
                    .buttonStyle(.plain)
                    .accessibilityIdentifier("todos.markDone." + row.todoID)
                }
            } else {
                Text(Self.attributed(said))
                    .lineLimit(said.lead == nil ? 1 : 2) // a plain line (a to-do's notes) is one line
            }
        }
        .font(Theme.Font.secondary)
    }

    /// The door to what it asks, drawn as the style's button (proposal 3.4: "[ Review Plan › ]"). Until the row answers
    /// in place (#10465's answers step) a tap on it is the row's, which opens the to-do where its asks are answered;
    /// the row's label says it.
    private func doorLine(_ door: TodoDoor, asks: Int) -> some View {
        HStack(spacing: Theme.Space.sm) {
            Button {} label: {
                HStack(spacing: Theme.Space.xxs) {
                    Text(door == .reviewPlan ? String(localized: "Review Plan [To-dos Door]") : String(localized: "Review [To-dos Door]"))
                    ThemedIcon(Theme.Icon.disclosure).font(Theme.Font.captionStrong)
                }
                .lineLimit(1)
            }
            .font(Theme.Font.secondaryStrong)
            .themedButton()
            .tint(Theme.accent)
            .fixedSize()
            .allowsHitTesting(false)
            if asks > 1 {
                Text("\(asks) asks [To-dos Door]")
                    .font(Theme.Font.caption)
                    .foregroundStyle(.secondary)
                    .lineLimit(1)
            }
        }
    }

    /// The lead in its tone, then the rest in the secondary color: one paragraph, so it wraps as one line of text.
    /// `then`: more words after it, in the secondary color (the separator before Mark Done).
    static func attributed(_ said: TodoLineText.Said, then more: String? = nil) -> AttributedString {
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
        if let more {
            var part = AttributedString(more)
            part.foregroundColor = .secondary
            out += part
        }
        return out
    }
}

/// The tick's 24-pt glyph on the title's first line (its button hits a finger's square, `TodoListRow`); a fold's line
/// keeps its room, unseen, so its words sit in the titles' column.
struct TodoTickGlyph: View {
    var closed: Bool

    var body: some View {
        ThemedIcon(closed ? Theme.Icon.successFilled : Theme.Icon.unchecked)
            .font(Theme.Font.title)
            .foregroundStyle(closed ? AnyShapeStyle(Theme.accent) : AnyShapeStyle(.tertiary))
            .accessibilityHidden(true)
    }
}
#endif
