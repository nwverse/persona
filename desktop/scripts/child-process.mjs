import { spawn } from "node:child_process";
import { createServer } from "node:net";
import { stopHub } from "./hub-process.mjs";

async function freePort() {
  const probe = createServer();
  await new Promise(done => probe.listen(0, "127.0.0.1", done));
  const { port } = probe.address();
  await new Promise(done => probe.close(done));
  return port;
}

/** Start the repository's synthetic hub on a port owned by this run. */
export async function startHub(root, env, attempt = 1, { python = "python3", mockScript = "scripts/mock/mock_hub.py" } = {}) {
  const port = await freePort();
  let log = "";
  const child = spawn(python, ["-I", "-X", "faulthandler", mockScript], {
    cwd: root, env: { ...process.env, PORT: String(port), PYTHONIOENCODING: "utf-8", PYTHONUTF8: "1", ...env }, stdio: ["ignore", "pipe", "pipe"],
  });
  child.stdout.on("data", data => { log = (log + data).slice(-8000); });
  child.stderr.on("data", data => { log = (log + data).slice(-8000); });
  const started = Date.now(), deadline = started + 90_000;
  for (;;) {
    if (child.exitCode !== null || child.signalCode !== null) throw new Error(`The synthetic hub exited before it was ready:\n${log}`);
    try { if ((await fetch(`http://127.0.0.1:${port}/api/v1/health`, { signal: AbortSignal.timeout(800) })).ok) break; } catch { /* still starting */ }
    if (Date.now() > deadline) {
      // SIGABRT makes Python's fault handler write every thread's stack before it dies (elsewhere it only ends it).
      const gone = new Promise(done => child.once("close", done));
      child.kill("SIGABRT");
      await Promise.race([gone, new Promise(done => setTimeout(done, 3000))]);
      child.kill("SIGKILL");
      console.error(`The synthetic hub was not ready in 90 s (attempt ${attempt}); where it stood:\n${log.slice(-4000)}`);
      if (attempt >= 2) throw new Error(`The synthetic hub was not ready in 90 s, twice:\n${log.slice(-1500)}`);
      return startHub(root, env, attempt + 1, { python, mockScript });
    }
    await new Promise(done => setTimeout(done, 150));
  }
  return {
    port,
    startMs: Date.now() - started,
    attempts: attempt,
    log: () => log,
    stop: () => stopHub(child),
  };
}
