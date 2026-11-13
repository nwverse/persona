// Read-only access to opencode's own database (docs/integrations/opencode.md "Where the chat comes from"): the plugin API gives a session's
// context (what is left after a compaction) and no list of sessions, while opencode.db holds every session and every
// message with a stable seq. The plugin runs in opencode's own process (bun: `bun:sqlite`); the tests run in node
// (`node:sqlite`). It is opened read-only with query_only and a short busy timeout and never written to: opencode
// writes it (WAL mode), readers do not block it.

import { existsSync } from 'node:fs'
import { homedir } from 'node:os'
import { join } from 'node:path'

/** Where opencode keeps its database: $PERSONA_OPENCODE_DB, else $XDG_DATA_HOME/opencode (default ~/.local/share). */
export function defaultDbPath(env = process.env, home = homedir()) {
  if (env.PERSONA_OPENCODE_DB) return env.PERSONA_OPENCODE_DB
  return join(env.XDG_DATA_HOME || join(home, '.local', 'share'), 'opencode', 'opencode.db')
}

/**
 * Opens the database read-only: { all(sql, ...params) → rows, close() }. Throws when it is not there or no sqlite
 * binding is available.
 * @param {string} path
 */
export async function openReadOnly(path) {
  if (!existsSync(path)) throw new Error(`opencode database not found: ${path}`)
  let db
  let query
  try {
    const { Database } = await import('bun:sqlite')
    db = new Database(path, { readonly: true })
    query = (sql) => db.query(sql)
  } catch (err) {
    if (db) throw err
    const { DatabaseSync } = await import('node:sqlite')
    db = new DatabaseSync(path, { readOnly: true })
    query = (sql) => db.prepare(sql)
  }
  db.exec('PRAGMA busy_timeout = 300')
  db.exec('PRAGMA query_only = 1')
  return {
    all: (sql, ...params) => query(sql).all(...params),
    close: () => db.close(),
  }
}
