// The open conversation's row in the sidebar (#2283): marked for assistive tech, its own fill, its actions without hover.
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import { t } from "../i18n";
import { Icon } from "../ui/icons";
import type { AgentSession } from "../lib/agents";
import type { Session } from "../lib/types";
import { setAgentDetails } from "../lib/context-usage";
import { AgentRow, PersonaRow, SessionRow } from "./Sidebar";
import { conversationMenuItems } from "./ConversationMenu";

// the sidebar's modules read the colour scheme and this browser's settings when they load
vi.hoisted(() => {
  const g = globalThis as Record<string, unknown>;
  g.matchMedia = () => ({ matches: false, addEventListener() {}, removeEventListener() {} });
  const kept = new Map<string, string>();
  g.localStorage = { getItem: (k: string) => kept.get(k) ?? null, setItem: (k: string, v: string) => void kept.set(k, v), removeItem: (k: string) => void kept.delete(k) };
});

// the avatar reads the persona store, which has no server snapshot
vi.mock("./assistant/Persona", async (importOriginal) => ({ ...(await importOriginal<typeof import("./assistant/Persona")>()), PersonaAvatar: () => null }));

const session: Session = { machine_id: "mac", session_id: "s1", title: "整理文档", cwd: "/code", status: "idle", state: "idle" };
const rowProps = { s: session, k: "s:mac/s1", reg: () => () => {}, hover: () => {}, to: { name: "session", mid: "mac", sid: "s1" } as const, go: () => () => {}, onMenu: () => {} };
const link = (html: string) => html.match(/<a [^>]*>/)![0];
const more = (html: string) => html.match(/<button [^>]*aria-label="Agent 的操作"[^>]*>/)![0];

describe("an agent's row", () => {
  it("is aria-current with the open row's fill when it is open", () => {
    const html = renderToStaticMarkup(<SessionRow {...rowProps} active />);
    expect(link(html)).toContain('aria-current="page"');
    expect(link(html)).toMatch(/class="[^"]*\bbg-hover-2\b/);
  });
  it("is neither when it is not", () => {
    const html = renderToStaticMarkup(<SessionRow {...rowProps} active={false} />);
    expect(link(html)).not.toContain("aria-current");
    expect(link(html)).not.toContain("bg-hover-2");
  });
  it("keeps its ⋯ shown when open, on hover only otherwise", () => {
    expect(more(renderToStaticMarkup(<SessionRow {...rowProps} active />))).toContain("opacity-100");
    expect(more(renderToStaticMarkup(<SessionRow {...rowProps} active={false} />))).toContain("opacity-0 group-hover/row:opacity-100");
  });
});

describe("an agent row's identity and stable trailing content", () => {
  const render = (patch: Partial<Session>, extra = {}) => renderToStaticMarkup(<SessionRow {...rowProps} s={{ ...session, ...patch }} active={false} {...extra} />);

  it("draws one agent mark, no CLI badge, and no idle or unknown state badge", () => {
    for (const state of ["idle", "unknown"] as const) {
      const html = render({ state, status: state, pane_id: "w1:p1" });
      expect(html.match(/data-agent-mark=/g)).toHaveLength(1);
      expect(html).not.toContain('data-agent-badge="cli"');
      expect(html).not.toContain("data-state-badge");
    }
  });

  it("puts working, needs-you, unread and offline on the agent mark's corner", () => {
    for (const state of ["working", "needs_you", "unread", "offline"] as const) {
      const html = render({ state, status: state });
      expect(html).toContain("data-state-badge");
      expect(html).toContain(`data-state="${state}"`);
      expect(html.match(/data-agent-mark=/g)).toHaveLength(1);
    }
  });

  it("does not add a corner badge for idle awaiting-reply, but keeps Just finished", () => {
    const awaiting = render({ state: "idle", status: "idle", awaits_reply: true });
    expect(awaiting).not.toContain("data-state-badge");
    expect(awaiting).toContain(t("lib-state-labels.awaits_reply"));
    const finished = render({ state: "idle", status: "idle", work: { phase: "done", just_finished: true } });
    expect(finished).toContain("data-state-badge");
    expect(finished).toContain('data-state="just_finished"');
    expect(finished).toContain(t("lib-state-labels.just_finished"));
  });

  it("keeps source and associated-tab details out of the row and its native tooltip", () => {
    const html = render({ pane_id: "botmux:oc_1/t1", slot_id: "w1:t2", slot_name: "Release Review", external_url: "https://example.com/topic" });
    expect(html).not.toContain("data-source=");
    expect(html).not.toContain(t("sidebar.tab_named", { label: "Release Review" }));
    expect(link(html)).not.toContain("title=");
    expect(html).not.toContain("role=\"link\"");
  });

  it("hides only the age on hover; context, pending count and the in-flow menu keep their places", () => {
    const html = render({ updated_at: "2026-11-14T09:00:00Z", pending_interactions: 2 }, { hint: "Project" });
    expect(html.match(/<span data-row-age[^>]*>/)![0]).toContain("group-hover/row:invisible");
    expect(html.match(/<span data-row-context[^>]*>/)![0]).not.toContain("invisible");
    expect(html.match(/<span data-pending-count[^>]*>/)![0]).not.toContain("invisible");
    expect(more(html)).toContain("opacity-0 group-hover/row:opacity-100");
    expect(more(html)).toContain("[@media(hover:none)]:my-0");
    expect(more(html)).not.toContain("absolute");
    const active = renderToStaticMarkup(<SessionRow {...rowProps} s={{ ...session, updated_at: "2026-11-14T09:00:00Z" }} active />);
    expect(active.match(/<span data-row-age[^>]*>/)![0]).not.toContain("invisible");
    expect(more(active)).toContain("opacity-100");
  });

  it("puts an optional machine chip before the existing second line", () => {
    const plain = render({}, { showMachine: true });
    expect(plain).toContain("data-machine-chip");
    expect(plain).toContain(">mac</span>");
    expect(render({})).not.toContain("data-machine-chip");
    const withLine = render({ state: "unread", last_text: "Read the release notes" }, { showMachine: true });
    expect(withLine).toContain("data-row-line");
    expect(withLine.indexOf("data-machine-chip")).toBeLessThan(withLine.indexOf("data-row-line"));
  });

  it("keeps the title's width first only when a real hint sits at the row's end", () => {
    const html = render({ space_kind: "codex_app", source_kind: "codex_history" }, { hint: "Project" });
    expect(html).toMatch(/<span class="[^"]*flex-1 truncate[^"]*basis-auto[^"]*">整理文档<\/span>/);
    expect(html).toMatch(/<span data-row-context[^>]*>Project<\/span>/);
    expect(render({ pane_id: "w1:p1" })).not.toContain("basis-auto");
  });

  it("keeps the multi-pane tab's separate count overlay", () => {
    expect(more(render({}, { endRoom: true }))).toContain("absolute");
  });
});

