import { Colors } from '../design/Colors';
import { Motion } from '../design/Motion';
import { PhoneStyle } from '../design/PhoneStyle';
import { Symbols } from '../design/Symbols';
import { Tokens } from '../design/Tokens';
import { tr } from '../design/Strings';
import { ClientModel } from '../models/ClientModel';
import { MachineReading, machineKey, machineOnline, machinePercent, osFamily } from '../models/MachineDashboard';
import { SubscriptionAccount, SubscriptionWindow, usageProviderTitle } from '../models/MachineSubscriptionUsage';
import { SessionWorkspace } from '../models/SessionWorkspace';
import { ResourceRow, Wire } from '../models/Wire';
import { sessionDisplayTitle } from '../core/SessionNaming';
import { NativeEnvironment, nativeEnvironment } from '../platform/NativeEnvironment';
import { NativeLiveView, nativeLiveView } from '../platform/NativeLiveView';
import { Haptics } from '../platform/Haptics';
import { SessionTools } from './SessionTools';
import { NeedsYouPulseDot, WorkingArcSpinner } from './StatusMotionMarks';

interface FleetPulseCounts {
  needsYou: number;
  working: number;
  unread: number;
  total: number;
}

interface MachineSessionSummary {
  needsYou: number;
  working: number;
}

/**
 * HarmonyOS foldable & tri-fold right-side 3-tab Live Workbench panel (`Pulse` | `Code` | `Machines`).
 * Automatically mounts as the 3rd column on Mate XT / Mate XT 2 tri-fold & MatePad XL displays,
 * and can be toggled on Mate X7 unfolded dual-screen displays.
 */
@ComponentV2
export struct FoldInspectorPanel {
  @Require @Param model: ClientModel;
  @Param detailModel: ClientModel | undefined = undefined;
  @Param panelWidth: number = 288;
  @Param postureLabel: string = '';
  @Param isTriFold: boolean = false;
  @Param initialTab: number = 0;
  @Param tabSignal: number = 0;
  @Param pendingInterruptKey: string = '';
  @Event onClose: () => void = () => {};
  @Event onOpenTools: () => void = () => {};
  @Event onOpenToolsTab: (tab: number) => void = (_tab: number) => {
    this.onOpenTools();
  };
  @Event onOpenSession: (row: ResourceRow) => void = () => {};
  @Event onStepRelay: (direction: 1 | -1) => void = () => {};
  @Event onInterruptSession: (row: ResourceRow) => void = () => {};
  @Event onOpenXiaoYi: () => void = () => {};
  @Event onOpenLink: (address: string) => void = () => {};
  @Local private env: NativeEnvironment = nativeEnvironment;
  @Local private liveView: NativeLiveView = nativeLiveView;
  @Local private rootWidth: number = 0;
  @Local private workbenchTab: number = 0;
  @Local private embeddedToolsTab: number = 0;
  @Local private embeddedWorkspace: SessionWorkspace | undefined = undefined;
  @Local private machineReadings: Map<string, MachineReading> = new Map();
  @Local private usageAccounts: SubscriptionAccount[] = [];
  @Local private telemetryLoading: boolean = false;
  private telemetryRevision: number = 0;

  aboutToAppear(): void {
    this.workbenchTab = Math.max(0, Math.min(2, this.initialTab));
    if (this.workbenchTab === 2) {
      this.refreshMachinesTelemetry();
    }
  }

  @Monitor('tabSignal', 'initialTab')
  externalTabChanged(): void {
    const clamped = Math.max(0, Math.min(2, this.initialTab));
    if (this.workbenchTab !== clamped) {
      this.workbenchTab = clamped;
    }
  }

  @Monitor('workbenchTab')
  workbenchTabChanged(): void {
    if (this.workbenchTab === 2) {
      this.refreshMachinesTelemetry();
    }
  }

  @Monitor('model.connected', 'model.profile')
  connectionStateChanged(): void {
    if (!this.model.profile.id) {
      this.telemetryRevision++;
      this.machineReadings = new Map();
      this.usageAccounts = [];
      return;
    }
    if (this.workbenchTab === 2 && this.model.connected) {
      this.refreshMachinesTelemetry();
    }
  }

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

  private normalizedRowState(row: ResourceRow): string {
    const data = row.data();
    const raw = row.state || Wire.text(data, 'status');
    if (Wire.bool(data, 'needs_you') || raw === 'needs_you' || raw === 'blocked') return 'needs_you';
    if (Wire.bool(data, 'working') || raw === 'working' || raw === 'running') return 'working';
    if (raw === 'done' || raw === 'unread' || Wire.bool(data, 'unread')) return 'unread';
    return 'idle';
  }

  private canInterruptRow(row: ResourceRow): boolean {
    if (this.normalizedRowState(row) !== 'working') return false;
    const owner = this.model.directoryOwnerFor(row) || this.activeOwner();
    return owner.canInterruptSession(row);
  }

  private sessionState(): string {
    const row = this.selectedSession();
    return row ? this.normalizedRowState(row) : 'idle';
  }

  private stateLabelKey(state: string): string {
    if (state === 'needs_you') return 'needs_you';
    if (state === 'working') return 'working';
    if (state === 'unread') return 'agent_group_unread';
    return 'idle';
  }

  private stateTint(state: string): ResourceColor {
    if (state === 'needs_you') return Colors.needs;
    if (state === 'working') return Colors.working;
    if (state === 'unread') return Colors.unread;
    return Colors.secondary;
  }

  private activeFleetSessions(): ResourceRow[] {
    const rows = this.model.directoryReady && this.model.directorySessions.length
      ? this.model.directorySessions
      : this.model.sessions;
    return rows.filter((row: ResourceRow) => {
      if (row.kind !== 'session') return false;
      const data = row.data();
      return !Wire.bool(data, 'archived') && !Wire.bool(data, 'assistant') && !Wire.bool(data, 'hidden_by_directory');
    });
  }

