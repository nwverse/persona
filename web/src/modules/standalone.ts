// The modules with a page of their own (core/module-host.ts `standalonePages`, docs/web/web.md "Modules"):
// part of the module list (index.ts takes them in), kept in a file of its own because main.tsx imports it before App.
//
// main.tsx may reach only what this file reaches, and App reaches it too, so what it reaches is split out of App's
// chunks. Were it the whole list, every module's code would be reachable without App, and the build could keep nothing
// App shares with the modules inside App's chunks (the first screen grew by about 30 KB when it tried); were it the
// host, App would import the host's every name from another chunk. So it reaches the descriptors, this browser's
// switch and the strings loader, and draws the page itself: before any hello only the switch can turn a module off
// (the hub flag rule), and the host's checks of the descriptor and its code run where App registers the list.
import type { ReactElement } from "react";
import type { ModuleDescriptor } from "../core/module-host";
import { moduleSwitchedOff } from "../core/module-switch";
import { addCatalogs } from "../i18n";
import { stripBase } from "../lib/base";
import { screenShare } from "./screen-share/module";

export const STANDALONE_MODULES: readonly ModuleDescriptor[] = [screenShare];

/**
 * The page main.tsx draws for this address in place of the app, when a module that is on has it as a page of its own
 * (its strings, then its code, loaded; the address' token taken); null for the app. While that module is off the address
 * is an unknown one: its fragment (a token the app meant for the page) goes first, so App never takes it for a sign-in,
 * and neither the module's chunk nor its strings are requested.
 */
export async function standalonePage(address: string, list: readonly ModuleDescriptor[] = STANDALONE_MODULES): Promise<ReactElement | null> {
  const u = new URL(address);
  const base = stripBase(u.pathname).split("/").filter(Boolean)[0];
  const d = base ? list.find((m) => m.standalonePages?.includes(base)) : undefined;
  if (!d) return null;
  if (moduleSwitchedOff(d.id)) {
    if (u.hash) window.history.replaceState(window.history.state, "", u.pathname + u.search);
    return null;
  }
  if (d.locales) await addCatalogs(d.locales);
  const code = await d.load();
  if (!code.Standalone) throw new Error(`module "${d.id}" names the page of its own /${base}, which its code does not draw`);
  return code.Standalone(address);
}
