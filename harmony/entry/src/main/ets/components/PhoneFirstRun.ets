import { LengthMetrics } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { Motion } from '../design/Motion';
import { MotionTokens } from '../core/MotionTokens';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { Haptics } from '../platform/Haptics';

@ComponentV2
export struct PhoneFirstRun {
  @Param error: string = '';
  @Param pendingCleanup: boolean = false;
  @Event onScan: () => void = () => {};
  @Event onManual: () => void = () => {};
  @Event onDemo: () => void = () => {};
  @Event onCleanup: () => void = () => {};
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private viewportWidth: number = 402;
  @Local private pressedId: string = '';

  private metric(standard: number, largest: number): number {
    return PhoneStyle.formSize(standard, largest, this.env.fontScale);
  }

  private ink(): ResourceColor {
    return this.env.dark ? Color.White : Color.Black;
  }

  private isWideHero(): boolean {
    return this.viewportWidth >= 560;
  }

  private help(): void {
    this.getUIContext().showAlertDialog({
      title: tr('welcome_action_help'),
      message: tr('welcome_action_hint'),
      alignment: DialogAlignment.Center,
      width: Math.min(340, Math.max(0, this.viewportWidth - 32)),
      cornerRadius: 30,
      primaryButton: { value: tr('welcome_action_done'), action: () => {} }
    });
  }

