// Classify only the deliberately exercised negative cases and the synthetic hub's documented compatibility fallbacks.
// Everything else, including another instance of a familiar message, remains a failure.
export const SETUP_URL = "persona-setup://app/setup.html";
export const SETUP_SECURITY_URLS = new Set(["persona-setup://app/setup-preload.cjs", "persona-setup://app/main.js"]);
export const NAVIGATION_NEGATIVE_URL = "https://example.com/";

function inHub(hubURL, candidate) {
  if (!hubURL || !candidate) return false;
  try {
    const hub = new URL(hubURL), page = new URL(candidate);
    return page.origin === hub.origin && page.pathname.startsWith(hub.pathname);
  } catch { return false; }
}

function httpEvent(value) {
  const text = value.text ?? "";
  const status = Number(/^Failed to load resource: the server responded with a status of (\d+) /.exec(text)?.[1]);
  if (status !== 401 && status !== 404) return undefined;
  let url;
  try { url = new URL(value.url); } catch { return undefined; }
  if (url.username || url.password || url.hash) return undefined;
  return { status, url: url.href, path: url.pathname, search: url.search, origin: url.origin };
}

export function setupCSPResource(value) {
  const text = value.text ?? "";
  const url = /^Connecting to '([^']+)' violates the following Content Security Policy directive:/.exec(text)?.[1]
    ?? /^Fetch API cannot load ([^ ]+)\. Refused to connect because it violates the document's Content Security Policy\.$/.exec(text)?.[1];
  if (!url || !SETUP_SECURITY_URLS.has(url)) return undefined;
  return url;
}

function classifyConsole(value, phase, page) {
  if (value.kind === "runtime") return { classification: "unexpected", reason: "uncaught renderer exception" };
  const target = page(value.targetId);
  if (!target) return { classification: "unexpected", reason: "renderer error has no owning page" };
  const action = value.action;
  if (action && (action.targetId !== value.targetId || action.consoleErrors > value.index)) {
    return { classification: "unexpected", reason: "diagnostic is not after its declared action" };
  }
  const resource = httpEvent(value);
  if (resource) {
    if (!phase.hubURL) return { classification: "unexpected", reason: `HTTP ${resource.status} has no declared synthetic hub` };
    const hub = new URL(phase.hubURL);
    const request = phase.requests.find(item => item.requestId === value.requestId && item.sessionId === value.sessionId && item.url === resource.url && item.status === resource.status && item.responseTargetId === value.targetId);
    const hubPath = resource.path.startsWith(hub.pathname) ? resource.path.slice(hub.pathname.length) : undefined;
    if (!request || request.targetId !== value.targetId || request.documentURL !== value.pageURL || !inHub(phase.hubURL, value.pageURL) || resource.origin !== hub.origin || hubPath === undefined) {
      return { classification: "unexpected", reason: `HTTP ${resource.status} is not paired with an observed request on this page` };
    }
    if (phase.name === "hub-sign-in") {
      if (value.targetId === phase.signInTargetId && resource.status === 401 && hubPath === "api/v1/auth/password/state" && !resource.search
        && !request.hasAuthorization && !request.hasCookie) {
        return { classification: "expected", reason: "unsigned-in Login's optional password-mode probe" };
      }
      return { classification: "unexpected", reason: `HTTP ${resource.status} is not the unsigned-in password-mode probe` };
    }
    if ((phase.name === "hub-chat" || phase.name === "final-observation") && phase.hubTargetId && value.targetId === phase.hubTargetId && resource.status === 404) {
      const optional = hubPath === "api/v1/hub/update" ? "older hub has no update notice"
        : hubPath === "api/v1/workspace-board/settings" ? "older hub has no board-setting switch"
          : hubPath === "api/v1/plugins" ? "hub has no voice extension"
            : /^api\/v1\/assistant\/conversations\/[0-9a-f-]+\/feed$/.test(hubPath) && (/^\?paginate=1(?:&since=[^&]+)?$/.test(resource.search) || /^\?cursor=[^&]+$/.test(resource.search)) ? "older hub has no assistant feed"
              : undefined;
      if (optional) return { classification: "expected", reason: optional };
    }
    return { classification: "unexpected", reason: `HTTP ${resource.status} is not a declared synthetic-hub compatibility response` };
  }
  if (phase.name === "setup-security" && target.url === SETUP_URL) {
    const url = setupCSPResource(value);
    if (url && action?.kind === "setup-resource-negative" && action.url === url) {
      return { classification: "expected", reason: `deliberate CSP denial for ${url}` };
    }
  }
  return { classification: "unexpected", reason: "unclassified renderer log error" };
}

