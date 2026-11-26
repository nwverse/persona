// @vitest-environment jsdom
// Unit & integration tests for the lazy-loaded unified IntegrationDetail view (integrations_v2, #14847).
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { t } from "../../i18n";
import { channelStateText, type ChannelDetail } from "../../lib/channels";
import type { HubConnector } from "../../lib/connectors";
import type { HubExtensionDetail } from "../../lib/extensions";
import type { FeishuBotStatus } from "../../lib/feishu-bot";
import { chosenManager, managerHeaderFor, resetManagerChoice } from "../../lib/manager-choice";
import { setRoster } from "../../lib/managers";
import { applyCompleteSessions } from "../../lib/store";
import { FEATURES } from "./FeishuBot";
import { IntegrationDetail, isIntegrationsV2Active, partitionExtensionFields } from "./IntegrationDetail";

vi.mock("../../ui/atoms", async (original) => ({
  ...(await original<typeof import("../../ui/atoms")>()),
  toast: vi.fn(),
}));
(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

interface RecordedRequest {
  manager: string | null;
  url: string;
  method: string;
  body: unknown;
}

const ok = (body: unknown) => ({
  ok: true,
  status: 200,
  text: async () => JSON.stringify(body),
  headers: new Headers(),
});
const notFound = () => ({
  ok: false,
  status: 404,
  text: async () => JSON.stringify({ error: "not found" }),
  headers: new Headers(),
});

const ROSTER = {
  default: "assistant",
  managers: [
    { id: "assistant", name: "Default", builtin: true, online: true },
    { id: "work", name: "Work", builtin: false, online: true },
  ],
};

const COMPOSITE_EXTENSION: HubExtensionDetail = {
  id: "larkx",
  name: "Lark Extended",
  kind: "process",
  source: "installed",
  status: "running",
  version: "2.1.0",
  enabled: true,
  origin: "market:official/larkx",
  auto_upgrade: true,
  description: "Composite connector, channel, and inbox extension.",
  contributes: {
    connectors: ["larkx_conn"],
    channel: ["larkx_chat"],
  },
  auth: {
    title: "Workspace OAuth",
    state: "signed_in",
    account: "owner@example.com",
  },
  form: {
    fields: [
      { name: "endpoint", type: "string", format: "uri", title: "API Endpoint", section: "connection" },
      { name: "mention_prefix", type: "string", title: "Mention Prefix", section: "channel" },
      { name: "triage_rules", type: "string", title: "Triage Rules", section: "inbox" },
      { name: "display_alias", type: "string", title: "Display Alias" },
      { name: "debug_trace", type: "boolean", title: "Debug Trace", section: "advanced" },
    ],
  },
  config: {
    endpoint: "https://api.example.com",
    mention_prefix: "@bot",
    triage_rules: "Only urgent items",
    display_alias: "Team Lark",
    debug_trace: false,
  },
  secrets: [
    { name: "app_secret", title: "App Secret", required: true, set: true, hint: "1234" },
  ],
  permissions: [
    { key: "network", title: "Network access", detail: "Connects to workspace APIs" },
  ],
};

const COMPOSITE_CHANNEL: ChannelDetail = {
  id: "larkx_chat",
  name: "Lark Chat",
  kind: "im",
  source: "plugin",
  plugin: "larkx",
  settings_url: "settings/plugins/larkx",
  installed: true,
  enabled: true,
  state: "connected",
  account: { name: "Owner" },
  capabilities: { push: true, threads: true, format: "markdown" },
  bind: "qr",
  target: { kind: "main" },
  thread_mode: "channel_default",
  thread_idle_hours: 0,
  features: [{ id: "asks", title: "Questions & approvals", on: true }],
  waiting: { answers: 0, asks: 0 },
};

const COMPOSITE_CONNECTOR: HubConnector = {
  id: "larkx_conn",
  name: "Lark Workspace",
  status: "ok",
  account: "owner@example.com",
  enabled: true,
  archive: true,
  inbox: true,
  capabilities: ["login", "archive", "inbox"],
};

const FEISHU_BOT_STATUS: FeishuBotStatus = {
  enabled: true,
  connected: true,
  has_secret: true,
  from_config: false,
  app_id: "cli_example_123456",
  domain: "feishu",
  bot_name: "Persona Feishu",
  owner: { name: "Alice", paired_at: 1700000000 },
  features: { main: true, cards: false, report_steps: true, answer_new_card: true, topic_cot: false, read_group: false, all_cards: false, pushes: true, sync: true, reports: true, board: false, workbench: false, only_conclusions: false, agents: true },
  nudge_after: 15,
};

const FEISHU_CHANNEL: ChannelDetail = {
  id: "feishu",
  name: "Feishu",
  kind: "im",
  source: "builtin",
  installed: true,
  enabled: true,
  state: "connected",
  account: { name: "Alice" },
  capabilities: { push: true, threads: true, asks: "buttons" },
  bind: "fields",
  target: { kind: "main" },
  settings_url: "settings/connectors/feishu-bot",
  target_override: { mode: "topics" },
  features: FEATURES.map(({ key, title }) => ({ id: key, title, on: !!FEISHU_BOT_STATUS.features?.[key] })),
  waiting: { answers: 0, asks: 0 },
};

let host: HTMLDivElement;
let root: Root;
let requests: RecordedRequest[];
let currentExt: HubExtensionDetail;
let currentChannels: Record<string, ChannelDetail>;
let currentConnectors: HubConnector[];
let currentMailAccounts: { id: string; email: string; provider: string; status: string }[];
let currentFeishuBot: FeishuBotStatus;
let currentRepositories: { id: string; provider?: string }[] | "error" | null;

const flush = async () => {
  for (let i = 0; i < 14; i++) {
    await act(async () => {
      await Promise.resolve();
    });
  }
};

const SEED_SESSION = { machine_id: "hub", session_id: "seed-1", status: "idle", updated_at: "2026-01-01T00:00:00Z" } as never;

function setStoreAssistant(integrations_v2: boolean | undefined, manager = "assistant") {
  // Ensure the seed session is present so applyCompleteSessions always hits its `also`-only branch.
  applyCompleteSessions([SEED_SESSION]);
  applyCompleteSessions([SEED_SESSION], [], {
    features: ["channels", "connectors", "extensions"],
    assistant: {
      machine_id: "hub",
      session_id: "main-1",
      manager,
      integrations_v2,
      conversations: [],
    } as never,
  });
}

function section(id: string): HTMLElement | null {
  return host.querySelector<HTMLElement>(`[data-integration-section="${id}"]`);
}

function sectionIds(): string[] {
  return [...host.querySelectorAll<HTMLElement>("[data-integration-section]")].map((el) => el.getAttribute("data-integration-section") || "");
}

function setInputValue(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
  const proto = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  const setter = Object.getOwnPropertyDescriptor(proto, "value")!.set!;
  setter.call(input, value);
  input.dispatchEvent(new Event("input", { bubbles: true }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

beforeEach(() => {
  requests = [];
  currentExt = structuredClone(COMPOSITE_EXTENSION);
  currentChannels = {
    larkx_chat: structuredClone(COMPOSITE_CHANNEL),
    feishu: structuredClone(FEISHU_CHANNEL),
  };
  currentConnectors = [
    structuredClone(COMPOSITE_CONNECTOR),
    { id: "mail", name: "Mail", status: "ok", enabled: true, inbox: true, capabilities: ["inbox"] },
    {
      id: "github",
      name: "GitHub",
      status: "ok",
      account: "octocat",
      enabled: true,
      archive: true,
      inbox: true,
      capabilities: ["login", "archive", "inbox"],
      options: { ci: "mine", muted_repos: ["org/noisy"], ignore_branches: ["train/*"] },
    },
  ];
  currentMailAccounts = [{ id: "m1", email: "me@example.com", provider: "imap", status: "ok" }];
  currentFeishuBot = structuredClone(FEISHU_BOT_STATUS);
  currentRepositories = [{ id: "repo-1", provider: "github" }, { id: "repo-2", provider: "github" }];

  resetManagerChoice();
  setRoster(ROSTER);
  setStoreAssistant(true);

  vi.stubGlobal("fetch", vi.fn(async (input: string | URL, init?: RequestInit) => {
    const url = String(input);
    const method = (init?.method || "GET").toUpperCase();
    let body: unknown = undefined;
    if (typeof init?.body === "string" && init.body) {
      try { body = JSON.parse(init.body); } catch { body = init.body; }
    }
    const manager = new Headers(init?.headers).get("X-Persona-Manager");
    requests.push({ url, method, body, manager });

    if (url.includes("/mail/accounts")) {
      return ok({ accounts: currentMailAccounts });
    }
    if (url.includes("/bots/feishu")) {
      if (method === "PUT" && body && typeof body === "object") {
        currentFeishuBot = { ...currentFeishuBot, ...(body as Partial<FeishuBotStatus>) };
      }
      return ok(currentFeishuBot);
    }
    if (url.includes("/connectors/github/auth")) {
      return ok({ source: "device", machines: [], missing: [] });
    }
    if (url.includes("/connectors/") && url.endsWith("/settings") && method === "PUT") {
      const m = /\/connectors\/([^/]+)\/settings/.exec(url);
      const cid = m ? decodeURIComponent(m[1]) : "";
      const found = currentConnectors.find((c) => c.id === cid && (c.manager ?? null) === manager);
      if (found && body && typeof body === "object") {
        Object.assign(found, body);
        return ok(found);
      }
      return notFound();
    }
    if (/\/connectors(\?|$)/.test(url)) {
      return ok({ connectors: currentConnectors });
    }
    if (url.includes("/channels/")) {
      const testMatch = /\/channels\/([^/?]+)\/test/.exec(url);
      if (testMatch && method === "POST") {
        return ok({ ok: true, note: "Test delivered" });
      }
      const chMatch = /\/channels\/([^/?]+)/.exec(url);
      const cid = chMatch ? decodeURIComponent(chMatch[1]) : "";
      const ch = currentChannels[cid];
      if (!ch) return notFound();
      if (method === "PUT" && body && typeof body === "object") {
        const patch = body as Partial<ChannelDetail>;
        const mode = patch.thread_mode ?? ch.thread_mode;
        const next: ChannelDetail = {
          ...ch,
          ...patch,
          target_override: mode === "per_group_and_topic"
            ? { mode: "groups_and_topics" }
            : mode === "per_topic" ? { mode: "topics" } : undefined,
        };
        currentChannels[cid] = next;
        return ok(next);
      }
      return ok(ch);
    }
    if (url.includes("/-/upgrades")) {
      return ok({ auto: true });
    }
    if (url.includes("/-/config") && method === "PUT") {
      const cfg = (body as { config?: Record<string, unknown> })?.config ?? {};
      currentExt = { ...currentExt, config: cfg as HubExtensionDetail["config"] };
      return ok(currentExt);
    }
    if (url.includes("/-/enabled") && method === "PUT") {
      const enabled = (body as { enabled?: boolean })?.enabled ?? true;
      currentExt = { ...currentExt, enabled, status: enabled ? "running" : "disabled" };
      return ok(currentExt);
    }
    if (url.includes("/extensions/") || url.includes("/plugins/")) {
      return ok(currentExt);
    }
    if (url.includes("/subscriptions/summary")) {
      return ok({ lines: [] });
    }
    if (url.includes("/assistant/repositories")) {
      if (currentRepositories === "error") return notFound();
      if (currentRepositories === null) return ok({});
      return ok({ manager: "assistant", repositories: currentRepositories });
    }
    if (url.includes("/lark/status")) {
      return ok({ configured: true, connected: true, name: "Team Lark" });
    }
    if (url.includes("/lark/accounts")) {
      return ok({ accounts: [] });
    }
    if (url.includes("/trust")) {
      return notFound();
    }
    return ok({});
  }));

  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  document.documentElement.classList.remove("dark");
  vi.unstubAllGlobals();
  setRoster(null);
  resetManagerChoice();
});

describe("IntegrationDetail pure helpers & lazy import", () => {
  it("can be loaded via dynamic import() with both named and default exports", async () => {
    const mod = await import("./IntegrationDetail");
    expect(mod.IntegrationDetail).toBe(IntegrationDetail);
    expect(mod.default).toBe(IntegrationDetail);
  });

  it("partitions fields by section and preserves legacy/unknown fields in general", () => {
    const parts = partitionExtensionFields([
      { name: "a", type: "string", section: "connection" },
      { name: "b", type: "string" },
      { name: "c", type: "string", section: "channel" },
      { name: "d", type: "string", section: "inbox" },
      { name: "e", type: "boolean", section: "advanced" },
      { name: "f", type: "string", section: "future_section" as never },
    ]);
    expect(parts.connection.map((x) => x.name)).toEqual(["a"]);
    expect(parts.channel.map((x) => x.name)).toEqual(["c"]);
    expect(parts.inbox.map((x) => x.name)).toEqual(["d"]);
    expect(parts.general.map((x) => x.name)).toEqual(["b", "f"]);
    expect(parts.advanced.map((x) => x.name)).toEqual(["e"]);
  });

  it("gates integrations_v2 fail-closed and respects assistant manager scope", () => {
    expect(isIntegrationsV2Active(null)).toBe(false);
    expect(isIntegrationsV2Active({ manager: "assistant", integrations_v2: false })).toBe(false);
    expect(isIntegrationsV2Active({ manager: "assistant" })).toBe(false);
    expect(isIntegrationsV2Active({ manager: "assistant", integrations_v2: true }, "assistant")).toBe(true);
    expect(isIntegrationsV2Active({ manager: "assistant", integrations_v2: true }, "other")).toBe(false);
  });
});

describe("integrations_v2 gating & legacy isolation", () => {
  it("renders fallback and issues zero API requests when integrations_v2 is false", async () => {
    setStoreAssistant(false);
    act(() => {
      root.render(
        <IntegrationDetail
          target={{ kind: "extension", id: "larkx" }}
          fallback={<div data-legacy-fallback>Legacy view</div>}
        />,
      );
    });
    await flush();

    expect(host.querySelector("[data-legacy-fallback]")?.textContent).toBe("Legacy view");
    expect(host.querySelector("[data-integration-detail]")).toBeNull();
    expect(requests).toHaveLength(0);
  });

  it("renders nothing by default when integrations_v2 is omitted (older hub)", async () => {
    setStoreAssistant(undefined);
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "mail" }} />);
    });
    await flush();

    expect(host.innerHTML).toBe("");
    expect(requests).toHaveLength(0);
  });
});

