import type { ClientModel } from './ClientModel';
import { Wire } from './Wire';

export class LaunchChoice {
  id: string;
  title: string;
  detail: string;
  disabled: boolean;
  description: string;
  constructor(id: string, title: string, detail: string = '', disabled: boolean = false, description: string = '') {
    this.id = id; this.title = title; this.detail = detail; this.disabled = disabled; this.description = description;
  }
}
export class RecentAgentSetup {
  machine: string = '';
  cwd: string = '';
  tree: string = '';
  launcher: string = '';
  model: string = '';
  effort: string = '';
  preset: string = '';
  get key(): string { return JSON.stringify([this.machine,this.cwd,this.tree,this.launcher,this.model,this.effort]); }
}
export function loadAgentSetups(raw: string): RecentAgentSetup[] {
  return Wire.rows(Wire.parse(raw),'entries').slice(0,8).map((v:Record<string,Object>) => {
    const s = new RecentAgentSetup();
    s.machine = Wire.text(v,'machine'); s.cwd = Wire.text(v,'cwd'); s.tree = Wire.text(v,'tree');
    s.launcher = Wire.text(v,'launcher'); s.model = Wire.text(v,'model'); s.effort = Wire.text(v,'effort'); s.preset = Wire.text(v,'preset');
    return s;
  }).filter((s:RecentAgentSetup) => !!s.machine);
}
export function rememberAgentSetup(previous: RecentAgentSetup[], item: RecentAgentSetup): RecentAgentSetup[] {
  return [item].concat(previous.filter((v:RecentAgentSetup) => v.key !== item.key)).slice(0,8);
}

// The picker can outlive a machine change while the node is answering. Keep reads bound to
// their original machine, owner and connection generation; a delayed reply cannot repoint Start.
@ObservedV2
export class AgentLaunchChoices {
  @Trace loading: boolean = false;
  @Trace error: string = '';
  @Trace path: string = '';
  @Trace parent: string = '';
  @Trace directories: LaunchChoice[] = [];
  @Trace recentDirectories: LaunchChoice[] = [];
  @Trace models: LaunchChoice[] = [];
  @Trace locked: string = '';
  @Trace acceptsTypedModel: boolean = false;
  @Trace effortNames: string[] = [];
  private modelData: Record<string,Object>[] = [];
  private revision: number = 0;

  clear(): void {
    this.revision++; this.loading = false; this.error = ''; this.path = ''; this.parent = '';
    this.directories = []; this.recentDirectories = []; this.models = []; this.locked = ''; this.effortNames = []; this.modelData = []; this.acceptsTypedModel = false;
  }
  cancelRead(): void { this.revision++; this.loading = false; }
  async folder(owner:ClientModel,machine:string,path:string): Promise<void> {
    const rev = ++this.revision, epoch = owner.connectionGeneration(), scope = owner.scope();
    const valid = (): boolean => this.revision === rev && owner.owns(epoch,scope);
    this.loading = true; this.error = ''; this.path = ''; this.parent = ''; this.directories = []; this.recentDirectories = [];
    try {
      const value = await owner.repo().call('/api/v1/machines/' + encodeURIComponent(machine) + '/dirs?path=' + encodeURIComponent(path.trim()));
      if (!valid()) return;
      const resolved = Wire.text(value,'path');
      if (!resolved) throw new Error('phone_directory_read_failed');
      this.path = resolved; this.parent = Wire.text(value,'parent');
      this.directories = Wire.rows(value,'entries').filter((v:Record<string,Object>) => !!Wire.text(v,'path'))
        .map((v:Record<string,Object>) => new LaunchChoice(Wire.text(v,'path'),Wire.text(v,'name') || Wire.text(v,'path'),Wire.bool(v,'is_git') ? 'Git' : ''));
      this.recentDirectories = Wire.rows(value,'recent').filter((v:Record<string,Object>) => !!Wire.text(v,'path'))
        .map((v:Record<string,Object>) => new LaunchChoice(Wire.text(v,'path'), Wire.text(v,'name') || Wire.text(v,'path'), Wire.text(v,'path')));
    } catch (_) { if (valid()) this.error = 'phone_directory_read_failed'; }
    finally { if (valid()) this.loading = false; }
  }
  async loadModels(owner:ClientModel,machine:string,launcher:string): Promise<void> {
    this.clear();
    if (!launcher) return;
    const rev = this.revision, scope = owner.scope(), epoch = owner.connectionGeneration();
    const valid = (): boolean => rev === this.revision && owner.owns(epoch,scope);
    this.loading = true;
    try {
      const value = await owner.repo().call('/api/v1/machines/' + encodeURIComponent(machine) + '/launchers/' +
        encodeURIComponent(launcher) + '/models?purpose=agent.new');
      if (!valid()) return;
      this.locked = Wire.text(value,'locked');
      this.acceptsTypedModel = !this.locked && ['claude','codex','pi','cursor','agy'].includes(Wire.text(value,'agent'));
      this.modelData = Wire.rows(value,'models').filter((v:Record<string,Object>) => !!Wire.text(v,'id'));
      // The host default is already represented by the first, empty-ID choice in the phone form.
      // Choosing the default keeps that semantic even after a launcher changes its default model.
      this.models = this.modelData.filter((v:Record<string,Object>) => !Wire.bool(v,'default') && !Wire.bool(v,'is_default'))
        .map((v:Record<string,Object>) => new LaunchChoice(Wire.text(v,'id'),Wire.text(v,'name') || Wire.text(v,'id'),
          launcher+' · '+Wire.text(v,'id'),false,Wire.text(v,'description')));
      this.updateEfforts('');
    } catch (_) { if (valid()) this.error = 'flow_models_offline'; }
    finally { if (valid()) this.loading = false; }
  }
  selectedModelId(id:string): string {
    const selected = this.modelData.find((m:Record<string,Object>) => Wire.text(m,'id') === id);
    return selected && (Wire.bool(selected,'is_default') || Wire.bool(selected,'default')) ? '' : id;
  }
  defaultModelName():string {
    const model = this.modelData.find((m:Record<string,Object>) => Wire.bool(m,'is_default') || Wire.bool(m,'default'));
    return model ? Wire.text(model,'name') || Wire.text(model,'id') : '';
  }
  updateEfforts(id:string): void {
    const model = id ? this.modelData.find((m:Record<string,Object>) => Wire.text(m,'id') === id) :
      this.modelData.find((m:Record<string,Object>) => Wire.bool(m,'is_default') || Wire.bool(m,'default'));
    const available: string[] = [];
    for (const row of model ? [model] : id ? [] : this.modelData) {
      for (const value of Wire.strings(row,'efforts')) if (!available.includes(value)) available.push(value);
    }
    this.effortNames = available;
  }
}