describe("an agent's row with 「Agent 行显示详情」 (#2430)", () => {
  const detailed: Session = { ...session, model: "claude-opus-5-5", context_tokens: 78000, context_window: 200000 };
  const render = (s: Session, extra = {}) => renderToStaticMarkup(<SessionRow {...rowProps} s={s} active={false} {...extra} />);
  afterEach(() => {
    setAgentDetails(false);
  });
  it("is byte for byte the same as before while the setting is off", () => {
    expect(render(detailed)).toBe(render(session)); // the model and context change nothing
    expect(render(detailed)).not.toContain("opus 5.5");
  });
  it("adds a second line with folder, model and labelled context used when on", () => {
    setAgentDetails(true);
    const html = render(detailed);
    expect(html).toContain("opus 5.5");
    expect(html).toContain("上下文 39%");
    expect(html).toContain("code");
  });
  it("uses amber caution from 80% context used", () => {
    setAgentDetails(true);
    expect(render({ ...detailed, context_tokens: 170000 })).toContain("text-orange");
    const danger = render({ ...detailed, context_tokens: 185000 });
    expect(danger).toContain("text-orange");
    expect(danger).not.toContain("text-red");
  });
  it("draws no second line for a session that says nothing, or for a conversation of the assistant", () => {
    setAgentDetails(true);
    const plain = { ...session, cwd: "" };
    expect(render(plain)).not.toContain("flex-col");
    expect(render(detailed, { showState: false })).not.toContain("opus 5.5");
  });
});

describe("a personal agent's row", () => {
  const s = { id: "a1", title: "周报助手", provider: "lark", unreadCount: 0, rankTimeMs: 0 } as AgentSession;
  const props = { s, reg: () => () => {}, hover: () => {}, go: () => () => {}, pinned: false, onMenu: () => {} };
  const pin = (html: string) => html.match(/<button [^>]*aria-label="置顶"[^>]*>/)![0];
  it("is aria-current with the open row's fill and its pin shown when open", () => {
    const html = renderToStaticMarkup(<AgentRow {...props} active />);
    expect(link(html)).toContain('aria-current="page"');
    expect(link(html)).toMatch(/class="[^"]*\bbg-hover-2\b/);
    expect(pin(html)).toMatch(/class="[^"]*\bopacity-100\b/);
  });
  it("shows its pin on hover only otherwise", () => {
    const html = renderToStaticMarkup(<AgentRow {...props} active={false} />);
    expect(link(html)).not.toContain("aria-current");
    expect(pin(html)).toMatch(/class="[^"]*\bopacity-0\b/);
  });
});

