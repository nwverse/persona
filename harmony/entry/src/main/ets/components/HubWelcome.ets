import { LengthMetrics } from '@kit.ArkUI';
import { ButtonStyles } from '../design/Controls';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { Symbols } from '../design/Symbols';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

@ComponentV2
export struct HubWelcome {
  @Param error: string = '';
  @Param cleanup: boolean = false;
  @Local environment: NativeEnvironment = nativeEnvironment;
  @Event onScan: () => void = () => {};
  @Event onOther: () => void = () => {};
  @Event onGuide: () => void = () => {};
  @Event onDemo: () => void = () => {};
  @Event onVideo: () => void = () => {};
  @Event onCleanup: () => void = () => {};

  @Builder welcomeLink(symbol: Resource, title: string, description: string, action: () => void, id: string, disclosure: boolean = true) {
    Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
      Row({ space: Tokens.inset }) {
        SymbolGlyph(symbol).fontSize(Tokens.symbolFont).fontColor([Colors.link]).width(Tokens.avatar)
          .accessibilityLevel('no')
        Column({ space: Tokens.fine }) {
          Text(tr(title)).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
          Text(tr(description)).fontSize(Tokens.meta).fontColor(Colors.secondary).lineSpacing(LengthMetrics.fp(Tokens.fine))
        }.alignItems(HorizontalAlign.Start).layoutWeight(1)
        if (disclosure) SymbolGlyph(Symbols.expand).fontSize(Tokens.meta).fontColor([Colors.secondary]).accessibilityLevel('no')
      }.width('100%').padding({ left: Tokens.inset, right: Tokens.page, top: Tokens.page, bottom: Tokens.page })
    }.width('100%').height('auto').constraintSize({ minHeight: Tokens.touch }).padding(0).borderRadius(Tokens.card)
      .backgroundColor(Colors.canvas).accessibilityText(tr(title) + ' ' + tr(description)).onClick(action).id(id)
  }

  build() {
    Scroll() {
      Column({ space: Tokens.section + Tokens.inset }) {
        Column({ space: Tokens.section }) {
          Image($r('app.media.startIcon')).width(Tokens.attachmentPreview).height(Tokens.attachmentPreview)
            .objectFit(ImageFit.Contain).borderRadius(Tokens.card).accessibilityLevel('no')
          Column({ space: Tokens.page }) {
            Text('Persona').fontSize(Tokens.markdownHeading1).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
            Text(tr('hub_welcome_summary')).fontSize(Tokens.body).fontColor(Colors.secondary).textAlign(TextAlign.Center)
              .lineSpacing(LengthMetrics.fp(Tokens.fine)).id('welcome.intro')
          }.width('100%')
        }.width('100%')
        Column({ space: Tokens.section }) {
          if (canIUse('SystemCapability.Multimedia.Scan.ScanBarcode')) {
            Column({ space: Tokens.page }) {
              Button(tr('hub_scan_connect')).attributeModifier(ButtonStyles.primary).width('100%')
                .onClick(() => this.onScan()).id('welcome.connect')
              Text(tr('hub_welcome_code_hint')).fontSize(Tokens.meta).fontColor(Colors.secondary).textAlign(TextAlign.Center)
                .lineSpacing(LengthMetrics.fp(Tokens.fine)).id('welcome.hint')
            }.width('100%')
            Button(tr('hub_other_ways')).attributeModifier(ButtonStyles.secondary).width('100%')
              .onClick(() => this.onOther()).id('welcome.other')
          } else {
            Button(tr('welcome')).attributeModifier(ButtonStyles.primary).width('100%')
              .onClick(() => this.onOther()).id('welcome.connect')
          }
        }.width('100%')
        Column() {
          if (this.environment.fontScale >= 1.5) {
            this.welcomeLink(Symbols.persona, 'hub_demo_title', 'hub_demo_note', () => this.onDemo(), 'welcome.demo')
            Button(tr('hub_tour_play')).attributeModifier(ButtonStyles.quiet).alignSelf(ItemAlign.Start)
              .margin({ left: Tokens.inset + Tokens.avatar }).onClick(() => this.onVideo()).id('welcome.video')
          } else {
            Row() {
              Column() {
                this.welcomeLink(Symbols.persona, 'hub_demo_title', 'hub_demo_note', () => this.onDemo(), 'welcome.demo', false)
              }.layoutWeight(1)
              Button(tr('hub_tour_short')).attributeModifier(ButtonStyles.quiet)
                .accessibilityText(tr('hub_tour_play')).onClick(() => this.onVideo()).id('welcome.video')
            }.width('100%').padding({ right: Tokens.gap })
          }
          Divider().color(Colors.divider).strokeWidth(Tokens.divider).margin({ left: Tokens.inset + Tokens.avatar + Tokens.inset, right: Tokens.page })
          this.welcomeLink($r('sys.symbol.qrcode'), 'hub_get_code', 'hub_get_code_note', () => this.onGuide(), 'welcome.guide')
        }.width('100%').backgroundColor(Colors.canvas).borderRadius(Tokens.card).clip(true)
        if (this.error || this.cleanup) Column({ space: Tokens.gap }) {
          if (this.error) Text(tr(this.error)).fontSize(Tokens.meta).fontColor(Colors.danger).textAlign(TextAlign.Center)
          if (this.cleanup) Button(tr('retry_cleanup')).attributeModifier(ButtonStyles.secondary).onClick(() => this.onCleanup())
        }.width('100%')
      }.width('100%').constraintSize({ maxWidth: Tokens.masterMax })
        .padding({ left: Tokens.section, right: Tokens.section, top: Tokens.section + Tokens.gap, bottom: Tokens.section + Tokens.gap })
    }.height('100%').width('100%').backgroundColor(Colors.grouped)
  }
}