describe("Built-in services", () => {
  it("renders Mail with Account & Connection accounts and Inbox & Sync switch", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "mail" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "inbox"]);
    expect(section("connection")!.textContent).toContain("me@example.com");
    expect(section("inbox")!.textContent).toContain(t("settings-hubconnector.to_inbox"));
    expect(requests.some((r) => r.url.includes("/bots/feishu"))).toBe(false);
  });

  it("gives Feishu Bot a single set of controls and refreshes its channel after a bot save", async () => {
    const onSettings = vi.fn();
    await act(async () => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "feishu-bot" }} onSettings={onSettings} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "channel", "inbox"]);
    expect(section("connection")!.textContent).toContain("Alice");
    const delivery = section("channel")!;
    expect(delivery.querySelector("[data-channel-routing]")).toBeNull();
    expect(delivery.textContent).toContain(t("settings-channels.target_row"));
    expect(delivery.textContent).toContain(t("settings-channels.target_override_topics"));
    expect(delivery.textContent).not.toContain(t("settings-channels.target_override_settings"));
    expect(delivery.textContent).not.toContain(t("settings-channels.enable", { name: FEISHU_CHANNEL.name }));
    expect(delivery.textContent).not.toContain(t("settings-channels.send_test"));
    const buttons = [...section("connection")!.querySelectorAll<HTMLButtonElement>("button")];
    expect(buttons.filter((b) => b.textContent === t("settings-feishubot.send_test"))).toHaveLength(1);
    for (const { key, title } of FEATURES) {
      const switches = delivery.querySelectorAll<HTMLButtonElement>(`[role="switch"][aria-label="${title}"]`);
      expect(switches, key).toHaveLength(1);
      if (key === "all_cards") expect(switches[0].disabled).toBe(true);
    }
    const enable = buttons.filter((b) => b.getAttribute("aria-label") === t("settings-feishubot.enable"));
    expect(enable).toHaveLength(1);
    requests.length = 0;
    currentChannels.feishu.state = "disabled";
    await act(async () => enable[0].click());
    await flush();
    const writes = requests.filter((r) => r.method === "PUT");
    expect(writes).toHaveLength(1);
    expect(writes[0]).toMatchObject({ url: expect.stringContaining("/bots/feishu"), body: { enabled: false } });
    expect(requests.some((r) => r.method === "GET" && r.url.endsWith("/channels/feishu"))).toBe(true);
    expect(host.querySelector("h1")!.closest("section")!.textContent).toContain(channelStateText(currentChannels.feishu));
    expect(section("channel")!.textContent).toContain(t("settings-feishubot.topic_groups"));
    const kbBtn = section("inbox")!.querySelector<HTMLButtonElement>('[data-settings-row="knowledge-scope-shortcut"]');
    expect(kbBtn).not.toBeNull();
    act(() => kbBtn!.click());
    expect(onSettings).toHaveBeenCalledWith("settings/knowledge#knowledge-items");
  });

  it("renders built-in GitHub connector with Saved repositories shortcut and tolerates repository API failure", async () => {
    const onSettings = vi.fn();
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "github" }} onSettings={onSettings} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "inbox"]);
    expect(section("connection")!.textContent).toContain("octocat");
    expect(section("inbox")!.textContent).toContain("org/noisy");
    expect(section("inbox")!.querySelector('[data-settings-row="knowledge-scope-shortcut"]')).toBeNull();
    const repoBtn = section("connection")!.querySelector<HTMLButtonElement>('[data-settings-row="saved-repositories-shortcut"]');
    expect(repoBtn).not.toBeNull();
    expect(repoBtn!.textContent).toContain(t("settings-integrations.saved_repositories_title"));
    expect(repoBtn!.textContent).toContain(t("settings-integrations.saved_repositories_count", { count: 2 }));
    act(() => repoBtn!.click());
    expect(onSettings).toHaveBeenCalledWith("settings/persona#repositories");
    expect(requests.some((r) => r.url.includes("/bots/feishu"))).toBe(false);

    // Repository query failure does not block rendering or clicking the shortcut.
    onSettings.mockClear();
    currentRepositories = "error";
    act(() => root.render(null));
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "builtin", id: "github" }} onSettings={onSettings} />);
    });
    await flush();
    const fallbackBtn = section("connection")!.querySelector<HTMLButtonElement>('[data-settings-row="saved-repositories-shortcut"]');
    expect(fallbackBtn).not.toBeNull();
    act(() => fallbackBtn!.click());
    expect(onSettings).toHaveBeenCalledWith("settings/persona#repositories");
  });

  it("renders Knowledge sync scope shortcut in Inbox & Sync for Mail, Lark gateway, and history-capable connectors", async () => {
    const onSettings = vi.fn();
    currentConnectors.push({
      id: "chat-history",
      name: "History Connector",
      status: "ok",
      enabled: true,
      archive: true,
      inbox: false,
      capabilities: ["history"],
    });

    for (const target of [
      { kind: "builtin", id: "mail" } as const,
      { kind: "builtin", id: "lark" } as const,
      { kind: "builtin", id: "chat-history", connectorId: "chat-history" } as const,
    ]) {
      onSettings.mockClear();
      act(() => root.render(null));
      act(() => {
        root.render(<IntegrationDetail target={target} onSettings={onSettings} />);
      });
      await flush();

      const kbBtn = section("inbox")?.querySelector<HTMLButtonElement>('[data-settings-row="knowledge-scope-shortcut"]');
      expect(kbBtn, `${target.id} knowledge shortcut`).not.toBeNull();
      expect(kbBtn!.textContent).toContain(t("settings-integrations.knowledge_scope_title"));
      act(() => kbBtn!.click());
      expect(onSettings).toHaveBeenCalledWith("settings/knowledge#knowledge-items");
    }
  });

  it("omits the Back button when onBack is undefined (desktop split-pane / phone) and renders it when provided", async () => {
    const onBack = vi.fn();
    for (const target of [
      { kind: "builtin", id: "mail" } as const,
      { kind: "builtin", id: "feishu-bot" } as const,
      { kind: "extension", id: "larkx" } as const,
    ]) {
      act(() => {
        root.render(<IntegrationDetail target={target} />);
      });
      await flush();
      const hiddenBack = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(t("settings-integrations.back")));
      expect(hiddenBack, `${target.id} without onBack`).toBeUndefined();

      act(() => {
        root.render(<IntegrationDetail target={target} onBack={onBack} />);
      });
      await flush();
      const visibleBack = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(t("settings-integrations.back")));
      expect(visibleBack, `${target.id} with onBack`).toBeDefined();
      act(() => visibleBack!.click());
    }
    expect(onBack).toHaveBeenCalledTimes(3);
  });
});

