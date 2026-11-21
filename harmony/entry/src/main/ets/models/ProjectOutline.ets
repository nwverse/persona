import { PersonaProject, ProjectAgent, ProjectBrowserState, ProjectWorktree } from './ProjectCatalog';

export class ProjectLine {
  kind: string;
  key: string;
  project: PersonaProject;
  tree?: ProjectWorktree;
  agent?: ProjectAgent;
  tabLead?: ProjectAgent;
  tabTitle: string = '';
  paneName: string = '';
  starter: boolean = false;
  level: number;
  open: boolean = false;
  count: number = 0;
  first: boolean = false;
  last: boolean = false;
  constructor(kind:string,key:string,project:PersonaProject,level:number = 0,tree?:ProjectWorktree,agent?:ProjectAgent) {
    this.kind = kind; this.key = kind + '/' + key; this.project = project; this.tree = tree; this.agent = agent; this.level = level;
  }
}

export class ProjectHost {
  key: string;
  name: string;
  projects: PersonaProject[] = [];
  constructor(project:PersonaProject) { this.key = project.hostKey; this.name = project.machine; }
}

/** Stable full host identities keep different Hubs' computers distinct, even if both are named “MacBook”. */
export function projectHosts(projects:PersonaProject[]): ProjectHost[] {
  const hosts = new Map<string,ProjectHost>();
  for (const project of projects) {
    let host = hosts.get(project.hostKey);
    if (!host) { host = new ProjectHost(project); hosts.set(project.hostKey,host); }
    host.projects.push(project);
  }
  return Array.from(hosts.values());
}

function unstructuredIdle(p:PersonaProject): boolean {
  return p.activeCount === 0 && p.pinnedAt === 0 && p.trees.every((t:ProjectWorktree) =>
    (!t.source || t.source === 'session-directory') && !t.children.length);
}

class ProjectMatch {
  all: boolean;
  own: Set<string> = new Set();
  visible: Set<string> = new Set();
  agents: Map<string,ProjectAgent[]> = new Map();
  constructor(p:PersonaProject,query:string) {
    this.all = !query || [p.title,p.machine].join(' ').toLocaleLowerCase().includes(query);
    if (this.all) return;
    const byKey = new Map<string,ProjectWorktree>();
    for (const tree of p.trees) byKey.set(tree.key,tree);
    for (const t of p.trees) {
      const own = [t.title,t.branch,t.cwd,t.projectTitle].join(' ').toLocaleLowerCase().includes(query);
      if (own) this.own.add(t.key);
      const matching = t.agents.filter((a:ProjectAgent) => a.search.includes(query));
      this.agents.set(t.key,matching);
      if (own || matching.length) {
        let current: ProjectWorktree | undefined = t;
        while (current && !this.visible.has(current.key)) {
          this.visible.add(current.key); current = byKey.get(current.parentKey);
        }
      }
    }
  }
}

export function projectOutline(projects:PersonaProject[], state:ProjectBrowserState, focus:string = ''): ProjectLine[] {
  const result:ProjectLine[] = [];
  const query = focus ? '' : state.query.trim().toLocaleLowerCase();
  const groups = projectHosts(focus ? projects.filter((p:PersonaProject) => p.key === focus) : projects);
  const matches = new Map<string,ProjectMatch>();
  for (const host of groups) {
    if (!focus && state.machine && state.machine !== host.key) continue;
    const visible = host.projects.filter((p:PersonaProject) => {
      const match = new ProjectMatch(p,query); matches.set(p.key,match);
      return match.all || !!match.visible.size;
    });
    if (!visible.length) continue;
    if (!focus && !state.machine && groups.length > 1) {
      const header = new ProjectLine('host',host.key,visible[0]);
      header.open = !!query || state.open(header.key,true);
      header.count = visible.reduce((n:number,p:PersonaProject) => n + p.sessionCount,0);
      result.push(header);
      if (!header.open) continue;
    }
    const active = visible.filter((p:PersonaProject) => !unstructuredIdle(p));
    const idle = visible.filter(unstructuredIdle);
    for (const p of (focus || query ? visible : active.concat(idle))) {
      if (!focus && !query && idle[0] === p) result.push(new ProjectLine('idle-projects',host.key,p));
      appendProject(result,p,state,matches.get(p.key)!,!!query,!!focus);
    }
  }
  return result;
}

function appendProject(out:ProjectLine[], p:PersonaProject, state:ProjectBrowserState, match:ProjectMatch, search:boolean, focus:boolean):void {
  const start = out.length;
  const quiet = unstructuredIdle(p);
  if (!focus) {
    const header = new ProjectLine('project',p.key,p);
    header.open = search || state.open(header.key,!quiet); header.count = p.sessionCount;
    out.push(header);
    if (!header.open) { header.first = true; header.last = true; return; }
  }
  if (p.flat && !focus) appendAgents(out,p,p.roots[0],state,match.all ? p.roots[0].agents : match.agents.get(p.roots[0].key) || [],1,search,quiet);
  else {
    const level = focus ? 0 : 1;
    for (const root of p.roots) appendTree(out,p,root,state,match,match.all,search,quiet,level);
  }
  if (out.length > start) { out[start].first = true; out[out.length - 1].last = true; }
}