  private fleetCounts(): FleetPulseCounts {
    const rows = this.activeFleetSessions();
    if (!rows.length) {
      return {
        needsYou: this.liveView.currentState.needsYou,
        working: this.liveView.currentState.working,
        unread: this.liveView.currentState.unread,
        total: this.liveView.currentState.needsYou + this.liveView.currentState.working + this.liveView.currentState.unread
      };
    }
    let needsYou = 0;
    let working = 0;
    let unread = 0;
    for (const row of rows) {
      const state = this.normalizedRowState(row);
      if (state === 'needs_you') needsYou++;
      else if (state === 'working') working++;
      else if (state === 'unread') unread++;
    }
    return { needsYou: needsYou, working: working, unread: unread, total: rows.length };
  }

  private prioritizedSessions(): ResourceRow[] {
    const rank = (row: ResourceRow): number => {
      const state = this.normalizedRowState(row);
      if (state === 'needs_you') return 0;
      if (state === 'unread') return 1;
      if (state === 'working') return 2;
      return 3;
    };
    return this.activeFleetSessions()
      .slice()
      .sort((a: ResourceRow, b: ResourceRow) => rank(a) - rank(b))
      .slice(0, 6);
  }

  private urgentSession(): ResourceRow | undefined {
    return this.activeFleetSessions().find((row: ResourceRow) => this.normalizedRowState(row) === 'needs_you');
  }

  private displaySessionTitle(row: ResourceRow, fallback: string = tr('agent_untitled')): string {
    const preferTab = this.model.directoryOwnerFor(row)?.useHerdrTabName ?? this.activeOwner().useHerdrTabName;
    return sessionDisplayTitle(row.title, row.data(), preferTab) || fallback;
  }

  private machineRows(): ResourceRow[] {
    return this.model.directoryReady && this.model.directoryMachines.length
      ? this.model.directoryMachines
      : this.model.machines;
  }

  private machineSummary(machine: ResourceRow): MachineSessionSummary {
    let needsYou = 0;
    let working = 0;
    for (const session of this.activeFleetSessions()) {
      if (session.machine !== machine.id) continue;
      if (machine.group && session.group && session.group !== machine.group) continue;
      if (machine.manager && session.manager && session.manager !== machine.manager) continue;
      const state = this.normalizedRowState(session);
      if (state === 'needs_you') needsYou++;
      else if (state === 'working') working++;
    }
    return { needsYou: needsYou, working: working };
  }

  private async refreshMachinesTelemetry(): Promise<void> {
    const root = this.model;
    if (!root.profile.id) return;
    const rev = ++this.telemetryRevision;
    const owners = root.directoryOwners().filter((owner: ClientModel) => owner.connected);
    if (!owners.length) return;
    this.telemetryLoading = true;
    const nextReadings = new Map<string, MachineReading>(this.machineReadings);
    const nextAccounts: SubscriptionAccount[] = [];
    const now = Date.now();
    try {
      await Promise.all(owners.map(async (owner: ClientModel): Promise<void> => {
        const epoch = owner.connectionGeneration();
        const scope = owner.scope();
        try {
          const metricsResp = await owner.repo().call('/api/v1/metrics');
          if (rev === this.telemetryRevision && owner.owns(epoch, scope) && Array.isArray(metricsResp['machines'])) {
            for (const info of Wire.rows(metricsResp, 'machines')) {
              const id = Wire.text(info, 'machine_id');
              if (id) {
                nextReadings.set(machineKey(owner.profile.id, owner.profile.manager, id), new MachineReading(info));
              }
            }
          }
        } catch (_) { /* Metrics endpoint is optional per Hub. */ }

        try {
          const usageResp = await owner.repo().call('/api/v1/usage/windows');
          if (rev === this.telemetryRevision && owner.owns(epoch, scope) && Array.isArray(usageResp['accounts'])) {
            for (const row of Wire.rows(usageResp, 'accounts')) {
              const account = new SubscriptionAccount(row, owner.profile.id);
              if (account.visible(now)) nextAccounts.push(account);
            }
          }
        } catch (_) { /* Usage windows endpoint is optional per Hub. */ }
      }));
      if (rev !== this.telemetryRevision) return;
      this.machineReadings = nextReadings;
      if (nextAccounts.length > 0 || owners.length > 0) {
        this.usageAccounts = nextAccounts;
      }
    } finally {
      if (rev === this.telemetryRevision) {
        this.telemetryLoading = false;
      }
    }
  }

  private formatLoadSummary(machine: ResourceRow): string {
    const key = machineKey(machine.group, machine.manager, machine.id);
    const reading = this.machineReadings.get(key);
    if (!reading) return '';
    const sys = Object.keys(reading.system).length ? reading.system : Wire.object(machine.data(), 'system');
    const cores = Wire.number(sys, 'cores');
    const loadVal = Wire.number(reading.latest, 'load', -1);
    if (loadVal < 0) return '';
    const loadText = loadVal.toFixed(loadVal >= 10 ? 1 : 2);
    if (cores > 0) {
      const per = loadVal / cores;
      const perText = per.toFixed(per >= 10 ? 1 : 2);
      return tr('machine_load') + ' ' + loadText + ' · ' + tr('machine_load_per_core').replace('{per}', perText);
    }
    return tr('machine_load') + ' ' + loadText;
  }

  private effectivePanelWidth(): number {
    return this.rootWidth > 0 ? this.rootWidth : this.panelWidth;
  }

  private useCompactChipGrid(): boolean {
    return this.effectivePanelWidth() < 316;
  }