describe("Composite extension (connector + channel + sectioned config + lifecycle)", () => {
  it("renders all 5 sections inline, folds advanced fields, and saves unified config atomically", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["connection", "channel", "inbox", "extension-settings", "management"]);

    // 1. Account & Connection: auth + connector + section="connection" field
    expect(section("connection")!.textContent).toContain("Workspace OAuth");
    expect(section("connection")!.textContent).toContain("owner@example.com");
    expect(section("connection")!.querySelector('[data-field="endpoint"]')).not.toBeNull();

    // 2. Chat & Delivery: channel toggle, Send to target, thread_mode & thread_idle_hours, section="channel" field
    expect(section("channel")!.textContent).toContain(t("settings-channels.target_row"));
    expect(section("channel")!.textContent).toContain(t("settings-integrations.thread_mode_title"));
    expect(section("channel")!.textContent).toContain(t("settings-integrations.thread_idle_hours_title"));
    expect(section("channel")!.querySelector('[data-field="mention_prefix"]')).not.toBeNull();

    // 3. Inbox & Sync: connector feeds + section="inbox" field + knowledge sync scope shortcut for larkx
    expect(section("inbox")!.textContent).toContain(t("settings-hubconnector.to_inbox"));
    expect(section("inbox")!.querySelector('[data-field="triage_rules"]')).not.toBeNull();
    expect(section("inbox")!.querySelector('[data-settings-row="knowledge-scope-shortcut"]')).not.toBeNull();

    // 4. Extension Settings: unsectioned field visible, advanced field folded by default, secret present
    expect(section("extension-settings")!.querySelector('[data-field="display_alias"]')).not.toBeNull();
    expect(section("extension-settings")!.querySelector('[data-field="debug_trace"]')).toBeNull();
    expect(section("extension-settings")!.textContent).toContain("App Secret");

    // Expand Advanced disclosure -> reveals section="advanced" field
    const advButton = section("extension-settings")!.querySelector<HTMLButtonElement>("[data-integration-advanced] button")!;
    expect(advButton.getAttribute("aria-expanded")).toBe("false");
    act(() => advButton.click());
    await flush();
    expect(advButton.getAttribute("aria-expanded")).toBe("true");
    expect(section("extension-settings")!.querySelector('[data-field="debug_trace"]')).not.toBeNull();

    // 5. Plugin Management: version, enable, updates, uninstall
    expect(section("management")!.textContent).toContain("2.1.0");
    expect(section("management")!.textContent).toContain(t("settings-extensions.uninstall"));

    // Edit fields in two different sections (connection + general) -> single unified Save persists all fields
    const endpointInput = section("connection")!.querySelector<HTMLInputElement>("#extension-field-endpoint")!;
    const aliasInput = section("extension-settings")!.querySelector<HTMLInputElement>("#extension-field-display_alias")!;
    act(() => {
      setInputValue(endpointInput, "https://next.example.com");
      setInputValue(aliasInput, "Updated Lark");
    });
    await flush();

    const saveBtn = [...host.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("common.save"))!;
    expect(saveBtn).toBeDefined();
    await act(async () => saveBtn.click());
    await flush();

    const putCfg = requests.find((r) => r.method === "PUT" && r.url.includes("/-/config"));
    expect(putCfg).toBeDefined();
    expect(putCfg!.body).toEqual({
      config: {
        endpoint: "https://next.example.com",
        mention_prefix: "@bot",
        triage_rules: "Only urgent items",
        display_alias: "Updated Lark",
        debug_trace: false,
      },
    });
  });

  it("keeps the override link when it opens another integration", async () => {
    const onSettings = vi.fn();
    currentChannels.larkx_chat.settings_url = "settings/plugins/other";
    currentChannels.larkx_chat.target_override = { mode: "threads" };
    await act(async () => root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} onSettings={onSettings} />));
    await flush();
    const link = [...section("channel")!.querySelectorAll("button")].find(
      (b) => b.textContent === t("settings-channels.target_override_extension_settings"),
    );
    expect(link).toBeDefined();
    act(() => link!.click());
    expect(onSettings).toHaveBeenCalledWith("settings/plugins/other");
  });

  it("locks channel controls during a routing save and keeps the refreshed route after a late reply", async () => {
    const fetchNow = fetch;
    let finishPut!: () => void;
    vi.stubGlobal("fetch", (input: RequestInfo | URL, init?: RequestInit) => {
      if (String(input).endsWith("/channels/larkx_chat") && init?.method === "PUT") {
        return new Promise((resolve) => {
          finishPut = () => resolve(ok({ ...COMPOSITE_CHANNEL, thread_mode: "per_topic" }));
        });
      }
      return fetchNow(input, init);
    });
    await act(async () => root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} />));
    await flush();
    const delivery = section("channel")!;
    const option = (label: string) => [...delivery.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find(
      (b) => b.textContent?.includes(label),
    )!;
    const enable = delivery.querySelector<HTMLButtonElement>('[role="switch"]')!;
    await act(async () => option(t("settings-integrations.thread_mode_topic_title")).click());
    expect(enable.disabled).toBe(true);
    currentChannels.larkx_chat.thread_mode = "per_group_and_topic";
    const refresh = host.querySelector<HTMLButtonElement>(`button[aria-label="${t("settings-integrations.refresh")}"]`)!;
    await act(async () => refresh.click());
    await flush();
    const groupTopic = t("settings-integrations.thread_mode_group_topic_title");
    expect(option(groupTopic).getAttribute("aria-checked")).toBe("true");
    await act(async () => finishPut());
    await flush();
    expect(option(groupTopic).getAttribute("aria-checked")).toBe("true");
    expect(delivery.querySelector<HTMLButtonElement>('[role="switch"]')!.disabled).toBe(false);
  });

  it("saves thread_mode and thread_idle_hours via PUT /api/v1/channels/{id} and sends test messages", async () => {
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} onSettings={vi.fn()} />);
    });
    await flush();

    // Switch thread_mode to per_group_and_topic
    const groupTopicOption = [...section("channel")!.querySelectorAll<HTMLButtonElement>('[role="radio"]')].find((b) =>
      b.textContent?.includes(t("settings-integrations.thread_mode_group_topic_title")),
    )!;
    expect(groupTopicOption).toBeDefined();
    await act(async () => groupTopicOption.click());
    await flush();

    const modePut = requests.find((r) => r.method === "PUT" && r.url.includes("/channels/larkx_chat"));
    expect(modePut?.body).toEqual({ thread_mode: "per_group_and_topic" });
    // Response updated target_override -> override hint appears immediately
    expect(section("channel")!.textContent).toContain(t("settings-channels.target_override_groups_and_topics"));

    expect(section("channel")!.textContent).not.toContain(t("settings-channels.target_override_extension_settings"));

    // Update thread_idle_hours to 48 and click Save inside the ConfigurationForm field
    const idleField = section("channel")!.querySelector<HTMLElement>('[data-configuration-field="thread_idle_hours"]')!;
    const idleInput = idleField.querySelector<HTMLInputElement>("input")!;
    act(() => setInputValue(idleInput, "48"));
    await flush();

    const idleSave = [...idleField.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent === t("settings-configuration.save"))!;
    await act(async () => idleSave.click());
    await flush();

    const idlePut = requests.filter((r) => r.method === "PUT" && r.url.includes("/channels/larkx_chat")).pop();
    expect(idlePut?.body).toEqual({ thread_idle_hours: 48 });

    // Send test message
    const testBtn = [...section("channel")!.querySelectorAll<HTMLButtonElement>("button")].find((b) => b.textContent?.includes(t("settings-channels.send_test")))!;
    await act(async () => testBtn.click());
    await flush();
    expect(requests.some((r) => r.method === "POST" && r.url.includes("/channels/larkx_chat/test"))).toBe(true);
    expect(section("channel")!.querySelector('[role="status"]')?.textContent).toBe("Test delivered");
  });
});