  @Builder
  featureBadge(symbol: Resource, label: string, tint: ResourceColor) {
    Row({ space: 6 }) {
      SymbolGlyph(symbol)
        .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
        .fontColor([tint])
        .accessibilityLevel('no')
      Text(label)
        .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
        .fontWeight(FontWeight.Medium)
        .fontColor(Colors.secondary)
        .maxLines(1)
    }
    .padding({ left: 10, right: 11, top: 6, bottom: 6 })
    .backgroundColor(this.env.dark ? '#CC242426' : '#E6FFFFFF')
    .backgroundBlurStyle(BlurStyle.COMPONENT_THICK)
    .borderRadius(Tokens.capsule)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: Tokens.capsule })
    .shadow(PhoneStyle.surfaceShadow({ radius: 8, color: this.env.dark ? '#33000000' : '#0A000000', offsetY: 2 }))
  }

  @Builder
  shortcut(kind: string, image: Resource, action: () => void) {
    Button({ type: ButtonType.Normal }) {
      Column({ space: 7 }) {
        Stack() {
          SymbolGlyph(image).fontSize(this.metric(22, 28) + 'vp')
            .fontColor([kind === 'demo' ? Colors.link : this.ink()]).accessibilityLevel('no')
        }.width(this.metric(52, 62)).aspectRatio(1).borderRadius(32)
          .backgroundColor(this.env.dark ? '#D9252528' : '#EBFFFFFF')
          .backgroundBlurStyle(BlurStyle.COMPONENT_THICK)
          .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 32 })
          .shadow(PhoneStyle.cardElevation(this.env.dark))
        Text(tr('welcome_action_' + kind)).fontSize(this.metric(14, 19) + 'vp')
          .lineHeight(this.metric(18, 24) + 'vp').fontWeight(FontWeight.Medium)
          .fontColor(this.ink()).maxLines(1)
      }
    }.padding({ left: 4, right: 4, top: 2, bottom: 2 }).backgroundColor(Color.Transparent)
      .accessibilityText(tr('welcome_action_' + kind))
      .scale({ x: this.pressedId === kind ? MotionTokens.pressScale : 1, y: this.pressedId === kind ? MotionTokens.pressScale : 1 })
      .animation(Motion.release(this.env.reduceMotion))
      .onTouch((event: TouchEvent) => {
        if (event.type === TouchType.Down) this.pressedId = kind;
        else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.pressedId = '';
      })
      .onClick(() => { Haptics.tap(); action(); }).id('welcome.' + kind)
  }

  @Builder
  actions() {
    this.shortcut('other', $r('sys.symbol.keyboard'), () => this.onManual())
    this.shortcut('help', $r('sys.symbol.questionmark_circle'), () => this.help())
    this.shortcut('demo', $r('sys.symbol.play_circle_fill'), () => this.onDemo())
  }

  @Builder
  heroContent() {
    Column({ space: 26 }) {
      Column({ space: 14 }) {
        Stack() {
          Stack() {
            Image(this.env.dark ? $r('app.media.welcome_app_icon_dark') : $r('app.media.welcome_app_icon'))
              .width(88).height(88).borderRadius(22).objectFit(ImageFit.Cover).accessibilityLevel('no')
          }
          .padding(4)
          .borderRadius(26)
          .backgroundColor(this.env.dark ? '#CC232327' : '#F2FFFFFF')
          .border({ width: 0.75, color: PhoneStyle.cardBorder(this.env.dark), radius: 26 })
          .shadow(PhoneStyle.heroElevation(this.env.dark))
        }
        Column({ space: 6 }) {
          Text(tr('persona')).fontSize(this.metric(32, 38) + 'vp').lineHeight(this.metric(38, 44) + 'vp')
            .fontWeight(FontWeight.Bold).fontColor(this.ink())
          Text(tr('phone_welcome_intro'))
            .fontSize(this.metric(15, 20) + 'vp')
            .lineHeight(this.metric(21, 27) + 'vp')
            .fontColor(Colors.secondary)
            .textAlign(TextAlign.Center)
            .constraintSize({ maxWidth: 320 })
        }.alignItems(HorizontalAlign.Center)

        Flex({ wrap: FlexWrap.Wrap, justifyContent: FlexAlign.Center, space: { main: LengthMetrics.vp(8), cross: LengthMetrics.vp(8) } }) {
          this.featureBadge(Symbols.persona, tr('phone_settings_assistant'), Colors.ai)
          this.featureBadge(Symbols.agents, tr('agents'), Colors.link)
          this.featureBadge(Symbols.device, tr('phone_machines'), Colors.teal)
        }.width('100%').margin({ top: 4 })
      }.alignItems(HorizontalAlign.Center)

      Button({ type: ButtonType.Capsule }) {
        Row({ space: 9 }) {
          SymbolGlyph($r('sys.symbol.qrcode')).fontSize(this.metric(18, 24) + 'vp')
            .fontColor([Color.White]).accessibilityLevel('no')
          Text(tr('welcome_action_scan')).fontSize(this.metric(17, 22) + 'vp').fontWeight(600)
            .fontColor(Color.White).maxLines(1)
        }
      }.width('100%').height(this.metric(58, 68)).padding({ left: 16, right: 16 })
        .backgroundColor(this.env.dark ? '#0A84FF' : '#007AFF')
        .linearGradient({
          direction: GradientDirection.RightBottom,
          colors: this.env.dark ? [['#1A8FFF', 0], ['#0066E6', 1]] : [['#0A84FF', 0], ['#0062E6', 1]]
        })
        .shadow({ radius: 18, color: this.env.dark ? '#550A84FF' : '#3D007AFF', offsetY: 6 })
        .scale({ x: this.pressedId === 'connect' ? MotionTokens.pressScale : 1, y: this.pressedId === 'connect' ? MotionTokens.pressScale : 1 })
        .animation(Motion.release(this.env.reduceMotion))
        .onTouch((event: TouchEvent) => {
          if (event.type === TouchType.Down) this.pressedId = 'connect';
          else if (event.type === TouchType.Up || event.type === TouchType.Cancel) this.pressedId = '';
        })
        .accessibilityText(tr('welcome_action_scan')).onClick(() => { Haptics.tap(); this.onScan(); }).id('welcome.connect')

      if (this.viewportWidth >= this.metric(260, 330)) {
        Row({ space: 28 }) { this.actions() }.justifyContent(FlexAlign.Center)
      } else {
        Column({ space: 10 }) { this.actions() }
      }

      if (this.error) {
        Row({ space: 8 }) {
          SymbolGlyph($r('sys.symbol.exclamationmark_triangle')).fontSize('16vp').fontColor([Colors.danger])
          Text(this.error).fontSize(14).fontColor(Colors.danger).layoutWeight(1)
        }
        .width('100%')
        .padding({ left: 14, right: 14, top: 10, bottom: 10 })
        .backgroundColor(this.env.dark ? '#28FF453A' : '#14BA1A1A')
        .borderRadius(14)
        .border({ width: 0.5, color: Colors.danger, radius: 14 })
      }
      if (this.pendingCleanup) {
        Button(tr('retry_cleanup'), { buttonStyle: ButtonStyleMode.TEXTUAL }).onClick(() => { Haptics.tap(); this.onCleanup(); })
      }
    }
    .width('100%')
    .constraintSize({ maxWidth: this.isWideHero() ? 440 : 380 })
    .padding(this.isWideHero() ? { left: 36, right: 36, top: 36, bottom: 32 } : { left: 24, right: 24, top: 40, bottom: 28 })
    .backgroundColor(this.isWideHero() ? Colors.glassSurface : Color.Transparent)
    .backgroundBlurStyle(this.isWideHero() ? BlurStyle.COMPONENT_THICK : BlurStyle.NONE)
    .borderRadius(this.isWideHero() ? 32 : 0)
    .border(this.isWideHero() ? { width: PhoneStyle.surfaceEdgeWidth(0.75), color: PhoneStyle.cardBorder(this.env.dark), radius: 32 } : { width: 0 })
    .shadow(this.isWideHero() ? PhoneStyle.heroElevation(this.env.dark) : { radius: 0, color: Color.Transparent })
  }

  build() {
    Stack() {
      // Soft atmospheric gradient backdrop echoing HarmonyOS NEXT Liquid Glass lighting.
      Column()
        .width('100%')
        .height('100%')
        .linearGradient({
          direction: GradientDirection.RightBottom,
          colors: [[Colors.heroGlow, 0], [Colors.heroGlowAlt, 0.34], [Colors.canvas, 0.72], [Colors.canvas, 1]]
        })
        .hitTestBehavior(HitTestMode.None)

      Scroll() {
        Column() {
          this.heroContent()
        }
        .width('100%')
        .constraintSize({ minHeight: '100%' })
        .padding(this.isWideHero() ? { left: 32, right: 32, top: 32, bottom: 32 } : 0)
        .justifyContent(FlexAlign.Center)
        .alignItems(HorizontalAlign.Center)
      }.width('100%').height('100%').scrollBar(BarState.Off)
        .onAreaChange((_: Area, area: Area) => { this.viewportWidth = Number(area.width); })
    }.width('100%').height('100%').backgroundColor(Colors.canvas)
  }
}
