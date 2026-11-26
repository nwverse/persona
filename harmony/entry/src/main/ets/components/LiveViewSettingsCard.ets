import { Colors } from '../design/Colors';
import { Motion } from '../design/Motion';
import { MotionTokens } from '../core/MotionTokens';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import {
  FleetItem,
  FleetLiveState,
  fleetItemProgressPercent,
  sampleFleetState
} from '../core/FleetLiveState';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { NativeLiveView, nativeLiveView } from '../platform/NativeLiveView';
import { Haptics } from '../platform/Haptics';
import { toastCenter } from '../models/ToastCenter';

const PREVIEW_MODES: string[] = ['needsYou', 'working', 'background', 'done'];

/**
 * Interactive Fleet Live View preview & settings section matching iOS
 * `Settings › Notifications › Live Activities` (docs/apple/live-activity.md).
 */
@ComponentV2
export struct LiveViewSettingsCard {
  @Local private liveView: NativeLiveView = nativeLiveView;
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private previewMode: string = 'needsYou';
  @Local private pressedId: string = '';

  @Computed get previewState(): FleetLiveState {
    return sampleFleetState(this.previewMode, this.liveView.lockDetails);
  }

  private phaseTint(phase: string): ResourceColor {
    if (phase === 'needsYou') return Colors.needs;
    if (phase === 'working') return Colors.working;
    if (phase === 'done') return Colors.unread;
    return Colors.link;
  }

  private capsuleBg(phase: string): string {
    if (phase === 'needsYou') return '#D93A3A';
    if (phase === 'working') return '#B35400';
    if (phase === 'done') return '#1D7A4C';
    return '#0071E3';
  }

  private headline(state: FleetLiveState): string {
    if (state.phase === 'needsYou') return tr('live_view_needs_count').replace('{count}', String(state.needsYou));
    if (state.phase === 'working') return tr('live_view_working_count').replace('{count}', String(state.working));
    if (state.phase === 'done') return tr('live_view_unread_count').replace('{count}', String(state.unread));
    return tr('live_view_all_caught_up');
  }

  private capsuleLabel(state: FleetLiveState): string {
    if (state.phase === 'needsYou') return tr('live_view_capsule_needs').replace('{count}', String(state.needsYou));
    if (state.phase === 'working') return tr('live_view_capsule_working').replace('{count}', String(state.working));
    if (state.phase === 'done') return tr('live_view_capsule_unread').replace('{count}', String(state.unread));
    return tr('live_view_capsule_done');
  }

  private itemLine(item: FleetItem, details: boolean): string {
    if (!details) {
      if (item.kind === 'permission') return tr('live_view_anon_permission');
      if (item.kind === 'plan') return tr('live_view_anon_plan');
      if (item.kind === 'question') return tr('live_view_anon_question');
      if (item.kind === 'working') return tr('live_view_anon_working');
      return tr('live_view_anon_unread');
    }
    return item.detail || item.place || item.title;
  }

  @Builder previewCapsule() {
    Row({ space: 8 }) {
      Circle().width(8).height(8).fill('#FFFFFF')
      Text(this.capsuleLabel(this.previewState))
        .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
        .fontWeight(FontWeight.Bold)
        .fontColor('#FFFFFF')
        .maxLines(1)
      if (this.previewState.phase === 'needsYou' && this.previewState.working > 0) {
        Text('· ' + tr('live_view_capsule_working').replace('{count}', String(this.previewState.working)))
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontColor('#E6FFFFFF')
          .maxLines(1)
      } else if (this.previewState.phase === 'working' && this.previewState.items[0]?.background && fleetItemProgressPercent(this.previewState.items[0]) >= 0) {
        Text(String(fleetItemProgressPercent(this.previewState.items[0])) + '%')
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontWeight(FontWeight.Medium)
          .fontColor('#E6FFFFFF')
          .maxLines(1)
      }
    }
    .padding({ left: 12, right: 12, top: 6, bottom: 6 })
    .backgroundColor(this.capsuleBg(this.previewState.phase))
    .borderRadius(Tokens.capsule)
    .animation(Motion.state(this.env.reduceMotion))
    .id('live_view.preview.capsule')
  }

