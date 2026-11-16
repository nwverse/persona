/** Stop a synthetic hub child, including one that already exited by signal. */
export async function stopHub(child) {
  if (child.exitCode !== null || child.signalCode !== null) return;
  const gone = new Promise(done => child.once("close", done));
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 3000);
  await gone;
  clearTimeout(timer);
}
