import { BusinessError } from '@kit.BasicServicesKit';
import { scanBarcode, scanCore } from '@kit.ScanKit';
import { i18n } from '@kit.LocalizationKit';
import { BrowserLoginResult } from '../models/BrowserLoginResult';
import { ClientModel } from '../models/ClientModel';
import { browserIdentity, platformName } from '../models/DeviceIdentity';
import { ButtonStyles, ScrollStyles } from '../design/Controls';
import { Colors } from '../design/Colors';
import { DeviceMark } from '../design/DeviceMark';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { nativeEnvironment } from '../platform/NativeEnvironment';
import { nativeSheetInset } from '../platform/NativeInsets';

@ComponentV2
export struct BrowserLogin {
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Event onSignedIn: () => void = () => {};
  @Local scanning: boolean = false;
  @Local sheetOpen: boolean = false;
  @Local result?: BrowserLoginResult;
  @Local bottomInset: number = 0;
  private sheetReady: boolean = false;
  private scanRevision: number = 0;
  private owner?: ClientModel;
  private scope: string = '';
  private pairing: number = -1;

  aboutToDisappear(): void { this.stopScan(); this.closeSheet(); }
  @Monitor('active', 'model', 'model.connected', 'model.profile.id', 'model.profile.manager')
  ownerChanged(): void {
    // Scan Kit hides the destination and the existing Hub may reconnect before returning.
    if (this.scanning && !this.ownsScan(this.scanRevision)) this.stopScan();
    if (!this.result || !this.sheetOpen) return;
    if (!this.active || !this.result.samePairing(this.model)) this.closeSheet();
    else if (!this.result.connected()) this.result.unavailable();
  }

  private ownsScan(revision: number): boolean {
    return this.scanning && revision === this.scanRevision && this.owner === this.model &&
      this.scope === this.model.scope() && this.pairing === this.model.pairingRevision();
  }

  private stopScan(): void {
    this.scanRevision++;
    this.scanning = false;
    this.owner = undefined;
  }

  private closeSheet(): void {
    this.result?.dismiss();
    this.sheetOpen = false;
  }

  private showResult(): BrowserLoginResult {
    const result = new BrowserLoginResult(this.model, () => this.onSignedIn());
    this.result = result;
    this.sheetOpen = true;
    return result;
  }

  private supportsScan(): boolean {
    return !this.model.demo && this.model.features.includes('browser_qr_match');
  }

  private async scan(): Promise<void> {
    const context = this.getUIContext().getHostContext();
    if (!context || !this.active || this.scanning || this.sheetOpen || !this.supportsScan() || !this.model.connected) return;
    const revision = ++this.scanRevision;
    this.scanning = true;
    this.owner = this.model;
    this.scope = this.model.scope();
    this.pairing = this.model.pairingRevision();
    try {
      const code = await scanBarcode.startScanForResult(context, {
        scanTypes: [scanCore.ScanType.QR_CODE], enableAlbum: true, enableMultiMode: false
      });
      for (let attempt = 0; attempt < 50; attempt++) {
        await new Promise<void>((resolve: () => void) => setTimeout(resolve, 200));
        if (!this.ownsScan(revision)) return;
        if (this.active && this.model.connected) {
          this.showResult().load(code.originalValue);
          return;
        }
      }
      if (this.active) this.showResult().unavailable();
    } catch (error) {
      if (this.ownsScan(revision) && this.active && (error as BusinessError).code !== 1000500002) {
        this.showResult().unavailable();
      }
    } finally {
      if (revision === this.scanRevision) this.stopScan();
    }
  }

  private measureSheet(): void {
    if (!this.sheetReady) return;
    const inset = nativeSheetInset(this.getUIContext(), 'browser.login.sheet', nativeEnvironment.safeBottomPx);
    if (Math.abs(inset - this.bottomInset) > 0.1) this.bottomInset = inset;
  }

  private provenance(result: BrowserLoginResult): string {
    const network = tr('browser_qr_' + result.network);
    if (!result.requestedAt) return network;
    const language = this.model.language === 'default' ? i18n.System.getAppPreferredLanguage() : this.model.language;
    const time = new Date(result.requestedAt).toLocaleTimeString(language || undefined, { hour: '2-digit', minute: '2-digit' });
    return network + ' · ' + time;
  }

  @Builder heading(label: string) {
    Text(label).fontSize(Tokens.markdownHeading3).fontWeight(FontWeight.Bold).fontColor(Colors.ink)
      .textAlign(TextAlign.Center).width('100%').accessibilityRole(AccessibilityRoleType.TITLE_BAR).id('browser.login.heading')
  }

  @Builder note(label: string) {
    Text(label).fontSize(Tokens.body).fontColor(Colors.secondary).textAlign(TextAlign.Center).width('100%')
  }

  @Builder browserRow(result: BrowserLoginResult) {
    Row({ space: Tokens.inset }) {
      DeviceMark({ family: browserIdentity(result.name).family, fallback: $r('sys.symbol.compass'), tint: Colors.link, markSize: Tokens.markdownHeading1 })
      Column({ space: Tokens.fine }) {
        Text(browserIdentity(result.name).title || tr('device_kind_browser')).fontSize(Tokens.body)
          .fontColor(Colors.ink).fontWeight(FontWeight.Medium).id('browser.login.name')
        Text([platformName(browserIdentity(result.name).system), this.provenance(result)].filter((s: string) => !!s).join(' · '))
          .fontSize(Tokens.meta).fontColor(Colors.secondary)
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)
    }.width('100%').padding(Tokens.page).backgroundColor(Colors.control).borderRadius(Tokens.card).accessibilityGroup(true)
  }

