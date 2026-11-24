import { CapsuleSegmentButtonV2, ColorMetrics, LengthMetrics, SegmentButtonV2Items } from '@kit.ArkUI';
import { i18n } from '@kit.LocalizationKit';
import { DeviceMark } from '../design/DeviceMark';
import { machinePlatform, platformName } from '../models/DeviceIdentity';
import { ResourceRow } from '../models/Wire';
import { Colors } from '../design/Colors';
import { ButtonStyles, SelectStyles } from '../design/Controls';
import { tr } from '../design/Strings';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { ClientModel } from '../models/ClientModel';
import { BoundRow } from '../models/RowBinding';
import { SignedInDevice, SignedInDevices } from '../models/SignedInDevices';
import { BrowserLogin } from './BrowserLogin';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

const searchThreshold = 10;

@ComponentV2
export struct SignedInDevicesPage {
  @Require @Param model: ClientModel;
  @Param active: boolean = true;
  @Local directory: SignedInDevices = new SignedInDevices();
  @Local revoked: boolean = false;
  @Local query: string = '';
  @Local environment: NativeEnvironment = nativeEnvironment;
  private listScroller: Scroller = new Scroller();
  private filterOwner?: ClientModel;
  private filterProfile: string = '';
  private mounted: boolean = false;

  @Computed get visibleRows(): BoundRow<SignedInDevice>[] {
    return this.directory.matching(this.revoked, this.query, (row: SignedInDevice): string =>
      this.kindLabel(row.kind) + ' ' + platformName(this.deviceSystem(row)) + (row.id === this.directory.currentId ? ' ' + tr('devices_this') : ''));
  }

  aboutToAppear(): void { this.mounted = true; this.updateOwner(); }
  aboutToDisappear(): void { this.mounted = false; this.directory.bind(); }
  @Monitor('active', 'model', 'model.connected', 'model.profile', 'model.demo')
  updateOwner(): void {
    if (this.filterOwner !== this.model || this.filterProfile !== this.model.profile.id) {
      this.filterOwner = this.model;
      this.filterProfile = this.model.profile.id;
      this.revoked = false; this.query = '';
    }
    this.directory.bind(this.mounted && this.active ? this.model : undefined);
  }

  private kindLabel(kind: string): string {
    if (kind === 'web') return tr('device_kind_browser');
    if (kind === 'phone' || kind === 'watch' || kind === 'link') return tr('device_kind_' + kind);
    return tr('device_kind_other');
  }

  private kindIcon(kind: string): Resource {
    if (kind === 'web') return $r('sys.symbol.compass');
    if (kind === 'phone') return Symbols.device;
    if (kind === 'watch') return $r('sys.symbol.watch');
    return Symbols.machine;
  }

  @Computed get linkedSystems(): Map<string, string> {
    const systems = new Map<string, string>();
    this.model.machines.forEach((row: ResourceRow) => systems.set(row.id, machinePlatform(row.data()).family));
    return systems;
  }

  private deviceSystem(device: SignedInDevice): string {
    if (device.browser) return device.browser.system;
    if (device.kind === 'phone' && device.id === this.directory.currentId) return 'harmonyos';
    if (device.kind === 'link' && device.machineId) return this.linkedSystems.get(device.machineId) || '';
    return '';
  }

  private subtitle(device: SignedInDevice): string {
    const system = platformName(this.deviceSystem(device));
    return [system || this.kindLabel(device.kind), this.timeLabel(device)].filter((part: string) => !!part).join(' · ');
  }

  private timeLabel(device: SignedInDevice): string {
    if (!device.activityTime) return '';
    const label = device.revoked ? 'devices_revoked_at' : device.lastSeen ? 'devices_last_used' : 'devices_added';
    const language = this.model.language === 'default' ? i18n.System.getAppPreferredLanguage() : this.model.language;
    const date = new Date(device.activityTime), today = new Date();
    const time = date.toDateString() === today.toDateString()
      ? date.toLocaleTimeString(language || undefined, { hour: '2-digit', minute: '2-digit' })
      : date.toLocaleDateString(language || undefined, { year: date.getFullYear() === today.getFullYear() ? undefined : 'numeric', month: 'short', day: 'numeric' });
    return tr(label) + ' ' + time;
  }

