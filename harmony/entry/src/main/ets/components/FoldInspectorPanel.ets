import { Colors } from '../design/Colors';
import { Motion } from '../design/Motion';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { ResourceRow, Wire } from '../models/Wire';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { NativeLiveView, nativeLiveView } from '../platform/NativeLiveView';
import { Haptics } from '../platform/Haptics';
import { NeedsYouPulseDot, WorkingArcSpinner } from './StatusMotionMarks';

interface FleetPulseCounts {
  needsYou: number;
  working: number;
  unread: number;
  total: number;
}

/**
 * HarmonyOS foldable & tri-fold right-side Live Context Inspector panel.
 * Automatically mounts as the 3rd column on Mate XT tri-fold / MatePad XL displays,
 * and can be toggled on Mate X7 unfolded dual-screen displays.
 */
@ComponentV2
export struct FoldInspectorPanel {
  @Require @Param model: ClientModel;
  @Param detailModel: ClientModel | undefined = undefined;
  @Param panelWidth: number = 288;
  @Param postureLabel: string = '';
  @Event onClose: () => void = () => {};
  @Event onOpenTools: () => void = () => {};
  @Event onOpenToolsTab: (tab: number) => void = (_tab: number) => {
    this.onOpenTools();
  };
  @Event onOpenSession: (row: ResourceRow) => void = () => {};
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private liveView: NativeLiveView = nativeLiveView;

  private activeOwner(): ClientModel {
    return this.detailModel || this.model;
  }

  private selectedSession(): ResourceRow | undefined {
    const owner = this.activeOwner();
    return owner.selected && owner.selected.id ? owner.selected : undefined;
  }

  private sessionData(): Record<string, Object> {
    const row = this.selectedSession();
    return row ? row.data() : {};
  }

  private normalizedRowState(row: ResourceRow): string {
    const data = row.data();
    const raw = row.state || Wire.text(data, 'status');
    if (Wire.bool(data, 'needs_you') || raw === 'needs_you' || raw === 'blocked') return 'needs_you';
    if (Wire.bool(data, 'working') || raw === 'working' || raw === 'running') return 'working';
    if (raw === 'done' || raw === 'unread' || Wire.bool(data, 'unread')) return 'unread';
    return 'idle';
  }

  private sessionState(): string {
    const row = this.selectedSession();
    return row ? this.normalizedRowState(row) : 'idle';
  }

  private stateLabelKey(state: string): string {
    if (state === 'needs_you') return 'needs_you';
    if (state === 'working') return 'working';
    if (state === 'unread') return 'agent_group_unread';
    return 'idle';
  }

  private stateTint(state: string): ResourceColor {
    if (state === 'needs_you') return Colors.needs;
    if (state === 'working') return Colors.working;
    if (state === 'unread') return Colors.unread;
    return Colors.secondary;
  }

  private activeFleetSessions(): ResourceRow[] {
    const rows = this.model.directoryReady && this.model.directorySessions.length
      ? this.model.directorySessions
      : this.model.sessions;
    return rows.filter((row: ResourceRow) => {
      if (row.kind !== 'session') return false;
      const data = row.data();
      return !Wire.bool(data, 'archived') && !Wire.bool(data, 'assistant') && !Wire.bool(data, 'hidden_by_directory');
    });
  }

  private fleetCounts(): FleetPulseCounts {
    const rows = this.activeFleetSessions();
    if (!rows.length) {
      return {
        needsYou: this.liveView.currentState.needsYou,
        working: this.liveView.currentState.working,
        unread: this.liveView.currentState.unread,
        total: this.liveView.currentState.needsYou + this.liveView.currentState.working + this.liveView.currentState.unread
      };
    }
    let needsYou = 0;
    let working = 0;
    let unread = 0;
    for (const row of rows) {
      const state = this.normalizedRowState(row);
      if (state === 'needs_you') needsYou++;
      else if (state === 'working') working++;
      else if (state === 'unread') unread++;
    }
    return { needsYou: needsYou, working: working, unread: unread, total: rows.length };
  }

