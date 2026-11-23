// The module list (#13434, docs/web/web.md "Modules"): every optional feature module of the web app, the only place
// their ids appear outside src/modules/<id>. Each entry is a static descriptor (module.ts: what it contributes, its hub
// feature flag) whose code is a dynamic import(), so importing this list loads no module's code and none of its strings.
// App.tsx imports it once, before the first route is read. Adding a module: docs/web/web.md "Adding a module".
import { registerModule, registeredModules, type ModuleDescriptor } from "../core/module-host";
import { health } from "./health/module";

export const MODULES: readonly ModuleDescriptor[] = [health];

/** Registers every module once (a second call, a hot reload or a test importing the list again, is a no-op). */
export function registerModules(): void {
  for (const m of MODULES) if (!registeredModules().includes(m)) registerModule(m);
}

registerModules();