describe("Older Hub and legacy extension graceful degradation", () => {
  it.each([
    ["built-in voice", { id: "voice", kind: "voice", source: "builtin" }, false],
    ["app", { kind: "app" }, false],
    ["market", { source: "market" }, false],
    ["uninstalled plugin", { installed: false }, false],
    ["read-only plugin", { read_only: true }, false],
    ["mention plugin", { kind: "mention" }, true],
  ] as const)("shows the expected routing controls for %s", async (_name, patch, visible) => {
    const channel = "source" in patch && patch.source === "builtin"
      ? { ...FEISHU_CHANNEL, ...patch, id: "voice", name: "Voice" }
      : { ...COMPOSITE_CHANNEL, ...patch };
    currentChannels[channel.id] = channel;
    const target = channel.source === "builtin"
      ? { kind: "builtin" as const, id: channel.id, channelId: channel.id, connectorId: null }
      : { kind: "extension" as const, id: "larkx" };
    act(() => root.render(<IntegrationDetail target={target} />));
    await flush();
    expect(section("channel")).not.toBeNull();
    expect(!!section("channel")!.querySelector("[data-channel-routing]")).toBe(visible);
  });

  it("degrades routing controls to read-only on an older Hub omitting thread_mode/thread_idle_hours", async () => {
    currentChannels.larkx_chat = {
      ...COMPOSITE_CHANNEL,
      thread_mode: undefined,
      thread_idle_hours: undefined,
    };

    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "larkx" }} />);
    });
    await flush();

    const ch = section("channel")!;
    expect(ch.textContent).toContain(t("settings-integrations.routing_hub_unsupported"));
    const radios = [...ch.querySelectorAll<HTMLButtonElement>('[data-configuration-field="thread_mode"] [role="radio"]')];
    expect(radios.length).toBe(3);
    // Fieldset is disabled so clicking does not issue an unsupported PUT
    const fieldset = ch.querySelector<HTMLFieldSetElement>('[data-configuration-field="thread_mode"] fieldset');
    expect(fieldset?.disabled).toBe(true);
    expect(requests.some((r) => r.method === "PUT" && r.url.includes("/channels/"))).toBe(false);
  });

  it("renders legacy extensions without section metadata seamlessly and omits unused sections", async () => {
    currentExt = {
      id: "legacy-tool",
      name: "Legacy Tool",
      kind: "process",
      source: "installed",
      status: "running",
      version: "1.0.0",
      enabled: true,
      origin: "market:official/legacy-tool",
      auto_upgrade: false,
      form: {
        fields: [
          { name: "api_url", type: "string", title: "API URL" },
          { name: "max_items", type: "integer", title: "Max Items", default: 20 },
        ],
      },
      config: { api_url: "https://legacy.example.com" },
    };

    // Verify both light and dark themes render without error or hard-coded theme issues
    document.documentElement.classList.add("dark");
    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "legacy-tool" }} />);
    });
    await flush();

    expect(sectionIds()).toEqual(["extension-settings", "management"]);
    expect(section("extension-settings")!.querySelector('[data-field="api_url"]')).not.toBeNull();
    expect(section("extension-settings")!.querySelector('[data-field="max_items"]')).not.toBeNull();
    expect(section("extension-settings")!.querySelector("[data-integration-advanced]")).toBeNull();
    expect(host.querySelector('[data-settings-row="saved-repositories-shortcut"]')).toBeNull();
    expect(host.querySelector('[data-settings-row="knowledge-scope-shortcut"]')).toBeNull();
  });

  it("shows Knowledge sync scope shortcut for an external extension whose connector advertises history capability", async () => {
    const onSettings = vi.fn();
    currentExt = {
      id: "acme-chat",
      name: "Acme Chat",
      kind: "process",
      source: "installed",
      status: "running",
      version: "1.0.0",
      enabled: true,
      origin: "market:official/acme-chat",
      contributes: { connectors: ["acme_conn"] },
    };
    currentConnectors = [
      { id: "acme_conn", name: "Acme", status: "ok", enabled: true, archive: true, capabilities: ["history"] },
    ];

    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "acme-chat" }} onSettings={onSettings} />);
    });
    await flush();

    const kbBtn = section("inbox")?.querySelector<HTMLButtonElement>('[data-settings-row="knowledge-scope-shortcut"]');
    expect(kbBtn).not.toBeNull();
    act(() => kbBtn!.click());
    expect(onSettings).toHaveBeenCalledWith("settings/knowledge#knowledge-items");
  });

  it("shows Saved repositories shortcut for a GitHub-compatible extension", async () => {
    const onSettings = vi.fn();
    currentExt = {
      id: "github-extra",
      name: "GitHub Extra",
      kind: "process",
      source: "installed",
      status: "running",
      version: "1.0.0",
      enabled: true,
      origin: "market:official/github-extra",
      contributes: { repository_providers: ["github"] },
    };

    act(() => {
      root.render(<IntegrationDetail target={{ kind: "extension", id: "github-extra" }} onSettings={onSettings} />);
    });
    await flush();

    const repoBtn = section("connection")?.querySelector<HTMLButtonElement>('[data-settings-row="saved-repositories-shortcut"]');
    expect(repoBtn).not.toBeNull();
    expect(repoBtn!.textContent).toContain(t("settings-integrations.saved_repositories_count", { count: 2 }));
    act(() => repoBtn!.click());
    expect(onSettings).toHaveBeenCalledWith("settings/persona#repositories");
  });
});

