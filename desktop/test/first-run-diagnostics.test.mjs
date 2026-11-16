import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtemp, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { DesktopCDP } from "../scripts/first-run-cdp.mjs";
import { classifyFirstRunDiagnostics, setupCSPResource } from "../scripts/first-run-diagnostics.mjs";
import { finalizeFirstRun } from "../scripts/first-run-finalize.mjs";

function encode(value) {
  const body = Buffer.from(JSON.stringify(value));
  if (body.length >= 65536) throw new Error("fixture payload is too large");
  const header = Buffer.alloc(body.length < 126 ? 2 : 4);
  header[0] = 0x81;
  header[1] = body.length < 126 ? body.length : 126;
  if (header.length === 4) header.writeUInt16BE(body.length, 2);
  return Buffer.concat([header, body]);
}

async function cdpFixture(t) {
  const sockets = new Set();
  let socket;
  let received = Buffer.alloc(0);
  const send = value => { if (socket && !socket.destroyed) socket.write(encode(value)); };
  const event = (method, params, sessionId) => send({ method, params, ...(sessionId ? { sessionId } : {}) });
  const target = (targetId, url, title = "Synthetic") => event("Target.targetCreated", { targetInfo: { targetId, type: "page", url, title } });
  const server = createServer((req, res) => {
    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify({ webSocketDebuggerUrl: `ws://127.0.0.1:${server.address().port}/cdp` }));
  });
  server.on("upgrade", (req, conn) => {
    socket = conn;
    sockets.add(conn);
    const accept = createHash("sha1").update(req.headers["sec-websocket-key"] + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").digest("base64");
    conn.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
    // The fixture only sends events and answers CDP calls. Decode masked client frames without another dependency.
    conn.on("data", chunk => {
      received = Buffer.concat([received, chunk]);
      for (;;) {
        if (received.length < 2) return;
        const second = received[1];
        let length = second & 0x7f, header = 2;
        if (length === 126) {
          if (received.length < 4) return;
          length = received.readUInt16BE(2); header = 4;
        } else if (length === 127) {
          if (received.length < 10) return;
          length = Number(received.readBigUInt64BE(2)); header = 10;
        }
        if (second & 0x80) header += 4;
        if (received.length < header + length) return;
        const opcode = received[0] & 0x0f;
        let body = received.subarray(header, header + length);
        if (second & 0x80) {
          const mask = received.subarray(header - 4, header);
          body = Buffer.from(body.map((byte, index) => byte ^ mask[index % 4]));
        }
        received = received.subarray(header + length);
        if (opcode === 0x8) { conn.end(); continue; }
        if (opcode === 0x9) { conn.write(Buffer.from([0x8a, 0])); continue; }
        if (opcode !== 0x1) continue;
        const message = JSON.parse(body.toString("utf8"));
        if (!message.id) continue;
        if (message.method === "Target.getTargets") send({ id: message.id, result: { targetInfos: [] } });
        else if (message.method === "Target.attachToTarget") {
          if (message.params.targetId === "unattachable") send({ id: message.id, error: { code: -32000, message: "synthetic attach refusal" } });
          else send({ id: message.id, result: { sessionId: `session-${message.params.targetId}` } });
        }
        else if (message.method === "Runtime.evaluate") send({ id: message.id, result: { result: { type: "boolean", value: true } } });
        else send({ id: message.id, result: {} });
      }
    });
  });
  await new Promise(done => server.listen(0, "127.0.0.1", done));
  const directory = await mkdtemp(join(tmpdir(), "persona-cdp-test-"));
  await writeFile(join(directory, "DevToolsActivePort"), `${server.address().port}\n/devtools/browser/fixture`);
  t.after(async () => {
    for (const conn of sockets) conn.destroy();
    server.closeAllConnections();
    await new Promise(done => server.close(done));
    await rm(directory, { recursive: true, force: true });
  });
  return { directory, send, event, target };
}

async function connected(t) {
  const fixture = await cdpFixture(t);
  const client = new DesktopCDP(fixture.directory, fixture.directory);
  await client.connect();
  t.after(() => client.close());
  return { client, ...fixture };
}

async function settled() { await new Promise(done => setTimeout(done, 30)); }