  private groupLabel(revoked: boolean): string {
    const name = tr(revoked ? 'devices_revoked' : 'devices_signed_in');
    if (!this.directory.rows.length && (this.directory.error || this.directory.loading)) return name;
    const count = revoked ? this.directory.revokedRows.length : this.directory.signedInRows.length;
    return name + ' (' + String(count) + ')';
  }

  private selectGroup(index: number): void {
    if (this.revoked === (index === 1)) return;
    this.revoked = index === 1;
    this.resetListPosition();
  }

  private resetListPosition(): void {
    if (this.listScroller.currentOffset().yOffset > 0) {
      this.listScroller.scrollTo({ xOffset: 0, yOffset: 0, animation: false });
    }
  }

  private confirmRevoke(device: SignedInDevice): void {
    const revoke = this.directory.prepareRevoke(device);
    if (!revoke) return;
    const self = device.id === this.directory.currentId;
    const name = device.name || this.kindLabel(device.kind);
    this.getUIContext().showAlertDialog({
      title: tr('devices_revoke') + ' · ' + name,
      message: self ? tr('devices_revoke_self') + '\n' + tr('forget_hub_notice') : tr('devices_revoke_note'),
      primaryButton: { value: tr('cancel'), action: () => {} },
      secondaryButton: { value: tr(self ? 'devices_revoke_unpair' : 'devices_revoke'), fontColor: Colors.danger, action: () => { revoke(); } }
    });
  }