describe("manager-owned integration links", () => {
  it("scopes contributed and target connectors to the extension owner for the lifetime of the detail", async () => {
    currentConnectors.push({ ...COMPOSITE_CONNECTOR, manager: "work" }, { id: "imessage", name: "Messages", status: "ok", manager: "work" });
    await act(async () => root.render(
      <IntegrationDetail integrationsV2 target={{ kind: "extension", id: "larkx", connectorId: "imessage", manager: "work" }} />,
    ));
    await flush();
    const toggle = section("inbox")!.querySelector<HTMLButtonElement>(`[role="switch"][aria-label="${t("settings-hubconnector.to_inbox")}"]`)!;
    await act(async () => toggle.click());

    expect(requests.filter((r) => r.url.endsWith("/connectors/larkx_conn/settings"))).toEqual([
      expect.objectContaining({ method: "PUT", body: { inbox: false }, manager: "work" }),
    ]);
    expect(requests).toContainEqual(expect.objectContaining({ url: "/api/v1/imessage", method: "GET", manager: "work" }));
    expect(currentConnectors.find((c) => c.id === "larkx_conn" && c.manager === "work")?.inbox).toBe(false);
    expect(currentConnectors.find((c) => c.id === "larkx_conn" && !c.manager)?.inbox).toBe(true);
    expect(managerHeaderFor("/connectors/imessage/settings")).toBe("work");
    expect(managerHeaderFor("/connectors/unrelated/settings")).toBeUndefined();
    expect(chosenManager()).toBeNull();
    act(() => root.render(null));
    for (const path of ["/connectors/larkx_conn/settings", "/connectors/imessage/settings", "/imessage"]) {
      expect(managerHeaderFor(path)).toBeUndefined();
    }
  });

  it.each([
    ["github", "saved-repositories-shortcut", "settings/persona#repositories"],
    ["mail", "knowledge-scope-shortcut", "settings/knowledge#knowledge-items"],
  ])("opens %s settings with the owner's assistant already selected", async (id, row, destination) => {
    const onSettings = vi.fn(() => chosenManager());
    await act(async () => root.render(<IntegrationDetail integrationsV2 target={{ kind: "builtin", id, manager: "work" }} onSettings={onSettings} />));
    await flush();
    expect(chosenManager()).toBeNull();
    if (id === "github") {
      expect(requests).toContainEqual(expect.objectContaining({ url: "/api/v1/assistant/repositories", manager: "work" }));
    }
    act(() => host.querySelector<HTMLButtonElement>(`[data-settings-row="${row}"]`)!.click());
    expect(onSettings).toHaveBeenCalledWith(destination);
    expect(onSettings).toHaveReturnedWith("work");
  });
});