function finalOutcome(observations) {
  return finalizeFirstRun({ platform: "win32", blocked: [], steps: [{ name: "synthetic completed functional step", ok: true }], observations });
}

test("real CDP event framing retains target and request attribution", async t => {
  const { client, event, target } = await connected(t);
  target("setup", "persona-setup://app/setup.html");
  const page = await client.pageFor("setup", value => value.url === "persona-setup://app/setup.html");
  await page.evaluate("true");
  const hubURL = "http://127.0.0.1:18760/";
  target("hub", hubURL);
  const hub = await client.pageFor("replacement hub", value => value.url === hubURL);
  await hub.evaluate("true");
  event("Network.requestWillBeSent", { requestId: "password", documentURL: hubURL, request: { url: `${hubURL}api/v1/auth/password/state` } }, "session-hub");
  event("Network.responseReceived", { requestId: "password", response: { status: 401 } }, "session-hub");
  event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 401 (Unauthorized)", url: `${hubURL}api/v1/auth/password/state` } }, "session-hub");
  await settled();
  const snapshot = client.diagnostics();
  assert.equal(snapshot.consoleErrors[0].targetId, "hub");
  assert.equal(snapshot.consoleErrors[0].pageURL, hubURL);
  assert.equal(snapshot.requests.length, 1);
  assert.equal(snapshot.requests[0].requestId, "password");
  assert.equal(snapshot.requests[0].documentURL, hubURL);
  assert.equal(snapshot.requests[0].status, 401);
  assert.equal(snapshot.requests[0].hasAuthorization, false);
  assert.equal(snapshot.requests[0].hasCookie, false);
  assert.equal(snapshot.consoleErrors[0].requestId, "password");
  const classified = classifyFirstRunDiagnostics({ phase: "hub-sign-in", ...snapshot, hubURL, signInTargetId: "hub" });
  assert.equal(classified.ok, true);
  assert.equal(classified.expected.length, 1);
  assert.equal(finalOutcome([classified]).exitCode, 0);
  assert.equal(finalOutcome([classified]).verdict.functionalFirstRunAccepted, true);
});

test("every unclassified error turns otherwise successful functional steps into FAIL", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  const kinds = [
    () => event("Runtime.exceptionThrown", { exceptionDetails: { text: "Uncaught synthetic exception", url } }, "session-hub"),
    () => event("Log.entryAdded", { entry: { level: "error", text: "ResizeObserver loop completed with undelivered notifications", url } }, "session-hub"),
    () => event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 404 (Not Found)", url: `${url}api/v1/actual-feature` } }, "session-hub"),
    () => event("Network.loadingFailed", { requestId: "unknown", type: "Document", errorText: "net::ERR_ABORTED", url, documentURL: url }, "session-hub"),
  ];
  for (const inject of kinds) {
    inject();
    await settled();
    const result = classifyFirstRunDiagnostics({ phase: "hub-chat", ...client.diagnostics(), hubURL: url, hubTargetId: "hub" });
    assert.equal(result.ok, false);
    assert.ok(result.unexpected.length > 0);
    const outcome = finalOutcome([result]);
    assert.equal(outcome.exitCode, 1);
    assert.equal(outcome.verdict.ok, false);
    assert.equal(outcome.verdict.functionalFirstRunAccepted, false);
    await client.phase("next negative control");
  }
});