  @Builder previewCard() {
    Column({ space: 10 }) {
      Row({ space: 8 }) {
        Stack() {
          Image($r('app.media.phone_persona_face')).width(22).height(22).objectFit(ImageFit.Contain)
        }
        .width(28)
        .height(28)
        .borderRadius(14)
        .backgroundColor(Colors.subtle)
        Column({ space: 2 }) {
          Text(this.headline(this.previewState))
            .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.ink)
            .maxLines(1)
          Text(tr('persona'))
            .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
            .fontColor(Colors.secondary)
            .maxLines(1)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        Row({ space: 6 }) {
          if (this.previewState.needsYou > 0) this.countChip(String(this.previewState.needsYou), Colors.needs)
          if (this.previewState.working > 0) this.countChip(String(this.previewState.working), Colors.working)
          if (this.previewState.unread > 0) this.countChip(String(this.previewState.unread), Colors.unread)
        }
      }.width('100%')

      ForEach(this.previewState.items.slice(0, 2), (item: FleetItem) => {
        Column({ space: 4 }) {
          Row({ space: 6 }) {
            Circle().width(6).height(6).fill(item.kind === 'working' ? Colors.working : item.kind === 'unread' ? Colors.unread : Colors.needs)
            Text(this.liveView.lockDetails ? item.title : this.itemLine(item, false))
              .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
              .fontWeight(FontWeight.Medium)
              .fontColor(Colors.ink)
              .maxLines(1)
              .textOverflow({ overflow: TextOverflow.Ellipsis })
              .layoutWeight(1)
            if (this.liveView.lockDetails && item.background) {
              Text(tr('live_view_background_tag'))
                .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                .fontColor(Colors.working)
                .padding({ left: 6, right: 6, top: 2, bottom: 2 })
                .backgroundColor(Colors.control)
                .borderRadius(6)
            }
          }.width('100%')
          if (this.liveView.lockDetails) {
            Text(this.itemLine(item, true))
              .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
              .fontColor(Colors.secondary)
              .maxLines(1)
              .textOverflow({ overflow: TextOverflow.Ellipsis })
              .width('100%')
              .padding({ left: 12 })
          }
          if (item.background && fleetItemProgressPercent(item) >= 0) {
            Row() {
              Row()
                .width(String(fleetItemProgressPercent(item)) + '%')
                .height(4)
                .backgroundColor(Colors.working)
                .borderRadius(2)
            }
            .width('100%')
            .height(4)
            .backgroundColor(Colors.divider)
            .borderRadius(2)
            .margin({ top: 2 })
          }
        }.width('100%').alignItems(HorizontalAlign.Start)
      }, (item: FleetItem) => this.previewMode + ':' + item.id + ':' + String(this.liveView.lockDetails) + ':' + item.detail)
    }
    .width('100%')
    .padding(Tokens.page)
    .backgroundColor(this.env.dark ? '#E61F1F23' : '#F8FFFFFF')
    .backgroundBlurStyle(BlurStyle.COMPONENT_THICK)
    .borderRadius(Tokens.card)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.75), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.card })
    .shadow(PhoneStyle.heroElevation(this.env.dark))
    .animation(Motion.state(this.env.reduceMotion))
    .id('live_view.preview.card')
  }

  @Builder countChip(value: string, tint: ResourceColor) {
    Row({ space: 4 }) {
      Circle().width(6).height(6).fill(tint)
      Text(value)
        .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
        .fontWeight(FontWeight.Bold)
        .fontColor(Colors.ink)
    }
    .padding({ left: 8, right: 8, top: 3, bottom: 3 })
    .backgroundColor(this.env.dark ? '#33FFFFFF' : Colors.control)
    .borderRadius(10)
  }

  private modeTitle(mode: string): string {
    if (mode === 'needsYou') return tr('needs_you');
    if (mode === 'working') return tr('working');
    if (mode === 'background') return tr('live_view_background_tag');
    return tr('agent_group_unread');
  }

  build() {
    Column({ space: Tokens.inset }) {
      Text(tr('live_view_section_title'))
        .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
        .fontWeight(FontWeight.Medium)
        .fontColor(Colors.secondary)
        .width('100%')
        .padding({ left: 4 })

      // Status-bar capsule + Lock-screen banner preview inside an ambient glass stage
      Column({ space: 12 }) {
        Row() {
          this.previewCapsule()
        }.width('100%').justifyContent(FlexAlign.Center)
        this.previewCard()
        Scroll() {
          Row({ space: 6 }) {
            ForEach(PREVIEW_MODES, (mode: string) => {
              Button(this.modeTitle(mode), { type: ButtonType.Capsule })
                .height(32)
                .padding({ left: 11, right: 11 })
                .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
                .fontWeight(this.previewMode === mode ? FontWeight.Bold : FontWeight.Medium)
                .fontColor(this.previewMode === mode ? Colors.link : Colors.secondary)
                .backgroundColor(this.previewMode === mode ? Colors.selection : (this.env.dark ? '#28282B' : Colors.control))
                .border({ width: this.previewMode === mode ? 1 : 0.5, color: this.previewMode === mode ? Colors.link : PhoneStyle.cardBorder(this.env.dark), radius: Tokens.capsule })
                .scale({ x: this.pressedId === mode ? MotionTokens.pressScale : 1, y: this.pressedId === mode ? MotionTokens.pressScale : 1 })
                .animation(Motion.state(this.env.reduceMotion))
                .onTouch((event: TouchEvent) => {
                  if (event.type === TouchType.Down) this.pressedId = mode;
                  else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.pressedId = '';
                })
                .onClick(() => {
                  Haptics.selection();
                  Motion.run(this.getUIContext(), Motion.state(this.env.reduceMotion), () => {
                    this.previewMode = mode;
                  });
                })
                .id('live_view.preview.mode.' + mode)
            }, (mode: string) => mode)
          }.padding({ right: 4 })
        }.scrollable(ScrollDirection.Horizontal).scrollBar(BarState.Off).width('100%')
      }
      .width('100%')
      .padding({ left: 12, right: 12, top: 14, bottom: 12 })
      .borderRadius(22)
      .linearGradient({
        direction: GradientDirection.RightBottom,
        colors: [[Colors.heroGlow, 0], [Colors.heroGlowAlt, 0.5], [this.env.dark ? '#18181B' : '#EAECEF', 1]]
      })
      .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 22 })

      // Live View toggles
      Column() {
        Row({ space: Tokens.inset }) {
          Column({ space: 2 }) {
            Text(tr('live_view_enable'))
              .fontSize(Tokens.body)
              .maxFontScale(Tokens.interfaceFontMaxScale)
              .fontColor(Colors.ink)
            Text(tr('live_view_enable_detail'))
              .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
              .fontColor(Colors.secondary)
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
          Toggle({ type: ToggleType.Switch, isOn: this.liveView.enabled })
            .enabled(!this.liveView.busy)
            .accessibilityText(tr('live_view_enable'))
            .onChange((value: boolean) => {
              Haptics.tap();
              this.liveView.setEnabled(value).catch(() => {});
            })
            .id('live_view.toggle.enabled')
        }.width('100%').constraintSize({ minHeight: 54 }).padding({ left: Tokens.page, right: Tokens.page, top: 8, bottom: 8 })

        Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({ left: Tokens.page })

        Row({ space: Tokens.inset }) {
          Column({ space: 2 }) {
            Text(tr('live_view_lock_details'))
              .fontSize(Tokens.body)
              .maxFontScale(Tokens.interfaceFontMaxScale)
              .fontColor(Colors.ink)
            Text(tr('live_view_lock_details_detail'))
              .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
              .fontColor(Colors.secondary)
          }.layoutWeight(1).alignItems(HorizontalAlign.Start)
          Toggle({ type: ToggleType.Switch, isOn: this.liveView.lockDetails })
            .enabled(!this.liveView.busy && this.liveView.enabled)
            .accessibilityText(tr('live_view_lock_details'))
            .onChange((value: boolean) => {
              Haptics.tap();
              this.liveView.setLockDetails(value).catch(() => {});
            })
            .id('live_view.toggle.details')
        }.width('100%').constraintSize({ minHeight: 54 }).padding({ left: Tokens.page, right: Tokens.page, top: 8, bottom: 8 })

        Divider().strokeWidth(Tokens.divider).color(Colors.divider).margin({ left: Tokens.page })

        Row({ space: 10 }) {
          Button(tr('live_view_test_publish'), { type: ButtonType.Capsule })
            .layoutWeight(1)
            .height(42)
            .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
            .fontWeight(FontWeight.Medium)
            .fontColor(Colors.onAction)
            .backgroundColor(Colors.action)
            .shadow({ radius: 10, color: '#26007AFF', offsetY: 3 })
            .scale({ x: this.pressedId === 'publish' ? MotionTokens.pressScale : 1, y: this.pressedId === 'publish' ? MotionTokens.pressScale : 1 })
            .animation(Motion.release(this.env.reduceMotion))
            .onTouch((event: TouchEvent) => {
              if (event.type === TouchType.Down) this.pressedId = 'publish';
              else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.pressedId = '';
            })
            .enabled(this.liveView.enabled && !this.liveView.busy)
            .onClick(() => {
              Haptics.tap();
              this.liveView.publishSample(this.previewMode).then((ok: boolean) => {
                if (ok) { Haptics.success(); toastCenter.success(tr('live_view_published_toast')); }
                else Haptics.error();
              });
            })
            .id('live_view.test.publish')
          if (this.liveView.active) {
            Button(tr('live_view_test_stop'), { type: ButtonType.Capsule })
              .height(42)
              .padding({ left: 16, right: 16 })
              .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
              .fontWeight(FontWeight.Medium)
              .fontColor(Colors.link)
              .backgroundColor(Colors.control)
              .border({ width: 0.5, color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.capsule })
              .scale({ x: this.pressedId === 'stop' ? MotionTokens.pressScale : 1, y: this.pressedId === 'stop' ? MotionTokens.pressScale : 1 })
              .animation(Motion.release(this.env.reduceMotion))
              .onTouch((event: TouchEvent) => {
                if (event.type === TouchType.Down) this.pressedId = 'stop';
                else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.pressedId = '';
              })
              .onClick(() => {
                Haptics.tap();
                this.liveView.stopNow().catch(() => {});
              })
              .id('live_view.test.stop')
          }
        }.width('100%').padding({ left: Tokens.page, right: Tokens.page, top: 10, bottom: 12 })
      }
      .width('100%')
      .backgroundColor(Colors.surface)
      .borderRadius(PhoneStyle.cardRadius)
      .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: PhoneStyle.cardRadius })
      .shadow(PhoneStyle.cardElevation(this.env.dark))

      if (this.liveView.statusCode) {
        Row({ space: 6 }) {
          SymbolGlyph($r('sys.symbol.info_circle')).fontSize(Tokens.interfaceFontVp(14, this.env.fontScale)).fontColor([Colors.secondary])
          Text(tr(this.liveView.statusCode))
            .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
            .fontColor(Colors.secondary)
            .layoutWeight(1)
        }.width('100%').padding({ left: 4, right: 4 }).id('live_view.status')
      }
    }.width('100%').padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.page })
  }
}