  @Builder credential(binding: BoundRow<SignedInDevice>) {
    Row({ space: Tokens.inset }) {
      DeviceMark({
        family: binding.value.browser ? binding.value.browser.family : this.deviceSystem(binding.value),
        fallback: this.kindIcon(binding.value.kind), tint: binding.value.revoked ? Colors.secondary : Colors.link
      }).id('devices.mark.' + binding.value.id)
      Column({ space: Tokens.fine }) {
        Row({ space: Tokens.gap }) {
          Text(binding.value.browser?.title || binding.value.name || this.kindLabel(binding.value.kind)).fontSize(Tokens.body)
            .fontWeight(binding.value.revoked ? FontWeight.Regular : FontWeight.Medium)
            .fontColor(binding.value.revoked ? Colors.secondary : Colors.ink)
            .maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis }).flexShrink(1)
          if (binding.value.id === this.directory.currentId) {
            Text(tr('devices_this')).fontSize(Tokens.caption).fontColor(Colors.link)
              .padding({ left: Tokens.gap, right: Tokens.gap }).backgroundColor(Colors.selection)
              .borderRadius(Tokens.capsule).flexShrink(0)
          }
        }.width('100%')
        Row({ space: Tokens.fine }) {
          if (binding.value.browser?.system) {
            DeviceMark({ family: binding.value.browser.system, markSize: Tokens.meta, tint: Colors.secondary })
              .id('devices.os.' + binding.value.id)
          }
          Text(this.subtitle(binding.value)).fontSize(Tokens.meta).fontColor(Colors.secondary).layoutWeight(1)
        }.width('100%')
      }.alignItems(HorizontalAlign.Start).layoutWeight(1)
      if (!binding.value.revoked) {
        Button(tr('devices_revoke'), { buttonStyle: ButtonStyleMode.TEXTUAL })
          .fontColor(Colors.danger).constraintSize({ minHeight: Tokens.touch })
          .enabled(!this.directory.revoking && !this.directory.loading && !this.directory.error)
          .accessibilityText(tr('devices_revoke') + ' ' + (binding.value.name || this.kindLabel(binding.value.kind)))
          .id('devices.revoke.' + binding.value.id).onClick(() => this.confirmRevoke(binding.value))
      }
    }.width('100%').padding({ left: Tokens.page, right: Tokens.inset, top: Tokens.inset, bottom: Tokens.inset })
  }

  @Builder groupSelector() {
    Row({ space: Tokens.gap }) {
      if (this.environment.fontScale >= 1.5) {
        Select([{ value: this.groupLabel(false) }, { value: this.groupLabel(true) }])
          .attributeModifier(SelectStyles.inline).selected(this.revoked ? 1 : 0).value(this.groupLabel(this.revoked))
          .layoutWeight(1).onSelect((index: number) => this.selectGroup(index)).id('devices.groups')
      } else {
        CapsuleSegmentButtonV2({
          items: new SegmentButtonV2Items([{ text: this.groupLabel(false) }, { text: this.groupLabel(true) }]),
          selectedIndex: this.revoked ? 1 : 0, $selectedIndex: (index: number) => this.selectGroup(index),
          buttonBackgroundColor: ColorMetrics.resourceColor(Colors.control),
          itemSelectedBackgroundColor: ColorMetrics.resourceColor(Colors.canvas),
          itemSelectedFontColor: ColorMetrics.resourceColor(Colors.ink), itemFontColor: ColorMetrics.resourceColor(Colors.secondary),
          itemFontSize: LengthMetrics.fp(Tokens.meta), itemSelectedFontSize: LengthMetrics.fp(Tokens.meta),
          itemMinHeight: LengthMetrics.vp(Tokens.touch), buttonPadding: LengthMetrics.vp(Tokens.fine)
        }).layoutWeight(1).id('devices.groups')
      }
      Button({ type: ButtonType.Circle }) {
        if (this.directory.loading) {
          LoadingProgress().width(Tokens.symbol).height(Tokens.symbol).color(Colors.secondary)
        } else {
          SymbolGlyph(Symbols.refresh).fontSize(Tokens.symbolFont).fontColor([Colors.secondary])
        }
      }.attributeModifier(ButtonStyles.chrome).accessibilityText(tr('refresh')).bindTips(tr('refresh')).id('devices.refresh')
        .enabled(this.active && this.model.connected && !this.model.demo && !this.directory.revoking && !this.directory.loading)
        .onClick(() => { this.directory.refresh(); })
    }.width('100%')
  }

  build() {
    Column({ space: Tokens.inset }) {
      if (canIUse('SystemCapability.Multimedia.Scan.ScanBarcode')) {
        BrowserLogin({ model: this.model, active: this.active, onSignedIn: () => { this.directory.refresh(); } })
      }
      this.groupSelector()
      if (this.directory.rows.length >= searchThreshold || this.query) {
        Search({ value: this.query, placeholder: tr('devices_search') }).width('100%').id('devices.search')
          .onChange((value: string) => { this.query = value; this.resetListPosition(); })
      }
      List({ scroller: this.listScroller }) {
        if (this.directory.error || (!this.directory.loading && !this.visibleRows.length)) {
          ListItem() {
            Text(tr(this.directory.error || (this.query.trim() ? 'devices_no_match' : this.revoked ? 'devices_no_history' : 'devices_empty')))
              .fontSize(Tokens.meta).fontColor(Colors.secondary).padding(Tokens.page)
          }.backgroundColor(Colors.canvas).borderRadius(Tokens.card)
        }
        Repeat<BoundRow<SignedInDevice>>(this.visibleRows).each((row: RepeatItem<BoundRow<SignedInDevice>>) => {
          ListItem() { this.credential(row.item) }.backgroundColor(Colors.canvas)
            .borderRadius({
              topLeft: row.index === 0 ? Tokens.card : 0, topRight: row.index === 0 ? Tokens.card : 0,
              bottomLeft: row.index === this.visibleRows.length - 1 ? Tokens.card : 0,
              bottomRight: row.index === this.visibleRows.length - 1 ? Tokens.card : 0
            })
        }).key((row: BoundRow<SignedInDevice>) => row.key).virtualScroll({ totalCount: this.visibleRows.length, reusable: false })
      }.layoutWeight(1).width('100%').scrollBar(BarState.Auto)
        .divider({ strokeWidth: Tokens.divider, startMargin: Tokens.page + Tokens.symbol + Tokens.inset, endMargin: Tokens.page, color: Colors.divider })
        .id('devices.list')
    }.width('100%').height('100%').backgroundColor(Colors.grouped)
      .padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.listTail })
  }
}