test("declared setup negatives are expected only at their exact action and URL", async t => {
  const { client, event, target } = await connected(t);
  target("setup", "persona-setup://app/setup.html");
  await client.pageFor("setup", value => value.url === "persona-setup://app/setup.html");
  for (const url of ["persona-setup://app/setup-preload.cjs", "persona-setup://app/main.js"]) {
    await client.action("setup", "setup-resource-negative", url);
    event("Log.entryAdded", { entry: { level: "error", text: `Connecting to '${url}' violates the following Content Security Policy directive: "default-src 'none'".` } }, "session-setup");
    event("Log.entryAdded", { entry: { level: "error", text: `Fetch API cannot load ${url}. Refused to connect because it violates the document's Content Security Policy.` } }, "session-setup");
    await client.until("both deliberate CSP log entries", () => client.diagnostics().consoleErrors.filter(value => setupCSPResource(value) === url && value.action?.url === url).length === 2);
    client.finishAction();
  }
  await client.action("setup", "setup-navigation-negative", "https://example.com/");
  event("Page.frameRequestedNavigation", { url: "https://example.com/" }, "session-setup");
  event("Network.requestWillBeSent", { requestId: "outside", documentURL: "persona-setup://app/setup.html", request: { url: "https://example.com/" } }, "session-setup");
  event("Network.loadingFailed", { requestId: "outside", type: "Document", errorText: "net::ERR_ABORTED", url: "https://example.com/", documentURL: "persona-setup://app/setup.html" }, "session-setup");
  await client.until("the deliberate refused navigation", () => client.diagnostics().failedRequests.some(value => value.url === "https://example.com/" && value.action?.kind === "setup-navigation-negative"));
  client.finishAction();
  await settled();
  const result = classifyFirstRunDiagnostics({ phase: "setup-security", ...client.diagnostics() });
  assert.equal(result.ok, true, JSON.stringify(result, null, 2));
  assert.equal(finalOutcome([result]).exitCode, 0);
  for (const changed of [
    { phase: "hub-chat" },
    { phase: "setup-security", consoleErrors: [{ kind: "runtime", text: "Uncaught", targetId: "setup" }] },
  ]) {
    const negative = classifyFirstRunDiagnostics(changed.phase === "hub-chat" ? { phase: changed.phase, ...client.diagnostics() } : changed);
    assert.equal(negative.ok, false);
  }
});

test("an error after sign-in or on a replacement hub cannot borrow the earlier expected phase", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("setup", "persona-setup://app/setup.html");
  await client.pageFor("setup", value => value.url === "persona-setup://app/setup.html");
  event("Log.entryAdded", { entry: { level: "error", text: "Fetch API cannot load persona-setup://app/main.js. Refused to connect because it violates the document's Content Security Policy.", url: "persona-setup://app/main.js" } }, "session-setup");
  await settled();
  const setup = await client.phase("setup-security");
  assert.equal(classifyFirstRunDiagnostics(setup).ok, false); // The navigation and second resource must also be observed.
  target("hub", url);
  await client.pageFor("replacement hub", value => value.url === url);
  event("Network.requestWillBeSent", { requestId: "password-after", documentURL: url, request: { url: `${url}api/v1/auth/password/state` } }, "session-hub");
  event("Network.responseReceived", { requestId: "password-after", response: { status: 401 } }, "session-hub");
  event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 401 (Unauthorized)", url: `${url}api/v1/auth/password/state` } }, "session-hub");
  event("Runtime.exceptionThrown", { exceptionDetails: { text: "Uncaught replacement error", url } }, "session-hub");
  await settled();
  const replacement = await client.phase("hub-chat");
  const result = classifyFirstRunDiagnostics(replacement);
  assert.equal(result.unexpected.length, 3); // the post-login 401 log, its unpaired response and the uncaught exception
  assert.equal(finalOutcome([result]).exitCode, 1);
});

test("Runtime.consoleAPICalled error reaches the final verdict through the real collector", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  event("Runtime.consoleAPICalled", { type: "error", args: [{ type: "string", value: "synthetic console.error" }] }, "session-hub");
  await settled();
  const result = classifyFirstRunDiagnostics({ phase: "hub-chat", ...client.diagnostics(), hubURL: url, hubTargetId: "hub" });
  assert.equal(result.unexpected[0].kind, "console");
  assert.equal(finalOutcome([result]).exitCode, 1);
  assert.equal(finalOutcome([result]).verdict.functionalFirstRunAccepted, false);
});

