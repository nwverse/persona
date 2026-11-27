import { ResourceRow, Wire } from './Wire';

type ReadArchive = (path: string) => Promise<Record<string, Object>>;

/** The archived directory of one Hub/manager pair. Lists may refresh all machines or just one. */
@ObservedV2
export class SessionArchive {
  @Trace rows: ResourceRow[] = [];
  private request: number = 0;
  private allRead: number = 0;
  private machineReads = new Map<string, number>();

  has(machine: string): boolean { return this.allRead > 0 || this.machineReads.has(machine); }

  async read(fetch: ReadArchive, group: string, manager: string, machine: string, current: () => boolean): Promise<ResourceRow[]> {
    const revision = ++this.request;
    const params: string[] = [];
    if (machine) params.push('machine_id=' + encodeURIComponent(machine));
    if (manager) params.push('manager=' + encodeURIComponent(manager));
    const data = await fetch('/api/v1/sessions/archived' + (params.length ? '?' + params.join('&') : ''));
    if (!current()) return [];
    if (!Array.isArray(data['sessions'])) throw new Error('incomplete_sessions');
    const rows: ResourceRow[] = [];
    for (const entry of Wire.rows(data, 'sessions')) {
      if (Wire.bool(entry, 'assistant') || (Wire.has(entry, 'archived') && !Wire.bool(entry, 'archived'))) continue;
      // Older archive responses identify archived sessions by the collection alone.
      entry['archived'] = true;
      const row = ResourceRow.from(entry, 'session');
      if (!row.manager) row.manager = manager;
      if (row.manager !== manager || !row.machine || !row.id || (machine && row.machine !== machine)) continue;
      row.group = group;
      rows.push(row);
    }
    if (machine) {
      if (revision > Math.max(this.allRead, this.machineReads.get(machine) || 0)) {
        this.machineReads.set(machine, revision);
        this.rows = ResourceRow.reconcile(this.rows, this.rows.filter(row => row.machine !== machine).concat(rows));
      }
      return this.rows.filter(row => row.machine === machine);
    }
    if (revision > this.allRead) {
      this.allRead = revision;
      const newer = (row: ResourceRow): boolean => (this.machineReads.get(row.machine) || 0) > revision;
      this.rows = ResourceRow.reconcile(this.rows, rows.filter(row => !newer(row)).concat(this.rows.filter(newer)));
      this.machineReads.forEach((version: number, id: string) => { if (version <= revision) this.machineReads.delete(id); });
    }
    return this.rows;
  }
}