  private machineCpuPercent(machine: ResourceRow): number {
    const key = machineKey(machine.group, machine.manager, machine.id);
    const reading = this.machineReadings.get(key);
    if (!reading) return -1;
    const cpu = Wire.number(reading.latest, 'cpu', -1);
    return cpu >= 0 && Number.isFinite(cpu) ? Math.max(0, Math.min(100, cpu)) : -1;
  }

  private machineLoadPerCore(machine: ResourceRow): number {
    const key = machineKey(machine.group, machine.manager, machine.id);
    const reading = this.machineReadings.get(key);
    if (!reading) return -1;
    const sys = Object.keys(reading.system).length ? reading.system : Wire.object(machine.data(), 'system');
    const cores = Wire.number(sys, 'cores');
    const loadVal = Wire.number(reading.latest, 'load', -1);
    if (cores <= 0 || loadVal < 0 || !Number.isFinite(loadVal)) return -1;
    return loadVal / cores;
  }

  private telemetryGaugeTint(percent: number, warnAt: number, dangerAt: number): ResourceColor {
    if (percent >= dangerAt) return Colors.danger;
    if (percent >= warnAt) return Colors.working;
    return Colors.positive;
  }

  private machineHardwareCaption(machine: ResourceRow): string {
    const key = machineKey(machine.group, machine.manager, machine.id);
    const reading = this.machineReadings.get(key);
    const rawSystem = Wire.object(machine.data(), 'system');
    const sys = reading && Object.keys(reading.system).length ? reading.system : rawSystem;
    const osLabel = Wire.text(sys, 'name') || Wire.text(machine.data(), 'os') || osFamily(sys, Wire.text(machine.data(), 'os'));
    const cores = Wire.number(sys, 'cores');
    const cpuVal = reading ? Wire.number(reading.latest, 'cpu', -1) : -1;
    return [osLabel, cores > 0 ? String(cores) + 'c' : '', cpuVal >= 0 ? 'CPU ' + machinePercent(cpuVal) : ''].filter((v: string) => !!v).join(' · ');
  }

  @Builder statusMark(state: string) {
    if (state === 'working') {
      WorkingArcSpinner({ diameter: 14, tint: Colors.working, surface: Color.Transparent, showCoreDot: true })
    } else if (state === 'needs_you') {
      NeedsYouPulseDot({ dotSize: 8, tint: Colors.needs, surface: Colors.surface, bordered: false })
    } else if (state === 'unread') {
      Circle().width(8).height(8).fill(Colors.unread)
    } else {
      Circle().width(8).height(8).fill(Colors.tertiary)
    }
  }