test("HTTP exemptions require the exact hub origin, owning window and unsigned-in request", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  for (const testCase of [
    { id: "wrong-origin", url: "http://other.invalid/api/v1/auth/password/state", target: "hub", phase: "hub-sign-in" },
    { id: "wrong-window", url: `${url}api/v1/auth/password/state`, target: "other", phase: "hub-sign-in" },
    { id: "authorized", url: `${url}api/v1/auth/password/state`, target: "hub", phase: "hub-sign-in", headers: { Authorization: "synthetic-not-recorded" } },
    { id: "post-login", url: `${url}api/v1/auth/password/state`, target: "hub", phase: "hub-chat" },
  ]) {
    if (testCase.target === "other") {
      target("other", url);
      await client.pageFor("other hub", value => value.url === url && value.targetId === "other");
    }
    const pageURL = url;
    event("Network.requestWillBeSent", { requestId: testCase.id, documentURL: pageURL, request: { url: testCase.url, headers: testCase.headers ?? {} } }, `session-${testCase.target}`);
    event("Network.responseReceived", { requestId: testCase.id, response: { status: 401 } }, `session-${testCase.target}`);
    event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 401 (Unauthorized)", url: testCase.url } }, `session-${testCase.target}`);
    await settled();
    const result = classifyFirstRunDiagnostics({ phase: testCase.phase, ...client.diagnostics(), hubURL: url, signInTargetId: "hub", hubTargetId: "hub" });
    assert.equal(result.ok, false, testCase.id);
    assert.equal(finalOutcome([result]).exitCode, 1, testCase.id);
    await client.phase(`${testCase.id} control`);
  }
});

test("navigation exemption rejects pre-action and duplicate failed requests", async t => {
  const { client, event, target } = await connected(t);
  target("setup", "persona-setup://app/setup.html");
  await client.pageFor("setup", value => value.url === "persona-setup://app/setup.html");
  event("Network.requestWillBeSent", { requestId: "preexisting", documentURL: "persona-setup://app/setup.html", request: { url: "https://example.com/" } }, "session-setup");
  await settled();
  await client.action("setup", "setup-navigation-negative", "https://example.com/");
  event("Network.loadingFailed", { requestId: "preexisting", type: "Document", errorText: "net::ERR_ABORTED", url: "https://example.com/", documentURL: "persona-setup://app/setup.html" }, "session-setup");
  await client.until("first pre-existing failure", () => client.diagnostics().failedRequests.length === 1);
  const first = classifyFirstRunDiagnostics({ phase: "setup-security", ...client.diagnostics() });
  assert.equal(first.ok, false);
  event("Network.requestWillBeSent", { requestId: "duplicate", documentURL: "persona-setup://app/setup.html", request: { url: "https://example.com/" } }, "session-setup");
  event("Network.loadingFailed", { requestId: "duplicate", type: "Document", errorText: "net::ERR_ABORTED", url: "https://example.com/", documentURL: "persona-setup://app/setup.html" }, "session-setup");
  await client.until("duplicate failure", () => client.diagnostics().failedRequests.length === 2);
  client.finishAction();
  const result = classifyFirstRunDiagnostics({ phase: "setup-security", ...client.diagnostics() });
  assert.equal(result.ok, false);
  assert.ok(result.unexpected.length >= 1);
  assert.equal(finalOutcome([result]).exitCode, 1);
});

test("an actual Chromium refused navigation is bound by Page.frameRequestedNavigation without a request event", async t => {
  const { client, event, target } = await connected(t);
  target("setup", "persona-setup://app/setup.html");
  await client.pageFor("setup", value => value.url === "persona-setup://app/setup.html");
  await client.action("setup", "setup-navigation-negative", "https://example.com/");
  event("Page.frameRequestedNavigation", { url: "https://example.com/" }, "session-setup");
  event("Network.loadingFailed", { requestId: "navigation-only", type: "Document", errorText: "net::ERR_ABORTED", canceled: true }, "session-setup");
  await client.until("the navigation request and failure are paired", () => client.diagnostics().failedRequests.some(value => value.url === "https://example.com/" && value.documentURL === "persona-setup://app/setup.html"));
  client.finishAction();
  const result = classifyFirstRunDiagnostics({ phase: "setup-security", ...client.diagnostics() });
  assert.equal(result.ok, true, JSON.stringify(result));
  assert.equal(result.expected.length, 1);
  assert.equal(finalOutcome([result]).exitCode, 0);
});

test("an actual HTTP error without a console log still fails", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  event("Network.requestWillBeSent", { requestId: "no-log", documentURL: url, request: { url: `${url}api/v1/assistant/conversations` } }, "session-hub");
  event("Network.responseReceived", { requestId: "no-log", response: { status: 503 } }, "session-hub");
  await settled();
  const result = classifyFirstRunDiagnostics({ phase: "hub-chat", ...client.diagnostics(), hubURL: url, hubTargetId: "hub" });
  assert.equal(result.ok, false);
  assert.equal(result.unexpected[0].kind, "response");
  assert.equal(finalOutcome([result]).exitCode, 1);
});

