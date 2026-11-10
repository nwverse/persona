import SwiftUI

/// A compact task navigation row, not a filter chip. The task title can truncate while the Open action stays visible;
/// the whole row is one accessible button with the design system's minimum touch target.
struct TaskOpenButton: View {
    var title: String
    var action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: Theme.Space.s) {
                Text(title)
                    .lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
                Text("Open [Dispatched Task]")
                    .foregroundStyle(Theme.accent)
                    .lineLimit(1)
                    .fixedSize()
                ThemedIcon(Theme.Icon.disclosure)
                    .foregroundStyle(.tertiary)
                    .accessibilityHidden(true)
            }
            .font(Theme.Font.captionStrong)
            .padding(.horizontal, Theme.Space.m)
            .scaledFrame(minHeight: Theme.Size.tapTarget)
            .background(Theme.Fill.well, in: .rect(cornerRadius: Theme.Radius.control, style: .continuous))
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
    }
}
