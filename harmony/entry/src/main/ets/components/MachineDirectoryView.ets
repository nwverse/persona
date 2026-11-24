import { LengthMetrics } from '@kit.ArkUI';
import { ButtonStyles } from '../design/Controls';
import { Colors } from '../design/Colors';
import { DeviceMark } from '../design/DeviceMark';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { MachineCard, MachineReading, MachineReadings } from '../models/MachineDirectory';
import { ResourceRow } from '../models/Wire';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';

function rate(value: number): string {
  if (value < 1000) return Math.round(value) + ' B/s';
  if (value < 1000000) return (value / 1000).toFixed(1) + ' kB/s';
  if (value < 1000000000) return (value / 1000000).toFixed(1) + ' MB/s';
  return (value / 1000000000).toFixed(1) + ' GB/s';
}

@ComponentV2
export struct MachineFactsCard {
  @Require @Param card: MachineCard;
  @Param sample: MachineReading | undefined = undefined;
  @Param scopeLabel: string = '';
  @Param disclose: boolean = false;
  @Param showAgents: boolean = true;
  @Local environment: NativeEnvironment = nativeEnvironment;

  private metricText(key: string): string {
    const value = key === 'cpu' ? this.sample?.cpu : key === 'machine_memory' ? this.sample?.memory :
      key === 'machine_rx' ? this.sample?.rx : this.sample?.tx;
    const label = key === 'cpu' ? 'CPU' : tr(key);
    if (value === undefined || value < 0) return label + ' —';
    return label + ' ' + (key === 'machine_rx' || key === 'machine_tx' ? rate(value) : Math.round(value) + '%');
  }
  @Builder metric(key: string) {
    Text(this.metricText(key))
      .fontSize(Tokens.meta).fontColor(Colors.secondary).width(this.environment.fontScale >= 1.5 ? '100%' : '49%')
  }

  build() {
    Column({ space: Tokens.inset }) {
      Row({ space: Tokens.inset }) {
        DeviceMark({ family: this.card.platform.family, fallback: Symbols.machine, markSize: Tokens.markdownHeading2 })
          .id('machines.os.' + (this.card.platform.family || 'unknown'))
        Column({ space: Tokens.fine }) {
          Text(this.card.row.title || this.card.row.id).fontSize(Tokens.title).fontWeight(FontWeight.Medium)
            .fontColor(Colors.ink).maxLines(2).textOverflow({ overflow: TextOverflow.Ellipsis })
          if (this.card.platform.detail || this.scopeLabel) Text([this.card.platform.detail, this.scopeLabel].filter((v: string) => !!v).join(' · '))
            .fontSize(Tokens.meta).fontColor(Colors.secondary)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)
        if (this.disclose) SymbolGlyph(Symbols.expand).fontSize(Tokens.symbolFont).fontColor([Colors.secondary]).accessibilityLevel('no')
      }.width('100%')
      Flex({ wrap: FlexWrap.Wrap, alignItems: ItemAlign.Center, space: { main: LengthMetrics.vp(Tokens.inset), cross: LengthMetrics.vp(Tokens.fine) } }) {
        Text(tr(this.card.pending ? 'machine_pending' : this.card.online ? 'machine_online' : 'offline')).fontSize(Tokens.meta)
          .fontWeight(FontWeight.Medium).fontColor(this.card.pending ? Colors.working : this.card.online ? Colors.positive : Colors.secondary)
        if (this.showAgents && this.card.online && !this.card.pending) {
          Text(tr('machine_agent_count').replace('{count}', String(this.card.agents.length))).fontSize(Tokens.meta).fontColor(Colors.secondary)
          if (this.card.needs) Text(tr('needs_you') + ' ' + this.card.needs).fontSize(Tokens.meta).fontColor(Colors.needs)
          if (this.card.working) Text(tr('working') + ' ' + this.card.working).fontSize(Tokens.meta).fontColor(Colors.secondary)
        }
      }.width('100%')
      if (this.card.online && !this.card.pending) {
        Flex({ wrap: FlexWrap.Wrap, justifyContent: FlexAlign.SpaceBetween, space: { cross: LengthMetrics.vp(Tokens.fine) } }) {
          this.metric('cpu')
          this.metric('machine_memory')
          this.metric('machine_rx')
          this.metric('machine_tx')
        }.width('100%').id('machines.metrics.' + this.card.row.id)
      }
    }.width('100%').padding(Tokens.page).alignItems(HorizontalAlign.Start).backgroundColor(Colors.canvas).borderRadius(Tokens.card)
  }
}

@ComponentV2
export struct MachineLoadingCard {
  @Local revealed: boolean = false;
  private timer: number = -1;
  aboutToAppear(): void { this.timer = setTimeout(() => this.revealed = true, 180); }
  aboutToDisappear(): void { clearTimeout(this.timer); }

  @Builder bar(width: Dimension, height: number) {
    Row().width(width).height(height).borderRadius(Tokens.inset).backgroundColor(Colors.control)
  }
  build() {
    Column({ space: Tokens.page }) {
      Row({ space: Tokens.inset }) {
        this.bar(Tokens.avatar, Tokens.avatar)
        Column({ space: Tokens.gap }) {
          this.bar('54%', Tokens.inset)
          this.bar('33%', Tokens.gap)
        }.layoutWeight(1).alignItems(HorizontalAlign.Start)
      }.width('100%')
      this.bar('38%', Tokens.gap)
    }.width('100%').alignItems(HorizontalAlign.Start).padding(Tokens.page)
      .backgroundColor(Colors.canvas).borderRadius(Tokens.card)
      .visibility(this.revealed ? Visibility.Visible : Visibility.Hidden).accessibilityLevel('no-hide-descendants')
  }
}