test("declared optional feed accepts only the real paginated URL on the hub chat target", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  const feed = `${url}api/v1/assistant/conversations/b2e4d6f8-0000-4000-8000-000000000001/feed?paginate=1`;
  event("Network.requestWillBeSent", { requestId: "feed", documentURL: url, request: { url: feed } }, "session-hub");
  event("Network.responseReceived", { requestId: "feed", response: { status: 404 } }, "session-hub");
  event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 404 (Not Found)", url: feed } }, "session-hub");
  await settled();
  const result = classifyFirstRunDiagnostics({ phase: "hub-chat", ...client.diagnostics(), hubURL: url, hubTargetId: "hub" });
  assert.equal(result.ok, true, JSON.stringify(result));
  assert.equal(result.expected.length, 1);
  assert.equal(finalOutcome([result]).exitCode, 0);
});

test("a supported subpath sign-in keeps exact page/hub binding without accepting another origin", async t => {
  const { client, event, target } = await connected(t);
  const hubURL = "http://127.0.0.1:18760/hub/";
  const pageURL = `${hubURL}login`;
  target("subpath", pageURL);
  await client.pageFor("subpath login", value => value.url === pageURL);
  const probe = `${hubURL}api/v1/auth/password/state`;
  event("Network.requestWillBeSent", { requestId: "subpath-password", documentURL: pageURL, request: { url: probe } }, "session-subpath");
  event("Network.responseReceived", { requestId: "subpath-password", response: { status: 401 } }, "session-subpath");
  event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 401 (Unauthorized)", url: probe, documentURL: pageURL } }, "session-subpath");
  await settled();
  const result = classifyFirstRunDiagnostics({ phase: "hub-sign-in", ...client.diagnostics(), hubURL, signInTargetId: "subpath" });
  assert.equal(result.ok, true, JSON.stringify(result));
  assert.equal(finalOutcome([result]).exitCode, 0);
  const offOriginProbe = `http://other.invalid/hub/api/v1/auth/password/state`;
  event("Network.requestWillBeSent", { requestId: "off-origin-password", documentURL: pageURL, request: { url: offOriginProbe } }, "session-subpath");
  event("Network.responseReceived", { requestId: "off-origin-password", response: { status: 401 } }, "session-subpath");
  event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 401 (Unauthorized)", url: offOriginProbe, documentURL: pageURL } }, "session-subpath");
  await settled();
  const offOrigin = classifyFirstRunDiagnostics({ phase: "hub-sign-in", ...client.diagnostics(), hubURL, signInTargetId: "subpath" });
  assert.equal(offOrigin.ok, false);
});

test("a failed replacement-window attachment is evidence of failure, not an empty observation", async t => {
  const { client, target } = await connected(t);
  target("unattachable", "http://127.0.0.1:18760/");
  await client.until("the failed attachment is reported", () => client.diagnostics().attachFailures.length === 1);
  const result = classifyFirstRunDiagnostics({ phase: "hub-chat", ...client.diagnostics(), hubURL: "http://127.0.0.1:18760/", hubTargetId: "unattachable" });
  assert.equal(result.ok, false);
  assert.equal(result.unexpected[0].kind, "attachment");
  assert.equal(finalOutcome([result]).exitCode, 1);
});

test("a post-observation runtime error belongs to final-observation and fails the shared finalizer", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  await client.phase("hub-chat");
  event("Runtime.exceptionThrown", { exceptionDetails: { text: "late application exception", url } }, "session-hub");
  await settled();
  const final = classifyFirstRunDiagnostics({ phase: "final-observation", ...client.diagnostics(), hubURL: url });
  assert.equal(final.ok, false);
  assert.equal(finalOutcome([final]).exitCode, 1);
  assert.equal(finalOutcome([final]).verdict.functionalFirstRunAccepted, false);
});