function classifyRequest(value, phase, page) {
  const target = page(value.targetId);
  if (!target) return { classification: "unexpected", reason: "failed request has no owning page" };
  const action = value.action;
  if (phase.name === "setup-security" && target.url === SETUP_URL && value.type === "Document" && value.errorText === "net::ERR_ABORTED"
    && value.url === NAVIGATION_NEGATIVE_URL && value.documentURL === SETUP_URL && value.requestSequence > action?.sequence && value.sequence > action?.sequence
    && value.sequence < action?.endSequence && action?.kind === "setup-navigation-negative" && action.url === NAVIGATION_NEGATIVE_URL && action.targetId === value.targetId
    && phase.failures.filter(item => item.action === action).length === 1) {
    return { classification: "expected", reason: "deliberate off-site navigation refused; the page stayed at setup" };
  }
  return { classification: "unexpected", reason: `unclassified ${value.errorText} network failure` };
}

/** CDP request/response pairs prove the HTTP log belongs to the same page; no credentials or query strings enter it. */
export function classifyFirstRunDiagnostics({ phase: name, consoleErrors = [], failedRequests = [], pages = [], requests = [], actions = [], attachFailures = [], hubURL, signInTargetId, hubTargetId }) {
  const page = id => pages.find(value => value.targetId === id);
  const phase = { name, requests, consoleErrors, hubURL, signInTargetId, hubTargetId, failures: failedRequests };
  const consoleRows = consoleErrors.map((value, index) => ({ kind: value.kind, text: value.text, url: value.url, targetId: value.targetId, action: value.action, requestId: value.requestId, sessionId: value.sessionId, ...classifyConsole({ ...value, index }, phase, page) }));
  const requestRows = failedRequests.map(value => ({ type: value.type, errorText: value.errorText, url: value.url, documentURL: value.documentURL, targetId: value.targetId, action: value.action, ...classifyRequest(value, phase, page) }));
  const responseRows = requests.filter(value => value.status === 401 || value.status === 404 || value.status >= 500)
    .filter(value => !consoleRows.some(row => row.classification === "expected" && row.requestId === value.requestId && row.sessionId === value.sessionId))
    .map(value => ({ kind: "response", url: value.url, status: value.status, targetId: value.targetId, sessionId: value.sessionId, requestId: value.requestId, classification: "unexpected", reason: `HTTP ${value.status} has no declared same-page compatibility log` }));
  const attachRows = attachFailures.map(value => ({ kind: "attachment", targetId: value.targetId, classification: "unexpected", reason: `CDP attach failed: ${value.error}` }));
  const unexpected = [...consoleRows, ...requestRows, ...responseRows, ...attachRows].filter(value => value.classification === "unexpected");
  const evidenceFor = action => [...consoleRows, ...requestRows].filter(value => value.classification === "expected"
    && value.action?.targetId === action.targetId && value.action?.kind === action.kind && value.action?.url === action.url && value.action?.sequence === action.sequence);
  for (const action of actions) {
    const evidence = evidenceFor(action);
    const complete = action.kind === "setup-resource-negative"
      ? evidence.length === 2 && ["Connecting to '", "Fetch API cannot load "].every(prefix => evidence.filter(value => value.text?.startsWith(prefix)).length === 1)
      : action.kind === "setup-navigation-negative" ? evidence.length === 1 : false;
    if (!complete) unexpected.push({ kind: "action", targetId: action.targetId, url: action.url, classification: "unexpected", reason: `declared ${action.kind} lacks its complete expected diagnostic` });
  }
  return {
    phase: name,
    expected: [...consoleRows, ...requestRows].filter(value => value.classification === "expected"),
    unexpected,
    ok: unexpected.length === 0,
    ...(unexpected.length ? { failure: `${unexpected.length} unexpected renderer/network diagnostic(s): ${unexpected[0].reason}` } : {}),
  };
}
