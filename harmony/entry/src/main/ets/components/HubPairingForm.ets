import { ButtonStyles, ScrollStyles } from '../design/Controls';
import { Colors } from '../design/Colors';
import { tr } from '../design/Strings';
import { Tokens } from '../design/Tokens';

@ComponentV2
export struct HubPairingForm {
  @Require @Param mode: string;
  @Param address: string = '';
  @Param code: string = '';
  @Param error: string = '';
  @Param busy: boolean = false;
  @Param targetName: string = '';
  @Param targetAddress: string = '';
  @Param alreadyPaired: boolean = false;
  @Event onMode: (mode: string) => void = () => {};
  @Event onAddress: (value: string) => void = () => {};
  @Event onCode: (value: string) => void = () => {};
  @Event onScan: (photo: boolean) => void = () => {};
  @Event onVerify: () => void = () => {};
  @Event onTrust: () => void = () => {};
  @Event onCancel: () => void = () => {};

  @Builder heading(key: string) {
    Text(tr(key)).fontSize(Tokens.markdownHeading3).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
      .width('100%').textAlign(TextAlign.Center).id('pair.heading')
  }

  @Builder note(key: string) {
    Text(tr(key)).fontSize(Tokens.body).fontColor(Colors.secondary).textAlign(TextAlign.Center).width('100%')
  }

  build() {
    Scroll() {
      Column({ space: Tokens.page }) {
        if (this.targetAddress) {
          this.heading('hub_confirm_title')
          Column({ space: Tokens.fine }) {
            Text(this.targetName).fontSize(Tokens.title).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
              .id('pair.target-name')
            Text(this.targetAddress).fontSize(Tokens.meta).fontColor(Colors.secondary).wordBreak(WordBreak.BREAK_ALL)
          }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.page).backgroundColor(Colors.control).borderRadius(Tokens.card)
          this.note(this.alreadyPaired ? 'hub_refresh_pairing' : 'hub_confirm_hint')
          Row({ space: Tokens.inset }) {
            Button(tr('cancel')).attributeModifier(ButtonStyles.secondary).layoutWeight(1).enabled(!this.busy).onClick(() => this.onCancel())
            Button(tr('trust')).attributeModifier(ButtonStyles.primary).layoutWeight(1).enabled(!this.busy).onClick(() => this.onTrust()).id('pair.trust')
          }.width('100%')
        } else if (this.mode === 'guide') {
          this.heading('hub_get_code')
          Column({ space: Tokens.gap }) {
            Text(tr('hub_code_computer')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            Text(tr('hub_guide_first')).fontSize(Tokens.meta).fontColor(Colors.secondary)
            Text('personad hub pair').fontFamily('monospace').fontSize(Tokens.body).fontColor(Colors.ink)
              .copyOption(CopyOptions.LocalDevice).padding(Tokens.inset).width('100%').backgroundColor(Colors.control).borderRadius(Tokens.inset)
          }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.page).backgroundColor(Colors.canvas).borderRadius(Tokens.card)
          Column({ space: Tokens.gap }) {
            Text(tr('hub_code_device')).fontSize(Tokens.body).fontWeight(FontWeight.Medium).fontColor(Colors.ink)
            Text(tr('hub_guide_existing')).fontSize(Tokens.meta).fontColor(Colors.secondary)
          }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.page).backgroundColor(Colors.canvas).borderRadius(Tokens.card)
          Button(tr('hub_have_link')).attributeModifier(ButtonStyles.primary).width('100%').onClick(() => this.onMode('link'))
        } else if (this.mode === 'scan') {
          SymbolGlyph($r('sys.symbol.qrcode')).fontSize(Tokens.avatarChoice).fontColor([Colors.link]).accessibilityLevel('no')
          this.heading('hub_scan_connect')
          this.note('hub_where_code')
          Button(tr('hub_open_camera')).attributeModifier(ButtonStyles.primary).width('100%')
            .enabled(!this.busy).onClick(() => this.onScan(false)).id('pair.scan')
          Button(tr('hub_choose_photo')).attributeModifier(ButtonStyles.secondary).width('100%')
            .enabled(!this.busy).onClick(() => this.onScan(true)).id('pair.photo')
          Button(tr('hub_other_ways')).attributeModifier(ButtonStyles.quiet).enabled(!this.busy)
            .onClick(() => this.onMode('link')).id('pair.other')
        } else {
          if (this.mode === 'manual') {
            this.heading('hub_manual')
            this.note('hub_manual_hint')
            Column({ space: Tokens.gap }) {
              Text(tr('hub_address')).fontSize(Tokens.meta).fontColor(Colors.secondary)
              TextInput({ text: this.address, placeholder: tr('hub_address') }).type(InputType.URL)
                .onChange((v: string) => this.onAddress(v)).enabled(!this.busy).id('pair.address').width('100%')
              Text(tr('pair_code')).fontSize(Tokens.meta).fontColor(Colors.secondary)
              TextInput({ text: this.code, placeholder: tr('pair_code') }).type(InputType.Password).showPasswordIcon(false)
                .enableAutoFill(false).onChange((v: string) => this.onCode(v)).enabled(!this.busy).id('pair.code').width('100%')
            }.width('100%').alignItems(HorizontalAlign.Start)
          } else {
            this.heading('hub_paste_link')
            this.note('hub_link_hint')
            Column({ space: Tokens.gap }) {
              Text(tr('hub_pairing_link')).fontSize(Tokens.meta).fontColor(Colors.secondary)
              TextInput({ text: this.address, placeholder: tr('hub_pairing_link') }).type(InputType.Password).showPasswordIcon(false)
                .enableAutoFill(false).onChange((v: string) => this.onAddress(v)).enabled(!this.busy).id('pair.address').width('100%')
            }.width('100%').alignItems(HorizontalAlign.Start)
          }
          Button(tr('verify')).attributeModifier(ButtonStyles.primary).width('100%')
            .enabled(!this.busy && !!this.address.trim() && (this.mode !== 'manual' || !!this.code.trim()))
            .onClick(() => this.onVerify()).id('pair.verify')
          Button(tr(this.mode === 'manual' ? 'hub_paste_link' : 'hub_manual')).attributeModifier(ButtonStyles.secondary)
            .width('100%').enabled(!this.busy).onClick(() => this.onMode(this.mode === 'manual' ? 'link' : 'manual')).id('pair.switch-method')
          if (canIUse('SystemCapability.Multimedia.Scan.ScanBarcode')) {
            Button(tr('hub_scan_connect')).attributeModifier(ButtonStyles.quiet).enabled(!this.busy).onClick(() => this.onMode('scan'))
          }
        }
        if (this.busy) LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.link)
        if (this.error) Text(tr(this.error)).fontSize(Tokens.meta).fontColor(Colors.danger).textAlign(TextAlign.Center).id('pair.error')
      }.width('100%').constraintSize({ maxWidth: Tokens.masterMax }).padding(Tokens.section)
    }.width('100%').height('100%').attributeModifier(ScrollStyles.form).id('pair.content')
  }
}
