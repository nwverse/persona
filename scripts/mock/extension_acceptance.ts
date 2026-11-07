import { componentPage, type Program } from "@persona/extension";
import * as UI from "@persona/extension/ui";
import { useState } from "@persona/extension/ui";
import snapshots from "./snapshots.json";
import manifest from "./persona-extension.json";

// An isolated acceptance fixture, never a production marketplace contribution.
// Shared JSON data is preserved; only required native keys and callback handles are supplied by the official SDK.
const fixtures = snapshots as Record<string, any>;
const components: Record<string, any> = {
  text: UI.Text, metric: UI.Metric, progress: UI.Progress, row: UI.Row, button: UI.Button,
  link: UI.Link, field: UI.Field, divider: UI.Divider, chart: UI.Chart, stat_grid: UI.StatGrid,
  segmented: UI.Segmented, markdown: UI.Markdown, table: UI.Table, empty: UI.Empty,
  loading: UI.Loading, header: UI.Header, breadcrumb: UI.Breadcrumb, file_tree: UI.FileTree,
  code: UI.Code, diff: UI.Diff, comment_thread: UI.CommentThread, composer: UI.Composer,
};
let actions = 0;
const contexts: Record<string, unknown>[] = [];
function context(request: any) {
  contexts.push(Object.fromEntries(["size", "platform", "locale", "tz", "ui_version", "machine"]
    .filter(key => request[key] !== undefined).map(key => [key, request[key]])));
  if (contexts.length > 128) contexts.shift();
}
function nativeProps(value: any, field: string, run: (action: any, values: any) => any): any {
  if (Array.isArray(value)) return value.map(item => nativeProps(item, field, run));
  if (!value || typeof value !== "object") return value;
  if (["action", "load"].includes(field) && typeof value.id === "string") {
    const { id, ...options } = value;
    return UI.callback(values => run(value, values), options);
  }
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, nativeProps(item, key, run)]));
}
function Snapshot({ name, request }: { name: string; request: any }) {
  const [values, setValues] = useState<Record<string, string>>({});
  const [more, setMore] = useState(false);
  const initial = request.state ? JSON.parse(request.state) : {};
  let selected = fixtures[initial.p] ? initial.p : name;
  if (selected === "list" && values.q) selected = "list-search";
  else if (selected === "list" && more) selected = "list-more";
  if (selected === "segmented" && values.range === "30d") selected = "segmented-30d";
  const view = structuredClone(fixtures[selected] ?? fixtures.home);
  const run = (action: any, input: Record<string, string>) => {
    actions++;
    if (action.push) return { push: action.push };
    if (action.id === "more") { setMore(true); return {}; }
    if (["range", "search", "kind", "answer"].includes(action.id)) { setValues(previous => ({ ...previous, ...input })); return {}; }
    return { toast: "Synthetic action accepted" };
  };
  const { sections, ...props } = view;
  return UI.createElement(UI.View, { ...nativeProps(props, "", run), key: "snapshot-root", children:
    sections.map((section: any, sectionIndex: number) => {
      const { items, key, ...props } = section;
      return UI.createElement(UI.Section, { ...nativeProps(props, "", run), key: key ?? "section-" + sectionIndex,
        children: items.map((item: any, itemIndex: number) => {
          const { type, key, ...props } = item;
          const component = components[type];
          return component ? UI.createElement(component, { ...nativeProps(props, "", run),
            key: key ?? type + "-" + sectionIndex + "-" + itemIndex }) : null;
        }) });
    }) });
}
const ui: Record<string, any> = {};
for (const name of Object.keys(fixtures)) {
  ui[name] = componentPage(request => { context(request); return UI.createElement(Snapshot, { name, request, key: "fixture" }); });
}
ui.showcase = componentPage(request => { context(request); return UI.createElement(Snapshot, { name: "home", request, key: "fixture" }); });
ui["showcase-card"] = { render: (request: any) => { context(request); return fixtures.card; } };
ui["showcase-widget"] = { render: (request: any) => { context(request); return fixtures["widget-" + request.size]; } };
ui.machine = { render: (request: any) => { context(request); return { sections: [{ items: [{ type: "metric", label: "Test computer", value: request.machine?.id ?? "missing" }] }] }; } };

export default {
  id: "showcase", version: "0.1.0",
  init: () => manifest.contributes,
  ui,
  shortcuts: {
    echo: (request: any) => { context(request); return { text: request.values.text }; },
    count: (request: any) => {
      context(request);
      const view = structuredClone(fixtures["shortcut-count"]);
      view.sections[0].items[0].value = request.values.n;
      if (request.values.unit === "apple") view.sections[0].items[0].detail = "Apples";
      const text = `${request.values.n} ${request.values.unit ?? "star"}`;
      return { text: request.values.loud === "true" ? text.toUpperCase() : text, view };
    },
    stats: () => ({ text: JSON.stringify({ actions, contexts }) }),
  },
} satisfies Program;
