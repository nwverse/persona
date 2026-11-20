import { useEffect, useSyncExternalStore } from "react";
import { t } from "../i18n";
import { api, ApiError, credentialOwner, errorText, getToken, subscribeCredentialOwner } from "./api";
import { chosenManager, defaultKnown, onDefaultManager, onManagerChoice, shownManager, stampedHeaders } from "./manager-choice";
import { onHubEvent } from "./store";

const PATH = "/assistant/starred/collections";

export interface StarCollection {
  id: string;
  name: string;
}

export interface StarOrganization {
  revision: number;
  can_write: boolean;
  collections: readonly StarCollection[];
  assignments: Readonly<Record<string, string>>;
}

export type StarCollectionChange =
  | { type: "create"; name: string }
  | { type: "rename"; id: string; name: string }
  | { type: "delete"; id: string }
  | { type: "assign"; ids: readonly string[]; collection: string | null };

type Organization = {
  status: "loading" | "ready" | "unsupported" | "failed";
  error: string;
  data?: StarOrganization;
  writing: boolean;
  scope: number;
};

let manager = "";
let scope = 0;
let readSequence = 0;
let writing: symbol | undefined;
let users = 0;
let stop: (() => void)[] | undefined;
const blank = (): Organization => ({ status: "loading", error: "", writing: !!writing, scope });
let state = blank();
const listeners = new Set<() => void>();
const subscribe = (listener: () => void) => (listeners.add(listener), () => void listeners.delete(listener));
const snapshot = () => state;

function publish(next: Organization) {
  state = next;
  listeners.forEach((listener) => listener());
}

function request() {
  const credential = credentialOwner();
  const at = scope;
  const id = shownManager();
  const current = () => at === scope && id === shownManager() && credential === credentialOwner();
  return {
    current,
    options: { token: getToken(), tokenOwner: credential, headers: stampedHeaders(id), isAuthContextCurrent: current },
  };
}

function publishData(data: StarOrganization) {
  if (data.revision < (state.data?.revision ?? 0)) return;
  publish({ ...state, status: "ready", data, error: "" });
}

export async function reloadStarCollections() {
  if (!users || !(defaultKnown() || chosenManager() !== null) || !getToken()) return;
  const owner = request();
  const n = ++readSequence;
  const current = () => n === readSequence && owner.current() && users > 0;
  try {
    const data = await api<StarOrganization>(PATH, owner.options);
    if (current()) {
      publishData(data);
      return state.data;
    }
  } catch (error) {
    if (!current()) return;
    if (error instanceof ApiError && error.status === 404) {
      publish({ ...state, status: "unsupported", data: undefined, error: "" });
    } else {
      publish({ ...state, status: state.data ? "ready" : "failed", error: errorText(error) });
    }
  }
}

function resetOwner() {
  scope++;
  manager = shownManager();
  writing = undefined;
  publish(blank());
  void reloadStarCollections();
}

export function useStarOrganization() {
  useEffect(() => {
    if (users++ === 0) {
      manager = shownManager();
      stop = [
        onHubEvent("hello", reloadStarCollections),
        onHubEvent("assistant_starred", reloadStarCollections),
        onManagerChoice(() => { if (manager !== shownManager()) resetOwner(); }),
        onDefaultManager((previous) => { if (!previous.settled && manager === shownManager()) void reloadStarCollections(); }),
        subscribeCredentialOwner(resetOwner),
      ];
      void reloadStarCollections();
    }
    return () => {
      if (--users === 0) {
        stop?.forEach((off) => off());
        stop = undefined;
        scope++;
        writing = undefined;
        readSequence++;
        publish(blank());
      }
    };
  }, []);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

function collectionRequest(change: StarCollectionChange, revision: number, owner: ReturnType<typeof request>): Promise<StarOrganization> {
  const options = owner.options;
  switch (change.type) {
    case "create":
      return api(PATH, { ...options, method: "POST", json: { revision, name: change.name } });
    case "rename":
      return api(`${PATH}/${encodeURIComponent(change.id)}`, { ...options, method: "PATCH", json: { revision, name: change.name } });
    case "delete":
      return api(`${PATH}/${encodeURIComponent(change.id)}?revision=${revision}`, { ...options, method: "DELETE" });
    case "assign":
      return api(PATH + "/assignments", {
        ...options, method: "PATCH", json: { revision, star_ids: change.ids, collection_id: change.collection },
      });
  }
}

type Result = { status: "saved" | "retired" } | { status: "conflict" | "failed"; error: string };

export async function changeStarCollections(change: StarCollectionChange, revision: number, drawnScope: number): Promise<Result> {
  if (drawnScope !== scope || writing || !users || !getToken() || !state.data?.can_write) return { status: "retired" };
  if (revision !== state.data.revision) return { status: "conflict", error: t("starred.collection_conflict") };
  const owner = request();
  const write = Symbol();
  writing = write;
  publish({ ...state, writing: true });
  try {
    const data = await collectionRequest(change, revision, owner);
    if (!owner.current() || !users) return { status: "retired" };
    readSequence++;
    publishData(data);
    return { status: "saved" };
  } catch (error) {
    if (!owner.current() || !users) return { status: "retired" };
    if (error instanceof ApiError && error.status === 409 && error.code === "conflict") {
      await reloadStarCollections();
      return { status: "conflict", error: t("starred.collection_conflict") };
    }
    if (error instanceof ApiError && error.status === 404) await reloadStarCollections();
    return { status: "failed", error: errorText(error) };
  } finally {
    if (writing === write) {
      writing = undefined;
      if (owner.current() && users) publish({ ...state, writing: false });
    }
  }
}
