import type { PersonaProject, ProjectWorktree, ProjectAgent } from './ProjectCatalog';
import { ResourceRow, Wire } from './Wire';

export class SessionSpaceTab {
  readonly id:string;
  readonly row:ResourceRow;
  readonly slot:string;
  readonly slotIndex:number;
  readonly pane:string;
  readonly paneIndex:number;
  readonly paneNumber:number;
  readonly agent:string;
  readonly updated:number;
  readonly state:string;
  label:string;
  starter:boolean = false;

  constructor(row:ResourceRow,data:Record<string,Object>) {
    this.row = row; this.id = row.session || row.id;
    this.slot = Wire.text(data,'slot_id'); this.slotIndex = Wire.number(data,'slot_index');
    this.pane = Wire.text(data,'pane_id'); this.paneIndex = Wire.number(data,'pane_index',Number.MAX_SAFE_INTEGER);
    const herdr = /:p([0-9a-z]+)$/i.exec(this.pane), suffix = /([0-9]+)$/.exec(this.pane);
    this.paneNumber = herdr ? parseInt(herdr[1],36) : suffix ? Number(suffix[1]) : Number.MAX_SAFE_INTEGER;
    const rawAgent = Wire.text(data,'agent_label') || Wire.text(data,'agent') || Wire.text(data,'launcher');
    this.agent = rawAgent === 'claude' ? 'Claude' : rawAgent === 'codex' ? 'Codex' : rawAgent;
    this.updated = Date.parse(Wire.text(data,'updated_at')) || 0;
    this.state = row.state || Wire.text(data,'status');
    const name = Wire.text(data,'slot_name');
    this.label = Wire.has(data,'slot_index') && (!name || name === String(this.slotIndex)) ? '#'+this.slotIndex : name || row.title || this.id;
  }
}

export class SessionSpace {
  key:string = '';
  current:string = '';
  title:string = '';
  tabs:SessionSpaceTab[] = [];
}

function compareText(a:string,b:string):number { return a < b ? -1 : a > b ? 1 : 0; }

// The roster belongs to the opened detail's Hub and assistant. Display names and local paths can be shared
// between machines and worktrees, so only the host's space identity joins tabs.
export function sessionSpace(selected:ResourceRow,roster:ResourceRow[],projects:PersonaProject[],group:string,manager:string):SessionSpace {
  const result = new SessionSpace(), machine = selected.machine;
  const owns = (row:ResourceRow):boolean => row.kind === 'session' && row.machine === machine &&
    (!row.group || row.group === group) && (!row.manager || row.manager === manager);
  if (!group || !machine || !owns(selected)) return result;
  const current = selected.session || selected.id;
  const live = roster.find((row:ResourceRow) => owns(row) && (row.session || row.id) === current);
  if (!live) return result;
  const entry = live.data(), space = Wire.text(entry,'space_id'), source = Wire.text(entry,'space_kind');
  result.current = current; result.title = Wire.text(entry,'space_name');
  if (!result.title) {
    const project = projects.find((p:PersonaProject) => p.trees.some((t:ProjectWorktree) =>
      t.group === group && t.manager === manager && t.machine === machine &&
      t.agents.some((a:ProjectAgent) => (a.row.session || a.row.id) === current)));
    result.title = project?.title || '';
  }
  if (!space || !current) return result;
  result.key = JSON.stringify([group,manager,machine,source,space]);
  const panes = new Map<string,SessionSpaceTab>();
  for (const row of roster) {
    if (!owns(row)) continue;
    const data = row.data();
    if (Wire.text(data,'space_id') !== space || Wire.text(data,'space_kind') !== source ||
      row.state === 'offline' || Wire.text(data,'status') === 'offline' || Wire.bool(data,'archived') || Wire.bool(data,'assistant')) continue;
    const tab = new SessionSpaceTab(row,data), key = tab.pane ? 'pane:'+tab.pane : 'session:'+tab.id;
    const last = panes.get(key);
    if (tab.id && (!last || tab.id === current || last.id !== current && tab.updated > last.updated)) panes.set(key,tab);
  }
  result.tabs = Array.from(panes.values()).sort((a:SessionSpaceTab,b:SessionSpaceTab) =>
    a.slotIndex-b.slotIndex || compareText(a.slot,b.slot) || a.paneIndex-b.paneIndex || a.paneNumber-b.paneNumber || compareText(a.id,b.id));
  const slots = new Map<string,SessionSpaceTab[]>();
  for (const tab of result.tabs) {
    if (!tab.slot) continue;
    const siblings = slots.get(tab.slot);
    if (siblings) siblings.push(tab); else slots.set(tab.slot,[tab]);
  }
  for (const siblings of slots.values()) {
    if (siblings.length < 2) continue;
    const starter = siblings.find((item:SessionSpaceTab) => item.paneIndex === 0);
    for (const tab of siblings) {
      tab.starter = tab === starter;
      if (!tab.agent) continue;
      const peers = siblings.filter((s:SessionSpaceTab) => s !== starter && s.agent === tab.agent);
      const index = peers.indexOf(tab);
      tab.label += ' · '+tab.agent+(index >= 0 && peers.length > 1 ? ' '+String(index+1) : '');
    }
  }
  return result;
}
