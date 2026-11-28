// External Chrome DevTools Protocol driver for an existing packaged app's windows.
// No source, preload, IPC or production entry-point changes are made for the test.
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

export class DesktopCDP {
  #socket;
  #id = 0;
  #pending = new Map();
  #sequence = 0;
  #pages = new Map();
  #attached = new Map();
  #consoleErrors = [];
  #failedRequests = [];
  #requests = new Map();
  #extraInfo = new Map();
  #navigationRequests = [];
  #output;
  #frozen = false;
  #phaseStart = { consoleErrors: 0, failedRequests: 0, requests: [] };
  #actions = [];
  #activeAction;
  #attachFailures = [];

  constructor(profile, output) { this.profile = profile; this.#output = output; }

  async connect() {
    const [port] = (await readFile(join(this.profile, "DevToolsActivePort"), "utf8")).split("\n");
    const version = await (await fetch(`http://127.0.0.1:${port}/json/version`, { signal: AbortSignal.timeout(5000) })).json();
    this.#socket = new WebSocket(version.webSocketDebuggerUrl);
    this.#socket.addEventListener("message", event => {
      const message = JSON.parse(event.data);
      if (message.id) {
        const waiter = this.#pending.get(message.id);
        if (waiter) {
          clearTimeout(waiter.timer);
          this.#pending.delete(message.id);
          if (message.error) waiter.reject(new Error(JSON.stringify(message.error)));
          else waiter.resolve(message.result);
        }
        return;
      }
      if (this.#frozen) return;
      const sequence = ++this.#sequence;
      if (message.method === "Target.targetCreated" || message.method === "Target.targetInfoChanged") {
        const target = message.params.targetInfo;
        if (target.type === "page") this.#pages.set(target.targetId, target);
      }
      if (message.method === "Target.targetDestroyed") {
        this.#pages.delete(message.params.targetId);
        this.#attached.delete(message.params.targetId);
        // Keep response/error evidence after the target goes away; it still belongs to the completed observation.
      }
      const page = this.#pages.get(this.#targetForSession(message.sessionId));
      const pageURL = value => page?.url ?? value?.documentURL ?? value?.url ?? this.#requests.get(this.#requestKey(message.sessionId, value?.requestId))?.documentURL ?? "";
      if (message.method === "Runtime.exceptionThrown") {
        const value = message.params.exceptionDetails;
        this.#consoleErrors.push({ ...value, kind: "runtime", targetId: page?.targetId, pageURL: pageURL(value), sequence });
      }
      if (message.method === "Runtime.consoleAPICalled" && message.params.type === "error") {
        const value = message.params;
        this.#consoleErrors.push({ kind: "console", text: value.args.map(arg => arg.value ?? arg.description ?? arg.type).join(" "), targetId: page?.targetId, pageURL: page?.url, url: value.stackTrace?.callFrames[0]?.url, sequence });
      }
      if (message.method === "Log.entryAdded" && message.params.entry.level === "error") {
        const value = message.params.entry;
        const request = [...this.#requests.values()].reverse().find(item => item.url === value.url && item.status !== undefined && item.responseTargetId === page?.targetId && !item.logSequence);
        if (request) request.logSequence = sequence;
        this.#consoleErrors.push({ ...value, kind: "log", targetId: page?.targetId, pageURL: pageURL(value), action: this.#activeAction, sequence, requestId: request?.requestId, sessionId: request?.sessionId });
      }
      if (message.method === "Network.requestWillBeSent") {
        const key = this.#requestKey(message.sessionId, message.params.requestId);
        const extra = this.#extraInfo.get(key);
        const names = [...Object.keys(message.params.request.headers ?? {}), ...(extra ?? [])].map(name => name.toLowerCase());
        this.#requests.set(key, { requestId: message.params.requestId, sessionId: message.sessionId, url: message.params.request.url, targetId: page?.targetId, documentURL: message.params.documentURL ?? page?.url, sequence,
          hasAuthorization: names.includes("authorization"),
          hasCookie: names.includes("cookie") });
        this.#extraInfo.delete(key);
      }
      if (message.method === "Network.requestWillBeSentExtraInfo") {
        const key = this.#requestKey(message.sessionId, message.params.requestId);
        const names = Object.keys(message.params.headers ?? {}).map(name => name.toLowerCase());
        const value = this.#requests.get(key);
        if (value) {
          if (names.includes("authorization")) value.hasAuthorization = true;
          if (names.includes("cookie")) value.hasCookie = true;
        } else {
          // CDP explicitly permits ExtraInfo before requestWillBeSent. Preserve only header names, never values.
          this.#extraInfo.set(key, [...new Set([...(this.#extraInfo.get(key) ?? []), ...names])]);
        }
      }
      if (message.method === "Network.responseReceived") {
        const value = this.#requests.get(this.#requestKey(message.sessionId, message.params.requestId));
        if (value) {
          value.status = message.params.response.status;
          value.responseSequence = sequence;
          value.responseTargetId = page?.targetId;
        }
      }
      if (message.method === "Network.loadingFailed") {
        const request = this.#requests.get(this.#requestKey(message.sessionId, message.params.requestId));
        const navigation = !request && message.params.type === "Document"
          ? this.#navigationRequests.findLast(item => item.targetId === page?.targetId && item.sequence < sequence && this.#activeAction && item.sequence >= this.#activeAction.sequence)
          : undefined;
        this.#failedRequests.push({ ...message.params, targetId: page?.targetId ?? request?.targetId, pageURL: page?.url ?? request?.documentURL ?? "", url: request?.url ?? message.params.url ?? navigation?.url, documentURL: request?.documentURL ?? message.params.documentURL ?? navigation?.documentURL, action: this.#activeAction, requestSequence: request?.sequence ?? navigation?.sequence, sequence });
      }
      if (message.method === "Page.frameRequestedNavigation") {
        this.#navigationRequests.push({ targetId: page?.targetId, url: message.params.url, documentURL: page?.url, sequence });
      }
    });
    this.#socket.addEventListener("close", () => this.#failPending(new Error("The packaged app's CDP socket closed")));
    this.#socket.addEventListener("error", () => this.#failPending(new Error("The packaged app's CDP socket failed")));
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("Timed out connecting to the packaged app")), 10000);
      this.#socket.addEventListener("open", () => { clearTimeout(timer); resolve(); }, { once: true });
      this.#socket.addEventListener("error", error => { clearTimeout(timer); reject(error); }, { once: true });
    });
    await this.call("Target.setDiscoverTargets", { discover: true });
    this.#socket.addEventListener("message", event => {
      const message = JSON.parse(event.data);
      if (message.id || this.#frozen || message.method !== "Target.targetCreated") return;
      const target = message.params.targetInfo;
      if (target.type !== "page" || this.#attached.has(target.targetId)) return;
      void this.#attach(target.targetId).catch(error => {
        this.#attached.delete(target.targetId);
        this.#attachFailures.push({ targetId: target.targetId, error: String(error) });
      });
    });
    for (const target of (await this.call("Target.getTargets")).targetInfos.filter(t => t.type === "page")) {
      this.#pages.set(target.targetId, target);
      await this.#attach(target.targetId);
    }
  }

  #targetForSession(sessionId) {
    for (const [targetId, session] of this.#attached) if (session === sessionId) return targetId;
  }

  #requestKey(sessionId, requestId) { return `${sessionId ?? "browser"}:${requestId}`; }

  async #attach(targetId) {
    const { sessionId } = await this.call("Target.attachToTarget", { targetId, flatten: true });
    this.#attached.set(targetId, sessionId);
    for (const method of ["Runtime.enable", "Log.enable", "Network.enable", "Page.enable"]) await this.call(method, {}, sessionId);
  }

  #failPending(error) {
    for (const waiter of this.#pending.values()) { clearTimeout(waiter.timer); waiter.reject(error); }
    this.#pending.clear();
  }

  call(method, params = {}, sessionId) {
    const id = ++this.#id;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        this.#pending.delete(id);
        reject(new Error(`CDP ${method} timed out after 15 seconds`));
      }, 15000);
      this.#pending.set(id, { resolve, reject, timer });
      this.#socket.send(JSON.stringify({ id, sessionId, method, params }));
    });
  }

  #page(id) {
    const output = this.#output;
    const session = () => {
      const value = this.#attached.get(id);
      if (!value) throw new Error("The new window has not attached yet");
      return value;
    };
    return {
      id,
      url: () => this.#pages.get(id)?.url ?? "",
      evaluate: async expression => {
        const result = await this.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }, session());
        if (result.exceptionDetails) throw new Error(JSON.stringify(result.exceptionDetails));
        if (result.result.subtype === "error") throw new Error(result.result.description ?? "The page evaluation failed");
        return result.result.value;
      },
      capture: async name => {
        await mkdir(output, { recursive: true });
        const { data } = await this.call("Page.captureScreenshot", { format: "png" }, session());
        await writeFile(join(output, name), Buffer.from(data, "base64"));
      },
    };
  }

  async until(label, probe, timeout = 30000) {
    const deadline = Date.now() + timeout;
    let last;
    for (;;) {
      try { last = await probe(); if (last) return last; } catch (error) { last = String(error); }
      if (Date.now() > deadline) throw new Error(`${label} timed out: ${JSON.stringify(last)}`);
      await new Promise(resolve => setTimeout(resolve, 100));
    }
  }

  async pageFor(label, probe, timeout = 30000) {
    const target = await this.until(label, () => [...this.#pages.values()].find(probe), timeout);
    // Discover auto-attaches new windows. Wait for the same session instead of racing a duplicate explicit attachment.
    await this.until(`${label} debugger attachment`, () => this.#attached.has(target.targetId), timeout);
    return this.#page(target.targetId);
  }

  async action(targetId, kind, url) {
    if (this.#activeAction) throw new Error("The previous deliberate negative action has not finished");
    this.#activeAction = { targetId, kind, url, sequence: ++this.#sequence, consoleErrors: this.#consoleErrors.length, failedRequests: this.#failedRequests.length };
    this.#actions.push(this.#activeAction);
  }

  finishAction() {
    if (this.#activeAction) this.#activeAction.endSequence = ++this.#sequence;
    this.#activeAction = undefined;
  }

  async phase(name) {
    this.finishAction();
    const snapshot = this.diagnostics();
    this.#phaseStart = { consoleErrors: this.#consoleErrors.length, failedRequests: this.#failedRequests.length,
      requests: [...this.#requests.values()].filter(value => value.status !== undefined).map(value => ({ requestId: value.requestId, responseSequence: value.responseSequence, sessionId: value.sessionId })) };
    this.#actions = [];
    return { phase: name, ...snapshot };
  }

  diagnostics({ since = this.#phaseStart } = {}) {
    const errors = this.#consoleErrors.slice(since.consoleErrors).map(value => ({ kind: value.kind, text: value.text, description: value.exception?.description?.slice(0, 500), url: value.url, targetId: value.targetId, pageURL: value.pageURL, action: value.action, sequence: value.sequence, requestId: value.requestId, sessionId: value.sessionId }));
    return {
      consoleErrors: errors,
      failedRequests: this.#failedRequests.slice(since.failedRequests).map(value => ({ errorText: value.errorText, type: value.type, targetId: value.targetId, pageURL: value.pageURL, url: value.url, documentURL: value.documentURL, canceled: value.canceled, action: value.action, requestSequence: value.requestSequence, sequence: value.sequence })),
      actions: this.#actions.map(value => ({ ...value })),
      pages: [...this.#pages.values()].map(({ targetId, url, title }) => ({ targetId, url, title })),
      requests: [...this.#requests.values()].filter(value => value.status !== undefined && !since.requests?.some(previous => previous.requestId === value.requestId && previous.sessionId === value.sessionId && previous.responseSequence === value.responseSequence)).map(value => ({ requestId: value.requestId, sessionId: value.sessionId, targetId: value.targetId, url: value.url, documentURL: value.documentURL, status: value.status, sequence: value.sequence, responseSequence: value.responseSequence, responseTargetId: value.responseTargetId, logSequence: value.logSequence, hasAuthorization: value.hasAuthorization, hasCookie: value.hasCookie })),
      attachFailures: this.#attachFailures.map(value => ({ ...value })),
    };
  }

  /** Stop collecting before the one final report/verdict snapshot. This is the declared observation boundary, not a claim
   *  that an application can never fail after it. */
  freeze() { this.#frozen = true; }

  async close() {
    const socket = this.#socket;
    if (!socket || socket.readyState === WebSocket.CLOSED) return;
    const closed = new Promise(done => socket.addEventListener("close", done, { once: true }));
    if (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING) socket.close();
    let timer;
    try { await Promise.race([closed, new Promise(done => { timer = setTimeout(done, 2000); })]); }
    finally { clearTimeout(timer); }
  }
}
