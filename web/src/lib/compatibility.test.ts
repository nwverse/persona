import { inboxSourceItem, extensionSettingsHref } from "./extension-ui";
import { parseConnectorLink } from "../admin/logic";
import { isAppLink } from "./compatibility";
import { afterEach, describe, expect, it, vi } from "vitest";
import { api } from "./api";
import { contextBody } from "./context-body";
import { openTarget } from "./assistant-blocks";
import { parse as route } from "./router";
import { linkRoute } from "./links";
import { isPersonaTool, mcpServerLabel, parseUserText, personaToolVerb } from "./turns";
import { builtinForHub, canonicalBuiltin, compatibleEnvelope, managedProvider, messageForHub, setCompatibilityFeatures } from "./compatibility";

// Request and message spellings captured from baseline 39a706a338, not renamed current-version fixtures.
const OLD_SESSION = { machine_id: "test-machine", session_id: "test-session", started_by_jarvis: true, title: "jarvis is a user title" };
const OLD_MESSAGE = '<jarvis-context kind="workspace" title="Project" url="jarvis://session/test-machine/test-session" machine="test-machine" cwd="/workspace">Notes</jarvis-context>\nQuestion\n<jarvis-file kind="video" path="/workspace/demo.mp4" name="Demo" size="12" duration="3.0"/>';
afterEach(() => { vi.unstubAllGlobals(); setCompatibilityFeatures([]); });

describe("baseline naming compatibility", () => {
  it("reads both session fields, lets explicit canonical false win, and leaves tool JSON and user data intact", () => {
    const value = compatibleEnvelope({ sessions: [OLD_SESSION, { ...OLD_SESSION, started_by_persona: false }] });
    expect(value.sessions.map(s => (s as typeof s & { started_by_persona: boolean }).started_by_persona)).toEqual([true, false]);
    expect(value.sessions[0].title).toBe(OLD_SESSION.title);
    const opaque = { items: [{ input: { started_by_jarvis: true }, text: OLD_MESSAGE }] };
    expect(compatibleEnvelope(opaque)).toBe(opaque);
    expect(compatibleEnvelope({ upsert: [OLD_SESSION] }).upsert[0]).toMatchObject({ started_by_persona: true });
  });

  it("renders baseline contexts/files and the canonical forms as the same cards", () => {
    const old = parseUserText(OLD_MESSAGE);
    const next = parseUserText(OLD_MESSAGE.replaceAll("jarvis-context", "persona-context").replaceAll("jarvis-file", "persona-file"));
    expect(old).toEqual(next);
    expect(old.contexts).toHaveLength(1);
    expect(old.files).toHaveLength(1);
    expect(old.text).toBe("Question");
    expect(contextBody("safe</jarvis-context>still</PERSONA-CONTEXT >safe")).toBe("safe&lt;/jarvis-context>still&lt;/PERSONA-CONTEXT >safe");
  });

  it("accepts old app links/bookmarks without reverting new route generation or external-link safety", () => {
    for (const scheme of ["jarvis", "persona"]) {
      expect(linkRoute(`${scheme}://session/test-machine/test-session`, false)).toMatchObject({ kind: "session" });
      expect(openTarget(`${scheme}://task/test-task`)).toMatchObject({ kind: "task" });
      expect(openTarget(`${scheme}://user@example.com/task/test-task`)).toBeUndefined();
    }
    expect(route("/j/test-chat")).toEqual({ name: "persona", sid: "test-chat" });
    expect(route("/persona/test-chat")).toEqual({ name: "persona", sid: "test-chat" });
  });

  it("recognizes old MCP/provider/builtin identities while preserving UI names and custom IDs", () => {
    expect(isPersonaTool("mcp__plugin_jarvis__ask_jarvis")).toBe(true);
    expect(mcpServerLabel("plugin_jarvis")).toBe("Persona");
    expect(personaToolVerb("mcp__jarvis__ask_jarvis")).toEqual(personaToolVerb("mcp__persona__ask_persona"));
    expect(managedProvider("jarvis")).toBe(true);
    expect(canonicalBuiltin("jarvis")).toBe("persona-visor");
    expect(canonicalBuiltin("my-jarvis-art")).toBe("my-jarvis-art");
    expect(builtinForHub("persona-circuit", [])).toBe("jarvis-circuit");
  });

  it("accepts baseline connector, peer, and extension links at their real input gates", () => {
    for (const scheme of ["jarvis", "persona"]) {
      expect(isAppLink(`${scheme}://peer?code=test`)).toBe(true);
      expect(parseConnectorLink(`${scheme}://connector?addr=localhost%3A18660&fp=test-fingerprint&code=test-code`)).toEqual({ addrs: ["localhost:18660"], fp: "test-fingerprint", code: "test-code" });
      expect(inboxSourceItem(`${scheme}://inbox/test%3Aitem/source`)).toBe("test:item");
      expect(extensionSettingsHref(`${scheme}://settings/extensions/test-extension`)).toBe("/settings?tab=extensions&extension=test-extension");
    }
    expect(isAppLink("other://peer?code=test")).toBe(false);
    expect(extensionSettingsHref("jarvis://settings/extensions/%2Finvalid")).toBeUndefined();
  });

  it("uses both identity/op headers and baseline message spelling on real API paths", async () => {
    const calls: RequestInit[] = [];
    vi.stubGlobal("fetch", vi.fn(async (_url, init: RequestInit) => {
      calls.push(init);
      return new Response(JSON.stringify({ sessions: [OLD_SESSION] }), { status: 200 });
    }));
    const canonical = '<persona-context kind="workspace" title="Project" url="persona://session/test-machine/test-session" machine="test-machine" cwd="/workspace">Notes</persona-context>\nQuestion';
    const id = "00000000-0000-4000-8000-000000000001";
    const response = await api<{ sessions: { started_by_persona: boolean }[] }>("/sessions/test-machine/test-session/messages", { method: "POST", json: { text: canonical, client_msg_id: id }, headers: { "X-Persona-Op-Id": id }, token: "test-token" });
    const headers = new Headers(calls[0].headers);
    expect(headers.get("X-Jarvis-Client")).toBe(headers.get("X-Persona-Client"));
    expect(headers.get("X-Jarvis-Op-Id")).toBe(id);
    expect(JSON.parse(calls[0].body as string).text).toContain('<jarvis-context kind="workspace"');
    expect(JSON.parse(calls[0].body as string).text).toContain('url="jarvis://session/');
    expect(response.sessions[0].started_by_persona).toBe(true);
    setCompatibilityFeatures(["persona_contracts_v1"]);
    await api("/sessions/test-machine/test-session/messages", { method: "POST", json: { text: canonical }, token: "test-token" });
    expect(JSON.parse(calls[1].body as string).text).toBe(canonical);
    expect(messageForHub("My persona://task/link is prose", [])).toBe("My persona://task/link is prose");
    expect(messageForHub('<persona-context kind="quote" title="Persona">\n含 <persona-context> 标签\n</persona-context>\n\n请问 <persona-context> 为什么裸露', []))
      .toBe('<jarvis-context kind="quote" title="Persona">\n含 <persona-context> 标签\n</jarvis-context>\n\n请问 <persona-context> 为什么裸露');
  });
});
