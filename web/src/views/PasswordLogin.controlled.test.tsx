// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { NativeLogin, PasswordCredential, PasswordState } from "../lib/password";

const globals = vi.hoisted(() => ({
  passwordApi: vi.fn<(path: string, json?: unknown) => Promise<PasswordState>>(),
  loginWithPassword: vi.fn<(password: string, native: NativeLogin | null) => Promise<PasswordCredential | { code: string }>>(),
  readNativeLogin: vi.fn<(href: string) => NativeLogin | null>(),
  pair: vi.fn<(input: string) => Promise<void>>(),
  setToken: vi.fn<(token: string, id?: string) => void>(),
  errorText: vi.fn<(err: unknown) => string>((e) => (e instanceof Error ? e.message : String(e))),
}));

vi.mock("../lib/password", () => ({
  passwordApi: (...args: [path: string, json?: unknown]) => globals.passwordApi(...args),
  loginWithPassword: (password: string, native: NativeLogin | null) => globals.loginWithPassword(password, native),
  readNativeLogin: (href: string) => globals.readNativeLogin(href),
}));

vi.mock("../lib/auth", () => ({
  pair: (input: string) => globals.pair(input),
}));

vi.mock("../lib/api", () => ({
  setToken: (token: string, id?: string) => globals.setToken(token, id),
  errorText: (err: unknown) => globals.errorText(err),
}));

import { initI18n, t } from "../i18n";
import {
  NativePasswordLogin,
  PasswordLogin,
  PasswordLoginView,
  type NativePasswordLoginDeps,
  type PasswordLoginDeps,
} from "./PasswordLogin";

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

const enabledState: PasswordState = { enabled: true, hub_id: "hub-1", hub_name: "Hub", suite: "v1" };
const disabledState: PasswordState = { enabled: false, hub_id: "hub-1", hub_name: "Hub", suite: "v1" };
const nativeA: NativeLogin = { state: "A".repeat(43), challenge: "C".repeat(43), kind: "phone", name: "Phone A" };
const nativeB: NativeLogin = { state: "B".repeat(43), challenge: "D".repeat(43), kind: "desktop", name: "Mac B" };

let host: HTMLDivElement;
let root: Root;

function expectNoGlobalCalls() {
  expect(globals.passwordApi).not.toHaveBeenCalled();
  expect(globals.loginWithPassword).not.toHaveBeenCalled();
  expect(globals.readNativeLogin).not.toHaveBeenCalled();
  expect(globals.pair).not.toHaveBeenCalled();
  expect(globals.setToken).not.toHaveBeenCalled();
  expect(globals.errorText).not.toHaveBeenCalled();
}

function inputEl(): HTMLInputElement {
  return host.querySelector<HTMLInputElement>('input[type="password"]')!;
}

function submitButton(): HTMLButtonElement {
  return host.querySelector<HTMLButtonElement>('button[type="submit"]')!;
}

function modeToggleButton(): HTMLButtonElement | null {
  return host.querySelector<HTMLButtonElement>('button[type="button"]');
}

function alertText(): string {
  return host.querySelector('[role="alert"]')?.textContent ?? "";
}

async function typeValue(value: string) {
  await act(async () => {
    const el = inputEl();
    Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(el, value);
    el.dispatchEvent(new Event("input", { bubbles: true }));
  });
}

async function submitForm() {
  await act(async () => {
    host.querySelector("form")!.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
  });
}

beforeEach(async () => {
  await initI18n("en");
  globals.passwordApi.mockReset();
  globals.loginWithPassword.mockReset();
  globals.readNativeLogin.mockReset();
  globals.pair.mockReset();
  globals.setToken.mockReset();
  globals.errorText.mockReset().mockImplementation((e) => (e instanceof Error ? e.message : String(e)));
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
});

afterEach(async () => {
  await act(async () => root.unmount());
  host.remove();
});