  private prioritizedSessions(): ResourceRow[] {
    const rank = (row: ResourceRow): number => {
      const state = this.normalizedRowState(row);
      if (state === 'needs_you') return 0;
      if (state === 'working') return 1;
      if (state === 'unread') return 2;
      return 3;
    };
    return this.activeFleetSessions()
      .slice()
      .sort((a: ResourceRow, b: ResourceRow) => rank(a) - rank(b))
      .slice(0, 6);
  }

  @Builder statusMark(state: string) {
    if (state === 'working') {
      WorkingArcSpinner({ diameter: 14, tint: Colors.working, surface: Color.Transparent, showCoreDot: true })
    } else if (state === 'needs_you') {
      NeedsYouPulseDot({ dotSize: 8, tint: Colors.needs, surface: Colors.surface, bordered: false })
    } else if (state === 'unread') {
      Circle().width(8).height(8).fill(Colors.unread)
    } else {
      Circle().width(8).height(8).fill(Colors.tertiary)
    }
  }

  @Builder metaRow(label: string, value: string, mono: boolean = false, startEllipsis: boolean = false) {
    if (value) {
      Row({ space: 8 }) {
        Text(label)
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontColor(Colors.secondary)
          .maxLines(1)
        Text(value)
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontFamily(mono ? 'monospace' : 'sans-serif')
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.ink)
          .textAlign(TextAlign.End)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
          .ellipsisMode(startEllipsis ? EllipsisMode.START : EllipsisMode.CENTER)
          .layoutWeight(1)
      }.width('100%').padding({ top: 3, bottom: 3 })
    }
  }

  @Builder toolChip(labelKey: string, tab: number, identity: string, symbol?: Resource) {
    Button({ type: ButtonType.Normal }) {
      Column({ space: 4 }) {
        if (symbol) {
          SymbolGlyph(symbol)
            .fontSize('15vp')
            .fontColor([Colors.link])
        } else {
          Text('›_')
            .fontSize('12vp')
            .fontFamily('monospace')
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.link)
            .height(15)
            .textAlign(TextAlign.Center)
        }
        Text(tr(labelKey))
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.link)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }
      .width('100%')
      .height('100%')
      .justifyContent(FlexAlign.Center)
      .alignItems(HorizontalAlign.Center)
      .padding({ left: 4, right: 4 })
    }
    .layoutWeight(1)
    .height(52)
    .padding(0)
    .backgroundColor(Colors.selection)
    .borderRadius(12)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: Colors.divider, radius: 12 })
    .accessibilityText(tr(labelKey))
    .bindTips(tr(labelKey))
    .onClick(() => {
      Haptics.tap();
      this.onOpenToolsTab(tab);
    })
    .id(identity)
  }

  @Builder pulseCounterTile(count: number, labelKey: string, tint: ResourceColor, identity: string) {
    Column({ space: 3 }) {
      Row({ space: 5 }) {
        Circle().width(6).height(6).fill(tint)
        Text(String(count))
          .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
          .fontWeight(FontWeight.Bold)
          .fontColor(count > 0 ? Colors.ink : Colors.secondary)
          .maxLines(1)
      }
      Text(tr(labelKey))
        .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
        .fontColor(Colors.secondary)
        .maxLines(1)
        .textOverflow({ overflow: TextOverflow.Ellipsis })
    }
    .layoutWeight(1)
    .alignItems(HorizontalAlign.Center)
    .padding({ left: 4, right: 4, top: 8, bottom: 8 })
    .backgroundColor(count > 0 && tint === Colors.needs ? Colors.selection : Colors.control)
    .borderRadius(12)
    .accessibilityGroup(true)
    .accessibilityText(tr(labelKey) + ' ' + String(count))
    .id(identity)
  }

  @Builder fleetPulseCard() {
    Column({ space: 10 }) {
      Row({ space: 8 }) {
        Text(tr('fold_inspector_fleet'))
          .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
          .fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink)
          .layoutWeight(1)
        if (this.fleetCounts().total > 0) {
          Text(String(this.fleetCounts().total))
            .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.secondary)
            .padding({ left: 7, right: 7, top: 2, bottom: 2 })
            .backgroundColor(Colors.control)
            .borderRadius(Tokens.capsule)
        }
      }.width('100%')

      Row({ space: 6 }) {
        this.pulseCounterTile(this.fleetCounts().needsYou, 'needs_you', Colors.needs, 'fold.inspector.count.needs')
        this.pulseCounterTile(this.fleetCounts().working, 'working', Colors.working, 'fold.inspector.count.working')
        this.pulseCounterTile(this.fleetCounts().unread, 'agent_group_unread', Colors.unread, 'fold.inspector.count.unread')
      }.width('100%')

      if (this.fleetCounts().needsYou === 0 && this.fleetCounts().working === 0 && this.fleetCounts().unread === 0) {
        Row({ space: 6 }) {
          SymbolGlyph(Symbols.check).fontSize('12vp').fontColor([Colors.positive])
          Text(tr('live_view_all_caught_up'))
            .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
            .fontColor(Colors.secondary)
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
            .layoutWeight(1)
        }.width('100%').padding({ left: 2, right: 2 })
      }

      if (this.prioritizedSessions().length > 0) {
        Column({ space: 6 }) {
          ForEach(this.prioritizedSessions(), (row: ResourceRow) => {
            Button({ type: ButtonType.Normal }) {
              Row({ space: 8 }) {
                this.statusMark(this.normalizedRowState(row))
                Column({ space: 2 }) {
                  Text(row.title || tr('agent_untitled'))
                    .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                    .fontWeight(this.activeOwner().selected.key() === row.key() ? FontWeight.Bold : FontWeight.Medium)
                    .fontColor(Colors.ink)
                    .maxLines(1)
                    .textOverflow({ overflow: TextOverflow.Ellipsis })
                  Text([
                    this.model.machineName(row.machine) || row.machine,
                    tr(this.stateLabelKey(this.normalizedRowState(row)))
                  ].filter((part: string) => !!part).join(' · '))
                    .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                    .fontColor(Colors.secondary)
                    .maxLines(1)
                    .textOverflow({ overflow: TextOverflow.Ellipsis })
                }.layoutWeight(1).alignItems(HorizontalAlign.Start)
                SymbolGlyph(Symbols.expand).fontSize('11vp').fontColor([Colors.tertiary])
              }.width('100%').padding({ left: 10, right: 10, top: 8, bottom: 8 })
            }
            .width('100%')
            .padding(0)
            .backgroundColor(this.activeOwner().selected.key() === row.key() ? Colors.selection : Colors.control)
            .borderRadius(12)
            .border({
              width: this.activeOwner().selected.key() === row.key() ? 1 : 0,
              color: this.activeOwner().selected.key() === row.key() ? Colors.link : Color.Transparent,
              radius: 12
            })
            .animation(Motion.state(this.env.reduceMotion))
            .onClick(() => {
              Haptics.selection();
              this.onOpenSession(row);
            })
            .id('fold.inspector.session.' + row.id)
          }, (row: ResourceRow) => row.key() + ':' + this.normalizedRowState(row) + ':' + row.title)
        }.width('100%')
      }
    }
    .width('100%')
    .padding(14)
    .backgroundColor(Colors.surface)
    .borderRadius(18)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: Colors.divider, radius: 18 })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
  }

  build() {
    Row() {
      Divider().vertical(true).strokeWidth(0.5).color(Colors.divider).height('100%')
      Column({ space: 12 }) {
        Row({ space: 8 }) {
          Column({ space: 5 }) {
            Text(tr('fold_inspector_title'))
              .fontSize(Tokens.interfaceFontVp(16, this.env.fontScale))
              .fontWeight(FontWeight.Bold)
              .fontColor(Colors.ink)
              .maxLines(1)
            if (this.postureLabel) {
              Row({ space: 5 }) {
                SymbolGlyph(Symbols.apps).fontSize('11vp').fontColor([Colors.link])
                Text(this.postureLabel)
                  .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                  .fontWeight(FontWeight.Medium)
                  .fontColor(Colors.link)
                  .maxLines(1)
                  .textOverflow({ overflow: TextOverflow.Ellipsis })
              }
              .padding({ left: 8, right: 8, top: 3, bottom: 3 })
              .backgroundColor(Colors.selection)
              .borderRadius(Tokens.capsule)
              .id('fold.inspector.posture')
            }
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
          Button({ type: ButtonType.Circle }) {
            SymbolGlyph($r('sys.symbol.xmark')).fontSize('14vp').fontColor([Colors.secondary])
          }
          .width(32)
          .height(32)
          .padding(0)
          .backgroundColor(Colors.control)
          .accessibilityText(tr('close'))
          .bindTips(tr('close'))
          .onClick(() => {
            Haptics.tap();
            this.onClose();
          })
          .id('fold.inspector.close')
        }.width('100%').padding({ left: 16, right: 14, top: 12, bottom: 2 }).alignItems(VerticalAlign.Top)

        Scroll() {
          Column({ space: 12 }) {
            if (this.selectedSession()) {
              Column({ space: 8 }) {
                Row({ space: 8 }) {
                  this.statusMark(this.sessionState())
                  Text(this.selectedSession()!.title || tr('main_chat'))
                    .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
                    .fontWeight(FontWeight.Bold)
                    .fontColor(Colors.ink)
                    .maxLines(2)
                    .textOverflow({ overflow: TextOverflow.Ellipsis })
                    .layoutWeight(1)
                  Text(tr(this.stateLabelKey(this.sessionState())))
                    .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                    .fontWeight(FontWeight.Medium)
                    .fontColor(this.stateTint(this.sessionState()))
                    .padding({ left: 7, right: 7, top: 2, bottom: 2 })
                    .backgroundColor(this.sessionState() === 'needs_you' ? Colors.selection : Colors.control)
                    .borderRadius(Tokens.capsule)
                    .maxLines(1)
                }.width('100%').alignItems(VerticalAlign.Center)
                Divider().strokeWidth(0.5).color(Colors.divider)
                this.metaRow(tr('machine'), this.activeOwner().machineName(this.selectedSession()!.machine) || this.selectedSession()!.machine)
                this.metaRow(tr('model'), (Wire.text(this.sessionData(), 'model').split('/').pop() || Wire.text(this.sessionData(), 'launcher') || 'Claude'))
                this.metaRow(tr('branch'), Wire.text(this.sessionData(), 'git_branch'), true)
                this.metaRow(tr('phone_session_folder'), Wire.text(this.sessionData(), 'cwd'), true, true)
                if (this.selectedSession()!.kind === 'session' && this.selectedSession()!.machine) {
                  Divider().strokeWidth(0.5).color(Colors.divider).margin({ top: 2, bottom: 2 })
                  Row({ space: 6 }) {
                    this.toolChip('files', 0, 'fold.inspector.tools.files', Symbols.folder)
                    this.toolChip('git', 1, 'fold.inspector.tools.git', $r('sys.symbol.arrow_triangle_merge'))
                    this.toolChip('terminal', 2, 'fold.inspector.tools.terminal')
                  }.width('100%').id('fold.inspector.workspace')
                }
              }
              .width('100%')
              .padding(14)
              .backgroundColor(Colors.surface)
              .borderRadius(18)
              .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: Colors.divider, radius: 18 })
              .shadow(PhoneStyle.cardElevation(this.env.dark))
            }
            this.fleetPulseCard()
          }.width('100%').padding({ left: 12, right: 12, bottom: 24 })
        }.width('100%').layoutWeight(1).scrollBar(BarState.Off)
      }.width('100%').height('100%').layoutWeight(1).backgroundColor(Colors.grouped)
    }
    .width(this.panelWidth)
    .height('100%')
    .transition(Motion.slideFade(this.env.reduceMotion, 0))
    .id('fold.inspector.panel')
  }
}
