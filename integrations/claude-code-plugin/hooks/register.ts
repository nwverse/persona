import type { EngineInterface, Register } from 'claude-code'
import {
  BASE,
  CAPABILITIES,
  POLL_WAIT_MS,
  applyStepOverride,
  homeDir,
  mergeOverride,
  nextBackoff,
  readPoll,
  socketPath,
  verdict,
  type Message,
  type PollAnswer,
  type SessionOverride,
  type SettingsUpdate,
} from './bridge'

// Persona's plugin (README.md): while this session lives it asks the local Persona node for the owner's own messages
// and session-scoped model / effort picks from the Persona apps. Messages go in as the person's words (`asUser`), and
// model / effort overrides apply to main-loop `turn.step` requests without touching global settings files.

type Engine = EngineInterface

export const register: Register = on => {
  // One poll loop per load: a reload runs session.start again in a fresh environment, and the old loop goes with
  // its environment. A /clear keeps the loop: each poll names the session open now.
  let loop = 0
  let override: SessionOverride | undefined

  on('turn.step', async function* ($, e, next) {
    if (e.agentId !== undefined || override === undefined) return yield* next(e)
    const session = await $.session.id().catch(() => '')
    const sessionModel = override.model !== undefined ? await $.session.model().catch(() => undefined) : undefined
    const applied = applyStepOverride(e, override, session, sessionModel)
    override = applied.nextOverride
    return yield* next(applied.step)
  })

  on('session.start', async ($, e, next) => {
    const started = await next(e)
    const mine = ++loop
    void run(
      $,
      () => mine === loop,
      async (session, update) => {
        const sessionModel = update.model !== undefined ? await $.session.model().catch(() => undefined) : undefined
        override = mergeOverride(override, session, update, sessionModel)
      },
    )
    return started
  })
}

async function run(
  $: Engine,
  current: () => boolean,
  onSettings: (session: string, update: SettingsUpdate) => Promise<void>,
): Promise<void> {
  const home = homeDir(await $.env.get('USERPROFILE'), await $.env.get('HOME'))
  const sock = socketPath(home, await $.env.get('PERSONA_HOME'))
  if (sock === undefined) return
  const pluginVersion = await manifestVersion($)
  const engineVersion = await $.session
    .version()
    .then(v => v.version)
    .catch(() => '')
  let backoff = 0
  let reachable: boolean | undefined
  while (current()) {
    const session = await $.session.id()
    let answer: PollAnswer
    try {
      const res = await $.http.fetch(`${BASE}/poll`, post(sock, {
        session_id: session,
        plugin_version: pluginVersion,
        engine_version: engineVersion,
        capabilities: CAPABILITIES,
        wait_ms: POLL_WAIT_MS,
      }))
      answer = readPoll(res.status, res.text)
    } catch {
      answer = { kind: 'unavailable' }
    }
    if (!current()) return
    const now = answer.kind === 'messages'
    if (now !== reachable) {
      reachable = now
      $.ui.log(now ? 'Persona node reached' : `Persona node not reachable (${answer.kind})`, { to: 'debug' })
    }
    if (answer.kind !== 'messages') {
      backoff = answer.kind === 'refused' ? answer.retryMs : nextBackoff(backoff)
      await sleep($, backoff)
      continue
    }
    backoff = 0
    if (answer.settings !== undefined) await applySettings($, sock, session, answer.settings, onSettings)
    for (const m of answer.messages) await deliver($, sock, session, m)
    if (answer.retryMs > 0) await sleep($, answer.retryMs)
  }
}

async function applySettings(
  $: Engine,
  sock: string,
  session: string,
  update: SettingsUpdate,
  onSettings: (session: string, update: SettingsUpdate) => Promise<void>,
): Promise<void> {
  let ok = true
  try {
    await onSettings(session, update)
  } catch {
    ok = false
  }
  const ack = ok ? { submitted: true } : { submitted: false, reason: 'failed' }
  try {
    await $.http.fetch(`${BASE}/ack`, post(sock, { session_id: session, id: update.id, ...ack }))
  } catch {
    // The node went away before receiving the ack.
  }
}

/** Submits one message as the person's words, if it may go in now, and tells the node what became of it. */
async function deliver($: Engine, sock: string, session: string, m: Message): Promise<void> {
  let outcome: string = verdict(m, await $.clock.now())
  if (outcome === 'ok' && typeof m.text === 'string') {
    try {
      const submitted = await $.prompt.submit({ text: m.text, asUser: true })
      if (submitted.drop !== undefined) outcome = 'dropped'
    } catch {
      outcome = 'failed'
    }
  }
  const ack = outcome === 'ok' ? { submitted: true } : { submitted: false, reason: outcome }
  try {
    await $.http.fetch(`${BASE}/ack`, post(sock, { session_id: session, id: m.id, ...ack }))
  } catch {
    // The node went away: it reads the transcript to learn what became of the message.
  }
}

async function manifestVersion($: Engine): Promise<string> {
  try {
    const manifest: unknown = JSON.parse(await $.fs.read(`${$.plugin.root}/.claude-plugin/plugin.json`))
    const version = (manifest as { version?: unknown }).version
    return typeof version === 'string' ? version : ''
  } catch {
    return ''
  }
}

function post(socket: string, body: Record<string, unknown>) {
  return {
    method: 'POST',
    socketPath: socket,
    headers: { 'content-type': 'application/json' },
    body: JSON.stringify(body),
  }
}

function sleep($: Engine, ms: number): Promise<void> {
  return new Promise(resolve => {
    $.clock.after(ms, resolve)
  })
}