describe("PasswordLoginView", () => {
  it("renders password mode, token mode, native mode, errors, and appCallback purely with zero singleton calls", async () => {
    const onTokenChange = vi.fn();
    const onSubmit = vi.fn((e: React.FormEvent) => e.preventDefault());
    const onToggleMode = vi.fn();

    await act(async () => {
      root.render(
        <PasswordLoginView
          native={null}
          mode="password"
          passwordEnabled
          loading={false}
          busy={false}
          token="secret"
          error="bad password"
          appCallback=""
          onTokenChange={onTokenChange}
          onSubmit={onSubmit}
          onToggleMode={onToggleMode}
        />,
      );
    });
    expect(inputEl().placeholder).toBe(t("password.connection_password"));
    expect(alertText()).toBe("bad password");
    await act(async () => modeToggleButton()!.click());
    expect(onToggleMode).toHaveBeenCalledOnce();
    await submitForm();
    expect(onSubmit).toHaveBeenCalledOnce();

    await act(async () => {
      root.render(
        <PasswordLoginView
          native={nativeA}
          mode="password"
          passwordEnabled
          loading={false}
          busy={false}
          token=""
          error=""
          appCallback="persona://password-login?code=abc&state=xyz"
          onTokenChange={onTokenChange}
          onSubmit={onSubmit}
          onToggleMode={onToggleMode}
        />,
      );
    });
    expect(host.querySelector("a")?.getAttribute("href")).toBe("persona://password-login?code=abc&state=xyz");
    expect(modeToggleButton()).toBeNull();
    expectNoGlobalCalls();
  });
});

