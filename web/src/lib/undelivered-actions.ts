// Actions on kept messages, loaded with their optional chat bubble rather than the startup send path.
import { api, enc } from "./api";
import { getSession, upsertSession } from "./store";

function dropLocally(mid: string, sid: string, id: string) {
  const row = getSession(mid, sid);
  if (!row?.undelivered) return;
  upsertSession({ ...row, undelivered: row.undelivered.filter((m) => m.id !== id) });
}

/** Retry on one that gave up: the manager tries it again from now. */
export async function retryUndelivered(mid: string, sid: string, id: string): Promise<void> {
  await api(`/assistant/conversations/${enc(sid)}/undelivered/${enc(id)}/retry`, { method: "POST", json: {} });
  const row = getSession(mid, sid);
  if (row?.undelivered) {
    upsertSession({ ...row, undelivered: row.undelivered.map((m) => (m.id === id ? { ...m, state: "waiting" as const, error: undefined } : m)) });
  }
}

/** Delete: it is never sent. */
export async function deleteUndelivered(mid: string, sid: string, id: string): Promise<void> {
  await api(`/assistant/conversations/${enc(sid)}/undelivered/${enc(id)}`, { method: "DELETE" });
  dropLocally(mid, sid, id);
}
