// @vitest-environment jsdom
// Controlled ShellQuestionCard, SessionShellQuestion, TaskShellQuestion, and ShellQuestion bypass store/start-continue/api singletons and fence cross-session, cross-task, cross-question, and ABA races; default fallbacks stay pinned too.
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PlanTask } from "../lib/feed";
import type { AgentLaunch } from "../lib/new-agent";
import type { ShellQuestion as ShellQuestionModel } from "../lib/start-continue";
import type { Todo } from "../lib/todos";
import type { Session } from "../lib/types";
import type { ShellQuestionDeps } from "./ShellQuestionCard";

const mocks = vi.hoisted(() => ({
  errorText: vi.fn((e: unknown) => `default-err:${e instanceof Error ? e.message : String(e)}`),
  continueStart: vi.fn(),
  resendRefused: vi.fn(),
  answerTaskStart: vi.fn(),
  useStore: vi.fn(),
}));

vi.mock("../lib/api", async (orig) => ({
  ...(await orig<typeof import("../lib/api")>()),
  errorText: mocks.errorText,
}));
vi.mock("../lib/start-continue", async (orig) => ({
  ...(await orig<typeof import("../lib/start-continue")>()),
  continueStart: mocks.continueStart,
  resendRefused: mocks.resendRefused,
  answerTaskStart: mocks.answerTaskStart,
}));
vi.mock("../lib/store", async (orig) => ({
  ...(await orig<typeof import("../lib/store")>()),
  useStore: mocks.useStore,
}));

const {
  default: ShellQuestion,
  SessionShellQuestion,
  ShellQuestionCard,
  ShellQuestionCardView,
  TaskShellQuestion,
} = await import("./ShellQuestionCard");
const { shellQuestion, SHELL_WAITING, START_CONTINUE_FEATURE, TODO_START_ANSWER_FEATURE } = await import("../lib/start-continue");
const { initI18n, t } = await import("../i18n");

