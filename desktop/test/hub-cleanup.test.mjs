import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import test from "node:test";
import { stopHub } from "../scripts/hub-process.mjs";

test("the production hub stop does not wait for a close event that already happened", async () => {
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { stdio: "ignore" });
  const closed = new Promise(done => child.once("close", done));
  child.kill("SIGTERM");
  await closed;
  assert.equal(child.exitCode, null);
  assert.equal(child.signalCode, "SIGTERM");
  await Promise.race([stopHub(child), new Promise((_, reject) => setTimeout(() => reject(new Error("production stop hung")), 1000))]);
});