test("duplicate action identities and missing CSP forms cannot borrow one another's evidence", () => {
  const actions = [
    { targetId: "setup", kind: "setup-navigation-negative", url: "https://example.com/", sequence: 10, endSequence: 20 },
    { targetId: "setup", kind: "setup-navigation-negative", url: "https://example.com/", sequence: 20, endSequence: 30 },
  ];
  const onlySecond = classifyFirstRunDiagnostics({ phase: "setup-security", actions, pages: [{ targetId: "setup", url: "persona-setup://app/setup.html" }], failedRequests: [{ type: "Document", errorText: "net::ERR_ABORTED", url: "https://example.com/", documentURL: "persona-setup://app/setup.html", targetId: "setup", action: actions[1], requestSequence: 21, sequence: 22 }] });
  assert.equal(onlySecond.ok, false);
  const cspAction = { targetId: "setup", kind: "setup-resource-negative", url: "persona-setup://app/main.js", sequence: 40, endSequence: 50 };
  const oneForm = classifyFirstRunDiagnostics({ phase: "setup-security", actions: [cspAction], pages: [{ targetId: "setup", url: "persona-setup://app/setup.html" }], consoleErrors: [{ kind: "log", text: "Connecting to 'persona-setup://app/main.js' violates the following Content Security Policy directive: \"default-src 'none'\".", targetId: "setup", action: cspAction }] });
  assert.equal(oneForm.ok, false);
  assert.match(oneForm.failure, /complete expected diagnostic/);
});

test("identical request IDs on different sessions cannot hide an error response", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  target("other", url);
  await client.pageFor("other hub", value => value.url === url && value.targetId === "other");
  event("Network.requestWillBeSent", { requestId: "shared", documentURL: url, request: { url: `${url}api/v1/actual-feature` } }, "session-hub");
  event("Network.responseReceived", { requestId: "shared", response: { status: 503 } }, "session-hub");
  event("Network.requestWillBeSent", { requestId: "shared", documentURL: url, request: { url: `${url}api/v1/other` } }, "session-other");
  event("Network.responseReceived", { requestId: "shared", response: { status: 200 } }, "session-other");
  await settled();
  const snapshot = client.diagnostics();
  assert.equal(snapshot.requests.length, 2);
  const result = classifyFirstRunDiagnostics({ phase: "hub-chat", ...snapshot, hubURL: url, hubTargetId: "hub" });
  assert.equal(result.ok, false);
  assert.ok(result.unexpected.some(value => value.kind === "response" && value.status === 503));
});

test("duplicate CSP evidence within one action fails instead of becoming extra expected entries", () => {
  const action = { targetId: "setup", kind: "setup-resource-negative", url: "persona-setup://app/main.js", sequence: 10, endSequence: 20 };
  const base = { kind: "log", targetId: "setup", action };
  const result = classifyFirstRunDiagnostics({ phase: "setup-security", pages: [{ targetId: "setup", url: "persona-setup://app/setup.html" }], actions: [action], consoleErrors: [
    { ...base, text: "Connecting to 'persona-setup://app/main.js' violates the following Content Security Policy directive: \"default-src 'none'\"." },
    { ...base, text: "Fetch API cannot load persona-setup://app/main.js. Refused to connect because it violates the document's Content Security Policy." },
    { ...base, text: "Connecting to 'persona-setup://app/main.js' violates the following Content Security Policy directive: \"default-src 'none'\"." },
  ] });
  assert.equal(result.ok, false);
  assert.equal(finalOutcome([result]).exitCode, 1);
});

test("a destroyed target retains error responses for final verdict", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  event("Network.requestWillBeSent", { requestId: "late-error", documentURL: url, request: { url: `${url}api/v1/failing` } }, "session-hub");
  event("Network.responseReceived", { requestId: "late-error", response: { status: 503 } }, "session-hub");
  event("Target.targetDestroyed", { targetId: "hub" });
  await settled();
  const snapshot = client.diagnostics();
  assert.equal(snapshot.requests.length, 1);
  assert.equal(snapshot.requests[0].sessionId, "session-hub");
  const result = classifyFirstRunDiagnostics({ phase: "final-observation", ...snapshot, hubURL: url });
  assert.equal(result.ok, false);
  assert.equal(result.unexpected[0].sessionId, "session-hub");
  assert.equal(result.unexpected[0].requestId, "late-error");
  assert.equal(finalOutcome([result]).exitCode, 1);
});