  @Builder resultContent(result: BrowserLoginResult) {
    Column({ space: Tokens.page }) {
      if (result.phase === 'checking') {
        LoadingProgress().width(Tokens.avatar).height(Tokens.avatar).color(Colors.link)
        this.heading(tr('browser_qr_checking'))
      } else if (result.phase === 'ask') {
        this.heading(tr('browser_qr_ask'))
        this.browserRow(result)
        this.note(tr(result.network === 'internet' ? 'browser_qr_remote_access' : 'browser_qr_access'))
      } else if (result.phase === 'number') {
        this.heading(tr('browser_qr_enter'))
        Text(result.number).fontSize(Tokens.signInNumber).fontFamily('monospace').fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink).maxLines(1).accessibilityText(result.number.split('').join(' ')).id('browser.login.number')
        this.note(tr('browser_qr_number'))
        Row({ space: Tokens.gap }) {
          LoadingProgress().width(Tokens.meta).height(Tokens.meta).color(Colors.secondary).accessibilityLevel('no')
          Text(tr('browser_qr_waiting')).fontSize(Tokens.meta).fontColor(Colors.secondary)
        }
      } else {
        SymbolGlyph(result.phase === 'success' ? $r('sys.symbol.checkmark_circle') : result.phase === 'denied' ? $r('sys.symbol.xmark') :
          result.phase === 'expired' ? $r('sys.symbol.clock') : $r('sys.symbol.exclamationmark_circle'))
          .fontSize(Tokens.avatarChoice).fontColor([result.phase === 'success' ? Colors.positive :
            result.phase === 'voided' || result.phase === 'failed' ? Colors.danger : Colors.secondary]).accessibilityLevel('no')
        this.heading(tr('browser_qr_' + result.phase))
        if (result.phase === 'success') {
          Text(result.name || tr('device_kind_browser')).fontSize(Tokens.body).fontColor(Colors.ink).textAlign(TextAlign.Center)
          this.note(tr('browser_qr_manage'))
        } else if (result.phase !== 'denied') {
          this.note(tr(result.phase === 'invalid' ? 'browser_qr_scan_web' : result.phase === 'other_hub' ? 'browser_qr_switch' : 'browser_qr_refresh'))
        }
      }
    }.width('100%').id('browser.login.content')
  }

  @Builder actions(result: BrowserLoginResult) {
    Row({ space: Tokens.inset }) {
      if (result.phase === 'ask') {
        Button(tr('deny')).attributeModifier(ButtonStyles.secondary).layoutWeight(1)
          .enabled(result.choice === undefined).onClick(() => { result.decide(false); }).id('browser.login.deny')
        Button(tr('allow')).attributeModifier(ButtonStyles.primary).layoutWeight(1)
          .enabled(result.choice === undefined).onClick(() => { result.decide(true); }).id('browser.login.allow')
      } else if (result.phase === 'number') {
        Button(tr('browser_qr_cancel')).attributeModifier(ButtonStyles.secondary).fontColor(Colors.danger).layoutWeight(1)
          .enabled(result.choice === undefined).onClick(() => { result.decide(false); }).id('browser.login.cancel')
      } else {
        Button(tr(result.phase === 'checking' ? 'cancel' : 'browser_qr_done')).attributeModifier(ButtonStyles.secondary).layoutWeight(1)
          .onClick(() => this.closeSheet()).id('browser.login.done')
      }
    }.width('100%').id('browser.login.actions')
  }

  @Builder resultSheet() {
    Column() {
      if (this.result) {
        Scroll() {
          Column({ space: Tokens.section }) {
            this.resultContent(this.result)
            this.actions(this.result)
          }.width('100%').padding({ top: Tokens.section, bottom: Tokens.section })
        }.layoutWeight(1).width('100%').attributeModifier(ScrollStyles.form)
      }
    }.width('100%').height('100%').constraintSize({ maxWidth: Tokens.readingMax })
      .padding({ left: Tokens.section, right: Tokens.section, bottom: this.bottomInset + Tokens.page })
      .id('browser.login.sheet').onAreaChange(() => this.measureSheet())
  }

  build() {
    Button({ type: ButtonType.Normal, buttonStyle: ButtonStyleMode.TEXTUAL }) {
      Row({ space: Tokens.inset }) {
        SymbolGlyph($r('sys.symbol.qrcode')).fontSize(Tokens.symbolFont).fontColor([Colors.link])
        Text(tr('browser_qr_scan')).fontSize(Tokens.body).fontColor(Colors.link).layoutWeight(1)
      }.width('100%').constraintSize({ minHeight: Tokens.touch })
    }.width('100%').padding({ left: Tokens.page, right: Tokens.page }).backgroundColor(Colors.canvas)
      .borderRadius(Tokens.card).enabled(this.model.connected && !this.scanning && !this.sheetOpen)
      .visibility(this.scanning || this.sheetOpen || this.supportsScan() ? Visibility.Visible : Visibility.None)
      .accessibilityText(tr('browser_qr_scan')).onClick(() => this.scan()).id('browser.login.scan')
      .bindSheet(this.sheetOpen, this.resultSheet(), { title: { title: tr('browser_qr_title') }, height: SheetSize.LARGE,
        showClose: true, onAppear: () => { this.sheetReady = true; this.measureSheet(); },
        onWillDismiss: (action: DismissSheetAction) => { this.closeSheet(); action.dismiss(); },
        onDisappear: () => { this.sheetReady = false; if (!this.sheetOpen) this.result = undefined; } })
  }
}