  @Builder metaRow(label: string, value: string, mono: boolean = false, startEllipsis: boolean = false) {
    if (value) {
      Row({ space: 8 }) {
        Text(label)
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontColor(Colors.secondary)
          .maxLines(1)
        Text(value)
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontFamily(mono ? 'monospace' : 'sans-serif')
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.ink)
          .textAlign(TextAlign.End)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
          .ellipsisMode(startEllipsis ? EllipsisMode.START : EllipsisMode.CENTER)
          .layoutWeight(1)
      }.width('100%').padding({ top: 3, bottom: 3 })
    }
  }

  @Builder toolChip(labelKey: string, tab: number, identity: string, symbol?: Resource) {
    Button({ type: ButtonType.Normal }) {
      Column({ space: 4 }) {
        if (symbol) {
          SymbolGlyph(symbol)
            .fontSize('15vp')
            .fontColor([Colors.link])
        } else {
          Text('›_')
            .fontSize('12vp')
            .fontFamily('monospace')
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.link)
            .height(15)
            .textAlign(TextAlign.Center)
        }
        Text(tr(labelKey))
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.link)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }
      .width('100%')
      .height('100%')
      .justifyContent(FlexAlign.Center)
      .alignItems(HorizontalAlign.Center)
      .padding({ left: 4, right: 4 })
    }
    .layoutWeight(1)
    .height(this.useCompactChipGrid() ? 48 : 52)
    .padding(0)
    .backgroundColor(Colors.selection)
    .borderRadius(12)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 12 })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
    .accessibilityText(tr(labelKey))
    .bindTips(tr(labelKey))
    .onClick(() => {
      Haptics.tap();
      if (this.isTriFold) {
        this.embeddedToolsTab = tab;
        this.workbenchTab = 1;
        this.embeddedWorkspace?.selectTab(tab);
      } else {
        this.onOpenToolsTab(tab);
      }
    })
    .id(identity)
  }

  @Builder pulseCounterTile(count: number, labelKey: string, tint: ResourceColor, identity: string) {
    Column({ space: 3 }) {
      Row({ space: 5 }) {
        Circle().width(6).height(6).fill(tint)
        Text(String(count))
          .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
          .fontWeight(FontWeight.Bold)
          .fontColor(count > 0 ? Colors.ink : Colors.secondary)
          .maxLines(1)
      }
      Text(tr(labelKey))
        .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
        .fontColor(Colors.secondary)
        .maxLines(1)
        .textOverflow({ overflow: TextOverflow.Ellipsis })
    }
    .layoutWeight(1)
    .alignItems(HorizontalAlign.Center)
    .padding({ left: 4, right: 4, top: 8, bottom: 8 })
    .backgroundColor(count > 0 && tint === Colors.needs ? Colors.selection : Colors.control)
    .borderRadius(12)
    .accessibilityGroup(true)
    .accessibilityText(tr(labelKey) + ' ' + String(count))
    .id(identity)
  }

  @Builder workbenchSegment(labelKey: string, index: number, identity: string) {
    Button({ type: ButtonType.Normal }) {
      Text(tr(labelKey))
        .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
        .fontWeight(this.workbenchTab === index ? FontWeight.Bold : FontWeight.Medium)
        .fontColor(this.workbenchTab === index ? Colors.ink : Colors.secondary)
        .maxLines(1)
        .textOverflow({ overflow: TextOverflow.Ellipsis })
    }
    .layoutWeight(1)
    .height(30)
    .padding({ left: 6, right: 6 })
    .backgroundColor(this.workbenchTab === index ? Colors.surface : Color.Transparent)
    .borderRadius(9)
    .shadow(this.workbenchTab === index ? PhoneStyle.cardElevation(this.env.dark) : { radius: 0, color: Color.Transparent })
    .accessibilitySelected(this.workbenchTab === index)
    .onClick(() => {
      if (this.workbenchTab !== index) {
        Haptics.selection();
        this.workbenchTab = index;
      }
    })
    .id(identity)
  }

  @Builder urgentNeedsYouCard(row: ResourceRow) {
    Row({ space: 10 }) {
      NeedsYouPulseDot({ dotSize: 9, tint: Colors.needs, surface: Colors.surface, bordered: false })
      Column({ space: 2 }) {
        Text(this.displaySessionTitle(row))
          .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
          .fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
        Text([this.model.machineName(row.machine) || row.machine, tr('needs_you')].filter((v: string) => !!v).join(' · '))
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontColor(Colors.needs)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }.layoutWeight(1).alignItems(HorizontalAlign.Start)

      Button(tr('fold_urgent_review'), { type: ButtonType.Capsule })
        .height(28)
        .padding({ left: 10, right: 10 })
        .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
        .fontWeight(FontWeight.Medium)
        .backgroundColor(Colors.action)
        .fontColor(Colors.onAction)
        .onClick(() => {
          Haptics.selection();
          this.onOpenSession(row);
        })
        .id('fold.inspector.urgent.review')
    }
    .width('100%')
    .padding({ left: 12, right: 10, top: 10, bottom: 10 })
    .backgroundColor(Colors.selection)
    .borderRadius(16)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 16 })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
    .id('fold.inspector.urgent')
  }

  @Builder relayBar() {
    Row({ space: 8 }) {
      Row({ space: 6 }) {
        Circle()
          .width(7)
          .height(7)
          .fill(this.fleetCounts().needsYou > 0 ? Colors.needs : this.fleetCounts().unread > 0 ? Colors.unread : this.fleetCounts().working > 0 ? Colors.working : Colors.positive)
        Text(
          (this.fleetCounts().needsYou + this.fleetCounts().unread) > 0
            ? tr('fold_relay_queue').replace('{count}', String(this.fleetCounts().needsYou + this.fleetCounts().unread))
            : tr('fold_relay_clear')
        )
          .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
          .fontWeight(600)
          .fontColor((this.fleetCounts().needsYou + this.fleetCounts().unread) > 0 ? Colors.ink : Colors.secondary)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }.layoutWeight(1).alignItems(VerticalAlign.Center)

      Button({ type: ButtonType.Capsule }) {
        SymbolGlyph(Symbols.collapse).fontSize('12vp').fontColor([Colors.ink])
      }
      .width(32)
      .height(28)
      .padding(0)
      .backgroundColor(Colors.control)
      .enabled((this.fleetCounts().needsYou + this.fleetCounts().unread + this.fleetCounts().working) > 0)
      .accessibilityText(tr('fold_relay_prev'))
      .bindTips(tr('fold_relay_prev'))
      .onClick(() => {
        Haptics.selection();
        this.onStepRelay(-1);
      })
      .id('fold.relay.prev')

      Button({ type: ButtonType.Capsule }) {
        Row({ space: 4 }) {
          Text(tr('fold_relay_next'))
            .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
            .fontWeight(FontWeight.Medium)
            .fontColor((this.fleetCounts().needsYou + this.fleetCounts().unread + this.fleetCounts().working) > 0 ? Colors.link : Colors.secondary)
            .maxLines(1)
          SymbolGlyph(Symbols.expand)
            .fontSize('11vp')
            .fontColor([(this.fleetCounts().needsYou + this.fleetCounts().unread + this.fleetCounts().working) > 0 ? Colors.link : Colors.secondary])
        }
      }
      .height(28)
      .padding({ left: 10, right: 9 })
      .backgroundColor((this.fleetCounts().needsYou + this.fleetCounts().unread) > 0 ? Colors.selection : Colors.control)
      .enabled((this.fleetCounts().needsYou + this.fleetCounts().unread + this.fleetCounts().working) > 0)
      .accessibilityText(tr('fold_relay_next'))
      .bindTips(tr('fold_relay_next'))
      .onClick(() => {
        Haptics.selection();
        this.onStepRelay(1);
      })
      .id('fold.relay.next')
    }
    .width('100%')
    .padding({ left: 12, right: 10, top: 10, bottom: 10 })
    .backgroundColor(Colors.surface)
    .borderRadius(16)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 16 })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
    .id('fold.relay.bar')
  }

  @Builder fleetPulseCard() {
    Column({ space: 10 }) {
      Row({ space: 8 }) {
        Text(tr('fold_inspector_fleet'))
          .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
          .fontWeight(FontWeight.Bold)
          .fontColor(Colors.ink)
          .layoutWeight(1)
        if (this.fleetCounts().total > 0) {
          Text(String(this.fleetCounts().total))
            .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
            .fontWeight(FontWeight.Bold)
            .fontColor(Colors.secondary)
            .padding({ left: 7, right: 7, top: 2, bottom: 2 })
            .backgroundColor(Colors.control)
            .borderRadius(Tokens.capsule)
        }
      }.width('100%')

      Row({ space: 6 }) {
        this.pulseCounterTile(this.fleetCounts().needsYou, 'needs_you', Colors.needs, 'fold.inspector.count.needs')
        this.pulseCounterTile(this.fleetCounts().working, 'working', Colors.working, 'fold.inspector.count.working')
        this.pulseCounterTile(this.fleetCounts().unread, 'agent_group_unread', Colors.unread, 'fold.inspector.count.unread')
      }.width('100%')

      if (this.fleetCounts().needsYou === 0 && this.fleetCounts().working === 0 && this.fleetCounts().unread === 0) {
        Row({ space: 6 }) {
          SymbolGlyph(Symbols.check).fontSize('12vp').fontColor([Colors.positive])
          Text(tr('live_view_all_caught_up'))
            .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
            .fontColor(Colors.secondary)
            .maxLines(1)
            .textOverflow({ overflow: TextOverflow.Ellipsis })
            .layoutWeight(1)
        }.width('100%').padding({ left: 2, right: 2 })
      }

      if (this.prioritizedSessions().length > 0) {
        Column({ space: 6 }) {
          ForEach(this.prioritizedSessions(), (row: ResourceRow) => {
            Row({ space: 6 }) {
              Button({ type: ButtonType.Normal }) {
                Row({ space: 8 }) {
                  this.statusMark(this.normalizedRowState(row))
                  Column({ space: 2 }) {
                    Text(this.displaySessionTitle(row))
                      .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                      .fontWeight(this.activeOwner().selected.key() === row.key() ? FontWeight.Bold : FontWeight.Medium)
                      .fontColor(Colors.ink)
                      .maxLines(1)
                      .textOverflow({ overflow: TextOverflow.Ellipsis })
                    Text([
                      this.model.machineName(row.machine) || row.machine,
                      tr(this.stateLabelKey(this.normalizedRowState(row)))
                    ].filter((part: string) => !!part).join(' · '))
                      .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                      .fontColor(Colors.secondary)
                      .maxLines(1)
                      .textOverflow({ overflow: TextOverflow.Ellipsis })
                  }.layoutWeight(1).alignItems(HorizontalAlign.Start)
                  SymbolGlyph(Symbols.expand).fontSize('11vp').fontColor([Colors.tertiary])
                }.width('100%').padding({ left: 10, right: 10, top: 8, bottom: 8 })
              }
              .layoutWeight(1)
              .padding(0)
              .backgroundColor(this.activeOwner().selected.key() === row.key() ? Colors.selection : Colors.control)
              .borderRadius(12)
              .border({
                width: this.activeOwner().selected.key() === row.key() ? 1 : 0,
                color: this.activeOwner().selected.key() === row.key() ? Colors.link : Color.Transparent,
                radius: 12
              })
              .animation(Motion.state(this.env.reduceMotion))
              .onClick(() => {
                Haptics.selection();
                this.onOpenSession(row);
              })
              .id('fold.inspector.session.' + row.id)

              if (this.canInterruptRow(row)) {
                Button({ type: ButtonType.Circle }) {
                  SymbolGlyph(Symbols.stop).fontSize('10vp').fontColor([Colors.danger])
                }
                .width(30)
                .height(30)
                .padding(0)
                .backgroundColor(Colors.control)
                .enabled(this.pendingInterruptKey !== row.key())
                .accessibilityText(tr('fold_interrupt_agent'))
                .bindTips(tr('fold_interrupt_agent'))
                .onClick(() => {
                  Haptics.attention();
                  this.onInterruptSession(row);
                })
                .id('fold.inspector.interrupt.' + row.id)
              }
            }.width('100%').alignItems(VerticalAlign.Center)
          }, (row: ResourceRow) => row.key() + ':' + this.normalizedRowState(row) + ':' + this.displaySessionTitle(row))
        }.width('100%')
      }
    }
    .width('100%')
    .padding(14)
    .backgroundColor(Colors.surface)
    .borderRadius(18)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 18 })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
  }

  @Builder xiaoYiChip() {
    Button({ type: ButtonType.Normal }) {
      Column({ space: 4 }) {
        SymbolGlyph(Symbols.persona)
          .fontSize('15vp')
          .fontColor([Colors.ai])
        Text(tr('phone_xiaoyi'))
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontWeight(FontWeight.Medium)
          .fontColor(Colors.ai)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
      }
      .width('100%')
      .height('100%')
      .justifyContent(FlexAlign.Center)
      .alignItems(HorizontalAlign.Center)
      .padding({ left: 4, right: 4 })
    }
    .layoutWeight(1)
    .height(this.useCompactChipGrid() ? 48 : 52)
    .padding(0)
    .backgroundColor(Colors.selection)
    .borderRadius(12)
    .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 12 })
    .shadow(PhoneStyle.cardElevation(this.env.dark))
    .accessibilityText(tr('phone_xiaoyi'))
    .bindTips(tr('phone_xiaoyi'))
    .onClick(() => {
      Haptics.tap();
      this.onOpenXiaoYi();
    })
    .id('fold.inspector.tools.xiaoyi')
  }

  @Builder pulseTabContent() {
    Scroll() {
      Column({ space: 12 }) {
        if (this.urgentSession()) {
          this.urgentNeedsYouCard(this.urgentSession()!)
        }
        this.relayBar()
        if (this.selectedSession()) {
          Column({ space: 8 }) {
            Row({ space: 8 }) {
              this.statusMark(this.sessionState())
              Text(this.displaySessionTitle(this.selectedSession()!, tr('main_chat')))
                .fontSize(Tokens.interfaceFontVp(15, this.env.fontScale))
                .fontWeight(FontWeight.Bold)
                .fontColor(Colors.ink)
                .maxLines(2)
                .textOverflow({ overflow: TextOverflow.Ellipsis })
                .layoutWeight(1)
              Text(tr(this.stateLabelKey(this.sessionState())))
                .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                .fontWeight(FontWeight.Medium)
                .fontColor(this.stateTint(this.sessionState()))
                .padding({ left: 7, right: 7, top: 2, bottom: 2 })
                .backgroundColor(this.sessionState() === 'needs_you' ? Colors.selection : Colors.control)
                .borderRadius(Tokens.capsule)
                .maxLines(1)
            }.width('100%').alignItems(VerticalAlign.Center)
            Divider().strokeWidth(0.5).color(Colors.divider)
            this.metaRow(tr('machine'), this.activeOwner().machineName(this.selectedSession()!.machine) || this.selectedSession()!.machine)
            this.metaRow(tr('model'), (Wire.text(this.sessionData(), 'model').split('/').pop() || Wire.text(this.sessionData(), 'launcher') || 'Claude'))
            this.metaRow(tr('branch'), Wire.text(this.sessionData(), 'git_branch'), true)
            this.metaRow(tr('phone_session_folder'), Wire.text(this.sessionData(), 'cwd'), true, true)
            if (this.selectedSession()!.kind === 'session' && this.selectedSession()!.machine) {
              Divider().strokeWidth(0.5).color(Colors.divider).margin({ top: 2, bottom: 2 })
              Column() {
                if (this.useCompactChipGrid()) {
                  Column({ space: 8 }) {
                    Row({ space: 8 }) {
                      this.toolChip('files', 0, 'fold.inspector.tools.files', Symbols.folder)
                      this.toolChip('git', 1, 'fold.inspector.tools.git', $r('sys.symbol.arrow_triangle_merge'))
                    }.width('100%')
                    Row({ space: 8 }) {
                      this.toolChip('terminal', 2, 'fold.inspector.tools.terminal')
                      this.xiaoYiChip()
                    }.width('100%')
                  }.width('100%')
                } else {
                  Row({ space: 6 }) {
                    this.toolChip('files', 0, 'fold.inspector.tools.files', Symbols.folder)
                    this.toolChip('git', 1, 'fold.inspector.tools.git', $r('sys.symbol.arrow_triangle_merge'))
                    this.toolChip('terminal', 2, 'fold.inspector.tools.terminal')
                    this.xiaoYiChip()
                  }.width('100%')
                }
              }.width('100%').id('fold.inspector.workspace')
            }
          }
          .width('100%')
          .padding(14)
          .backgroundColor(Colors.surface)
          .borderRadius(18)
          .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 18 })
          .shadow(PhoneStyle.cardElevation(this.env.dark))
        }
        this.fleetPulseCard()
      }.width('100%').padding({ left: 12, right: 12, bottom: 24 })
    }.width('100%').layoutWeight(1).scrollBar(BarState.Off)
  }

  @Builder embeddedToolPill(labelKey: string, tab: number, identity: string) {
    Button({ type: ButtonType.Capsule }) {
      Text(tr(labelKey))
        .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
        .fontWeight(this.embeddedToolsTab === tab ? FontWeight.Bold : FontWeight.Medium)
        .fontColor(this.embeddedToolsTab === tab ? Colors.link : Colors.secondary)
        .maxLines(1)
    }
    .height(26)
    .padding({ left: 10, right: 10 })
    .backgroundColor(this.embeddedToolsTab === tab ? Colors.selection : Colors.control)
    .onClick(() => {
      Haptics.selection();
      this.embeddedToolsTab = tab;
      this.embeddedWorkspace?.selectTab(tab);
    })
    .id(identity)
  }

  @Builder codeTabContent() {
    if (this.selectedSession()?.kind === 'session' && this.selectedSession()?.machine) {
      Column({ space: 6 }) {
        Row({ space: 6 }) {
          this.embeddedToolPill('files', 0, 'fold.workbench.code.files')
          this.embeddedToolPill('git', 1, 'fold.workbench.code.git')
          this.embeddedToolPill('terminal', 2, 'fold.workbench.code.terminal')
          Blank().layoutWeight(1)
          Button({ type: ButtonType.Capsule }) {
            Row({ space: 4 }) {
              Text(tr('fold_workbench_expand_tools'))
                .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                .fontWeight(FontWeight.Medium)
                .fontColor(Colors.link)
                .maxLines(1)
              SymbolGlyph(Symbols.expand).fontSize('10vp').fontColor([Colors.link])
            }
          }
          .height(26)
          .padding({ left: 9, right: 9 })
          .backgroundColor(Colors.control)
          .onClick(() => {
            Haptics.tap();
            this.onOpenToolsTab(this.embeddedToolsTab);
          })
          .id('fold.workbench.expand')
        }.width('100%').padding({ left: 12, right: 12 })

        Column() {
          SessionTools({
            model: this.activeOwner(),
            initialTab: this.embeddedToolsTab,
            onWorkspaceReady: (ws: SessionWorkspace | undefined) => {
              this.embeddedWorkspace = ws;
              if (ws) ws.selectTab(this.embeddedToolsTab);
            },
            onOpenLink: (address: string) => this.onOpenLink(address)
          })
        }
        .width('100%')
        .layoutWeight(1)
        .clip(true)
        .backgroundColor(Colors.surface)
        .borderRadius({ topLeft: 18, topRight: 18 })
        .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: { topLeft: 18, topRight: 18 } })
      }.width('100%').layoutWeight(1)
    } else {
      Column({ space: 10 }) {
        SymbolGlyph(Symbols.folder).fontSize('28vp').fontColor([Colors.tertiary])
        Text(tr('fold_workbench_pick_session'))
          .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
          .fontColor(Colors.secondary)
          .textAlign(TextAlign.Center)
          .padding({ left: 24, right: 24 })
      }
      .width('100%')
      .layoutWeight(1)
      .justifyContent(FlexAlign.Center)
      .alignItems(HorizontalAlign.Center)
      .id('fold.workbench.code.empty')
    }
  }

  @Builder machineTelemetryGauge(label: string, valueText: string, percent: number, tint: ResourceColor, identity: string) {
    Column({ space: 3 }) {
      Row({ space: 6 }) {
        Text(label)
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontColor(Colors.secondary)
          .layoutWeight(1)
          .maxLines(1)
          .textOverflow({ overflow: TextOverflow.Ellipsis })
        Text(valueText)
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontFamily('monospace')
          .fontWeight(600)
          .fontColor(tint)
          .maxLines(1)
      }.width('100%')
      Stack({ alignContent: Alignment.Start }) {
        Row().width('100%').height(6).borderRadius(3).backgroundColor(this.env.dark ? '#28FFFFFF' : '#14000000')
        Row()
          .width(String(Math.max(0, Math.min(100, percent))) + '%')
          .height(6)
          .borderRadius(3)
          .backgroundColor(tint)
      }.width('100%').height(6).clip(true)
    }.width('100%').id(identity)
  }

  @Builder usageWindowRow(w: SubscriptionWindow) {
    Column({ space: 3 }) {
      Row({ space: 6 }) {
        Text(w.label || (w.minutes === 300 ? '5h' : w.minutes === 10080 ? '7d' : 'Window'))
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontColor(Colors.secondary)
          .layoutWeight(1)
          .maxLines(1)
        Text(String(Math.round(Math.max(0, Math.min(100, w.percent)))) + '%')
          .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
          .fontWeight(600)
          .fontColor(w.percent >= 80 ? Colors.working : Colors.ink)
          .maxLines(1)
      }.width('100%')
      Stack({ alignContent: Alignment.Start }) {
        Row().width('100%').height(6).borderRadius(3).backgroundColor(this.env.dark ? '#28FFFFFF' : '#14000000')
        Row()
          .width(String(Math.max(0, Math.min(100, w.percent))) + '%')
          .height(6)
          .borderRadius(3)
          .backgroundColor(w.percent >= 80 ? Colors.working : Colors.link)
      }.width('100%').height(6).clip(true)
    }.width('100%')
  }

  @Builder machinesTabContent() {
    Scroll() {
      Column({ space: 12 }) {
        Column({ space: 10 }) {
          Row({ space: 8 }) {
            Text(tr('machines'))
              .fontSize(Tokens.interfaceFontVp(14, this.env.fontScale))
              .fontWeight(FontWeight.Bold)
              .fontColor(Colors.ink)
              .layoutWeight(1)
            if (this.telemetryLoading) {
              LoadingProgress().width(16).height(16)
            }
          }.width('100%')

          if (!this.machineRows().length) {
            Text(tr('empty'))
              .fontSize(Tokens.interfaceFontVp(12, this.env.fontScale))
              .fontColor(Colors.secondary)
              .padding({ top: 6, bottom: 6 })
          } else {
            Column({ space: 8 }) {
              ForEach(this.machineRows(), (machine: ResourceRow) => {
                Column({ space: 4 }) {
                  Row({ space: 8 }) {
                    Circle()
                      .width(8)
                      .height(8)
                      .fill(machineOnline(machine) ? Colors.positive : Colors.tertiary)
                    Text(machine.title || machine.id)
                      .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                      .fontWeight(600)
                      .fontColor(Colors.ink)
                      .maxLines(1)
                      .textOverflow({ overflow: TextOverflow.Ellipsis })
                      .layoutWeight(1)
                    if (this.machineSummary(machine).needsYou > 0) {
                      Text(String(this.machineSummary(machine).needsYou) + ' ' + tr('needs_you'))
                        .fontSize(Tokens.interfaceFontVp(10, this.env.fontScale))
                        .fontWeight(FontWeight.Bold)
                        .fontColor(Colors.needs)
                        .padding({ left: 6, right: 6, top: 2, bottom: 2 })
                        .backgroundColor(Colors.selection)
                        .borderRadius(Tokens.capsule)
                        .maxLines(1)
                    } else if (this.machineSummary(machine).working > 0) {
                      Text(String(this.machineSummary(machine).working) + ' ' + tr('working'))
                        .fontSize(Tokens.interfaceFontVp(10, this.env.fontScale))
                        .fontWeight(FontWeight.Medium)
                        .fontColor(Colors.working)
                        .padding({ left: 6, right: 6, top: 2, bottom: 2 })
                        .backgroundColor(Colors.control)
                        .borderRadius(Tokens.capsule)
                        .maxLines(1)
                    }
                  }.width('100%').alignItems(VerticalAlign.Center)

                  if (this.machineHardwareCaption(machine)) {
                    Text(this.machineHardwareCaption(machine))
                      .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                      .fontColor(Colors.secondary)
                      .maxLines(1)
                      .textOverflow({ overflow: TextOverflow.Ellipsis })
                      .width('100%')
                  }
                  if (this.formatLoadSummary(machine)) {
                    Text(this.formatLoadSummary(machine))
                      .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                      .fontFamily('monospace')
                      .fontColor(Colors.ink)
                      .maxLines(1)
                      .textOverflow({ overflow: TextOverflow.Ellipsis })
                      .width('100%')
                  }
                  if (this.machineCpuPercent(machine) >= 0 || this.machineLoadPerCore(machine) >= 0) {
                    Column({ space: 6 }) {
                      Text(tr('machine_cpu_load'))
                        .fontSize(Tokens.interfaceFontVp(10, this.env.fontScale))
                        .fontWeight(FontWeight.Medium)
                        .fontColor(Colors.secondary)
                        .maxLines(1)
                      if (this.machineCpuPercent(machine) >= 0) {
                        this.machineTelemetryGauge(
                          'CPU',
                          machinePercent(this.machineCpuPercent(machine)),
                          this.machineCpuPercent(machine),
                          this.telemetryGaugeTint(this.machineCpuPercent(machine), 75, 90),
                          'fold.workbench.machine.cpu.' + machine.id
                        )
                      }
                      if (this.machineLoadPerCore(machine) >= 0) {
                        this.machineTelemetryGauge(
                          tr('machine_load'),
                          tr('machine_load_per_core').replace('{per}', this.machineLoadPerCore(machine).toFixed(this.machineLoadPerCore(machine) >= 10 ? 1 : 2)),
                          Math.min(100, this.machineLoadPerCore(machine) * 100),
                          this.telemetryGaugeTint(this.machineLoadPerCore(machine) * 100, 75, 100),
                          'fold.workbench.machine.load.' + machine.id
                        )
                      }
                    }
                    .width('100%')
                    .alignItems(HorizontalAlign.Start)
                    .padding({ top: 4 })
                  }
                }
                .width('100%')
                .padding(10)
                .backgroundColor(Colors.control)
                .borderRadius(12)
                .id('fold.workbench.machine.' + machine.id)
              }, (machine: ResourceRow) => machine.key() + ':' + machine.raw)
            }.width('100%')
          }
        }
        .width('100%')
        .padding(14)
        .backgroundColor(Colors.surface)
        .borderRadius(18)
        .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 18 })
        .shadow(PhoneStyle.cardElevation(this.env.dark))

        if (this.usageAccounts.length > 0) {
          Column({ space: 10 }) {
            ForEach(this.usageAccounts, (account: SubscriptionAccount) => {
              Column({ space: 6 }) {
                Row({ space: 6 }) {
                  Text(account.name || usageProviderTitle(account.provider, 'AI'))
                    .fontSize(Tokens.interfaceFontVp(13, this.env.fontScale))
                    .fontWeight(FontWeight.Bold)
                    .fontColor(Colors.ink)
                    .maxLines(1)
                    .layoutWeight(1)
                  if (account.plan) {
                    Text(account.plan)
                      .fontSize(Tokens.interfaceFontVp(10, this.env.fontScale))
                      .fontWeight(FontWeight.Medium)
                      .fontColor(Colors.secondary)
                      .padding({ left: 6, right: 6, top: 2, bottom: 2 })
                      .backgroundColor(Colors.control)
                      .borderRadius(Tokens.capsule)
                  }
                }.width('100%')
                ForEach(account.liveWindows(Date.now()).slice(0, 2), (w: SubscriptionWindow) => {
                  this.usageWindowRow(w)
                }, (w: SubscriptionWindow) => w.label + ':' + String(w.minutes) + ':' + String(w.percent))
              }
              .width('100%')
              .padding(10)
              .backgroundColor(Colors.control)
              .borderRadius(12)
              .id('fold.workbench.usage.' + account.provider)
            }, (account: SubscriptionAccount) => account.id + ':' + account.snapshot)
          }
          .width('100%')
          .padding(14)
          .backgroundColor(Colors.surface)
          .borderRadius(18)
          .border({ width: PhoneStyle.surfaceEdgeWidth(0.5), color: PhoneStyle.cardBorder(this.env.dark), radius: 18 })
          .shadow(PhoneStyle.cardElevation(this.env.dark))
        }
      }.width('100%').padding({ left: 12, right: 12, bottom: 24 })
    }.width('100%').layoutWeight(1).scrollBar(BarState.Off)
  }

  build() {
    Row() {
      Divider().vertical(true).strokeWidth(0.5).color(Colors.divider).height('100%')
      Column({ space: 10 }) {
        Row({ space: 8 }) {
          Column({ space: 5 }) {
            Text(tr('fold_inspector_title'))
              .fontSize(Tokens.interfaceFontVp(16, this.env.fontScale))
              .fontWeight(FontWeight.Bold)
              .fontColor(Colors.ink)
              .maxLines(1)
            if (this.postureLabel) {
              Row({ space: 5 }) {
                SymbolGlyph(Symbols.apps).fontSize('11vp').fontColor([Colors.link])
                Text(this.postureLabel)
                  .fontSize(Tokens.interfaceFontVp(11, this.env.fontScale))
                  .fontWeight(FontWeight.Medium)
                  .fontColor(Colors.link)
                  .maxLines(1)
                  .textOverflow({ overflow: TextOverflow.Ellipsis })
              }
              .padding({ left: 8, right: 8, top: 3, bottom: 3 })
              .backgroundColor(Colors.selection)
              .borderRadius(Tokens.capsule)
              .id('fold.inspector.posture')
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
          .bindTips(tr('close'))
          .onClick(() => {
            Haptics.tap();
            this.onClose();
          })
          .id('fold.inspector.close')
        }.width('100%').padding({ left: 16, right: 14, top: 12, bottom: 0 }).alignItems(VerticalAlign.Top)

        Row({ space: 3 }) {
          this.workbenchSegment('fold_workbench_pulse', 0, 'fold.workbench.tab.pulse')
          this.workbenchSegment('fold_workbench_code', 1, 'fold.workbench.tab.code')
          this.workbenchSegment('fold_workbench_machines', 2, 'fold.workbench.tab.machines')
        }
        .width('calc(100% - 24vp)')
        .padding(3)
        .backgroundColor(Colors.control)
        .borderRadius(12)
        .id('fold.workbench.tabs')

        if (this.workbenchTab === 0) {
          this.pulseTabContent()
        } else if (this.workbenchTab === 1) {
          this.codeTabContent()
        } else {
          this.machinesTabContent()
        }
      }.width('100%').height('100%').layoutWeight(1).backgroundColor(Colors.grouped)
    }
    .width(this.panelWidth)
    .height('100%')
    .onAreaChange((_old: Area, area: Area) => {
      const w = Number(area.width);
      if (Number.isFinite(w) && w > 0 && Math.abs(w - this.rootWidth) > 1) {
        this.rootWidth = w;
      }
    })
    .transition(Motion.slideFade(this.env.reduceMotion, 0))
    .id('fold.inspector.panel')
  }
}
