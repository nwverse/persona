import { EventEmitter } from "node:events";

export const events = [];
export const windows = [];
export const controls = new Map();
export const scenario = { failNew: false, failNext: false, unhandled: [], oldPending: true, oldSucceeds: false };
process.on("unhandledRejection", error => { scenario.unhandled.push(String(error)); });
export let connect;
let oldLoad;

export let ready;
export let readySettled = false;
export const app = new class extends EventEmitter {
  isPackaged = true;
  requestSingleInstanceLock() { return true; }
  getPath() { return "/tmp/persona-test-only-not-a-real-home"; }
  getLocale() { return "en-US"; }
  getVersion() { return "0.1.0"; }
  whenReady() {
    ready = Promise.resolve();
    ready.then(() => { readySettled = true; });
    return ready;
  }
  isDefaultProtocolClient() { return true; }
  setAsDefaultProtocolClient() {}
  quit() { events.push("app.quit"); }
  async * [Symbol.asyncIterator]() { yield this; }
}();
export const protocol = { registerSchemesAsPrivileged() {}, handle() {} };
export const net = { fetch: async () => new Response("synthetic") };
export const shell = { openExternal: async () => {} };
export const powerMonitor = Object.assign(new EventEmitter(), { getSystemIdleTime: () => 0 });
export const screen = { getAllDisplays: () => [{ workArea: { x: 0, y: 0, width: 1000, height: 800 } }] };
export const Notification = class extends EventEmitter { static isSupported() { return false; } show() {} close() {} };
export const dialog = { showErrorBox: (title, text) => events.push({ title, text }) };
export const ipcMain = {
  on() {}, removeAllListeners() {}, removeHandler() {},
  handle(name, callback) { if (name === "desktop:hub-set") connect = callback; },
};
export const nativeImage = {
  createFromDataURL: () => ({ resize() { events.push("icon-resized"); return this; } }),
  createFromPath: () => ({}),
};
export const Menu = { buildFromTemplate: () => ({}) };
export const Tray = class extends EventEmitter {
  static instances = [];
  constructor() {
    super();
    Tray.instances.push(this);
    events.push("tray-created");
  }
  setToolTip() {}
  setContextMenu() {}
  on(name, callback) {
    super.on(name, callback);
    if (name === "click") events.push("tray-click-registered");
  }
};
export const autoUpdater = { on() {}, checkForUpdates: async () => undefined, quitAndInstall() {}, autoDownload: false, autoInstallOnAppQuit: false };
export const nativeTheme = {};

class Contents extends EventEmitter {
  constructor(owner) { super(); this.owner = owner; this.session = { setPermissionRequestHandler() {}, setPermissionCheckHandler() {} }; }
  setWindowOpenHandler() {}
  loadURL(url) { return this.owner.loadURL(url); }
  getURL() { return this.owner.url; }
  get mainFrame() { return this; }
  send() {}
}

export class BrowserWindow extends EventEmitter {
  constructor(options) {
    super();
    this.options = options;
    this.number = windows.length + 1;
    this.destroyed = false;
    this.url = "";
    this.webContents = new Contents(this);
    windows.push(this);
    events.push({ created: this.number, partition: options.webPreferences.partition });
  }
  setMenu() {}
  isDestroyed() { return this.destroyed; }
  destroy() { if (this.destroyed) return; this.destroyed = true; events.push({ destroyed: this.number }); this.emit("closed"); }
  close() { this.destroy(); }
  show() { events.push({ shown: this.number }); }
  hide() {}
  focus() {}
  isMinimized() { return false; }
  isMaximized() { return false; }
  getBounds() { return { x: 0, y: 0, width: 1000, height: 800 }; }
  getNormalBounds() { return this.getBounds(); }
  maximize() {}
  async loadURL(url) {
    events.push({ loading: this.number, url });
    if ((url === "https://old.example/old/" || url === "https://old.example/") && !scenario.oldSucceeds) {
      if (!oldLoad) {
        if (scenario.oldPending) {
          oldLoad = new Promise((_, reject) => { controls.set("reject-old", reject); });
        } else oldLoad = Promise.resolve();
      }
      await oldLoad;
    }
    if (this.destroyed) throw new Error("ERR_ABORTED: intentionally destroyed stale load");
    if (url === "https://new.example/" && scenario.failNew) throw new Error("synthetic hub connection failure");
    if (scenario.failNext) {
      scenario.failNext = false;
      throw new Error("synthetic hub connection failure");
    }
    this.url = url;
    events.push({ loaded: this.number, url });
  }
}

export async function settle() { await new Promise(resolve => setTimeout(resolve, 30)); }
export async function importMain() {
  const startup = import(process.env.PERSONA_TEST_MAIN);
  if (ready) await ready;
  await startup;
  await settle();
}
