import type { ClientModel } from './ClientModel';
import { ResourceRow, Wire } from './Wire';

export type RelationKind = 'side' | 'subtask';

function sessionKey(row: ResourceRow, id: string = row.session || row.id): string {
  return JSON.stringify([row.group, row.manager, row.machine, id]);
}

// A stopped session can have background work on a connected machine. Only a disconnected
// owner or an explicitly offline machine invalidates the activity reported for its whole family.
export function unavailableSessionSources(owners: ClientModel[]): string[] {
  const sources: string[] = [];
  for (const owner of owners) {
    const group = owner.profile.id || '', manager = owner.profile.manager || '';
    if (!owner.connected) {
      sources.push(JSON.stringify([group, manager]));
    } else {
      for (const machine of owner.machines) {
        if (machine.state === 'offline' || !Wire.bool(machine.data(), 'online', true)) {
          sources.push(JSON.stringify([group, manager, machine.id]));
        }
      }
    }
  }
  return sources.sort();
}

interface RelationData {
  parent: string;
  root: string;
  folded: string;
  kind: RelationKind;
  visible: boolean;
  agents: ResourceRow[];
}

const metadata = new WeakMap<ResourceRow, RelationData>();
const revisions = new WeakMap<ResourceRow, string>();

function read(row: ResourceRow): RelationData {
  const cached = metadata.get(row);
  if (cached && revisions.get(row) === row.raw) return cached;
  const raw = row.data(), id = row.session || row.id;
  const side = Wire.text(raw, 'parent_agent_id');
  const worker = Wire.text(raw, 'folded_as') === 'worker';
  const record: RelationData = {
    parent: side || Wire.text(raw, 'parent_session_id') || Wire.text(raw, 'asking_parent_session_id'),
    root: Wire.text(raw, 'root_session_id'), folded: worker ? '' : Wire.text(raw, 'folded_under'),
    kind: side || Wire.text(raw, 'folded_as') === 'side' ? 'side' : 'subtask',
    visible: !Wire.bool(raw, 'hidden_by_directory') && !Wire.bool(raw, 'deleted'), agents: []
  };
  if (record.visible) {
    const archived = Wire.bool(raw, 'archived');
    // The Hub can already have marked this source offline while its last process report still says working.
    const stale = row.state === 'offline' && (Wire.text(raw, 'state', row.state) !== 'offline' ||
      ['working', 'blocked'].includes(Wire.text(raw, 'status')));
    const agents = Wire.rows(raw, 'subagents').concat(Wire.rows(raw, 'workflows').flatMap(flow => Wire.rows(flow, 'agents')));
    for (const agent of agents) {
      const agentId = Wire.text(agent, 'id'), sid = Wire.text(agent, 'session_id');
      if (!agentId || sid !== id + '@' + agentId) continue;
      const status = Wire.text(agent, 'status');
      const running = Wire.bool(agent, 'running', status === 'running');
      const asking = Wire.text(agent, 'state') === 'needs_you';
      const caps: Record<string,Object> = {'read':true};
      const data: Record<string,Object> = {
        'session_id':sid, 'machine_id':row.machine, 'manager':row.manager, 'cwd':row.path, 'agent':Wire.text(raw, 'agent', 'claude'),
        'title':Wire.text(agent, 'description') || Wire.text(agent, 'label') || Wire.text(agent, 'type') || agentId,
        'last_text':Wire.text(agent, 'last_text'), 'updated_at':Wire.text(agent, 'updated_at'), 'model':Wire.text(agent, 'model'),
        'state':archived || stale ? 'offline' : asking ? 'needs_you' : running ? 'working' : 'idle',
        'status':archived || stale ? 'offline' : running ? 'working' : status || 'done', 'archived':archived,
        'parent_session_id':id, 'is_subagent':true, 'source_kind':'subagent_transcript', 'read_only':true, 'capabilities':caps
      };
      const child = ResourceRow.from(data, 'session');
      child.group = row.group;
      record.agents.push(child);
    }
  }
  metadata.set(row, record); revisions.set(row, row.raw);
  return record;
}

export class SessionRelations {
  readonly rows: ResourceRow[];
  private byId = new Map<string,ResourceRow>();
  private roots = new Map<string,ResourceRow>();
  private members = new Map<string,ResourceRow[]>();
  constructor(rows: ResourceRow[], unavailable: string[] = []) {
    const offline = new Set(unavailable);
    const effective = (row: ResourceRow): ResourceRow => {
      if (!offline.size || row.state === 'offline') return row;
      return offline.has(JSON.stringify([row.group, row.manager])) || offline.has(JSON.stringify([row.group, row.manager, row.machine]))
        ? row.copy(row.group, 'offline') : row;
    };
    this.rows = rows.map(effective);
    for (const row of this.rows) {
      if (row.machine && row.kind === 'session' && read(row).visible) this.byId.set(sessionKey(row), row);
    }
    // Prefer full directory rows, then ordinary agents, then agents reported only by a workflow.
    for (const row of rows) {
      if (!this.byId.has(sessionKey(row))) continue;
      for (const agent of read(row).agents) {
        const key = sessionKey(agent);
        if (this.byId.has(key)) continue;
        const child = effective(agent);
        this.byId.set(key, child); this.rows.push(child);
      }
    }
    // A null result is a known cycle. Every path entering it must stay independent,
    // including paths encountered after the cycle itself has already been visited.
    const resolved = new Map<string, ResourceRow | null>();
    for (const row of this.byId.values()) {
      const visited = new Set<string>();
      let node: ResourceRow | null = row;
      while (node) {
        const key = sessionKey(node);
        if (resolved.has(key)) { node = resolved.get(key) ?? null; break; }
        if (visited.has(key)) { node = null; break; }
        visited.add(key);
        const meta = read(node), self = node.session || node.id;
        let parent: ResourceRow | undefined;
        for (const id of [meta.parent, meta.folded, meta.root]) {
          if (id && id !== self) parent = this.byId.get(sessionKey(node, id));
          if (parent) break;
        }
        if (!parent) break;
        node = parent;
      }
      for (const key of visited) resolved.set(key, node);
    }
    resolved.forEach((root: ResourceRow | null, key: string) => {
      const row = this.byId.get(key)!;
      if (!root || row === root) return;
      this.roots.set(key, root);
      const rootKey = sessionKey(root), list = this.members.get(rootKey) || [];
      list.push(row); this.members.set(rootKey, list);
    });
  }
  find(row: ResourceRow): ResourceRow | undefined { return this.byId.get(sessionKey(row)); }
  root(row: ResourceRow): ResourceRow | undefined { return this.roots.get(sessionKey(row)); }
  children(row: ResourceRow): ResourceRow[] { return this.members.get(sessionKey(row)) || []; }
  kind(row: ResourceRow): RelationKind { return read(row).kind; }
}

interface DirectorySnapshot { availability: string; graph: SessionRelations }
const directories = new WeakMap<ResourceRow[], DirectorySnapshot>();
export function sessionRelations(rows: ResourceRow[], unavailable: string[] = []): SessionRelations {
  const availability = JSON.stringify(unavailable), snapshot = directories.get(rows);
  if (snapshot && snapshot.availability === availability) return snapshot.graph;
  const graph = new SessionRelations(rows, unavailable);
  directories.set(rows, {availability, graph});
  return graph;
}
