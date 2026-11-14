export interface DesktopNotification {
  readonly title: string;
  readonly body: string;
  readonly tag: string;
  /** Existing BASE-prefixed SPA path, never a complete remote URL. */
  readonly route: string;
  readonly silent?: boolean;
}

export interface DesktopBridge {
  readonly version: 1;
  readonly platform: string;
  readonly deviceName: string;
  notificationPermission(): string;
  setBadge(count: number): void;
  /** false keeps the existing browser notification fallback. */
  notify(notification: DesktopNotification): boolean;
  closeNotification(tag: string): void;
  onOpenNotification(callback: (route: string) => void): () => void;
  setLaunchAtLogin(enabled: boolean): Promise<{ enabled: boolean; requiresApproval?: boolean }>;
  getLaunchAtLogin(): Promise<{ enabled: boolean; requiresApproval?: boolean }>;
}

export function notificationRoute(value: string): string | undefined {
  if (!value.startsWith("/") || value.startsWith("//") || value.includes("\\") || /[\u0000-\u001f\u007f]/.test(value)) return undefined;
  const url = new URL(value, "https://desktop.invalid");
  return url.origin === "https://desktop.invalid" ? `${url.pathname}${url.search}` : undefined;
}
