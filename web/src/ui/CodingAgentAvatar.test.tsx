import { describe, expect, it } from "vitest";
import { renderToStaticMarkup } from "react-dom/server";
import {
  codingAgentAvatarLabel,
  KNOWN_HOSTING_ENGINES,
  resolveCodingAgentAvatar,
  resolveHostingEngine,
  resolveModelVendorMark,
  type CodingAgentAvatarSession,
} from "../lib/coding-agent-avatar";
import { CodingAgentAvatar, HostingEngineBadge } from "./CodingAgentAvatar";
import { AgentDeviceMark } from "../views/chat/ChatView";
import type { Session } from "../lib/types";

describe("coding-agent-avatar resolvers", () => {
  it("maps model identifiers (including vendor-prefixed gateway IDs) to bundled vendor marks", () => {
    expect(resolveModelVendorMark("claude-opus-5-5")).toEqual({ id: "claude", label: "Claude" });
    expect(resolveModelVendorMark("anthropic/claude-sonnet-4-6[1m]")).toEqual({ id: "claude", label: "Claude" });
    expect(resolveModelVendorMark("gpt-5.4")).toEqual({ id: "codex", label: "OpenAI" });
    expect(resolveModelVendorMark("o3-mini")).toEqual({ id: "codex", label: "OpenAI" });
    expect(resolveModelVendorMark("openai/codex-mini")).toEqual({ id: "codex", label: "OpenAI" });
    expect(resolveModelVendorMark("gemini-2.5-pro")).toEqual({ id: "gemini", label: "Gemini" });
    expect(resolveModelVendorMark("google-antigravity/gemini-4-argon:high")).toEqual({ id: "gemini", label: "Gemini" });
    expect(resolveModelVendorMark("deepseek-r1")).toEqual({ id: "dsh", label: "DeepSeek" });
    expect(resolveModelVendorMark("moonshot/kimi-k2")).toEqual({ id: "kimi", label: "Kimi" });
    expect(resolveModelVendorMark("xai/grok-3")).toEqual({ id: "grok", label: "Grok" });
    expect(resolveModelVendorMark("glm-4.7")).toBeUndefined();
    expect(resolveModelVendorMark("")).toBeUndefined();
  });

  it("maps known coding agents, Paseo provider launchers, and gateway model overrides accurately", () => {
    expect(resolveCodingAgentAvatar({ agent: "claude", model: "claude-opus-5-5" }).mark).toBe("claude");
    expect(resolveCodingAgentAvatar({ agent: "codex", model: "gpt-5.4" }).mark).toBe("codex");
    expect(resolveCodingAgentAvatar({ agent: "agy", model: "gemini-3-pro" }).mark).toBe("agy");
    expect(resolveCodingAgentAvatar({ agent: "gemini", model: "gemini-2.5-pro" }).mark).toBe("gemini");
    expect(resolveCodingAgentAvatar({ agent: "cursor", model: "claude-sonnet-4-6" }).mark).toBe("cursor");
    expect(resolveCodingAgentAvatar({ agent: "pi", model: "gpt-5" }).mark).toBe("pi");
    expect(resolveCodingAgentAvatar({ agent: "traex", model: "claude-sonnet-4-6" }).mark).toBe("trae");
    expect(resolveCodingAgentAvatar({ agent: "opencode" }).mark).toBe("opencode");
    expect(resolveCodingAgentAvatar({ agent: "paseo", launcher: "cursor", agent_label: "Cursor" }).mark).toBe("cursor");
    expect(resolveCodingAgentAvatar({ agent: "paseo", launcher: "traecli", agent_label: "TRAE CLI" }).mark).toBe("trae");

    // Claude Code proxying another vendor's model reflects the model's vendor mark.
    const proxied = resolveCodingAgentAvatar({ agent: "claude", model: "deepseek-r1" });
    expect(proxied.mark).toBe("dsh");
    expect(proxied.label).toBe("DeepSeek");

    // Unmarked custom harness running a recognized model uses the model vendor's mark.
    const customWithModel = resolveCodingAgentAvatar({ agent: "my-cli", agent_label: "My CLI", model: "kimi-k2" });
    expect(customWithModel.mark).toBe("kimi");
    expect(customWithModel.label).toBe("Kimi");
  });

  it("degrades unknown agents and models gracefully to a single uppercase letter", () => {
    const unknown = resolveCodingAgentAvatar({ agent: "acme-bot", agent_label: "Acme Bot", model: "internal-v2" });
    expect(unknown.mark).toBeNull();
    expect(unknown.letter).toBe("A");
    expect(unknown.label).toBe("Acme Bot");

    const omp = resolveCodingAgentAvatar({ agent: "omp" });
    expect(omp.mark).toBeNull();
    expect(omp.letter).toBe("O");

    const blank = resolveCodingAgentAvatar({ agent: "paseo", launcher: "???" });
    expect(blank.mark).toBeNull();
    expect(blank.letter).toBe("?");
  });

  it("resolves hosting engines from host, space_kind, pane_id, and runtime source_instance_id", () => {
    for (const id of KNOWN_HOSTING_ENGINES) {
      const resolved = resolveHostingEngine({ host: id });
      expect(resolved?.id).toBe(id);
      expect(resolved?.kind).toBe(id);
      expect(resolved?.name.length).toBeGreaterThan(0);
    }
    expect(resolveHostingEngine({ space_kind: "orca" })?.id).toBe("orca");
    expect(resolveHostingEngine({ pane_id: "orca:w1:p2" })?.id).toBe("orca");
    expect(resolveHostingEngine({ pane_id: "w9:p1" })?.id).toBe("herdr");
    expect(resolveHostingEngine({ pane_id: "herdr-core:t1" })?.id).toBe("herdr-core");
    expect(resolveHostingEngine({ source_kind: "runtime", source_instance_id: "orca" })?.id).toBe("orca");
    // Standalone desktop chats or historical rows without a host show no engine badge.
    expect(resolveHostingEngine({ space_kind: "codex_app" })).toBeNull();
    expect(resolveHostingEngine({})).toBeNull();

    // Unknown/future host identifier falls back cleanly to a custom letter badge.
    const custom = resolveHostingEngine({ host: "nebula" });
    expect(custom).toEqual({ id: "nebula", kind: "custom", name: "nebula", letter: "N" });
  });

  it("formats combined avatar + hosting engine labels cleanly", () => {
    const avatar = resolveCodingAgentAvatar({ agent: "claude", model: "claude-opus-5-5" });
    const orca = resolveHostingEngine({ host: "orca" });
    expect(codingAgentAvatarLabel(avatar, orca)).toBe("Claude Code · Orca");
    expect(codingAgentAvatarLabel(avatar, null)).toBe("Claude Code");
  });
});

