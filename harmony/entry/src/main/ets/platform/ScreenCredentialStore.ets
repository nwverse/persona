import { asset } from '@kit.AssetStoreKit';
import { util } from '@kit.ArkTS';
import { cryptoFramework } from '@kit.CryptoArchitectureKit';
import { RFBCredentials } from '../core/RFBAuth';

const SERVICE: string = 'persona.screen.login.v1';

function digest(parts: string[]): Uint8Array {
  const md = cryptoFramework.createMd('SHA256');
  md.updateSync({ data: new util.TextEncoder().encodeInto(JSON.stringify(parts)) });
  return md.digestSync().data;
}

function missing(error: Error): boolean {
  return (error as Object as Record<string, Object>)['code'] === 24000002;
}

function exact(alias: Uint8Array): asset.AssetMap {
  const query: asset.AssetMap = new Map();
  query.set(asset.Tag.ALIAS, alias);
  return query;
}

async function removeAlias(alias: Uint8Array): Promise<void> {
  try { await asset.remove(exact(alias)); }
  catch (error) { if (!missing(error as Error)) throw new Error('screen_store_failed'); }
}

/** One atomic, device-bound Asset Store item per paired Mac. No machine names or secrets in aliases. */
export class ScreenCredentialStore {
  private static pending: Promise<void> = Promise.resolve();
  private readonly profileLabel: Uint8Array;
  private readonly alias: Uint8Array;

  constructor(profile: string, hub: string, authority: string, manager: string, machine: string, uid: string) {
    this.profileLabel = digest([SERVICE, profile]);
    this.alias = digest([SERVICE, profile, hub, authority, manager, machine, uid]);
  }

  private static change(operation: () => Promise<void>): Promise<void> {
    const next = ScreenCredentialStore.pending.then(operation);
    ScreenCredentialStore.pending = next.catch((): void => {});
    return next;
  }

  private async read(): Promise<RFBCredentials | undefined> {
    const query = exact(this.alias);
    query.set(asset.Tag.RETURN_TYPE, asset.ReturnType.ALL);
    let rows: asset.AssetMap[];
    try { rows = await asset.query(query); }
    catch (error) { if (missing(error as Error)) return undefined; throw new Error('screen_store_failed'); }
    if (!rows.length) return undefined;
    const secret = rows[0].get(asset.Tag.SECRET);
    if (!(secret instanceof Uint8Array)) return undefined;
    try {
      const length = rows[0].get(asset.Tag.DATA_LABEL_NORMAL_1);
      if (!(length instanceof Uint8Array) || length.length !== 2 || secret.length % 2 !== 0) return undefined;
      const split = length[0] * 256 + length[1], count = secret.length / 2;
      if (split < 1 || split > 256 || count <= split || count - split > 256) return undefined;
      const value = new RFBCredentials();
      for (let i = 0; i < count; i++) {
        const unit = String.fromCharCode(secret[i * 2] * 256 + secret[i * 2 + 1]);
        if (i < split) value.account += unit;
        else value.password += unit;
      }
      return value;
    } finally { secret.fill(0); }
  }

  async load(): Promise<RFBCredentials | undefined> {
    await ScreenCredentialStore.pending;
    return this.read();
  }

  save(value: RFBCredentials): Promise<void> {
    return ScreenCredentialStore.change(async (): Promise<void> => {
      const account = value.account, password = value.password;
      if (!account || !password || account.length > 256 || password.length > 256) throw new Error('screen_store_failed');
      // Two 256-code-unit fields fit the 1 KiB secret limit, including non-Latin credentials.
      const secret = new Uint8Array(2 * (account.length + password.length));
      try {
        for (let i = 0; i < secret.length / 2; i++) {
          const code = i < account.length ? account.charCodeAt(i) : password.charCodeAt(i - account.length);
          secret[i * 2] = code >>> 8; secret[i * 2 + 1] = code & 255;
        }
        const entry = exact(this.alias);
        entry.set(asset.Tag.SECRET, secret);
        entry.set(asset.Tag.DATA_LABEL_CRITICAL_1, this.profileLabel);
        entry.set(asset.Tag.DATA_LABEL_NORMAL_1, new Uint8Array([account.length >>> 8, account.length & 255]));
        entry.set(asset.Tag.ACCESSIBILITY, asset.Accessibility.DEVICE_UNLOCKED);
        entry.set(asset.Tag.SYNC_TYPE, asset.SyncType.NEVER);
        entry.set(asset.Tag.CONFLICT_RESOLUTION, asset.ConflictResolution.OVERWRITE);
        await asset.add(entry);
      } finally { secret.fill(0); }
    });
  }

  forget(rejected?: RFBCredentials): Promise<void> {
    return ScreenCredentialStore.change(async (): Promise<void> => {
      if (rejected) {
        const saved = await this.read();
        if (!saved || saved.account !== rejected.account || saved.password !== rejected.password) return;
      }
      await removeAlias(this.alias);
    });
  }

  static forgetProfile(profile: string): Promise<void> {
    return ScreenCredentialStore.change(async (): Promise<void> => {
      const query: asset.AssetMap = new Map();
      query.set(asset.Tag.DATA_LABEL_CRITICAL_1, digest([SERVICE, profile]));
      query.set(asset.Tag.RETURN_TYPE, asset.ReturnType.ATTRIBUTES);
      query.set(asset.Tag.RETURN_LIMIT, 64);
      while (true) {
        let rows: asset.AssetMap[];
        try { rows = await asset.query(query); }
        catch (error) { if (missing(error as Error)) return; throw new Error('screen_store_failed'); }
        if (!rows.length) return;
        for (const row of rows) {
          const alias = row.get(asset.Tag.ALIAS);
          if (!(alias instanceof Uint8Array)) throw new Error('screen_store_failed');
          await removeAlias(alias);
        }
      }
    });
  }
}
