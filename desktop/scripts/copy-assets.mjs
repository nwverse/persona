import { copyFile, mkdir, rename, cp } from "node:fs/promises";
import { join } from "node:path";

// Only these three public resources may be served by the trusted local setup protocol. Preloads, IPC and locales stay outside.
const setup = join("dist", "setup");
await mkdir(setup, { recursive: true });
await copyFile(join("src", "setup.html"), join(setup, "setup.html"));
await copyFile(join("src", "setup.css"), join(setup, "setup.css"));
await copyFile(join("src", "setup.js"), join(setup, "setup.cjs"));
await cp(join("src", "locales"), join("dist", "locales"), { recursive: true });
for (const name of ["preload", "setup-preload", "contract", "link-contract", "notification-permission"]) {
  await rename(join("dist-preload", "desktop", "src", `${name}.cjs`), join("dist", `${name}.cjs`));
}
