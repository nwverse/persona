import { nativeEnvironment } from '../platform/NativeEnvironment';
import { Colors } from './Colors';
import { Tokens } from './Tokens';

// Compact iPhone's inset lists share their margins, hierarchy and grouped surfaces with the phone shell.
export class PhoneStyle {
  static readonly page: number = 16;
  static readonly cardRadius: number = 15;
  static readonly inset: number = 14;
  static readonly rowHeight: number = 48;
  static readonly bar: number = 44;
  static readonly title: number = 32;
}

@ComponentV2
export struct PhoneIconButton {
  @Require @Param symbol: Resource;
  @Require @Param label: string;
  @Param identity: string = '';
  @Param tint: ResourceColor = Colors.ink;
  @Param active: boolean = true;
  @Event action: () => void = () => {};
  build() {
    Button({ type: ButtonType.Circle }) {
      SymbolGlyph(this.symbol).fontSize('19vp').fontColor([this.tint])
    }.width(36).height(36).padding(0).backgroundColor(Colors.surface)
      .border({ width: 0.5, color: Colors.divider, radius: 18 })
      .responseRegion({ x: -4, y: -4, width: 44, height: 44 })
      .accessibilityText(this.label).bindTips(this.label).enabled(this.active).onClick(() => this.action()).id(this.identity)
  }
}

@ComponentV2
export struct PhoneSectionTitle {
  @Require @Param text: string;
  @Param trailing: string = '';
  build() {
    Flex({ direction: nativeEnvironment.fontScale >= 1.5 ? FlexDirection.Column : FlexDirection.Row,
      justifyContent: FlexAlign.SpaceBetween, alignItems: nativeEnvironment.fontScale >= 1.5 ? ItemAlign.Start : ItemAlign.Center }) {
      Text(this.text).fontSize(15).fontWeight(FontWeight.Medium).fontColor(Colors.secondary).flexShrink(1)
      if (this.trailing) Text(this.trailing).fontSize(13).fontColor(Colors.secondary)
    }.width('100%').padding({ left: 6, right: 6, top: 14, bottom: 7 })
  }
}

@ComponentV2
export struct PhoneSettingsRow {
  @Require @Param label: string;
  @Require @Param symbol: Resource;
  @Require @Param tint: ResourceColor;
  @Param identity: string = '';
  @Param detail: string = '';
  @Param last: boolean = false;
  @Event action: () => void = () => {};
  build() {
    Column() {
      Row({ space: 12 }) {
        SymbolGlyph(this.symbol).fontSize('20vp').fontColor([this.tint]).width(27).height(28)
        if (nativeEnvironment.fontScale >= 1.5 && this.detail) {
          Column({ space: 3 }) {
            Text(this.label).fontSize(Tokens.body).fontColor(Colors.ink)
            Text(this.detail).fontSize(14).fontColor(Colors.secondary)
          }.alignItems(HorizontalAlign.Start).layoutWeight(1)
        } else {
          Text(this.label).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1).maxLines(nativeEnvironment.fontScale >= 1.5 ? 4 : 2)
          if (this.detail) Text(this.detail).fontSize(14).fontColor(Colors.secondary).maxLines(1)
        }
        SymbolGlyph($r('sys.symbol.chevron_right')).fontSize('14vp').fontColor([Colors.secondary])
      }.width('100%').constraintSize({ minHeight: PhoneStyle.rowHeight }).padding({ left: 14, right: 14, top: 5, bottom: 5 })
      if (!this.last) Divider().color(Colors.divider).strokeWidth(0.5).margin({ left: 54 })
    }.width('100%').backgroundColor(Colors.surface).accessibilityGroup(true)
      .accessibilityText(this.label + (this.detail ? ' · ' + this.detail : '')).onClick(() => this.action()).id(this.identity)
  }
}
