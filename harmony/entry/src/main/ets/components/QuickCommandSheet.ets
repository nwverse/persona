import { Colors } from '../design/Colors';
import { Motion } from '../design/Motion';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow, Wire } from '../models/Wire';
import { herdrTabName } from '../core/SessionNaming';
import {
  buildQuickCommandGroups,
  QuickCommandCategory,
  QuickCommandEntry,
  QuickCommandGroup,
  QuickCommandLabels,
  QuickCommandTone,
  QuickSessionInput
} from '../core/QuickCommandPalette';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { Haptics } from '../platform/Haptics';
import { NeedsYouPulseDot, WorkingArcSpinner } from './StatusMotionMarks';

/**
 * ArkUI V2 Quick Command Palette modal/sheet for jumping across active agents,
 * workspaces, Live Workbench tabs, XiaoYi, LiveView preview, and Light/Dark theme.
 */
@ComponentV2
export struct QuickCommandSheet {
  @Require @Param model: ClientModel;
  @Param showWorkbench: boolean = false;
  @Param showLiveView: boolean = true;
  @Param autoFocus: boolean = false;
  @Event onSelectEntry: (entry: QuickCommandEntry) => void = () => {};
  @Event onClose: () => void = () => {};
  @Local private query: string = '';
  @Local private env: NativeEnvironment = nativeEnvironment;

  private sessionInputs(): QuickSessionInput[] {
    const rows = this.model.directoryReady && this.model.directorySessions.length
      ? this.model.directorySessions
      : this.model.sessions;
    const inputs: QuickSessionInput[] = [];
    for (const row of rows) {
      if (row.kind !== 'session') continue;
      const data = row.data();
      if (Wire.bool(data, 'archived') || Wire.bool(data, 'assistant') || Wire.bool(data, 'hidden_by_directory')) continue;
      const rawState = row.state || Wire.text(data, 'status');
      const state = Wire.bool(data, 'needs_you') || rawState === 'needs_you' || rawState === 'blocked'
        ? 'needs_you'
        : Wire.bool(data, 'working') || rawState === 'working' || rawState === 'running'
          ? 'working'
          : rawState === 'done' || rawState === 'unread' || Wire.bool(data, 'unread')
            ? 'unread'
            : 'idle';
      const machineName = this.model.machineName(row.machine) || row.machine;
      const branch = Wire.text(data, 'git_branch') || Wire.text(data, 'branch');
      const cwd = Wire.text(data, 'cwd');
      const subtitleParts = [machineName, branch].filter((part: string) => !!part);
      const preferHerdr = this.model.directoryOwnerFor(row)?.useHerdrTabName ?? this.model.useHerdrTabName;
      inputs.push({
        id: row.id,
        key: row.key(),
        title: row.title || tr('agent_untitled'),
        herdrTabName: herdrTabName(data),
        preferHerdrTabName: preferHerdr,
        subtitle: subtitleParts.join(' · '),
        machineName: machineName,
        state: state,
        keywords: [row.machine, branch, cwd, Wire.text(data, 'launcher'), Wire.text(data, 'model')]
          .filter((part: string) => !!part),
        data: data
      });
    }
    return inputs;
  }

  private paletteLabels(): QuickCommandLabels {
    return {
      mainConversation: tr('main_chat'),
      agents: tr('agents'),
      inbox: tr('inbox'),
      todos: tr('todos'),
      knowledge: tr('knowledge'),
      health: tr('phone_drawer_health'),
      machines: tr('machines'),
      settings: tr('settings'),
      toggleWorkbench: tr('quick_palette_toggle_workbench'),
      workbenchCode: tr('quick_palette_code_tab'),
      workbenchMachines: tr('quick_palette_machines_tab'),
      openXiaoYi: tr('phone_xiaoyi'),
      toggleTheme: tr('quick_palette_toggle_theme'),
      liveViewPreview: tr('quick_palette_liveview_preview'),
      needsYou: tr('needs_you'),
      working: tr('working'),
      unread: tr('agent_group_unread'),
      idle: tr('idle'),
      untitledAgent: tr('agent_untitled')
    };
  }

  @Computed get groups(): QuickCommandGroup[] {
    return buildQuickCommandGroups(this.query, this.sessionInputs(), {
      useHerdrTabName: this.model.useHerdrTabName,
      inboxVisible: this.model.inboxPageVisible(),
      todosVisible: this.model.todosPageVisible(),
      showWorkbench: this.showWorkbench,
      showLiveView: this.showLiveView,
      isDark: this.env.dark,
      labels: this.paletteLabels()
    });
  }

  private categoryTitle(category: QuickCommandCategory): string {
    if (category === 'agents') return tr('quick_palette_agents');
    if (category === 'workspaces') return tr('quick_palette_workspaces');
    return tr('quick_palette_actions');
  }

