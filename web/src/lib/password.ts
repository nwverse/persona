import { ApiError } from "./api";
import { BASE } from "./base";

export const passwordSuite = "opaque-ristretto255-sha512-argon2id-v1";
export interface PasswordState { enabled: boolean; hub_id: string; hub_name: string; suite: string }
interface PakeResponse { session: string; message: string; identity: string; suite: string }
export interface PasswordCredential { token: string; id: string; hub_id: string }
export interface NativeLogin { state: string; challenge: string; kind: "phone" | "desktop"; name: string }

export async function passwordApi<T>(path: string, json?: unknown): Promise<T> {
  const response = await fetch(`${BASE}/api/v1/auth/password${path}`, {
    method: json === undefined ? "GET" : "POST", cache: "no-store", credentials: "omit", redirect: "error",
    headers: json === undefined ? {} : { "Content-Type": "application/json" },
    body: json === undefined ? undefined : JSON.stringify(json),
  });
  const data = await response.json();
  if (!response.ok) throw new ApiError(response.status, data.error || "登录失败，请重试", data);
  return data as T;
}

export class PakeClient {
  private worker: Worker;
  private sequence = 0;
  private pending = new Map<number, { resolve: (m: string) => void; reject: (e: Error) => void; timer: ReturnType<typeof setTimeout> }>();
  private closed = false;
  constructor() {
    if (!globalThis.isSecureContext) throw new Error("密码登录需要 HTTPS，请通过加密地址打开 hub");
    this.worker = new Worker(`${BASE}/pake/worker.js`);
    this.worker.onmessage = ({ data }: MessageEvent<{ id: number; message?: string; error?: string }>) => {
      const p = this.pending.get(data.id);
      if (!p) return;
      clearTimeout(p.timer);
      this.pending.delete(data.id);
      if (data.error || !data.message) p.reject(new Error(data.error || "验证消息无效"));
      else p.resolve(data.message);
    };
    this.worker.onerror = () => this.close();
  }
  call(op: string, values: { password?: string; message?: string; identity?: string } = {}): Promise<string> {
    if (this.closed) return Promise.reject(new Error("密码验证已结束，请重试"));
    const id = ++this.sequence;
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => this.close(new Error("密码验证超时，请重试")), 120_000);
      this.pending.set(id, { resolve, reject, timer });
      this.worker.postMessage({ id, input: { op, ...values } });
    });
  }
  close(error = new Error("密码验证已取消，请重试")) {
    this.closed = true;
    this.worker.terminate();
    for (const p of this.pending.values()) { clearTimeout(p.timer); p.reject(error); }
    this.pending.clear();
  }
}

export async function loginWithPassword(password: string, native: NativeLogin | null): Promise<PasswordCredential | { code: string }> {
  const client = new PakeClient();
  try {
    const message = await client.call("login-start", { password });
    const response = await passwordApi<PakeResponse>("/start", {
      message, name: native?.name || "网页浏览器", kind: native?.kind || "web", challenge: native?.challenge || "",
    });
    if (response.suite !== passwordSuite) throw new Error("hub 的密码协议不兼容，请更新客户端");
    const proof = await client.call("login-finish", { message: response.message, identity: response.identity });
    return await passwordApi("/finish", { session: response.session, message: proof });
  } finally { client.close(); }
}

export async function registerPassword(
  password: string, revision: string, revokeDevices: boolean,
  request: <T>(path: string, method: "POST", body: unknown) => Promise<T>,
): Promise<void> {
  if ([...password].length < 8 || new TextEncoder().encode(password).length > 256 || !password.trim()) {
    throw new Error("连接密码至少 8 个字符，最多 256 字节");
  }
  const client = new PakeClient();
  try {
    const message = await client.call("register-start", { password });
    const response = await request<PakeResponse>("/password-login/register/start", "POST", { message, revision });
    if (response.suite !== passwordSuite) throw new Error("hub 的密码协议不兼容");
    const record = await client.call("register-finish", { message: response.message, identity: response.identity });
    await request("/password-login/register/finish", "POST", { session: response.session, message: record, revoke_devices: revokeDevices });
  } finally { client.close(); }
}

// The verifier stays in the native app. Only a single-use code and random state return through the app scheme.
export function readNativeLogin(href: string): NativeLogin | null {
  const u = new URL(href);
  if (u.pathname !== "/login/device") return null;
  const q = new URLSearchParams(u.hash.slice(1));
  if (["state", "challenge", "kind", "name"].some((key) => q.getAll(key).length > 1)) {
    throw new Error("App 的登录请求无效，请回到 App 重试");
  }
  const state = q.get("state") || "";
  const challenge = q.get("challenge") || "";
  const kind = q.get("kind");
  const name = q.get("name") || "Persona 手机";
  if (!/^[A-Za-z0-9_-]{43}$/.test(state) || !/^[A-Za-z0-9_-]{43}$/.test(challenge) ||
    (kind !== "phone" && kind !== "desktop") || [...name].length > 80) throw new Error("App 的登录请求无效，请回到 App 重试");
  return { state, challenge, kind, name };
}
