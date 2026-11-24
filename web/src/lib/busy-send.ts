import { useSyncExternalStore } from "react";

export type BusySend = "queue" | "follow_up";
const key = "persona.busy-send";
const changed = "persona:busy-send";

function read(): BusySend {
  return typeof localStorage !== "undefined" && localStorage.getItem(key) === "queue" ? "queue" : "follow_up";
}

function subscribe(update: () => void) {
  window.addEventListener(changed, update);
  window.addEventListener("storage", update);
  return () => {
    window.removeEventListener(changed, update);
    window.removeEventListener("storage", update);
  };
}

export function useBusySend() {
  return useSyncExternalStore(subscribe, read, () => "follow_up" as BusySend);
}

export function setBusySend(value: BusySend) {
  localStorage.setItem(key, value);
  window.dispatchEvent(new Event(changed));
}