  private toneColor(tone: QuickCommandTone): ResourceColor {
    if (tone === 'needs_you') return Colors.needs;
    if (tone === 'working') return Colors.working;
    if (tone === 'unread') return Colors.unread;
    if (tone === 'accent') return Colors.link;
    return Colors.secondary;
  }

  private toneWash(tone: QuickCommandTone): ResourceColor {
    if (tone === 'needs_you' || tone === 'accent') return Colors.selection;
    if (tone === 'working') return this.env.dark ? '#28FFB224' : '#189B5500';
    if (tone === 'unread') return this.env.dark ? '#244CC38A' : '#161D7A4C';
    return Colors.control;
  }

  private entrySymbol(entry: QuickCommandEntry): Resource {
    if (entry.iconKey === 'persona') return $r('sys.symbol.house');
    if (entry.iconKey === 'agents') return Symbols.agents;
    if (entry.iconKey === 'inbox') return $r('sys.symbol.inbox');
    if (entry.iconKey === 'todos') return $r('sys.symbol.list_checkmark');
    if (entry.iconKey === 'knowledge') return $r('sys.symbol.books_vertical_fill');
    if (entry.iconKey === 'health') return $r('sys.symbol.heart');
    if (entry.iconKey === 'machines') return $r('sys.symbol.matebook_and_phone_fill');
    if (entry.iconKey === 'settings') return Symbols.gear;
    if (entry.iconKey === 'workbench') return Symbols.apps;
    if (entry.iconKey === 'code') return $r('sys.symbol.code');
    if (entry.iconKey === 'xiaoyi') return $r('sys.symbol.wand_and_stars');
    if (entry.iconKey === 'sun') return $r('sys.symbol.sun_max');
    if (entry.iconKey === 'moon') return $r('sys.symbol.moon');
    if (entry.iconKey === 'liveview') return $r('sys.symbol.bell');
    return Symbols.agent;
  }

  private triggerEntry(entry: QuickCommandEntry): void {
    Haptics.selection();
    this.onSelectEntry(entry);
    this.onClose();
  }

  private executeTopMatch(): void {
    const firstGroup = this.groups[0];
    const firstEntry = firstGroup?.entries[0];
    if (firstEntry) {
      this.triggerEntry(firstEntry);
    }
  }

  @Builder entryLeadingIcon(entry: QuickCommandEntry) {
    Stack({ alignContent: Alignment.Center }) {
      if (entry.category === 'agents') {
        if (entry.tone === 'working') {
          WorkingArcSpinner({ diameter: 16, tint: Colors.working, surface: Color.Transparent, showCoreDot: true })
        } else if (entry.tone === 'needs_you') {
          NeedsYouPulseDot({ dotSize: 9, tint: Colors.needs, surface: Colors.surface, bordered: false })
        } else {
          SymbolGlyph(this.entrySymbol(entry))
            .fontSize('15vp')
            .fontColor([this.toneColor(entry.tone)])
        }
      } else {
        SymbolGlyph(this.entrySymbol(entry))
          .fontSize('15vp')
          .fontColor([entry.iconKey === 'xiaoyi' ? Colors.ai : this.toneColor(entry.tone)])
      }
    }
    .width(32)
    .height(32)
    .borderRadius(9)
    .backgroundColor(entry.iconKey === 'xiaoyi' ? Colors.selection : this.toneWash(entry.tone))
    .flexShrink(0)
  }

  @Builder entryRow(entry: QuickCommandEntry) {
    Button({ type: ButtonType.Normal }) {
      Row({ space: 10 }) {
        this.entryLeadingIcon(entry)
        Column({ space: 2 }) {
          Text(entry.title)
            .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
            .fontWeight(entry.tone === 'needs_you' ? FontWeight.Bold : FontWeight.Medium)
            .fontColor(Colors.ink)
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
          if (entry.subtitle) {
            Text(entry.subtitle)
              .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
              .fontColor(Colors.secondary)
              .maxLines(1)
              .textOverflow({ overflow: TextOverflow.Ellipsis })
          }
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)

        if (entry.badge) {
          Text(entry.badge)
            .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
            .fontWeight(FontWeight.Medium)
            .fontColor(this.toneColor(entry.tone))
            .padding({ left: 8, right: 8, top: 3, bottom: 3 })
            .backgroundColor(this.toneWash(entry.tone))
            .borderRadius(Tokens.capsule)
            .maxLines(1)
            .flexShrink(0)
        }
        if (entry.category !== 'actions') {
          SymbolGlyph(Symbols.expand)
            .fontSize('11vp')
            .fontColor([Colors.tertiary])
            .flexShrink(0)
        }
      }
      .width('100%')
      .constraintSize({ minHeight: 52 })
      .padding({ left: 12, right: 12, top: 8, bottom: 8 })
      .alignItems(VerticalAlign.Center)
    }
    .width('100%')
    .padding(0)
    .backgroundColor(Color.Transparent)
    .onClick(() => this.triggerEntry(entry))
    .id('quick.palette.item.' + entry.id)
  }

