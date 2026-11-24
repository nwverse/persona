import { CapsuleSegmentButtonV2, ColorMetrics, LengthMetrics, SegmentButtonV2ItemOptions, SegmentButtonV2Items } from '@kit.ArkUI';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

const systems: string[] = ['Mac', 'Linux', 'Windows'];

@ComponentV2
export struct AddComputerFields {
  @Require @Param name: string;
  @Require @Param system: string;
  @Event onName: (name: string) => void = () => {};
  @Event onSystem: (system: string) => void = () => {};
  @Local environment: NativeEnvironment = nativeEnvironment;
  private items: SegmentButtonV2Items = new SegmentButtonV2Items(systems.map((label: string): SegmentButtonV2ItemOptions => ({ text: label })));

  @Computed get stacked(): boolean {
    const width = this.getUIContext().px2vp(this.environment.windowWidthPx);
    return this.environment.fontScale >= 1.5 || (width > 0 && width / this.environment.fontScale < Tokens.compactPane);
  }

  @Builder label(key: string) {
    Text(tr(key)).fontSize(Tokens.body).fontColor(Colors.ink)
      .width(this.stacked ? undefined : Tokens.collapsed).flexShrink(0).id('machines.add.label.' + key)
  }

  @Builder systemChoices() {
    if (this.stacked) {
      Column() {
        ForEach(systems, (label: string) => {
          Row({ space: Tokens.gap }) {
            Text(label).fontSize(Tokens.body).fontColor(Colors.ink).layoutWeight(1)
            Radio({ value: label, group: 'machines.add.system' }).checked(this.system === label.toLowerCase())
              .onChange((checked: boolean) => { if (checked) this.onSystem(label.toLowerCase()); })
          }.width('100%').constraintSize({ minHeight: Tokens.touch }).padding({ left: Tokens.page, right: Tokens.inset })
            .accessibilityGroup(true).accessibilityText(label).accessibilityRole(AccessibilityRoleType.RADIO)
            .accessibilityChecked(this.system === label.toLowerCase())
            .onClick(() => this.onSystem(label.toLowerCase())).id('machines.add.os.' + label.toLowerCase())
        }, (label: string) => label)
      }.width('100%').backgroundColor(Colors.control).borderRadius(Tokens.card)
    } else {
      CapsuleSegmentButtonV2({ items: this.items,
        selectedIndex: systems.findIndex((label: string) => label.toLowerCase() === this.system),
        $selectedIndex: (index: number) => { if (systems[index]) this.onSystem(systems[index].toLowerCase()); },
        buttonBackgroundColor: ColorMetrics.resourceColor(Colors.control), itemFontColor: ColorMetrics.resourceColor(Colors.ink),
        itemSelectedBackgroundColor: ColorMetrics.resourceColor(Colors.action), itemSelectedFontColor: ColorMetrics.resourceColor(Colors.onAction),
        itemFontSize: LengthMetrics.fp(Tokens.meta), itemSelectedFontSize: LengthMetrics.fp(Tokens.meta),
        itemMinHeight: LengthMetrics.vp(Tokens.touch), buttonPadding: LengthMetrics.vp(Tokens.fine)
      }).width('100%')
    }
  }

  build() {
    Column({ space: Tokens.page }) {
      Flex({ direction: this.stacked ? FlexDirection.Column : FlexDirection.Row, alignItems: this.stacked ? ItemAlign.Start : ItemAlign.Center,
        space: { main: LengthMetrics.vp(Tokens.gap) } }) {
        this.label('name')
        TextInput({ text: this.name, placeholder: tr('machine_name_hint') }).fontSize(Tokens.body)
          .width(this.stacked ? '100%' : undefined).layoutWeight(this.stacked ? 0 : 1).constraintSize({ minHeight: Tokens.touch })
          .onChange((value: string) => this.onName(value)).id('machines.add.name')
      }.width('100%')
      Flex({ direction: this.stacked ? FlexDirection.Column : FlexDirection.Row, alignItems: this.stacked ? ItemAlign.Start : ItemAlign.Center,
        space: { main: LengthMetrics.vp(Tokens.gap) } }) {
        this.label('platform')
        Column() { this.systemChoices() }
          .width(this.stacked ? '100%' : undefined).layoutWeight(this.stacked ? 0 : 1).id('machines.add.system')
      }.width('100%')
    }.width('100%')
  }
}