describe("PasswordLogin controlled mode", () => {
  it("supports browser password login, token mode toggle, and error formatting with zero singleton calls", async () => {
    const onDone = vi.fn();
    const deps: PasswordLoginDeps = {
      passwordApi: vi.fn(async () => enabledState),
      loginWithPassword: vi.fn(async () => ({ token: "jd_pw", id: "dev_pw", hub_id: "hub-1" })),
      pair: vi.fn(async () => {}),
      setToken: vi.fn(),
      formatError: vi.fn((err) => (err instanceof Error ? err.message : String(err))),
      assignLocation: vi.fn(),
    };

    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} deps={deps} />);
    });
    expect(deps.passwordApi).toHaveBeenCalledExactlyOnceWith("/state");
    expect(inputEl().disabled).toBe(false);

    // Password login preserves spaces, clears input immediately, persists token, and calls onDone
    await typeValue("  my-pass  ");
    await submitForm();
    expect(deps.loginWithPassword).toHaveBeenCalledExactlyOnceWith("  my-pass  ", null);
    expect(inputEl().value).toBe("");
    expect(deps.setToken).toHaveBeenCalledExactlyOnceWith("jd_pw", "dev_pw");
    expect(onDone).toHaveBeenCalledOnce();

    // Invalid browser login result (missing token) surfaces password.invalid_result
    vi.mocked(deps.loginWithPassword!).mockResolvedValueOnce({ code: "wrong-shape" });
    await typeValue("bad-shape-pw");
    await submitForm();
    expect(alertText()).toBe(t("password.invalid_result"));
    expect(onDone).toHaveBeenCalledOnce();

    // Toggle to token mode and test invalid-token translation + trimmed pair
    await act(async () => modeToggleButton()!.click());
    expect(inputEl().placeholder).toBe("hub token");
    vi.mocked(deps.pair!).mockRejectedValueOnce(new Error("invalid token"));
    await typeValue("  bad-tok  ");
    await submitForm();
    expect(deps.pair).toHaveBeenCalledWith("bad-tok");
    expect(alertText()).toBe(t("password.invalid_token"));

    vi.mocked(deps.pair!).mockResolvedValueOnce();
    await submitForm();
    expect(onDone).toHaveBeenCalledTimes(2);
    expect(alertText()).toBe("");
    expectNoGlobalCalls();
  });

  it("handles disabled/failing /state, invalid password results, and NativePasswordLogin via controlled deps", async () => {
    const assignLocation = vi.fn();
    const onDone = vi.fn();
    const deps: NativePasswordLoginDeps = {
      readNativeLogin: vi.fn((href) => (href.includes("good") ? nativeA : href.includes("throw") ? (() => { throw new Error("bad"); })() : null)),
      passwordApi: vi.fn(async () => enabledState),
      loginWithPassword: vi.fn(async () => ({ code: "one-time-code" })),
      pair: vi.fn(async () => {}),
      setToken: vi.fn(),
      formatError: vi.fn((err) => (err instanceof Error ? err.message : String(err))),
      assignLocation,
    };

    // Null native request
    await act(async () => {
      root.render(<NativePasswordLogin href="https://hub.example/empty" deps={deps} />);
    });
    expect(alertText()).toBe(t("password.start_from_app"));

    // Invalid native request (throws)
    await act(async () => {
      root.render(<NativePasswordLogin href="https://hub.example/throw" deps={deps} />);
    });
    expect(alertText()).toBe(t("password.invalid_request_full"));

    // Valid native request -> callback URL + assignLocation
    await act(async () => {
      root.render(<NativePasswordLogin href="https://hub.example/good" onDone={onDone} deps={deps} />);
    });
    expect(deps.passwordApi).toHaveBeenCalledExactlyOnceWith("/state");
    await typeValue("native-pw");
    await submitForm();
    const expectedCallback = `persona://password-login?${new URLSearchParams({ code: "one-time-code", state: nativeA.state })}`;
    expect(assignLocation).toHaveBeenCalledExactlyOnceWith(expectedCallback);
    expect(host.querySelector("a")?.getAttribute("href")).toBe(expectedCallback);
    expect(onDone).not.toHaveBeenCalled();

    // Native disabled /state shows password.disabled and keeps input disabled
    await act(async () => {
      root.render(
        <PasswordLogin
          onDone={onDone}
          native={nativeB}
          deps={{
            ...deps,
            passwordApi: vi.fn(async () => disabledState),
          }}
        />,
      );
    });
    expect(alertText()).toBe(t("password.disabled"));
    expect(inputEl().disabled).toBe(true);
    expect(submitButton().disabled).toBe(true);

    // Browser sync throw from /state falls back cleanly to token mode
    await act(async () => {
      root.render(
        <PasswordLogin
          onDone={onDone}
          scopeKey="sync-err"
          deps={{
            ...deps,
            passwordApi: () => {
              throw new Error("sync-state-fail");
            },
          }}
        />,
      );
    });
    expect(inputEl().placeholder).toBe("hub token");
    expect(inputEl().disabled).toBe(false);
    expectNoGlobalCalls();
  });

  it("blocks duplicate synchronous submits and ignores late /state or submit completions after unmount", async () => {
    const statePending = deferred<PasswordState>();
    const loginPending = deferred<PasswordCredential>();
    const onDone = vi.fn();
    const setTok = vi.fn();
    const assignLocation = vi.fn();
    const loginSpy = vi.fn(() => loginPending.promise);

    // 1) Unmount while /state is pending
    await act(async () => {
      root.render(
        <PasswordLogin
          onDone={onDone}
          native={nativeA}
          deps={{
            passwordApi: () => statePending.promise,
            loginWithPassword: loginSpy,
            setToken: setTok,
            assignLocation,
            formatError: (e) => String(e),
          }}
        />,
      );
    });
    await act(async () => root.unmount());
    await act(async () => {
      statePending.resolve(disabledState);
      await Promise.resolve();
    });

    // 2) Sync double-submit + unmount while loginWithPassword is in flight
    root = createRoot(host);
    await act(async () => {
      root.render(
        <PasswordLogin
          onDone={onDone}
          deps={{
            passwordApi: async () => enabledState,
            loginWithPassword: loginSpy,
            setToken: setTok,
            assignLocation,
            formatError: (e) => String(e),
          }}
        />,
      );
    });
    await typeValue("secret-pw");
    await act(async () => {
      const form = host.querySelector("form")!;
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
      form.dispatchEvent(new Event("submit", { bubbles: true, cancelable: true }));
    });
    expect(loginSpy).toHaveBeenCalledOnce();
    expect(submitButton().disabled).toBe(true);

    // Unmount before login resolves -> late resolve must NOT call setToken or onDone
    await act(async () => root.unmount());
    await act(async () => {
      loginPending.resolve({ token: "late-tok", id: "late-id", hub_id: "hub-1" });
      await Promise.resolve();
    });
    expect(setTok).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
    expect(assignLocation).not.toHaveBeenCalled();
    expectNoGlobalCalls();
  });

  it("fences /state and submit ABA races across scopeKey and native prop changes without clearing newer busy state", async () => {
    const stateQueue: Array<ReturnType<typeof deferred<PasswordState>>> = [];
    const loginQueue: Array<ReturnType<typeof deferred<PasswordCredential | { code: string }>>> = [];
    const onDone = vi.fn();
    const setTok = vi.fn();
    const assignLocation = vi.fn();

    const deps: PasswordLoginDeps = {
      passwordApi: vi.fn(() => {
        const d = deferred<PasswordState>();
        stateQueue.push(d);
        return d.promise;
      }),
      loginWithPassword: vi.fn(() => {
        const d = deferred<PasswordCredential | { code: string }>();
        loginQueue.push(d);
        return d.promise;
      }),
      pair: vi.fn(async () => {}),
      setToken: setTok,
      formatError: (e) => (e instanceof Error ? e.message : String(e)),
      assignLocation,
    };

    // 1) /state ABA: scope A -> B -> A
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-a" deps={deps} />);
    });
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-b" deps={deps} />);
    });
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-a" deps={deps} />);
    });
    expect(stateQueue).toHaveLength(3);

    // Stale A#1 returns disabled and B rejects -> must be ignored while A#2 is still loading
    await act(async () => {
      stateQueue[0]!.resolve(disabledState);
      stateQueue[1]!.reject(new Error("stale-b"));
      await Promise.resolve();
    });
    expect(inputEl().disabled).toBe(true); // still loading A#2

    await act(async () => {
      stateQueue[2]!.resolve(enabledState);
      await Promise.resolve();
    });
    expect(inputEl().disabled).toBe(false);
    expect(inputEl().placeholder).toBe(t("password.connection_password"));

    // 2) Submit ABA: submit on hub-a (#1), switch hub-a -> hub-b -> hub-a, submit #2, then stale #1 settles
    await typeValue("pw-1");
    await submitForm();
    expect(loginQueue).toHaveLength(1);
    expect(submitButton().disabled).toBe(true);

    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-b" deps={deps} />);
    });
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-a" deps={deps} />);
    });
    await act(async () => {
      stateQueue[stateQueue.length - 1]!.resolve(enabledState);
      await Promise.resolve();
    });

    await typeValue("pw-2");
    await submitForm();
    expect(loginQueue).toHaveLength(2);
    expect(submitButton().disabled).toBe(true);

    // Stale login #1 settles (resolves) while login #2 is in flight -> must NOT call setToken/onDone or clear busy!
    await act(async () => {
      loginQueue[0]!.resolve({ token: "stale-tok", id: "stale-id", hub_id: "hub-a" });
      await Promise.resolve();
    });
    expect(setTok).not.toHaveBeenCalled();
    expect(onDone).not.toHaveBeenCalled();
    expect(submitButton().disabled).toBe(true);

    // Login #2 succeeds
    await act(async () => {
      loginQueue[1]!.resolve({ token: "fresh-tok", id: "fresh-id", hub_id: "hub-a" });
      await Promise.resolve();
    });
    expect(setTok).toHaveBeenCalledExactlyOnceWith("fresh-tok", "fresh-id");
    expect(onDone).toHaveBeenCalledOnce();

    // Submit ABA rejection: stale login #3 rejects while login #4 is in flight -> must NOT show stale error or clear busy!
    await typeValue("pw-3");
    await submitForm();
    const staleRejectLogin = loginQueue[loginQueue.length - 1]!;
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-c" deps={deps} />);
    });
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} scopeKey="hub-a" deps={deps} />);
    });
    await act(async () => {
      stateQueue[stateQueue.length - 1]!.resolve(enabledState);
      await Promise.resolve();
    });
    await typeValue("pw-4");
    await submitForm();
    const activeLogin = loginQueue[loginQueue.length - 1]!;
    expect(submitButton().disabled).toBe(true);

    await act(async () => {
      staleRejectLogin.reject(new Error("stale-failure"));
      await Promise.resolve();
    });
    expect(alertText()).toBe("");
    expect(submitButton().disabled).toBe(true);

    await act(async () => {
      activeLogin.resolve({ token: "tok-4", id: "id-4", hub_id: "hub-a" });
      await Promise.resolve();
    });
    expect(setTok).toHaveBeenLastCalledWith("tok-4", "id-4");
    expect(onDone).toHaveBeenCalledTimes(2);

    // 3) Switching native prop from nativeA -> nativeB while nativeA submit is in flight discards stale callback
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} native={nativeA} deps={deps} />);
    });
    await act(async () => {
      stateQueue[stateQueue.length - 1]!.resolve(enabledState);
      await Promise.resolve();
    });
    await typeValue("native-pw-a");
    await submitForm();
    const nativeALogin = loginQueue[loginQueue.length - 1]!;

    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} native={nativeB} deps={deps} />);
    });
    await act(async () => {
      nativeALogin.resolve({ code: "stale-native-code" });
      await Promise.resolve();
    });
    expect(assignLocation).not.toHaveBeenCalled();
    expect(host.querySelector("a")).toBeNull();
    expectNoGlobalCalls();
  });

  it("keeps typed password and skips refetch when re-rendered with structurally equal native and new inline deps/onDone", async () => {
    const stateSpy = vi.fn(async () => enabledState);
    const firstOnDone = vi.fn();
    const secondOnDone = vi.fn();
    const setTok = vi.fn();

    await act(async () => {
      root.render(
        <PasswordLogin
          onDone={firstOnDone}
          native={{ ...nativeA }}
          deps={{
            passwordApi: stateSpy,
            loginWithPassword: async () => ({ code: "c1" }),
            setToken: setTok,
            assignLocation: vi.fn(),
          }}
        />,
      );
    });
    await typeValue("kept-pw");
    await act(async () => {
      root.render(
        <PasswordLogin
          onDone={secondOnDone}
          native={{ ...nativeA }}
          deps={{
            passwordApi: stateSpy,
            loginWithPassword: async () => ({ code: "c2" }),
            setToken: setTok,
            assignLocation: vi.fn(),
          }}
        />,
      );
    });
    expect(stateSpy).toHaveBeenCalledOnce();
    expect(inputEl().value).toBe("kept-pw");
    expectNoGlobalCalls();
  });
});

