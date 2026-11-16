import { net, protocol } from "electron";
import { SETUP_SCHEME, setupResourceURL } from "./setup-resources.js";
export { SETUP_SCHEME };

// The main entry point registers the scheme before app.ready, then installs this exact handler from its ready callback.
// It grants no file:, Node, service-worker, fetch/stream or CSP-bypass capability.
export function installSetupProtocol(): void {
  protocol.handle(SETUP_SCHEME, async request => {
    const resource = setupResourceURL(request.url, request.method);
    if (!resource) return new Response("Not found", { status: 404 });
    return net.fetch(resource);
  });
}
