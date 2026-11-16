const fixture = name => new URL(name, import.meta.url).href;
export async function resolve(specifier, context, nextResolve) {
  if (specifier === "electron") return { url: fixture("./electron-lifecycle.mjs"), format: "module", shortCircuit: true };
  if (specifier === "electron-updater") return { url: fixture("./lifecycle-updater.mjs"), format: "module", shortCircuit: true };
  if (specifier === "./store.js" && context.parentURL?.endsWith("/dist-test/src/main.js")) return { url: fixture("./lifecycle-store.mjs"), format: "module", shortCircuit: true };
  if (specifier === "./strings.js" && context.parentURL?.endsWith("/dist-test/src/main.js")) return { url: fixture("./lifecycle-strings.mjs"), format: "module", shortCircuit: true };
  return nextResolve(specifier, context);
}
