import { useState } from "react";
import { registerPassword } from "../../lib/password";
import { Button, toast } from "../../ui/atoms";
import { adminApi, errorText } from "../api";
import { Card, ErrorBox, Loader, useLoad } from "../parts";

interface Settings { enabled: boolean; configured: boolean; revision: string }

export function PasswordLoginSettings() {
  const { data, error, reload, set } = useLoad(() => adminApi<Settings>("/password-login"));
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [revoke, setRevoke] = useState(false);
  const [busy, setBusy] = useState(false);
  async function save() {
    if (!data || busy) return;
    if (password !== confirmation) { toast("两次输入的密码不一致", "error"); return; }
    setBusy(true);
    try {
      const pending = registerPassword(password, data.revision, revoke, adminApi);
      setPassword(""); setConfirmation("");
      await pending;
      reload();
      toast("连接密码已设置，已开启密码登录", "ok");
      setRevoke(false);
    } catch (error) { toast(errorText(error), "error"); }
    finally { setBusy(false); }
  }
  async function toggle() {
    if (!data || busy) return;
    setBusy(true);
    try {
      set(await adminApi<Settings>("/password-login", "PUT", { enabled: !data.enabled, revision: data.revision, revoke_devices: revoke }));
      setRevoke(false);
      toast(data.enabled ? "已关闭密码登录" : "已开启密码登录", "ok");
    } catch (error) { toast(errorText(error), "error"); }
    finally { setBusy(false); }
  }
  return <div className="mb-5"><Card title="客户端密码登录" footer="连接密码与管理员密码分开。关闭只阻止新的密码登录；勾选退出所有设备后，手机、手表、浏览器和电脑链路都需要重新连接。">
    {!data ? error ? <ErrorBox error={error} onRetry={reload} /> : <Loader /> : <div className="space-y-4 p-4">
      <div className="flex items-center justify-between gap-4">
        <p className="text-[13px] text-ink-2">{data.enabled ? "已开启，网页和 App 可以使用连接密码登录。" : "未开启，仍可使用扫码或配对链接。"}</p>
        {data.configured && <Button disabled={busy} onClick={toggle}>{data.enabled ? "关闭密码登录" : "开启密码登录"}</Button>}
      </div>
      <form className="flex flex-col gap-3" onSubmit={(e) => { e.preventDefault(); void save(); }}>
        <label className="text-[12.5px] text-ink-2">{data.configured ? "新的连接密码" : "设置连接密码"}
          <input type="password" autoComplete="new-password" aria-label="连接密码" value={password} disabled={busy}
            onChange={(e) => setPassword(e.target.value)} className="mt-1 block h-10 w-full rounded-control bg-inset px-3 text-ink shadow-hairline" />
        </label>
        <label className="text-[12.5px] text-ink-2">再次输入
          <input type="password" autoComplete="new-password" aria-label="再次输入连接密码" value={confirmation} disabled={busy}
            onChange={(e) => setConfirmation(e.target.value)} className="mt-1 block h-10 w-full rounded-control bg-inset px-3 text-ink shadow-hairline" />
        </label>
        <label className="flex items-center gap-2 text-[12.5px] text-ink-2"><input type="checkbox" checked={revoke} disabled={busy} onChange={(e) => setRevoke(e.target.checked)} />同时退出所有设备</label>
        <div><Button type="submit" tone="primary" disabled={busy || !password || !confirmation}>{busy ? "正在保存…" : "保存密码并开启"}</Button></div>
      </form>
    </div>}
  </Card></div>;
}
