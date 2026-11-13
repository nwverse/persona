import { join } from "node:path";
import { fileURLToPath } from "node:url";
import { app } from "electron";

/** Development-only capture after a real DOM readiness condition; never a renderer-controlled switch. */
export function installSmokeCapture(): void {
  const args = process.argv;
  const at = args.indexOf("--desktop-smoke-capture");
  if (app.isPackaged || at < 0 || !args[at + 1]) return;
  const output = fileURLToPath(args[at + 1]!);
  const deadline = setTimeout(() => { app.exit(2); }, 45_000);
  app.on("browser-window-created", (_event, window) => {
    window.webContents.once("did-finish-load", () => {
      const started = Date.now();
      const capture = () => {
        void window.webContents.executeJavaScript(`({text: document.body.innerText, ready: (document.querySelector('#root')?.children.length > 0 && !['', 'Loading'].includes(document.body.innerText.trim())) || !!document.querySelector('form')})`).then(async ({ text, ready }) => {
          if (!ready && Date.now() - started < 25_000) { setTimeout(capture, 250); return; }
          const { writeFile, appendFile } = await import("node:fs/promises");
          if (process.env.PERSONA_DESKTOP_DEBUG_LOG) await appendFile(process.env.PERSONA_DESKTOP_DEBUG_LOG, `SMOKE DOM: ${text.slice(0, 1000)}\n`, "utf8");
          const image = await window.webContents.capturePage();
          await writeFile(output, image.toPNG());
          clearTimeout(deadline);
          app.exit(ready ? 0 : 1);
        }).catch(() => app.exit(1));
      };
      setTimeout(capture, 500);
    });
  });
}