function appendTree(out:ProjectLine[],p:PersonaProject,t:ProjectWorktree,state:ProjectBrowserState,match:ProjectMatch,
  inherited:boolean,search:boolean,quietRepo:boolean,level:number):void {
  if (!inherited && !match.visible.has(t.key)) return;
  const all = inherited || match.own.has(t.key);
  const line = new ProjectLine('tree',t.key,p,level,t);
  line.open = search || state.open(line.key,quietRepo || t.activeCount > 0 || t.pinnedAt > 0);
  line.count = t.agents.length; out.push(line);
  if (!line.open) return;
  const agents = all ? t.agents : match.agents.get(t.key) || [];
  appendAgents(out,p,t,state,agents,level + 1,search,t.activeCount === 0);
  const children = t.children.filter((child:ProjectWorktree) => all || match.visible.has(child.key));
  if (!children.length) return;
  const header = new ProjectLine('children',t.key,p,level + 1,t);
  header.count = children.length; header.open = search || state.open(header.key,false);
  out.push(header);
  if (header.open) for (const child of children) appendTree(out,p,child,state,match,all,search,false,level + 1);
}

// A tab occupies the position of its first session in the list. Its members follow their host pane order.
function tabEntries(agents: ProjectAgent[]): ProjectAgent[][] {
  const entries: ProjectAgent[][] = [], slots = new Map<string, ProjectAgent[]>();
  for (const agent of agents) {
    const existing = agent.tabKey ? slots.get(agent.tabKey) : undefined;
    if (existing) existing.push(agent);
    else {
      const entry = [agent];
      entries.push(entry);
      if (agent.tabKey) slots.set(agent.tabKey, entry);
    }
  }
  for (const entry of entries) {
    if (entry.length > 1) entry.sort((a: ProjectAgent, b: ProjectAgent) =>
      a.pane!.paneIndex - b.pane!.paneIndex || a.pane!.paneNumber - b.pane!.paneNumber ||
        (a.pane!.id < b.pane!.id ? -1 : a.pane!.id > b.pane!.id ? 1 : 0));
  }
  return entries;
}

const tabUrgency: Map<string, number> = new Map<string, number>([
  ['needs_you', 0], ['unread', 1], ['working', 2], ['idle', 3], ['unknown', 4], ['offline', 5]
]);

function appendEntry(out: ProjectLine[], p: PersonaProject, tree: ProjectWorktree, state: ProjectBrowserState,
  members: ProjectAgent[], level: number, search: boolean, matches?: Set<ProjectAgent>): void {
  const first = members[0];
  if (members.length === 1) {
    out.push(new ProjectLine('session', first.row.key(), p, level, tree, first));
    return;
  }
  const header = new ProjectLine('tab', JSON.stringify([tree.key, first.tabKey]), p, level, tree);
  header.tabTitle = first.tabTitle;
  header.tabLead = members.reduce((best: ProjectAgent, next: ProjectAgent) =>
    (tabUrgency.get(next.state) ?? 4) < (tabUrgency.get(best.state) ?? 4) ? next : best);
  header.count = members.length;
  header.open = search || state.open(header.key, true);
  out.push(header);
  if (!header.open) return;
  const starter = members.find((agent: ProjectAgent) => agent.pane!.paneIndex === 0);
  const totals = new Map<string, number>(), seen = new Map<string, number>();
  for (const agent of members) {
    const provider = agent.pane!.agent;
    if (agent !== starter && provider) totals.set(provider, (totals.get(provider) || 0) + 1);
  }
  for (const agent of members) {
    const provider = agent.pane!.agent;
    const numbered = agent !== starter && (totals.get(provider) || 0) > 1;
    if (numbered) seen.set(provider, (seen.get(provider) || 0) + 1);
    if (matches && !matches.has(agent)) continue;
    const row = new ProjectLine('session', agent.row.key(), p, level + (starter && agent !== starter ? 1 : 0), tree, agent);
    row.paneName = numbered ? provider + ' ' + String(seen.get(provider)) : provider;
    row.starter = agent === starter;
    out.push(row);
  }
}

function appendAgents(out:ProjectLine[],p:PersonaProject,t:ProjectWorktree,state:ProjectBrowserState,agents:ProjectAgent[],
  level:number,search:boolean,includeIdle:boolean):void {
  const matches = search && agents !== t.agents ? new Set<ProjectAgent>(agents) : undefined;
  const entries = tabEntries(t.agents).filter((entry: ProjectAgent[]) => !matches || entry.some((a: ProjectAgent) => matches.has(a)));
  // Keep idle panes next to active tab mates. A shared tab can appear in only one section.
  const active = search || includeIdle ? entries : entries.filter((entry: ProjectAgent[]) => entry.some((a: ProjectAgent) => a.active));
  const limit = active.length > 25 && !state.unlimited.includes(t.key) ? 20 : active.length;
  for (const entry of active.slice(0,limit)) appendEntry(out,p,t,state,entry,level,search,matches);
  if (limit < active.length) {
    const more = new ProjectLine('more',t.key,p,level,t);
    more.count = active.reduce((n: number, entry: ProjectAgent[]) => n + entry.filter((a: ProjectAgent) => !matches || matches.has(a)).length, 0);
    out.push(more);
  }
  if (search || includeIdle || active.length === entries.length) return;
  const idle = entries.filter((entry: ProjectAgent[]) => !entry.some((a: ProjectAgent) => a.active));
  const header = new ProjectLine('idle-agents',t.key,p,level,t);
  header.count = idle.reduce((n: number, entry: ProjectAgent[]) => n + entry.length, 0);
  header.open = state.open(header.key,false); out.push(header);
  if (header.open) for (const entry of idle) appendEntry(out,p,t,state,entry,level,false);
}

export function worktreeSessionLines(project: PersonaProject, tree: ProjectWorktree, state: ProjectBrowserState): ProjectLine[] {
  const rows: ProjectLine[] = [];
  for (const entry of tabEntries(tree.agents)) appendEntry(rows,project,tree,state,entry,0,false);
  return rows;
}
