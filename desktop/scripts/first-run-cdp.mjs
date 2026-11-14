// External Chrome DevTools Protocol driver for an existing packaged app's windows.
// No source, preload, IPC or production entry-point changes are made for the test.
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { join } from "node:path";

export class DesktopCDP {
  #socket;
  #id = 0;
  #pending = new Map();
  #pages = new Map();
  #attached = new Map();
  #consoleErrors = [];
  #failedRequests = [];
  #output;

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
      if (message.method === "Target.targetCreated" || message.method === "Target.targetInfoChanged") {
        const target = message.params.targetInfo;
        if (target.type === "page") this.#pages.set(target.targetId, target);
      }
      if (message.method === "Target.targetDestroyed") { this.#pages.delete(message.params.targetId); this.#attached.delete(message.params.targetId); }
      if (message.method === "Runtime.exceptionThrown") this.#consoleErrors.push(message.params.exceptionDetails);
      if (message.method === "Log.entryAdded" && message.params.entry.level === "error") this.#consoleErrors.push(message.params.entry);
      if (message.method === "Network.loadingFailed") this.#failedRequests.push(message.params);
    });
    this.#socket.addEventListener("close", () => this.#failPending(new Error("The packaged app's CDP socket closed")));
    this.#socket.addEventListener("error", () => this.#failPending(new Error("The packaged app's CDP socket failed")));
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("Timed out connecting to the packaged app")), 10000);
      this.#socket.addEventListener("open", () => { clearTimeout(timer); resolve(); }, { once: true });
      this.#socket.addEventListener("error", error => { clearTimeout(timer); reject(error); }, { once: true });
    });
    await this.call("Target.setDiscoverTargets", { discover: true });
    for (const target of (await this.call("Target.getTargets")).targetInfos.filter(t => t.type === "page")) {
      this.#pages.set(target.targetId, target);
      const { sessionId } = await this.call("Target.attachToTarget", { targetId: target.targetId, flatten: true });
      this.#attached.set(target.targetId, sessionId);
      for (const method of ["Runtime.enable", "Log.enable", "Network.enable"]) await this.call(method, {}, sessionId);
    }
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
    // Attach to each discovered target explicitly. setAutoAttach can race navigation from setup to the hub.
    await this.until(`${label} debugger attachment`, async () => {
      if (this.#attached.has(target.targetId)) return true;
      try {
        const { sessionId } = await this.call("Target.attachToTarget", { targetId: target.targetId, flatten: true });
        this.#attached.set(target.targetId, sessionId);
        for (const method of ["Runtime.enable", "Log.enable", "Network.enable"]) await this.call(method, {}, sessionId);
        return true;
      } catch { return false; }
    }, timeout);
    return this.#page(target.targetId);
  }

  diagnostics() {
    const errors = this.#consoleErrors.map(value => ({ text: value.text, description: value.exception?.description?.slice(0, 500), url: value.url }));
    return { consoleErrors: errors, failedRequests: this.#failedRequests.map(value => ({ errorText: value.errorText, type: value.type })), pages: [...this.#pages.values()].map(({ url, title }) => ({ url, title })) };
  }

  close() {
    if (this.#socket.readyState === WebSocket.OPEN || this.#socket.readyState === WebSocket.CONNECTING) this.#socket.close();
  }
}