describe("CodingAgentAvatar & HostingEngineBadge rendering", () => {
  it("renders a 40px circular contact avatar with model mark and bottom-right engine badge", () => {
    const session: CodingAgentAvatarSession = { agent: "claude", model: "claude-opus-5-5", host: "orca" };
    const html = renderToStaticMarkup(<CodingAgentAvatar session={session} size={40} />);
    expect(html).toContain('data-coding-agent-avatar="claude"');
    expect(html).toContain('data-hosting-engine="orca"');
    expect(html).toContain('data-agent-mark="claude"');
    expect(html).toContain('data-engine-badge="orca"');
    expect(html).toContain('aria-label="Claude Code · Orca"');
    expect(html).toContain('title="Claude Code · Orca"');
    expect(html).toContain("width:40px;height:40px");
    // Engine badge is positioned at bottom-right with a surface ring.
    expect(html).toMatch(/data-engine-badge="orca"[^>]*class="[^"]*-right-0\.5 -bottom-0\.5/);
    // Does not draw the CLI </> sub-badge inside the contact avatar so it never collides with the engine badge.
    expect(html).not.toContain('data-agent-badge="cli"');
  });

  it("renders distinct vector glyphs for every known hosting engine and a letter fallback for custom hosts", () => {
    const svgs = KNOWN_HOSTING_ENGINES.map((id) => {
      const engine = resolveHostingEngine({ host: id })!;
      return renderToStaticMarkup(<HostingEngineBadge engine={engine} size={16} />);
    });
    expect(new Set(svgs).size).toBe(KNOWN_HOSTING_ENGINES.length);

    const customHtml = renderToStaticMarkup(
      <HostingEngineBadge engine={resolveHostingEngine({ host: "zephyr" })!} size={16} />,
    );
    expect(customHtml).toContain('data-engine-badge="zephyr"');
    expect(customHtml).toContain('data-engine-kind="custom"');
    expect(customHtml).toContain(">Z</span>");
  });

  it("places live status at top-right and hosting engine badge at bottom-right with zero overlap", () => {
    const html = renderToStaticMarkup(
      <CodingAgentAvatar
        session={{ agent: "codex", model: "gpt-5.4", host: "herdr" }}
        size={40}
        ring="canvas"
        status={{ state: "needs_you" }}
      />,
    );
    expect(html).toMatch(/data-avatar-status="needs_you"[^>]*class="[^"]*-top-0\.5 -right-0\.5/);
    expect(html).toMatch(/data-engine-badge="herdr"[^>]*class="[^"]*-right-0\.5 -bottom-0\.5/);
    expect(html).toContain("ring-canvas");
  });

  it("renders a clean single-letter fallback for unknown agents", () => {
    const html = renderToStaticMarkup(
      <CodingAgentAvatar session={{ agent: "mystery-agent", agent_label: "Mystery", host: "t3code" }} size={40} />,
    );
    expect(html).toContain('data-coding-agent-avatar="letter"');
    expect(html).toContain('data-agent-mark="letter"');
    expect(html).toContain(">M</span>");
    expect(html).toContain('data-engine-badge="t3code"');
    expect(html).toContain('aria-label="Mystery · T3 Code"');
  });

  it("preserves AgentDeviceMark semantics while showing the hosting engine badge and combined label when hosted", () => {
    const hosted = {
      machine_id: "m1",
      session_id: "s1",
      title: "Refactor auth",
      cwd: "/work/app",
      status: "idle",
      state: "idle",
      agent: "agy",
      model: "gemini-3-pro",
      host: "paseo",
    } as Session;
    const html = renderToStaticMarkup(<AgentDeviceMark session={hosted} machineName="Workstation" />);
    expect(html).toContain('data-agent-device="online"');
    expect(html).toContain('data-coding-agent-avatar="agy"');
    expect(html).toContain('data-engine-badge="paseo"');
    expect(html).toContain("data-agent-remote");
    expect(html).toContain("Antigravity CLI · Paseo");
  });
});