test("ExtraInfo credentials make a password-state 401 unexpected in either CDP event order", async t => {
  const { client, event, target } = await connected(t);
  const url = "http://127.0.0.1:18760/";
  target("hub", url);
  await client.pageFor("hub", value => value.url === url);
  const cases = [
    ["Cookie", false, ["request", "extra"]],
    ["Cookie", true, ["extra", "request"]],
    ["Authorization", false, ["request", "extra"]],
    ["Authorization", true, ["extra", "request"]],
  ];
  for (const [header, extraFirst, order] of cases) {
    const requestId = `credentialed-${header}-${extraFirst}`;
    const events = {
      request: () => event("Network.requestWillBeSent", { requestId, documentURL: url, request: { url: `${url}api/v1/auth/password/state`, headers: {} } }, "session-hub"),
      extra: () => event("Network.requestWillBeSentExtraInfo", { requestId, headers: { [header]: "synthetic-not-recorded" } }, "session-hub"),
    };
    for (const name of order) events[name]();
    event("Network.responseReceived", { requestId, response: { status: 401 } }, "session-hub");
    event("Log.entryAdded", { entry: { level: "error", text: "Failed to load resource: the server responded with a status of 401 (Unauthorized)", url: `${url}api/v1/auth/password/state` } }, "session-hub");
    await settled();
    const snapshot = client.diagnostics();
    const request = snapshot.requests.find(value => value.requestId === requestId);
    assert.equal(request.hasCookie, header === "Cookie");
    assert.equal(request.hasAuthorization, header === "Authorization");
    const result = classifyFirstRunDiagnostics({ phase: "hub-sign-in", ...snapshot, hubURL: url, signInTargetId: "hub" });
    assert.equal(result.ok, false, `${header}, ExtraInfo first: ${extraFirst}`);
    const outcome = finalOutcome([result]);
    assert.equal(outcome.exitCode, 1);
    assert.equal(outcome.verdict.functionalFirstRunAccepted, false);
    await client.phase("next credential ordering negative");
  }
});

test("a late compatibility response stays bound to the same chat page without exempting other targets", () => {
  const hubURL = "http://127.0.0.1:18760/";
  const request = { requestId: "late-compatible", sessionId: "session-hub", targetId: "hub", responseTargetId: "hub", documentURL: hubURL, status: 404, url: `${hubURL}api/v1/hub/update` };
  const log = { kind: "log", text: "Failed to load resource: the server responded with a status of 404 (Not Found)", targetId: "hub", pageURL: hubURL, url: request.url, requestId: request.requestId, sessionId: request.sessionId };
  const base = { phase: "final-observation", hubURL, hubTargetId: "hub", pages: [{ targetId: "hub", url: hubURL }], requests: [request], consoleErrors: [log] };
  const accepted = classifyFirstRunDiagnostics(base);
  assert.equal(accepted.ok, true);
  assert.equal(accepted.expected.length, 1);
  assert.equal(finalOutcome([accepted]).exitCode, 0);
  for (const changed of [
    { hubTargetId: undefined },
    { hubTargetId: "another-window" },
    { requests: [{ ...request, url: `${hubURL}api/v1/unknown` }], consoleErrors: [{ ...log, url: `${hubURL}api/v1/unknown` }] },
    { requests: [{ ...request, documentURL: "https://example.com/" }] },
  ]) {
    const result = classifyFirstRunDiagnostics({ ...base, ...changed });
    assert.equal(result.ok, false, JSON.stringify(changed));
    assert.equal(finalOutcome([result]).exitCode, 1);
  }
});

test("freeze fixes the final observation and close waits for the real socket", async t => {
  const { client, event, target } = await connected(t);
  target("hub", "http://127.0.0.1:18760/");
  await client.pageFor("hub", value => value.url === "http://127.0.0.1:18760/");
  client.freeze();
  event("Runtime.exceptionThrown", { exceptionDetails: { text: "after freeze" } }, "session-hub");
  await settled();
  assert.equal(client.diagnostics().consoleErrors.length, 0);
  await client.close();
});
