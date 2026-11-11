// Going from a Persona conversation to an Agent it handed work to (#5096, after cindy's SessionHandoffCard): the
// Agent's own page, never a side panel. The Agent's page says where it came from ("Came from … · click to return",
// ChatView's HandoffBack), and going back puts the conversation's chat where it was (ChatView takes the place `leaveChat`
// kept on the conversation's history entry).
import { stripBase } from "./base";
import { REPIN_AT_BOTTOM } from "./disclosure";
import { navigate, routeAt, type Route } from "./router";
import { getState, taskSessionLookup, type State } from "./store";
import { mostUrgentAgent } from "./todos";

const FROM = "handoff";
const PLACE = "chatPlace";

/** Where an Agent's page was opened from: the conversation's address in the app (the page names it, ChatView). */
export interface HandoffFrom {
  path: string;
}

/** Where a chat was when the page left it: at its end, or the first line still in view and how far under the view's top
 *  it started (as search's "Back to where you were" keeps it). */
export interface ChatPlace {
  end: boolean;
  line?: string;
  offset: number;
}

function entry(state: unknown): Record<string, unknown> {
  return state && typeof state === "object" ? (state as Record<string, unknown>) : {};
}

const current = () => (typeof history !== "undefined" ? history.state : null);

/** The conversation the page now shown was opened from by a hand-off, if any. */
export function handoffFrom(state: unknown = current()): HandoffFrom | undefined {
  const v = entry(entry(state)[FROM]);
  return typeof v.path === "string" && v.path.startsWith("/") ? { path: v.path } : undefined;
}

/** The place this history entry's chat was left at, if a hand-off kept one. */
export function chatPlaceIn(state: unknown = current()): ChatPlace | undefined {
  const v = entry(entry(state)[PLACE]);
  if (typeof v.end !== "boolean") return undefined;
  return { end: v.end, line: typeof v.line === "string" ? v.line : undefined, offset: typeof v.offset === "number" && Number.isFinite(v.offset) ? v.offset : 0 };
}

/** Takes the kept place off this entry once it was used: a reload later opens the chat at its end, as before. */
export function dropChatPlace() {
  const { [PLACE]: _gone, ...rest } = entry(current());
  if (_gone !== undefined) history.replaceState(rest, "");
}

/** Where `sc`'s chat is now. */
export function chatPlaceOf(sc: HTMLElement): ChatPlace {
  if (sc.scrollHeight - sc.scrollTop - sc.clientHeight <= REPIN_AT_BOTTOM) return { end: true, offset: 0 };
  const top = sc.getBoundingClientRect().top;
  const at = [...sc.querySelectorAll<HTMLElement>("[data-line]")]
    .map((el) => ({ id: el.dataset.line, r: el.firstElementChild?.getBoundingClientRect() }))
    .find((x) => x.r && x.r.bottom > top);
  return { end: false, line: at?.id, offset: at?.r ? at.r.top - top : 0 };
}

/** Where a task opens: its most pressing Agent's page, or the to-do's own page while no Agent is on it (one that did
 *  not start has no page to show). */
export function taskRoute(id: string, s: State = getState()): Route {
  const todo = s.todos.find((x) => x.id === id);
  const lead = todo ? mostUrgentAgent({ ...todo, agents: todo.agents?.filter((a) => !a.start_error) }, taskSessionLookup(s))?.agent : undefined;
  return lead ? { name: "session", mid: lead.machine_id, sid: lead.session_id } : { name: "todos", id };
}

/** Opens a task from a conversation: its most pressing Agent's page, or the to-do's page while no Agent is on it. */
export function openTask(id: string) {
  leaveChat(taskRoute(id));
}

/** Opens an Agent's page from a conversation. */
export function openAgent(mid: string, sid: string) {
  leaveChat({ name: "session", mid, sid });
}

/**
 * Opens `to` as a page of its own. Left from an assistant conversation, the conversation's history entry keeps where
 * its chat is (Back puts it there again), and with `from` the new page's entry says which conversation it came from.
 */
export function leaveChat(to: Route, from = true) {
  const here = typeof location !== "undefined" ? routeAt(location.href) : null;
  if (here?.name !== "persona") return navigate(to);
  const sc = document.querySelector<HTMLElement>("[data-chat-scroll]");
  if (sc) history.replaceState({ ...entry(history.state), [PLACE]: chatPlaceOf(sc) }, "");
  navigate(to, false, from ? { [FROM]: { path: stripBase(location.pathname) + location.search } satisfies HandoffFrom } : null);
}
