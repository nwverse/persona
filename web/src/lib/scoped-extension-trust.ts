import { enc } from "./api";
import type { ScopedAssistantClient } from "./scoped-assistant-api";
import { listExtensionTrust, setExtensionTrust, type ExtensionTrust } from "./tracks";

const path = "/assistant/extension-trust";

export async function listScopedExtensionTrust(client: ScopedAssistantClient): Promise<ExtensionTrust[] | null> {
  const result = await client.request<{ items?: ExtensionTrust[] }>(path, undefined, async () => ({ items: await listExtensionTrust() ?? undefined }));
  return Array.isArray(result?.items) ? result.items : null;
}

export function setScopedExtensionTrust(client: ScopedAssistantClient, id: string, trust: ExtensionTrust): Promise<ExtensionTrust> {
  return client.request<ExtensionTrust>(`${path}/${enc(id)}`, { method: "PUT", json: { trust: trust.trust, categories: trust.categories } },
    () => setExtensionTrust(id, trust));
}
