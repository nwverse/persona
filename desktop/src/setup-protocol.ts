import { net, protocol } from "electron";
import { SETUP_SCHEME, setupResourceURL } from "./setup-resources.js";

// A stable origin makes the three relative setup resources work in development and in an asar. It grants no file:,
// Node, service-worker, fetch, streaming or CSP-bypass capability.
protocol.registerSchemesAsPrivileged([{ scheme: SETUP_SCHEME, privileges: { standard: true, secure: true } }]);

export function installSetupProtocol(): void {
  protocol.handle(SETUP_SCHEME, async request => {
    const resource = setupResourceURL(request.url, request.method);
    if (!resource) return new Response("Not found", { status: 404 });
    return net.fetch(resource);
  });
}
