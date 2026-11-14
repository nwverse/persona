import assert from "node:assert/strict";
import { EventEmitter } from "node:events";
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { connect } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { startGateway } from "../scripts/smoke-gateway.mjs";

async function fixture(t) {
  const directory = await mkdtemp(join(tmpdir(), "persona-gateway-test-"));
  const dist = join(directory, "dist");
  await mkdir(dist);
  await writeFile(join(dist, "index.html"), '<base href="/" />');
  let forwarded = "";
  const upstream = createServer((req, res) => {
    forwarded = req.url;
    res.writeHead(200, { "Content-Type": "application/json" }).end('{"ok":true}');
  });
  await new Promise(done => upstream.listen(0, "127.0.0.1", done));
  const gateway = await startGateway({ dist, mockPort: upstream.address().port, prefix: "/persona" });
  t.after(async () => {
    await gateway.close();
    await new Promise(done => { upstream.closeAllConnections(); upstream.close(done); });
    await rm(directory, { recursive: true, force: true });
  });
  return { gateway, forwarded: () => forwarded };
}

function upgrade(port, path, headers = {}) {
  return new Promise((done, reject) => {
    const socket = connect(port, "127.0.0.1", () => {
      const lines = [`GET ${path} HTTP/1.1`, `Host: 127.0.0.1:${port}`, "Connection: Upgrade", "Upgrade: websocket"];
      for (const [name, value] of Object.entries(headers)) lines.push(`${name}: ${value}`);
      socket.end(`${lines.join("\r\n")}\r\n\r\n`);
    });
    let response = "";
    socket.on("data", chunk => { response += chunk; });
    socket.on("end", () => done(response));
    socket.on("error", reject);
    setTimeout(() => { socket.destroy(); done(response); }, 2000).unref();
  });
}

test("an in-prefix request passes, with redacted reporting but an intact forwarded query", async t => {
  const { gateway, forwarded } = await fixture(t);
  const response = await fetch(`${gateway.url}api/v1/test?synthetic-secret=one`);
  assert.equal(response.status, 200);
  assert.equal(forwarded(), "/api/v1/test?synthetic-secret=one");
  assert.equal(gateway.outside().length, 0);
  assert.deepEqual(gateway.requests(), [{ method: "GET", path: "/persona/api/v1/test", status: 200 }]);
  assert.ok(!JSON.stringify(gateway.requests()).includes("synthetic-secret"));
});

test("outside HTTP and UPGRADE fail closed even without browser metadata, with no query in evidence", async t => {
  const { gateway } = await fixture(t);
  const raw = await fetch(`${gateway.url}../escaped?synthetic-secret=two`);
  assert.equal(raw.status, 404);
  const socket = await fetch(`http://127.0.0.1:${gateway.port}/upgrade?synthetic-secret=three`, { headers: { Origin: "http://example.test" } });
  assert.equal(socket.status, 404);
  const response = await upgrade(gateway.port, "/socket?synthetic-secret=four", { "Sec-WebSocket-Version": "13", "Sec-WebSocket-Key": "c3ludGhldGljLWtleQ==" });
  assert.match(response, /^HTTP\/1\.1 404/);
  const outside = gateway.outside();
  assert.equal(outside.length, 3);
  assert.ok(outside.some(entry => entry.includes("/escaped")));
  assert.ok(outside.some(entry => entry.includes("/upgrade")));
  assert.ok(outside.some(entry => entry.startsWith("UPGRADE /socket")));
  assert.ok(!JSON.stringify({ outside, requests: gateway.requests() }).includes("synthetic-secret"));
});

test("an already-loaded hub registers the same console listener only once", () => {
  const contents = new EventEmitter();
  const errors = [];
  const record = event => { if (event.level === "error") errors.push(event.message); };
  // This is the exact remove-then-attach sequence used by the initial hub fallback and the navigation observer.
  contents.removeListener("console-message", record);
  contents.on("console-message", record);
  contents.removeListener("console-message", record);
  contents.on("console-message", record);
  contents.emit("console-message", { level: "error", message: "synthetic failure" });
  assert.equal(contents.listenerCount("console-message"), 1);
  assert.deepEqual(errors, ["synthetic failure"]);
});
