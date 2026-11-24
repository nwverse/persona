// The modules with a page of their own (core/module-host.ts `standalonePages`, docs/web/web.md "Pages of their own"):
// part of the module list (index.ts takes them in), kept in a file of its own because main.tsx imports it before App.
// main.tsx may reach only what this file reaches: were it the whole list, every module's code would be reachable
// without App, and the build could no longer keep what App shares with the modules' chunks inside App's own chunks
// (the first screen grew by about 30 KB when it tried). Registering is index.ts's (or, on such a page, main.tsx's).
import type { ModuleDescriptor } from "../core/module-host";
import { screenShare } from "./screen-share/module";

export const STANDALONE_MODULES: readonly ModuleDescriptor[] = [screenShare];
