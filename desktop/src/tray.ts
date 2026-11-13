import { BrowserWindow, Menu, Tray, app, nativeImage } from "electron";
import { PERSONA_TRAY_ICON } from "./badge-art.js";

/** Closing a window leaves the signed-in renderer alive, so notifications and presence do not depend on a visible tab. */
export function closeToTray(window: BrowserWindow, quitting: () => boolean): void {
  window.on("close", event => {
    if (quitting()) return;
    event.preventDefault();
    window.hide();
  });
  if (process.platform === "darwin") {
    app.on("activate", () => window.show());
  }
}

export function installTray(window: BrowserWindow, quit: () => void): Tray {
  // Reuse the local Persona mark; never load a remote image into the privileged process.
  const icon = nativeImage.createFromDataURL(PERSONA_TRAY_ICON);
  const tray = new Tray(icon.resize({ width: 16, height: 16 }));
  tray.setToolTip("Persona");
  tray.setContextMenu(Menu.buildFromTemplate([
    { label: "Open Persona", click: () => { window.show(); window.focus(); } },
    { type: "separator" },
    { label: "Quit Persona", click: quit },
  ]));
  tray.on("click", () => { window.show(); window.focus(); });
  return tray;
}