  build() {
    Column({ space: 12 }) {
      Row({ space: 10 }) {
        Column({ space: 2 }) {
          Text(tr('quick_palette_title'))
            .fontSize(Tokens.interfaceFontVp(19, this.env.fontScale))
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.ink)
            .maxLines(1)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)

        Text('⌘K')
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontFamily('monospace')
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.secondary)
          .padding({ left: 8, right: 8, top: 4, bottom: 4 })
          .backgroundColor(Colors.control)
          .borderRadius(Tokens.capsule)

        Button({ type: ButtonType.Circle }) {
          SymbolGlyph($r('sys.symbol.xmark')).fontSize('14vp').fontColor([Colors.secondary])
        }
        .width(36)
        .height(36)
        .padding(0)
        .backgroundColor(Colors.control)
        .accessibilityText(tr('close'))
        .onClick(() => {
          Haptics.tap();
          this.onClose();
        })
        .id('quick.palette.close')
      }.width('100%').padding({ left: 16, right: 16, top: 16 })

      Row({ space: 8 }) {
        SymbolGlyph(Symbols.search)
          .fontSize('16vp')
          .fontColor([Colors.secondary])
          .flexShrink(0)
        TextInput({ text: this.query, placeholder: tr('quick_palette_placeholder') })
          .layoutWeight(1)
          .height(38)
          .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
          .fontColor(Colors.ink)
          .placeholderColor(Colors.secondary)
          .backgroundColor(Color.Transparent)
          .padding({ left: 0, right: 0 })
          .enterKeyType(EnterKeyType.Go)
          .defaultFocus(this.autoFocus)
          .onChange((value: string) => {
            this.query = value;
          })
          .onSubmit(() => this.executeTopMatch())
          .id('quick.palette.search')
        if (this.query.length > 0) {
          Button({ type: ButtonType.Circle }) {
            SymbolGlyph($r('sys.symbol.xmark')).fontSize('11vp').fontColor([Colors.secondary])
          }
          .width(26)
          .height(26)
          .padding(0)
          .backgroundColor(Colors.control)
          .accessibilityText(tr('clear'))
          .onClick(() => {
            Haptics.tap();
            this.query = '';
          })
          .id('quick.palette.clear')
        }
      }
      .width('calc(100% - 32vp)')
      .padding({ left: 12, right: 10, top: 4, bottom: 4 })
      .backgroundColor(Colors.surface)
      .borderRadius(PhoneStyle.cardRadius)
      .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: PhoneStyle.cardRadius })
      .shadow(PhoneStyle.cardElevation(this.env.dark))

      if (this.groups.length === 0) {
        Column({ space: 8 }) {
          SymbolGlyph(Symbols.search).fontSize('26vp').fontColor([Colors.tertiary])
          Text(tr('quick_palette_empty'))
            .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
            .fontColor(Colors.secondary)
        }
        .width('100%')
        .layoutWeight(1)
        .justifyContent(FlexAlign.Center)
        .alignItems(HorizontalAlign.Center)
        .id('quick.palette.empty')
      } else {
        Scroll() {
          Column({ space: 14 }) {
            ForEach(this.groups, (group: QuickCommandGroup) => {
              Column({ space: 6 }) {
                Text(this.categoryTitle(group.category))
                  .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
                  .fontWeight(FontWeight.Bold)
                  .fontColor(Colors.secondary)
                  .padding({ left: 4, right: 4 })
                  .width('100%')

                Column() {
                  ForEach(group.entries.slice(), (entry: QuickCommandEntry, index: number) => {
                    if (index > 0) {
                      Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({ left: 54 })
                    }
                    this.entryRow(entry)
                  }, (entry: QuickCommandEntry) => entry.id + ':' + entry.title + ':' + entry.subtitle + ':' + entry.badge + ':' + entry.iconKey)
                }
                .width('100%')
                .backgroundColor(Colors.surface)
                .borderRadius(PhoneStyle.cardRadius)
                .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: PhoneStyle.cardRadius })
                .shadow(PhoneStyle.cardElevation(this.env.dark))
                .clip(true)
              }.width('100%').alignItems(HorizontalAlign.Start)
            }, (group: QuickCommandGroup) => group.category + ':' + group.entries.map((entry: QuickCommandEntry) => entry.id + '|' + entry.title + '|' + entry.badge).join(','))
          }
          .width('100%')
          .padding({ left: 16, right: 16, bottom: 28 })
        }
        .width('100%')
        .layoutWeight(1)
        .align(Alignment.TopStart)
        .scrollBar(BarState.Off)
        .animation(Motion.state(this.env.reduceMotion))
      }
    }
    .width('100%')
    .height('100%')
    .backgroundColor(Colors.sheetBackground)
    .id('quick.palette.sheet')
  }
}
