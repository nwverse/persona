import { pathToFileURL } from "node:url";
import { componentSnapshot } from "../../sdk/ts/src/page-bridge.ts";
import { Hub } from "../../sdk/ts/src/index.ts";

// Helper conformance only: this is not an installed extension, manager transport, sandbox or Apple UI test.
const module = await import(pathToFileURL(process.argv[2]).href);
const program = module.default;
const actionSlots = new Set<string>(module.actionSlots);
const fixtures = await Bun.file(process.argv[3]).json();
const hub = new Hub(() => { throw new Error("Fixture parity must not call a real hub"); });
function callbackIDs(value: any, field = "", ids: string[] = []): string[] {
  if (Array.isArray(value)) value.forEach(item => callbackIDs(item, field, ids));
  else if (value && typeof value === "object") {
    if (actionSlots.has(field) && typeof value.id === "string") ids.push(value.id);
    Object.entries(value).forEach(([key, item]) => callbackIDs(item, key, ids));
  }
  return ids;
}
function normalize(value: unknown, field = ""): unknown {
  if (Array.isArray(value)) return value.map(item => normalize(item, field));
  if (!value || typeof value !== "object") return value;
  return Object.fromEntries(Object.entries(value).filter(([key, item]) => item !== undefined && key !== "key"
    && !(key === "id" && actionSlots.has(field)))
    .sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => [key, normalize(item, key)]));
}
let passed = 0;
for (const [name, expected] of Object.entries(fixtures)) {
  try {
    const actual = await componentSnapshot(program.ui[name], { contribution: name, placement: "nav_page", size: "full", hub,
      ui_version: 5, locale: "zh-Hans", platform: "mac", state: "" });
    const ids = callbackIDs(actual);
    if (ids.length !== callbackIDs(expected).length || ids.some(id => !id.startsWith("cb.")) || new Set(ids).size !== ids.length) {
      console.error("FAIL fixture callback registration:", name);
      continue;
    }
    if (JSON.stringify(normalize(actual)) !== JSON.stringify(normalize(expected))) {
      console.error("FAIL fixture data differs:", name);
      continue;
    }
    passed++;
  } catch (error) {
    console.error("FAIL fixture render:", name, error instanceof Error ? error.name : "unknown error");
  }
}
console.log(`RESULT fixture-native data parity: ${passed}/${Object.keys(fixtures).length}; helper-only, not hub/runtime acceptance`);
async function checkSearchValues(route: string) {
  const page = program.ui[route];
  const renderer = page.createRenderer({ authorize: () => true });
  try {
    renderer.mount(await page.root({ contribution: route, placement: "nav_page", size: "full", hub, ui_version: 5,
      locale: "zh-Hans", platform: "mac", state: route === "showcase" ? JSON.stringify({ p: "list" }) : "" }));
    for (const [index, query] of ["02", "no-synthetic-match"].entries()) {
      const snapshot = renderer.current();
      await renderer.event({ generation: snapshot.generation, version: snapshot.version, eventId: "query-" + index,
        key: snapshot.rootKey, actionId: snapshot.view.search.action.id, values: { q: query } });
      await new Promise<void>(resolve => queueMicrotask(resolve));
      const view = renderer.current().view;
      const rows = view.sections.flatMap((section: any) => section.items).filter((item: any) => item.type === "row");
      if (view.search.value !== query || (index === 0 ? !rows.some((item: any) => item.title.includes("02")) : rows.length !== 0)) {
        throw new Error("Search must filter the submitted query, not return the fixed search snapshot");
      }
    }
    console.log("RESULT fixture native search values:", route, "matching and empty queries PASS; helper-only");
  } finally { renderer.dispose(); }
}
for (const route of ["list", "showcase"]) await checkSearchValues(route);
process.exit(passed === Object.keys(fixtures).length ? 0 : 1);
