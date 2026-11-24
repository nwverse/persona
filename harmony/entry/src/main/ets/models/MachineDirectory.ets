import type { ClientModel } from './ClientModel';
import type { HttpFailure } from '../platform/HubTransport';
import { machinePlatform } from './DeviceIdentity';
import type { MachinePlatform } from './DeviceIdentity';
import { ResourceRow, Wire } from './Wire';

export class MachineCard {
  row: ResourceRow;
  platform: MachinePlatform;
  online: boolean;
  pending: boolean;
  agents: ResourceRow[] = [];
  needs: number = 0;
  working: number = 0;

  constructor(row: ResourceRow) {
    this.row = row;
    const data = row.data();
    this.platform = machinePlatform(data);
    this.online = row.state !== 'offline' && Wire.bool(data, 'online', row.state === 'online' || row.state === 'connected');
    this.pending = Wire.text(data, 'trust') === 'pending' || Wire.bool(data, 'pending_confirm');
  }

  addAgent(row: ResourceRow, raw: Record<string, Object>): void {
    this.agents.push(row);
    const children = Wire.object(raw, 'children');
    if (row.state === 'needs_you') this.needs++;
    if (row.state === 'working' && (!Wire.bool(raw, 'background_waiting') ||
      Wire.number(children, 'working') > 0 || Wire.number(children, 'needs_you') > 0)) this.working++;
  }

  priority(): number { return this.pending ? 0 : !this.online ? 1 : this.needs ? 2 : 3; }
}

export function machineCards(machines: ResourceRow[], sessions: ResourceRow[], group: string, manager: string): MachineCard[] {
  const byMachine = new Map<string, MachineCard>();
  const identity = (row: ResourceRow, id: string): string => JSON.stringify([row.group || group, row.manager || manager, id]);
  const cards = machines.map((row: ResourceRow) => {
    const card = new MachineCard(row);
    byMachine.set(identity(row, row.id), card);
    return card;
  });
  for (const agent of sessions) {
    if (!agent.machine || agent.state === 'offline') continue;
    const card = byMachine.get(identity(agent, agent.machine));
    if (!card) continue;
    const data = agent.data();
    if (Wire.bool(data, 'assistant') || Wire.text(data, 'status') === 'offline') continue;
    const source = Wire.text(data, 'source_kind');
    const codex = Wire.text(data, 'agent') === 'codex' || source.startsWith('codex_');
    const child = Wire.bool(data, 'is_subagent') || !!Wire.text(data, 'parent_session_id');
    const root = Wire.text(data, 'root_session_id');
    const nested = source === 'runtime' ? child : codex && (child || !!Wire.text(data, 'parent_thread_id') || (!!root && root !== (agent.session || agent.id)));
    if (nested && agent.state !== 'needs_you') continue;
    card.addAgent(agent, data);
  }
  return cards.sort((a: MachineCard, b: MachineCard) => a.priority() - b.priority());
}

export class MachineReading {
  cpu: number;
  memory: number;
  rx: number;
  tx: number;
  constructor(latest: Record<string, Object>) {
    this.cpu = Wire.number(latest, 'cpu', -1);
    const total = Wire.number(latest, 'mem_total'), used = Wire.number(latest, 'mem_used', -1);
    this.memory = total > 0 && used >= 0 ? Math.min(100, 100 * used / total) : -1;
    this.rx = Wire.number(latest, 'net_rx', -1);
    this.tx = Wire.number(latest, 'net_tx', -1);
  }
  hasData(): boolean { return this.cpu >= 0 || this.memory >= 0 || this.rx >= 0 || this.tx >= 0; }
}

// Each manager's metrics response is joined only to its own directory rows. A hidden page
// or a changed connection cannot publish a late sample into the next machine's page.
@ObservedV2
export class MachineReadings {
  @Trace values: Map<string, MachineReading> = new Map<string, MachineReading>();
  private sequence: number = 0;
  private timer: number = -1;
  private owner?: ClientModel;
  private binding: string = '';

  pause(): void {
    this.sequence++;
    clearTimeout(this.timer);
  }

  stop(): void {
    this.pause();
    this.owner = undefined; this.binding = ''; this.values = new Map<string, MachineReading>();
  }

  sample(owner: ClientModel, row: ResourceRow): MachineReading | undefined {
    // Subscribe even on the first paint, before a request has bound this monitor to its owner.
    const value = this.values.get(row.key());
    if (!owner.connected || owner !== this.owner ||
      this.binding !== JSON.stringify([owner.connectionGeneration(), owner.scope(), owner.pairingRevision()])) return undefined;
    return value;
  }

  start(owner: ClientModel, roster: () => ResourceRow[]): Promise<void> {
    const generation = owner.connectionGeneration(), scope = owner.scope(), pairing = owner.pairingRevision();
    const binding = JSON.stringify([generation, scope, pairing]);
    if (this.owner !== owner || this.binding !== binding) this.stop();
    clearTimeout(this.timer);
    const seq = ++this.sequence;
    this.owner = owner; this.binding = binding;
    const current = (): boolean => this.sequence === seq && owner.connected && !owner.demo &&
      owner.owns(generation, scope) && owner.pairingRevision() === pairing;
    const unsupported = new Set<string>();
    const refresh = async (): Promise<void> => {
      if (!current()) return;
      const groups = new Map<string, ResourceRow[]>();
      for (const row of roster()) {
        const key = JSON.stringify([row.group || owner.profile.id, row.manager]);
        if (!groups.has(key)) groups.set(key, []);
        groups.get(key)!.push(row);
      }
      const next = new Map<string, MachineReading>();
      const tasks: Promise<void>[] = [];
      groups.forEach((rows: ResourceRow[], key: string) => {
        if (!unsupported.has(key)) tasks.push((async (): Promise<void> => {
          try {
            const view = await owner.viewFor(rows[0]);
            if (!current() || !view.connected) return;
            const gen = view.connectionGeneration(), space = view.scope(), epoch = view.pairingRevision();
            const response = await view.repo().call('/api/v1/metrics');
            if (!current() || !view.connected || !view.owns(gen, space) || view.pairingRevision() !== epoch) return;
            const samples = new Map<string, MachineReading>();
            for (const item of Wire.rows(response, 'machines')) {
              if (!Wire.bool(item, 'online')) continue;
              const sample = new MachineReading(Wire.object(item, 'latest'));
              if (sample.hasData()) samples.set(Wire.text(item, 'machine_id'), sample);
            }
            for (const row of rows) {
              const v = samples.get(row.id);
              if (v) next.set(row.key(), v);
            }
          } catch (error) {
            if ((error as HttpFailure).status === 404) unsupported.add(key);
          }
        })());
      });
      await Promise.all(tasks);
      if (!current()) return;
      this.values = next;
      this.timer = setTimeout(() => { refresh(); }, 10000);
    };
    return refresh();
  }
}