@ComponentV2
export struct MachineRefresh {
  @Require @Param controlId: string;
  @Require @Param available: boolean;
  @Param loading: boolean = false;
  @BuilderParam content: () => void;
  @Event onRefresh: () => Promise<void> = async () => {};
  @Local refreshing: boolean = false;
  private refreshTask?: Promise<void>;

  private async reload(): Promise<void> {
    if (this.refreshTask) return;
    if (!this.available || this.loading) { this.refreshing = false; return; }
    this.refreshing = true;
    const task = this.onRefresh();
    this.refreshTask = task;
    try { await task; } finally { this.refreshTask = undefined; this.refreshing = false; }
  }

  build() {
    Refresh({ refreshing: $$this.refreshing }) { this.content() }
      .width('100%').height('100%').backgroundColor(Colors.grouped).refreshOffset(Tokens.tabHeight)
      .pullToRefresh(this.available).onRefreshing(() => { this.reload(); }).id(this.controlId)
      .accessibilityCustomActions([{ name: tr('refresh'), onAction: () => { this.reload(); } }])
  }
}

@ComponentV2
export struct MachineDirectoryView {
  @Require @Param model: ClientModel;
  @Require @Param cards: MachineCard[];
  @Require @Param readings: MachineReadings;
  @Param loading: boolean = false;
  @Param error: string = '';
  @Param addVisible: boolean = false;
  @Param addEnabled: boolean = false;
  @Param receiptAvailable: boolean = false;
  @Event onReceipt: () => void = () => {};
  @Event onMachine: (row: ResourceRow) => void = () => {};
  @Event onAdd: () => void = () => {};
  @Event onRefresh: () => Promise<void> = async () => {};
  @Computed get multipleScopes(): boolean {
    return new Set<string>(this.cards.map((c: MachineCard) => JSON.stringify([c.row.group, c.row.manager]))).size > 1;
  }

  @Computed get initialLoad(): boolean {
    return !this.model.directoryReady && this.loading && !this.cards.length;
  }

  @Builder roster() {
    List({ space: Tokens.inset }) {
      ListItem() {
        Text(this.initialLoad ? tr('machine_loading_list') : !this.cards.length && this.error ? tr('machines') :
          tr('machine_overview').replace('{total}', String(this.cards.length)).replace('{online}', String(this.cards.filter((item: MachineCard) => item.online).length)))
          .fontSize(Tokens.body).fontColor(Colors.ink).fontWeight(FontWeight.Medium).width('100%')
          .constraintSize({ minHeight: Tokens.chrome }).id('machines.summary')
      }
      if (this.addVisible) {
        ListItem() {
          Button(tr('add_computer')).attributeModifier(ButtonStyles.secondary).width('100%').enabled(this.addEnabled)
            .onClick(() => this.onAdd()).id('machines.add')
        }
      }
      if (this.receiptAvailable) ListItem() {
        Button(tr('machine_view_pair')).attributeModifier(ButtonStyles.quiet).onClick(() => this.onReceipt())
      }
      if (this.initialLoad) {
        ListItem() { MachineLoadingCard() }.id('machines.loading')
        ListItem() { MachineLoadingCard() }
      }
      if (!this.initialLoad && !this.error && !this.cards.length) {
        ListItem() {
          Column({ space: Tokens.gap }) {
            Text(tr('machine_empty')).fontSize(Tokens.title).fontColor(Colors.ink).fontWeight(FontWeight.Medium)
            Text(tr(this.addVisible ? 'machine_empty_add' : 'machine_empty_admin')).fontSize(Tokens.meta)
              .textAlign(TextAlign.Center).fontColor(Colors.secondary)
          }.width('100%').padding(Tokens.section).backgroundColor(Colors.canvas).borderRadius(Tokens.card).id('machines.empty')
        }
      }
      if (this.error) ListItem() { Text(tr(this.error)).fontSize(Tokens.meta).fontColor(Colors.danger).padding(Tokens.inset) }
      if (this.model.directoryUnavailable) ListItem() { Text(tr('machine_partial')).fontSize(Tokens.meta).fontColor(Colors.secondary) }
      Repeat<MachineCard>(this.cards).each((item: RepeatItem<MachineCard>) => {
        ListItem() {
          MachineFactsCard({ card: item.item, sample: this.readings.sample(this.model, item.item.row), disclose: true,
            scopeLabel: this.multipleScopes ? [this.model.groupName(item.item.row), item.item.row.manager].filter((s: string) => !!s).join(' · ') : '' })
        }.onClick(() => this.onMachine(item.item.row)).accessibilityGroup(true).id('machines.card.' + item.item.row.id)
      }).key((item: MachineCard) => item.row.key()).virtualScroll({ totalCount: this.cards.length, reusable: false })
    }.height('100%').width('100%').padding({ left: Tokens.page, right: Tokens.page, top: Tokens.gap, bottom: Tokens.page })
      .edgeEffect(EdgeEffect.None).backgroundColor(Colors.grouped).id('machines.list')
  }

  build() {
    MachineRefresh({ controlId: 'machines.pull', available: this.model.connected && !this.initialLoad, loading: this.loading,
      onRefresh: () => this.onRefresh() }) {
      this.roster()
    }
  }
}
