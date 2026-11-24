import type { ClientModel } from './ClientModel';
import type { HubRepository } from './HubRepository';
import { ResourceRow, Wire } from './Wire';

class RecentScreenStatus {
  key: string;
  request: Promise<Record<string, Object>>;
  until: number;
  constructor(key: string, request: Promise<Record<string, Object>>, until: number) {
    this.key = key; this.request = request; this.until = until;
  }
}

// A detail and its viewer share one status read within the same authenticated connection.
const recent = new WeakMap<HubRepository, RecentScreenStatus>();
const freshFor = 30_000;

export async function screenConnectionInfo(owner: ClientModel, machine: ResourceRow): Promise<Record<string, Object>> {
  const repository = owner.repo();
  const key = JSON.stringify([owner.connectionGeneration(), owner.scope(), machine.manager, machine.id,
    Wire.text(machine.data(), 'machine_uid')]);
  const now = Date.now();
  const previous = recent.get(repository);
  if (previous && previous.key === key && previous.until > now) return previous.request;
  const summary = Wire.object(machine.data(), 'computer');
  const age = now - Date.parse(Wire.text(summary, 'checked'));
  if (age >= 0 && age < freshFor && Wire.bool(summary, 'screen_sharing') && Wire.rows(summary, 'displays').length > 0) {
    return Promise.resolve(summary);
  }
  const request = repository.call('/api/v1/machines/' + encodeURIComponent(machine.id) + '/computer');
  const entry = new RecentScreenStatus(key, request, now + freshFor);
  recent.set(repository, entry);
  request.then((status: Record<string, Object>) => {
    if (!Wire.bool(status, 'screen_sharing') && recent.get(repository) === entry) recent.delete(repository);
  }).catch(() => { if (recent.get(repository) === entry) recent.delete(repository); });
  return request;
}
