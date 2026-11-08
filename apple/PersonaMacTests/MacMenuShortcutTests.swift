import AppKit
import Testing
@testable import Persona

/// The app's real menu bar (the tests run inside Persona.app): every shortcut docs/apple/mac.md「菜单和快捷键」 promises is
/// on a menu item, and no two items share one (a second ⌘⏎ / ⌘⌫ once took the composer's keys, 2026-11-01).
@MainActor
struct MacMenuShortcutTests {
    struct Shortcut: Hashable, CustomStringConvertible {
        let key: String
        let mods: NSEvent.ModifierFlags.RawValue
        var description: String {
            let m = NSEvent.ModifierFlags(rawValue: mods)
            return (m.contains(.control) ? "⌃" : "") + (m.contains(.option) ? "⌥" : "") + (m.contains(.shift) ? "⇧" : "")
                + (m.contains(.command) ? "⌘" : "") + key
        }
    }

    /// Waits for SwiftUI to have built the menus (the app finishes launching around the test run).
    private func items() async -> [(title: String, shortcut: Shortcut)] {
        for _ in 0..<50 {
            if let menu = NSApp.mainMenu, menu.items.contains(where: { $0.title == "Agent" }) { return walk(menu) }
            try? await Task.sleep(for: .milliseconds(100))
        }
        return NSApp.mainMenu.map(walk) ?? []
    }

    private func walk(_ menu: NSMenu) -> [(title: String, shortcut: Shortcut)] {
        menu.items.flatMap { item -> [(title: String, shortcut: Shortcut)] in
            var out: [(title: String, shortcut: Shortcut)] = []
            if !item.keyEquivalent.isEmpty {
                let mods = item.keyEquivalentModifierMask.intersection([.command, .shift, .option, .control])
                out.append((item.title, Shortcut(key: item.keyEquivalent.lowercased(), mods: mods.rawValue)))
            }
            if let sub = item.submenu { out += walk(sub) }
            return out
        }
    }

    @Test func promisedShortcutsAreOnTheMenus() async {
        let all = await items()
        let titles = Dictionary(all.map { ($0.title, $0.shortcut.description) }, uniquingKeysWith: { a, _ in a })
        let promised: [String: String] = [
            "快速打开…": "⌘k", "查找…": "⌘f", "查找下一个": "⌘g", "查找上一个": "⇧⌘g", "交给 Persona": "⌘j", "切换终端 / 对话": "⇧⌘j", "显示会话信息": "⌥⌘i", "聚焦输入框": "⌘i", "中断": "⌘.",
            "上一个 Agent": "⌘[", "下一个 Agent": "⌘]", "下一个需要你的": "⌃⌘n",
            "放大字号": "⌘+", "缩小字号": "⌘-", "标准字号": "⌘0",
            "隐藏侧边栏": "⌃⌘s", "刷新": "⌘r",
        ]
        for (title, shortcut) in promised {
            // The sidebar item says Show Sidebar while it is folded (remembered across launches, the tests run in the app).
            let found = title == "隐藏侧边栏" ? titles[title] ?? titles["显示侧边栏"] : titles[title]
            #expect(found == shortcut, "\(title)")
        }
    }

    @Test func noTwoItemsShareAShortcut() async {
        let all = await items()
        #expect(!all.isEmpty)
        let groups = Dictionary(grouping: all, by: \.shortcut).filter { $0.value.count > 1 }
        #expect(groups.isEmpty, "\(groups.map { "\($0.key): \($0.value.map(\.title))" })")
    }
}