describe("Persona's main row", () => {
  const main: Session = { machine_id: "hub", session_id: "main", title: "Persona", cwd: "/", status: "idle", state: "idle", assistant: true };
  const props = { reg: () => () => {}, hover: () => {}, go: () => () => {}, main };
  it("uses the existing home symbol and one Main chat line, without the assistant name or face", () => {
    const html = renderToStaticMarkup(<PersonaRow {...props} active />);
    const home = renderToStaticMarkup(<Icon name="home" size={14} stroke={1.8} className="shrink-0 text-ink" />);
    expect(html).toContain(home);
    expect(html).toContain(t("sidebar.main_chat"));
    expect(html).not.toContain("flex-col\"><span class=\"truncate text-[13.5px] font-semibold text-ink\">Persona");
    expect(html.replace(/<[^>]+>/g, "").match(new RegExp(t("sidebar.main_chat"), "g"))).toHaveLength(1);
  });
  it("has a ⋯ for the conversation's menu when the main conversation is there", () => {
    const html = renderToStaticMarkup(<PersonaRow {...props} active={false} onMenu={() => {}} />);
    expect(html).toMatch(/<button [^>]*aria-label="对话的操作"[^>]*aria-haspopup="menu"/);
  });
  it("draws no ⋯ without it", () => {
    expect(renderToStaticMarkup(<PersonaRow {...props} main={undefined} active={false} />)).not.toContain("对话的操作");
  });
  it("whose menu has only 复制会话 ID and 结束…", () => {
    expect(conversationMenuItems(main, { main: true }).map((i) => i.key)).toEqual(["copy", "end"]);
  });
});

describe("a 常驻 lead row (#6214 keepTrailing)", () => {
  const lead: Session = { machine_id: "hub", session_id: "lead-a", title: "增长", cwd: "/", status: "idle", state: "idle", assistant: true };
  const props = { s: lead, k: "j:lead-a", reg: () => () => {}, hover: () => {}, to: { name: "session", mid: "hub", sid: "lead-a" } as const, go: () => () => {}, onMenu: () => {}, hint: "在做 1 · 等你 1", hintFull: true, showState: false, keepTrailing: true };
  it("keeps its counts visible on hover and gives the ⋯ an in-flow slot instead of an overlay", () => {
    const html = renderToStaticMarkup(<SessionRow {...props} active={false} />);
    // the direction's name stays in the row even with no second line; the menu never replaces it
    expect(html).toContain("增长");
    // the counts never carry the hover-hiding class
    const counts = html.match(/<span[^>]*>在做 1 · 等你 1<\/span>/)![0];
    expect(counts).not.toContain("group-hover/row:invisible");
    // the ⋯ is an in-flow fader, not an absolute overlay covering the words
    const button = html.match(/<button [^>]*aria-label="Agent 的操作"[^>]*>/)![0];
    expect(button).toContain("opacity-0 group-hover/row:opacity-100");
    expect(button).not.toContain("absolute");
    // and the anchor reserves no overlay room of its own
    expect(link(html)).toMatch(/pr-1\b/);
  });
  it("draws its name once, with or without a second line (it read 「Per… Per…」 in a narrow sidebar)", () => {
    const named = { ...props, label: "Persona 产品" };
    expect(renderToStaticMarkup(<SessionRow {...named} active={false} />).match(/>Persona 产品</g)).toHaveLength(1);
    expect(renderToStaticMarkup(<SessionRow {...named} active />).match(/>Persona 产品</g)).toHaveLength(1);
    // a second line puts the name in the column, still once
    const busy = { ...named, showState: true, s: { ...lead, last_text: "整理了三条进展" } };
    expect(renderToStaticMarkup(<SessionRow {...busy} active={false} />).match(/>Persona 产品</g)).toHaveLength(1);
  });
  it("an ordinary row uses the same stable in-flow menu and keeps its tail on hover", () => {
    const html = renderToStaticMarkup(<SessionRow {...rowProps} hint="Project" active={false} />);
    expect(more(html)).toContain("opacity-0 group-hover/row:opacity-100");
    expect(more(html)).not.toContain("absolute");
    expect(html.match(/<span data-row-context[^>]*>/)![0]).not.toContain("invisible");
  });
});

describe("a session's associated tab name (#7595, #9769)", () => {
  const named: Session = { ...session, slot_id: "w1:t2", slot_name: "Release Review", pane_id: "w1:p1", updated_at: "2026-11-14T09:00:00Z" };
  const render = (patch: Partial<Session>, extra = {}) => renderToStaticMarkup(<SessionRow {...rowProps} s={{ ...session, ...patch }} hint="Project" active={false} {...extra} />);
  const tabName = (label: string) => t("sidebar.tab_named", { label });

  it("leaves the tab to the structured details card while the project hint stays", () => {
    for (const patch of [named, { ...named, slot_name: "Triage" }, { ...named, status: "offline", state: "offline" } as Partial<Session>]) {
      const html = render(patch);
      expect(html.match(/<span data-row-context[^>]*>([^<]*)</)![1]).toBe("Project");
      expect(html).toContain("Project");
      expect(html).not.toContain(tabName("Release Review"));
      expect(html).not.toContain(tabName("Triage"));
    }
  });

  it("keeps the old tab identity on non-agent row variants", () => {
    const html = render(named, { showState: false });
    expect(html).toContain(tabName("Release Review"));
    expect(html).toContain("整理文档");
    expect(html).toContain("Project");
  });

  it("keeps an unassociated structural fallback without minting a localized identity", () => {
    const html = render({ ...named, slot_id: undefined, tab_id: undefined, slot_name: "Workspace name", updated_at: undefined }, { tab: true, hint: undefined });
    expect(html).toContain("Workspace name");
    expect(html).not.toContain(tabName("Workspace name"));
  });
});
