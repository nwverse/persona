import { app } from "electron";
export const autoUpdater = {
  on() {},
  async checkForUpdates() {},
  quitAndInstall() {},
  autoDownload: false,
  autoInstallOnAppQuit: false,
};
export const appForUpdater = app;
export default { autoUpdater };