function deferred<T = void>() {
  let resolve!: (value: T | PromiseLike<T>) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const FIXED_NOW = Date.parse("2026-11-26T06:00:00.000Z");
const FUTURE_ISO = new Date(FIXED_NOW + 5 * 60_000).toISOString();
const LINE_A = "[oh-my-zsh] Would you like to update? [Y/n]";
const LINE_B = "Ignore insecure directories and continue [y] or abort compinit [n]?";

const makeArgs = (patch: Record<string, string> = {}): Record<string, string> => ({
  line: LINE_A,
  screen: "Last login: today\n" + LINE_A,
  kind: "update",
  choices: "y,n,enter",
  default: "y",
  kept_until: FUTURE_ISO,
  ...patch,
});

const makeQuestion = (patch: Record<string, string> = {}): ShellQuestionModel => shellQuestion(makeArgs(patch))!;

const makeTodo = (patch: Partial<Todo> = {}): Todo => ({
  id: "todo-a",
  title: "Inspect logs",
  origin: "conv-1",
  stage: "review",
  created_at: "2026-11-26T00:00:00Z",
  updated_at: "2026-11-26T00:00:00Z",
  ...patch,
});

const makeTask = (patch: Partial<PlanTask> = {}): PlanTask => ({
  title: "Inspect logs",
  prompt: "",
  machine: "mac",
  cwd: "/code/app",
  todo_id: "todo-a",
  error: "stopped on a question",
  error_code: SHELL_WAITING,
  error_args: makeArgs(),
  ...patch,
}) as PlanTask;

const makeSession = (patch: Partial<Session> = {}): Session => ({
  machine_id: "mac",
  session_id: "sess-a",
  title: "Agent A",
  cwd: "/code/app",
  status: "offline",
  start_error: "stopped",
  start_error_code: SHELL_WAITING,
  start_error_args: makeArgs(),
  ...patch,
}) as Session;

const makeLaunch = (patch: Partial<AgentLaunch> = {}): AgentLaunch => ({
  id: "launch-1",
  machineId: "mac",
  cwd: "/code/app",
  launcher: "",
  text: "fix it",
  attachments: [],
  phase: "failed",
  error: "stopped on a question",
  uploaded: {},
  startOpId: "o1",
  startPromptOpId: "o2",
  messageId: "m1",
  session: { machine_id: "mac", session_id: "sess-a" } as AgentLaunch["session"],
  shellWaiting: { code: SHELL_WAITING, ...makeArgs() },
  ...patch,
});

let host: HTMLDivElement;
let root: Root;

const flush = async () => {
  for (let i = 0; i < 6; i++) await act(async () => { await Promise.resolve(); });
};
const card = () => host.querySelector<HTMLElement>("[data-testid=shell-question]");
const choiceBtn = (c: string) => host.querySelector<HTMLButtonElement>(`[data-testid=shell-choice-${c}]`);
const alertText = () => host.querySelector("[role=alert]")?.textContent ?? "";
const detailsBtn = () => [...host.querySelectorAll("button")].find((b) => b.textContent?.includes(t("ui-errornotice.details")));

const expectNoDefaultSingletons = () => {
  expect(mocks.useStore).not.toHaveBeenCalled();
  expect(mocks.continueStart).not.toHaveBeenCalled();
  expect(mocks.resendRefused).not.toHaveBeenCalled();
  expect(mocks.answerTaskStart).not.toHaveBeenCalled();
  expect(mocks.errorText).not.toHaveBeenCalled();
};

beforeEach(async () => {
  (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  await initI18n("en");
  Object.values(mocks).forEach((m) => m.mockReset());
  mocks.errorText.mockImplementation((e: unknown) => `default-err:${e instanceof Error ? e.message : String(e)}`);
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(() => {
  act(() => root.unmount());
  host.remove();
  vi.useRealTimers();
});

describe("ShellQuestionCard controlled deps, view separation, and race guards", () => {
  it("runs SessionShellQuestion fully controlled via deps without touching default singletons", async () => {
    const req1 = deferred();
    const req2 = deferred();
    const continueFn = vi.fn().mockImplementationOnce(() => req1.promise).mockImplementationOnce(() => req2.promise);
    const resendFn = vi.fn();
    const formatError = vi.fn((e: unknown) => `custom:${e instanceof Error ? e.message : String(e)}`);
    const onAnswered = vi.fn();
    const deps: ShellQuestionDeps = {
      features: [START_CONTINUE_FEATURE],
      continueStart: continueFn,
      resendRefused: resendFn,
      formatError,
      now: FIXED_NOW,
    };

    await act(async () => root.render(
      <StrictMode>
        <SessionShellQuestion mid="mac" sid="sess-a" args={makeArgs()} onAnswered={onAnswered} deps={deps} />
      </StrictMode>,
    ));
    expect(card()).not.toBeNull();

    // Synchronous double-click and concurrent second-choice click admit only one submission.
    await act(async () => {
      choiceBtn("y")!.click();
      choiceBtn("y")!.click();
      choiceBtn("n")!.click();
    });
    expect(continueFn).toHaveBeenCalledTimes(1);
    expect(continueFn).toHaveBeenCalledWith("mac", "sess-a", "y");
    expect(choiceBtn("y")!.disabled).toBe(true);
    expect(choiceBtn("y")!.getAttribute("aria-busy")).toBe("true");
    expect(choiceBtn("n")!.disabled).toBe(true);

    // Rejection displays custom error and re-enables controls without calling resendRefused/onAnswered.
    await act(async () => req1.reject(new Error("tab refused")));
    await flush();
    expect(alertText()).toBe("custom:tab refused");
    expect(choiceBtn("y")!.disabled).toBe(false);
    expect(choiceBtn("n")!.disabled).toBe(false);
    expect(resendFn).not.toHaveBeenCalled();
    expect(onAnswered).not.toHaveBeenCalled();

    // Retry clears error and resolves -> triggers controlled resendRefused and onAnswered once.
    await act(async () => choiceBtn("n")!.click());
    expect(alertText()).toBe("");
    await act(async () => req2.resolve());
    await flush();
    expect(continueFn).toHaveBeenCalledWith("mac", "sess-a", "n");
    expect(resendFn).toHaveBeenCalledTimes(1);
    expect(resendFn).toHaveBeenCalledWith("mac", "sess-a");
    expect(onAnswered).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it("honors controlled capability overrides (canContinueStart, canAnswerTaskStart, hasFeature, features)", async () => {
    await act(async () => root.render(
      <SessionShellQuestion mid="mac" sid="sess-a" args={makeArgs()} deps={{ canContinueStart: false, now: FIXED_NOW }} />,
    ));
    expect(card()).toBeNull();

    await act(async () => root.render(
      <TaskShellQuestion todo={makeTodo()} task={makeTask()} deps={{ hasFeature: () => false, now: FIXED_NOW }} />,
    ));
    expect(card()).toBeNull();

    await act(async () => root.render(
      <TaskShellQuestion todo={makeTodo()} task={makeTask()} deps={{ canAnswerTaskStart: true, answerTaskStart: vi.fn(async () => {}), now: FIXED_NOW }} />,
    ));
    expect(card()).not.toBeNull();
    expectNoDefaultSingletons();
  });

  it("clears busy, error, and folded details across session switches (even with identical question) and fences stale settle", async () => {
    const reqA = deferred();
    const reqB = deferred();
    const continueFn = vi.fn((_m: string, sid: string) => (sid === "sess-a" ? reqA.promise : reqB.promise));
    const resendFn = vi.fn();
    const onAnswered = vi.fn();
    const deps: ShellQuestionDeps = {
      canContinueStart: true,
      continueStart: continueFn,
      resendRefused: resendFn,
      formatError: (e) => (e instanceof Error ? e.message : String(e)),
      now: FIXED_NOW,
    };

    // Expand details and submit on sess-a.
    await act(async () => root.render(
      <SessionShellQuestion mid="mac" sid="sess-a" args={makeArgs()} onAnswered={onAnswered} deps={deps} />,
    ));
    await act(async () => detailsBtn()!.click());
    expect(card()!.textContent).toContain("Last login");
    await act(async () => choiceBtn("y")!.click());
    expect(choiceBtn("y")!.disabled).toBe(true);

    // Switch to sess-b with identical args: busy clears and details collapse immediately.
    await act(async () => root.render(
      <SessionShellQuestion mid="mac" sid="sess-b" args={makeArgs()} onAnswered={onAnswered} deps={deps} />,
    ));
    expect(choiceBtn("y")!.disabled).toBe(false);
    expect(alertText()).toBe("");
    expect(card()!.textContent).not.toContain("Last login");

    // Submit on sess-b, then settle stale reqA -> must not trigger resend/onAnswered or clear sess-b busy.
    await act(async () => choiceBtn("n")!.click());
    expect(choiceBtn("n")!.disabled).toBe(true);
    await act(async () => reqA.resolve());
    await flush();
    expect(resendFn).not.toHaveBeenCalled();
    expect(onAnswered).not.toHaveBeenCalled();
    expect(choiceBtn("n")!.disabled).toBe(true);

    // Finish sess-b cleanly.
    await act(async () => reqB.resolve());
    await flush();
    expect(resendFn).toHaveBeenCalledTimes(1);
    expect(resendFn).toHaveBeenCalledWith("mac", "sess-b");
    expect(onAnswered).toHaveBeenCalledTimes(1);
    expectNoDefaultSingletons();
  });

  it.each(["resolve", "reject"] as const)(
    "fences ABA out-of-order %s across session switch A -> B -> A while A's first request is still pending",
    async (staleOutcome) => {
      const reqA1 = deferred();
      const reqA2 = deferred();
      let aCalls = 0;
      const continueFn = vi.fn(() => (++aCalls === 1 ? reqA1.promise : reqA2.promise));
      const resendFn = vi.fn();
      const onAnswered = vi.fn();
      const deps: ShellQuestionDeps = {
        canContinueStart: true,
        continueStart: continueFn,
        resendRefused: resendFn,
        formatError: (e) => (e instanceof Error ? e.message : String(e)),
        now: FIXED_NOW,
      };

      // Visit 1 on sess-a: start reqA1.
      await act(async () => root.render(
        <SessionShellQuestion mid="mac" sid="sess-a" args={makeArgs()} onAnswered={onAnswered} deps={deps} />,
      ));
      await act(async () => choiceBtn("y")!.click());
      expect(choiceBtn("y")!.disabled).toBe(true);

      // Switch sess-a -> sess-b -> sess-a (ABA): visit 2 starts unlocked.
      await act(async () => root.render(
        <SessionShellQuestion mid="mac" sid="sess-b" args={makeArgs()} onAnswered={onAnswered} deps={deps} />,
      ));
      await act(async () => root.render(
        <SessionShellQuestion mid="mac" sid="sess-a" args={makeArgs()} onAnswered={onAnswered} deps={deps} />,
      ));
      expect(choiceBtn("y")!.disabled).toBe(false);

      await act(async () => choiceBtn("n")!.click());
      expect(aCalls).toBe(2);
      expect(choiceBtn("n")!.disabled).toBe(true);

      // Stale reqA1 settles out of order before reqA2: must not fire resend/onAnswered, set error, or clear busy.
      await act(async () => {
        if (staleOutcome === "resolve") reqA1.resolve();
        else reqA1.reject(new Error("stale ABA rejection"));
      });
      await flush();
      expect(resendFn).not.toHaveBeenCalled();
      expect(onAnswered).not.toHaveBeenCalled();
      expect(alertText()).toBe("");
      expect(choiceBtn("n")!.disabled).toBe(true);

      // Current reqA2 resolves cleanly.
      await act(async () => reqA2.resolve());
      await flush();
      expect(resendFn).toHaveBeenCalledTimes(1);
      expect(resendFn).toHaveBeenCalledWith("mac", "sess-a");
      expect(onAnswered).toHaveBeenCalledTimes(1);
      expectNoDefaultSingletons();
    },
  );

  it.each(["resolve", "reject"] as const)(
    "clears error/busy and fences ABA %s across planned task switches (todo-a -> todo-b -> todo-a)",
    async (staleOutcome) => {
      const req1 = deferred();
      const req2 = deferred();
      let calls = 0;
      const answerTask = vi.fn(() => (++calls === 1 ? req1.promise : req2.promise));
      const onAnswered = vi.fn();
      const deps: ShellQuestionDeps = {
        features: [TODO_START_ANSWER_FEATURE],
        answerTaskStart: answerTask,
        formatError: (e) => (e instanceof Error ? e.message : String(e)),
        now: FIXED_NOW,
      };
      const todoA = makeTodo({ id: "todo-a" });
      const todoB = makeTodo({ id: "todo-b" });
      const taskA = makeTask({ todo_id: "todo-a" });
      const taskB = makeTask({ todo_id: "todo-b" });

      // Visit 1 on todo-a.
      await act(async () => root.render(<ShellQuestion todo={todoA} task={taskA} onAnswered={onAnswered} deps={deps} />));
      await act(async () => choiceBtn("y")!.click());
      expect(answerTask).toHaveBeenCalledWith(todoA, "y");
      expect(choiceBtn("y")!.disabled).toBe(true);

      // Switch todo-a -> todo-b -> todo-a.
      await act(async () => root.render(<ShellQuestion todo={todoB} task={taskB} onAnswered={onAnswered} deps={deps} />));
      expect(choiceBtn("y")!.disabled).toBe(false);
      await act(async () => root.render(<ShellQuestion todo={todoA} task={taskA} onAnswered={onAnswered} deps={deps} />));
      await act(async () => choiceBtn("n")!.click());
      expect(calls).toBe(2);
      expect(choiceBtn("n")!.disabled).toBe(true);

      // Stale req1 settles out of order: completely ignored.
      await act(async () => {
        if (staleOutcome === "resolve") req1.resolve();
        else req1.reject(new Error("stale task failure"));
      });
      await flush();
      expect(onAnswered).not.toHaveBeenCalled();
      expect(alertText()).toBe("");
      expect(choiceBtn("n")!.disabled).toBe(true);

      await act(async () => req2.resolve());
      await flush();
      expect(onAnswered).toHaveBeenCalledTimes(1);
      expectNoDefaultSingletons();
    },
  );

  it("clears error on in-place question switch, supports subscribed launch rows, respects controlled now, and fences unmount", async () => {
    let liveRow: Session | undefined;
    const listeners = new Set<() => void>();
    const emitRow = (row: Session | undefined) => {
      liveRow = row;
      listeners.forEach((l) => l());
    };
    const req1 = deferred();
    const req2 = deferred();
    const continueFn = vi.fn().mockImplementationOnce(() => req1.promise).mockImplementationOnce(() => req2.promise);
    const resendFn = vi.fn();
    const onAnswered = vi.fn();
    let clock = FIXED_NOW;
    const deps: ShellQuestionDeps = {
      features: [START_CONTINUE_FEATURE],
      getSession: () => liveRow,
      subscribe: (l) => {
        listeners.add(l);
        return () => { listeners.delete(l); };
      },
      continueStart: continueFn,
      resendRefused: resendFn,
      formatError: (e) => (e instanceof Error ? e.message : String(e)),
      now: () => clock,
    };

    const launch = makeLaunch();
    await act(async () => root.render(<ShellQuestion launch={launch} onAnswered={onAnswered} deps={deps} />));
    expect(card()!.textContent).toContain(LINE_A);

    // Fail first question so error is visible.
    await act(async () => choiceBtn("y")!.click());
    await act(async () => req1.reject(new Error("old question failed")));
    await flush();
    expect(alertText()).toBe("old question failed");

    // External session row renews the question via subscribe -> error clears immediately.
    await act(async () => emitRow(makeSession({
      start_error_args: makeArgs({ line: LINE_B, kind: "compinit", choices: "y,n", default: "" }),
    })));
    await flush();
    expect(card()!.textContent).toContain(LINE_B);
    expect(alertText()).toBe("");

    // Submit on renewed question, then unmount before settlement -> late resolve is ignored.
    await act(async () => choiceBtn("y")!.click());
    await act(async () => root.render(null));
    await act(async () => req2.resolve());
    await flush();
    expect(resendFn).not.toHaveBeenCalled();
    expect(onAnswered).not.toHaveBeenCalled();

    // Expired controlled now hides the card without starting timers.
    clock = Date.parse(FUTURE_ISO) + 1_000;
    await act(async () => root.render(<ShellQuestion launch={launch} deps={deps} />));
    expect(card()).toBeNull();
    expectNoDefaultSingletons();
  });

  it("resets ShellQuestionCard local error/busy directly when scopeKey or question changes and catches synchronous answer throws", async () => {
    const req1 = deferred();
    const answer = vi.fn().mockImplementationOnce(() => { throw new Error("sync boom"); }).mockImplementationOnce(() => req1.promise);
    const onAnswered = vi.fn();
    const formatError = vi.fn((e: unknown) => `fmt:${e instanceof Error ? e.message : String(e)}`);

    await act(async () => root.render(
      <ShellQuestionCard scopeKey="scope-1" question={makeQuestion()} answer={answer} onAnswered={onAnswered} deps={{ formatError, now: FIXED_NOW }} />,
    ));
    await act(async () => choiceBtn("y")!.click());
    await flush();
    expect(alertText()).toBe("fmt:sync boom");

    // Changing scopeKey clears the prior error immediately without remounting.
    await act(async () => root.render(
      <ShellQuestionCard scopeKey="scope-2" question={makeQuestion()} answer={answer} onAnswered={onAnswered} deps={{ formatError, now: FIXED_NOW }} />,
    ));
    expect(alertText()).toBe("");

    // Start async answer on scope-2, then change question line -> busy resets and stale resolve is ignored.
    await act(async () => choiceBtn("y")!.click());
    expect(choiceBtn("y")!.disabled).toBe(true);
    await act(async () => root.render(
      <ShellQuestionCard scopeKey="scope-2" question={makeQuestion({ line: LINE_B })} answer={answer} onAnswered={onAnswered} deps={{ formatError, now: FIXED_NOW }} />,
    ));
    expect(choiceBtn("y")!.disabled).toBe(false);
    await act(async () => req1.resolve());
    await flush();
    expect(onAnswered).not.toHaveBeenCalled();
    expectNoDefaultSingletons();
  });

  it("renders pure ShellQuestionCardView without touching default singletons", async () => {
    const onPick = vi.fn();
    await act(async () => root.render(
      <ShellQuestionCardView question={makeQuestion()} busyChoice="y" error="Pure view alert" onPick={onPick} />,
    ));
    expect(card()!.textContent).toContain(LINE_A);
    expect(alertText()).toBe("Pure view alert");
    expect(choiceBtn("y")!.disabled).toBe(true);
    expect(choiceBtn("n")!.disabled).toBe(true);

    await act(async () => root.render(
      <ShellQuestionCardView question={makeQuestion()} onPick={onPick} />,
    ));
    await act(async () => choiceBtn("n")!.click());
    expect(onPick).toHaveBeenCalledWith("n");
    expectNoDefaultSingletons();
  });

  it("preserves default store, continueStart, resendRefused, answerTaskStart, and errorText fallbacks when deps are omitted", async () => {
    const storeState = {
      features: [START_CONTINUE_FEATURE, TODO_START_ANSWER_FEATURE],
      sessions: { mac: [makeSession()] },
    };
    mocks.useStore.mockImplementation((sel: (s: typeof storeState) => unknown) => sel(storeState));
    mocks.continueStart.mockRejectedValueOnce(new Error("boom")).mockResolvedValueOnce(undefined);
    mocks.answerTaskStart.mockResolvedValueOnce(undefined);

    await act(async () => root.render(<ShellQuestion launch={makeLaunch()} />));
    expect(mocks.useStore).toHaveBeenCalled();
    await act(async () => choiceBtn("y")!.click());
    await flush();
    expect(mocks.continueStart).toHaveBeenCalledWith("mac", "sess-a", "y");
    expect(mocks.errorText).toHaveBeenCalled();
    expect(alertText()).toBe("default-err:boom");

    await act(async () => choiceBtn("n")!.click());
    await flush();
    expect(mocks.resendRefused).toHaveBeenCalledWith("mac", "sess-a");

    const todo = makeTodo();
    await act(async () => root.render(<ShellQuestion todo={todo} task={makeTask()} />));
    await act(async () => choiceBtn("y")!.click());
    await flush();
    expect(mocks.answerTaskStart).toHaveBeenCalledWith(todo, "y");
  });
});
