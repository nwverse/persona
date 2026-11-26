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

  private sessionState(): string {
    const row = this.selectedSession();
    if (!row) return 'idle';
    const data = row.data();
    if (Wire.bool(data, 'needs_you') || row.state === 'needs_you') return 'needs_you';
    if (Wire.bool(data, 'working') || row.state === 'working' || row.state === 'running') return 'working';
    if (row.state === 'done' || Wire.bool(data, 'unread')) return 'unread';
    return 'idle';
  }

  private urgentSessions(): ResourceRow[] {
    const rows = this.model.directoryReady && this.model.directorySessions.length ? this.model.directorySessions : this.model.sessions;
    return rows.filter((row: ResourceRow) => {
      if (row.kind !== 'session' || Wire.bool(row.data(), 'archived')) return false;
      return row.state === 'needs_you' || row.state === 'working' || row.state === 'done';
    }).slice(0, 4);
  }

  @Builder statusMark(state: string) {
    if (state === 'working') {
      WorkingArcSpinner({ diameter: 14, tint: Colors.working, surface: Color.Transparent, showCoreDot: false })
    } else if (state === 'needs_you') {
      NeedsYouPulseDot({ dotSize: 8, tint: Colors.needs, surface: Colors.surface, bordered: false })
    } else if (state === 'unread') {
      Circle().width(8).height(8).fill(Colors.unread)
    } else {
      Circle().width(8).height(8).fill(Colors.tertiary)
    }
  }

  @Builder metaRow(label: string, value: string, mono: boolean = false) {
    if (value) {
      Row({ space: 8 }) {
        Text(label)
          .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
          .fontColor(Colors.secondary)
          .maxLines(1)
        Text(value)
          .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
          .fontFamily(mono ? 'monospace' : 'sans-serif')
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.ink)
          .textAlign(TextAlign.End)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
          .ellipsisMode(EllipsisMode.CENTER)
          .layoutWeight(1)
      }.width('100%').padding({ top: 4, bottom: 4 })
    }
  }

  @Builder fleetPulseCard() {
    Column({ space: 10 }) {
      Row({ space: 8 }) {
        Text(tr('fold_inspector_fleet'))
          .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
          .fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink)
          .layoutWeight(1)
        if (this.liveView.currentState.needsYou > 0) {
          this.miniBadge(String(this.liveView.currentState.needsYou), Colors.needs)
        }
        if (this.liveView.currentState.working > 0) {
          this.miniBadge(String(this.liveView.currentState.working), Colors.working)
        }
        if (this.liveView.currentState.unread > 0) {
          this.miniBadge(String(this.liveView.currentState.unread), Colors.unread)
        }
      }.width('100%')

      if (this.urgentSessions().length === 0) {
        Text(tr('live_view_all_caught_up'))
          .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
          .fontColor(Colors.secondary)
          .width('100%')
      } else {
        ForEach(this.urgentSessions(), (row: ResourceRow) => {
          Button({ type: ButtonType.Normal }) {
            Row({ space: 8 }) {
              this.statusMark(row.state === 'done' ? 'unread' : row.state)
              Column({ space: 2 }) {
                Text(row.title || tr('agent_untitled'))
                  .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                  .fontWeight(FontWeight.Medium)
                  .fontColor(Colors.ink)
                  .maxLines(1)
                  .textOverflow({ overflow: TextOverflow.Ellipsis })
                Text(this.model.machineName(row.machine) || row.machine)
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
          .animation(Motion.state(this.env.reduceMotion))
          .onClick(() => {
            Haptics.selection();
            this.onOpenSession(row);
          })
          .id('fold.inspector.session.' + row.id)
        }, (row: ResourceRow) => row.key() + ':' + row.state)
      }
    }
    .width('100%')
    .padding(14)
    .backgroundColor(Colors.surface)
    .borderRadius(18)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: Colors.divider, radius: 18 })
    .shadow(PhoneStyle.surfaceShadow({ radius: 10, color: this.env.dark ? '#38000000' : '#0E000000', offsetY: 3 }))
  }

  @Builder miniBadge(value: string, tint: ResourceColor) {
    Row({ space: 4 }) {
      Circle().width(6).height(6).fill(tint)
      Text(value)
        .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
        .fontWeight(FontWeight.Bold)
        .fontColor(Colors.ink)
    }
    .padding({ left: 6, right: 6, top: 2, bottom: 2 })
    .backgroundColor(Colors.control)
    .borderRadius(10)
  }

  build() {
    Row() {
      Divider().vertical(true).strokeWidth(0.5).color(Colors.divider).height('100%')
      Column({ space: 12 }) {
        Row({ space: 8 }) {
          Column({ space: 2 }) {
            Text(tr('fold_inspector_title'))
              .fontSize(Tokens.interfaceFontVp(16, this.env.fontScale))
              .fontWeight(FontWeight.Bold)
              .fontColor(Colors.ink)
              .maxLines(1)
            if (this.postureLabel) {
              Text(this.postureLabel)
                .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                .fontColor(Colors.link)
                .maxLines(1)
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
          .onClick(() => {
            Haptics.tap();
            this.onClose();
          })
          .id('fold.inspector.close')
        }.width('100%').padding({ left: 16, right: 14, top: 12, bottom: 4 })

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
                }.width('100%')
                Divider().strokeWidth(0.5).color(Colors.divider)
                this.metaRow(tr('machine'), this.activeOwner().machineName(this.selectedSession()!.machine) || this.selectedSession()!.machine)
                this.metaRow(tr('model'), (Wire.text(this.sessionData(), 'model').split('/').pop() || Wire.text(this.sessionData(), 'launcher') || 'Claude'))
                this.metaRow(tr('branch'), Wire.text(this.sessionData(), 'git_branch'), true)
                this.metaRow(tr('phone_session_folder'), Wire.text(this.sessionData(), 'cwd'), true)
                if (this.selectedSession()!.kind === 'session' && this.selectedSession()!.machine) {
                  Button(tr('workspace'), { type: ButtonType.Capsule })
                    .width('100%')
                    .height(36)
                    .margin({ top: 4 })
                    .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                    .fontWeight(FontWeight.Medium)
                    .fontColor(Colors.link)
                    .backgroundColor(Colors.selection)
                    .onClick(() => {
                      Haptics.tap();
                      this.onOpenTools();
                    })
                    .id('fold.inspector.workspace')
                }
              }
              .width('100%')
              .padding(14)
              .backgroundColor(Colors.surface)
              .borderRadius(18)
              .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: Colors.divider, radius: 18 })
              .shadow(PhoneStyle.surfaceShadow({ radius: 10, color: this.env.dark ? '#38000000' : '#0E000000', offsetY: 3 }))
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
