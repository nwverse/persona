import { join } from "node:path";
import { BrowserWindow, ipcMain, screen } from "electron";
import { notificationRoute } from "./contract.cjs";
import { EMPTY_ISLAND, islandDisplay, islandRect, islandSides, islandState, islandView } from "./island-model.js";
import type { IslandState, IslandStatus } from "./island-model.js";

/** How long the pointer may be away before the open list closes: crossing the pill's edge does not close it. */
const CLOSE_AFTER_MS = 250;

/**
 * The island at the MacBook notch (#7669): a frameless, always-on-top window that takes no focus, over the menu bar
 * around the notch. It draws the latest state the web sent (`setState`) from a local page (island.html), opens into a
 * list while the pointer is over it, and hands a clicked row's route to `openRoute`, the notification-click path.
 * macOS only; off until the person turns it on in Settings, and hidden while there is nothing to show.
 */
export class NotchIsland {
  private window: BrowserWindow | undefined;
  private state: IslandState = EMPTY_ISLAND;
  private open = false;
  private closing: ReturnType<typeof setTimeout> | undefined;
  private readonly placeNow = () => this.render();

  constructor(private readonly openRoute: (route: string) => void) {}

  get supported(): boolean { return process.platform === "darwin"; }

  status(): IslandStatus { return { supported: this.supported, enabled: this.window !== undefined }; }

  setEnabled(on: boolean): IslandStatus {
    if (on && this.supported) this.create();
    else this.destroy();
    return this.status();
  }

  /** The web's latest state; kept while the island is off, so turning it on shows the current counts at once. */
  setState(value: unknown): void {
    const next = islandState(value);
    if (!next) return;
    this.state = next;
    this.render();
  }

  private create(): void {
    if (this.window && !this.window.isDestroyed()) return;
    const w = new BrowserWindow({
      ...this.rect(),
      show: false,
      frame: false,
      transparent: true,
      backgroundColor: "#00000000",
      resizable: false,
      movable: false,
      minimizable: false,
      maximizable: false,
      fullscreenable: false,
      focusable: false,
      skipTaskbar: true,
      hasShadow: false,
      type: "panel",
      acceptFirstMouse: true,
      enableLargerThanScreen: true,
      alwaysOnTop: true,
      title: "Persona",
      webPreferences: {
        preload: join(import.meta.dirname, "island-preload.cjs"),
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
        webSecurity: true,
        devTools: false,
        webviewTag: false,
        spellcheck: false,
      },
    });
    w.setAlwaysOnTop(true, "screen-saver");
    w.setVisibleOnAllWorkspaces(true, { visibleOnFullScreen: true });
    w.webContents.setWindowOpenHandler(() => ({ action: "deny" }));
    w.webContents.on("will-navigate", event => event.preventDefault());
    w.webContents.on("did-finish-load", this.placeNow);
    w.once("closed", () => { if (this.window === w) this.window = undefined; });
    this.window = w;
    this.listen(w);
    screen.on("display-metrics-changed", this.placeNow);
    screen.on("display-added", this.placeNow);
    screen.on("display-removed", this.placeNow);
    void w.loadFile(join(import.meta.dirname, "island.html"));
  }

  private destroy(): void {
    screen.removeListener("display-metrics-changed", this.placeNow);
    screen.removeListener("display-added", this.placeNow);
    screen.removeListener("display-removed", this.placeNow);
    for (const name of ["island:hover", "island:open"]) ipcMain.removeAllListeners(name);
    if (this.closing) clearTimeout(this.closing);
    this.closing = undefined;
    this.open = false;
    const w = this.window;
    this.window = undefined;
    if (w && !w.isDestroyed()) w.destroy();
  }

  /** The page's two operations: the pointer over it or away, and a row clicked. Only this window's page is heard. */
  private listen(w: BrowserWindow): void {
    for (const name of ["island:hover", "island:open"]) ipcMain.removeAllListeners(name);
    const own = (event: Electron.IpcMainEvent) => !w.isDestroyed() && event.sender === w.webContents && event.senderFrame === event.sender.mainFrame;
    ipcMain.on("island:hover", (event, inside: unknown) => {
      if (!own(event)) return;
      if (this.closing) clearTimeout(this.closing);
      this.closing = undefined;
      if (inside === true) {
        if (!this.open && this.state.rows.length) { this.open = true; this.render(); }
        return;
      }
      this.closing = setTimeout(() => { this.closing = undefined; if (this.open) { this.open = false; this.render(); } }, CLOSE_AFTER_MS);
    });
    ipcMain.on("island:open", (event, value: unknown) => {
      if (!own(event) || typeof value !== "string") return;
      const route = notificationRoute(value);
      if (!route || !this.state.rows.some(r => r.route === route)) return;
      this.open = false;
      this.render();
      this.openRoute(route);
    });
  }

  private display() {
    return islandDisplay(screen.getAllDisplays(), screen.getPrimaryDisplay());
  }

  private rect() {
    return islandRect(this.display(), this.open, this.state.rows.length);
  }

  /** Places the window for the current state, sends the page what to draw, and hides it while there is nothing. */
  private render(): void {
    const w = this.window;
    if (!w || w.isDestroyed()) return;
    const [left] = islandSides(this.state);
    if (!left) {
      this.open = false;
      w.hide();
      return;
    }
    if (this.open && !this.state.rows.length) this.open = false;
    w.setBounds(this.rect());
    w.webContents.send("island:view", islandView(this.state, this.display(), this.open));
    if (!w.isVisible()) w.showInactive();
  }
}