describe("PasswordLogin default fallback compatibility", () => {
  it("delegates to default passwordApi, loginWithPassword, setToken, pair, and readNativeLogin when deps are omitted", async () => {
    globals.passwordApi.mockResolvedValue(enabledState);
    globals.loginWithPassword.mockResolvedValue({ token: "tok_default", id: "dev_default", hub_id: "hub-1" });
    globals.pair.mockResolvedValue();
    globals.readNativeLogin.mockReturnValue(nativeA);

    const onDone = vi.fn();
    await act(async () => {
      root.render(<PasswordLogin onDone={onDone} error="from-link" />);
    });
    expect(globals.passwordApi).toHaveBeenCalledWith("/state");
    expect(alertText()).toBe("from-link");

    await typeValue("default-pw");
    await submitForm();
    expect(globals.loginWithPassword).toHaveBeenCalledWith("default-pw", null);
    expect(globals.setToken).toHaveBeenCalledWith("tok_default", "dev_default");
    expect(onDone).toHaveBeenCalledOnce();

    await act(async () => modeToggleButton()!.click());
    await typeValue("tok_pair");
    await submitForm();
    expect(globals.pair).toHaveBeenCalledWith("tok_pair");
    expect(onDone).toHaveBeenCalledTimes(2);

    // NativePasswordLogin with no props reads window.location.href via default readNativeLogin
    await act(async () => {
      root.render(<NativePasswordLogin />);
    });
    expect(globals.readNativeLogin).toHaveBeenCalledWith(window.location.href);
  });
});
