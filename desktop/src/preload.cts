import { contextBridge, ipcRenderer } from "electron";

// Fixed, self-contained CommonJS capabilities; no arbitrary IPC, tokens, socket messages or Node access.
const api = Object.freeze({
  version: 1,
  platform: process.platform,
  deviceName: "Persona desktop",
  setBadge(count: number) { ipcRenderer.send("desktop:badge", count); },
  notificationPermission(): string { return ipcRenderer.sendSync("desktop:notification-permission"); },
  notify(notification: { title: string; body: string; tag: string; route: string; silent?: boolean; ix?: string }): boolean {
    return ipcRenderer.sendSync("desktop:notify", notification) === true;
  },
  closeNotification(tag: string) { ipcRenderer.send("desktop:notification-close", tag); },
  clearNotifications() { ipcRenderer.send("desktop:notifications-clear"); },
  onOpenNotification(callback: (route: string) => void): () => void {
    const listener = (_event: Electron.IpcRendererEvent, route: string) => callback(route);
    ipcRenderer.on("desktop:notification-open", listener);
    return () => { ipcRenderer.removeListener("desktop:notification-open", listener); };
  },
  desktopPresence(): { active: boolean; notifies: boolean } { return ipcRenderer.sendSync("desktop:presence"); },
  onPresenceChange(callback: (presence: { active: boolean; notifies: boolean }) => void): () => void {
    const listener = (_event: Electron.IpcRendererEvent, presence: { active: boolean; notifies: boolean }) => callback(presence);
    ipcRenderer.on("desktop:presence-changed", listener);
    return () => { ipcRenderer.removeListener("desktop:presence-changed", listener); };
  },
  setLaunchAtLogin(enabled: boolean) { return ipcRenderer.invoke("desktop:login-set", enabled); },
  getLaunchAtLogin() { return ipcRenderer.invoke("desktop:login-get"); },
});

contextBridge.exposeInMainWorld("personaDesktop", api);
