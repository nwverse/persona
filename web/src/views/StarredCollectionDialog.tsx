import { useState } from "react";
import { t } from "../i18n";
import { changeStarCollections, reloadStarCollections, type StarCollectionChange, type StarOrganization } from "../lib/starred-collections";
import type { Star } from "../lib/starred-rows";
import { Button, Dialog, toast } from "../ui/atoms";

export const starredFieldClass = "h-10 w-full min-w-0 rounded-control bg-field px-3 text-meta text-ink shadow-inset-field outline-none focus:shadow-[inset_0_0_0_1px_var(--accent)] disabled:opacity-60";

export type StarCollectionAction =
  | { type: "create" }
  | { type: "rename"; id: string; name: string }
  | { type: "delete"; id: string; name: string }
  | { type: "move"; ids: readonly string[]; bulk?: boolean };

export function StarredCollectionDialog({ action, organization, scope, canWrite, busy, stars, onClose, onSaved }: {
  action: StarCollectionAction;
  organization: StarOrganization;
  scope: number;
  canWrite: boolean;
  busy: boolean;
  stars: readonly Star[];
  onClose: () => void;
  onSaved: () => void;
}) {
  const [name, setName] = useState(action.type === "rename" ? action.name : "");
  const [destination, setDestination] = useState("");
  const [revision, setRevision] = useState(organization.revision);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState(false);
  const [reviewing, setReviewing] = useState(false);
  const [freshFailed, setFreshFailed] = useState(false);
  const outdated = conflict || revision !== organization.revision;
  const blocked = busy || reviewing || !canWrite;
  const target = "id" in action ? organization.collections.find((item) => item.id === action.id) : undefined;
  const destinationExists = destination === "unfiled" || organization.collections.some((item) => item.id === destination);
  const ids = new Set(stars.map((star) => star.id));
  const unavailable = ("id" in action && !target) || (action.type === "move" && action.ids.some((id) => !ids.has(id)));
  const cleanName = name.trim();
  const nameLength = [...cleanName].length;
  const acceptableName = nameLength > 0 && nameLength <= 64;
  const ready = !blocked && !outdated && !unavailable && (
    action.type === "move" ? destinationExists : action.type === "delete" || acceptableName
  );

  const title = t(action.type === "create" ? "starred.new_collection" : action.type === "rename" ? "starred.rename_collection"
    : action.type === "delete" ? "starred.delete_collection" : action.bulk ? "starred.move_messages" : "starred.move_message");
  const button = t(action.type === "create" ? "starred.create_collection" : action.type === "rename" ? "common.save"
    : action.type === "delete" ? "starred.delete_collection" : "starred.move");

  const review = async () => {
    setReviewing(true);
    setFreshFailed(false);
    const fresh = await reloadStarCollections();
    if (fresh) {
      setRevision(fresh.revision);
      setConflict(false);
      setError("");
    } else {
      setFreshFailed(true);
    }
    setReviewing(false);
  };

  const submit = async () => {
    if (!ready) return;
    setError("");
    let change: StarCollectionChange;
    switch (action.type) {
      case "create": change = { type: "create", name: cleanName }; break;
      case "rename": change = { type: "rename", id: action.id, name: cleanName }; break;
      case "delete": change = { type: "delete", id: action.id }; break;
      case "move": change = { type: "assign", ids: action.ids, collection: destination === "unfiled" ? null : destination }; break;
    }
    const result = await changeStarCollections(change, revision, scope);
    if (result.status === "saved") {
      toast(t(action.type === "move" ? "starred.moved" : action.type === "delete" ? "starred.collection_deleted" : "starred.collection_saved"), "ok");
      onSaved();
    } else if (result.status === "failed") {
      setError(result.error);
    } else if (result.status === "conflict") {
      setConflict(true);
    }
  };

  return (
    <Dialog open onClose={() => { if (!busy && !reviewing) onClose(); }} title={title} footer={<>
      <Button tone="quiet" disabled={busy || reviewing} onClick={onClose}>{t("common.cancel")}</Button>
      <Button tone={action.type === "delete" ? "danger" : "primary"} busy={busy} disabled={!ready} onClick={() => void submit()}>{button}</Button>
    </>}>
      <form className="flex flex-col gap-3" onSubmit={(event) => { event.preventDefault(); void submit(); }}>
        {action.type === "delete" && <p className="text-meta text-ink-2">
          {t("starred.delete_collection_detail", { name: target?.name ?? action.name })}
        </p>}
        {action.type === "rename" && target && target.name !== action.name && <p className="text-label text-ink-2">
          {t("starred.current_collection_name", { name: target.name })}
        </p>}
        {(action.type === "create" || action.type === "rename") && <label className="block text-label text-ink-2">
          <span className="mb-1 block">{t("starred.collection_name")}</span>
          <input className={starredFieldClass} value={name} maxLength={128} disabled={blocked}
            aria-invalid={nameLength > 64 || undefined} onChange={(event) => setName(event.target.value)} />
        </label>}
        {nameLength > 64 && <p role="alert" className="text-label text-red">{t("starred.collection_name_limit")}</p>}
        {action.type === "move" && <label className="block text-label text-ink-2">
          <span className="mb-1 block">{t("starred.move_to")}</span>
          <select className={starredFieldClass} value={destinationExists ? destination : ""} disabled={blocked} onChange={(event) => setDestination(event.target.value)}>
            <option value="" disabled>{t("starred.choose_collection")}</option>
            <option value="unfiled">{t("starred.unfiled")}</option>
            {organization.collections.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
          </select>
        </label>}
        {unavailable && <p role="alert" className="text-label text-red">{t(action.type === "move" ? "starred.message_unavailable" : "starred.collection_unavailable")}</p>}
        {outdated && !unavailable && <div className="flex flex-wrap items-center gap-2">
          <p role="alert" className="min-w-0 flex-1 text-label text-ink-2">{t(freshFailed ? "starred.collection_reload_failed" : "starred.collection_conflict")}</p>
          <Button busy={reviewing} disabled={busy} onClick={() => void review()}>{t("starred.review_collections")}</Button>
        </div>}
        {error && !outdated && <p role="alert" className="text-label text-red">{error}</p>}
      </form>
    </Dialog>
  );
}
