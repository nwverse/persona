const startPrefix = 'Tool is running as a background task with task id:'
const endNotice = /\bTask id "([^"\n]+)" (?:was )?(finished|failed|canceled|cancelled|killed|stopped)\b/i
const taskMessage = /^[ \t]*\[Message\] timestamp=(\S+) sender=(\S+) priority=\S+ content=([\s\S]*)/m
const cronIteration = /\(iteration [0-9]+\)$/
const heartbeatNotice = /^(?:\[Notice\]\s+)?Task(?:\s+id\s+"[^"\n]+")?\s+is\s+still\s+running\b/i
const serverRestartNotice = /^(?:\[Notice\]\s+)?All your subagents and background tasks have been stopped due to server restart\b/i
const genericHeaderRe = /^(?:Created At: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})\r?\nCompleted At: \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})(?:\r?\n|$))+/
const emptyTaskList = 'No background tasks are currently running.'
const taskListHeader = /^You have ([1-9][0-9]*) background task\(s\) currently running:$/
const taskListRow = /^- .* \(`([^`\s]+)`\)$/
const terminalStatuses = new Map([
  ['DONE', 'completed'], ['ERROR', 'failed'], ['CANCELED', 'killed'], ['CANCELLED', 'killed'],
])
const kinds = new Map([['schedule', 'monitor'], ['run_workflow', 'workflow'], ['invoke_subagent', 'agent']])
const retainedForMs = 24 * 60 * 60 * 1000
const endedLimit = 20
const runningLimit = 50
const taskName = /^task-[0-9]+$/
const liveStep = new Set(['RUNNING', 'PENDING', 'IN_PROGRESS', 'QUEUED'])

// Step parsing can replace a GENERIC result with its final output. Keep the
// original task notice as private metadata so the outcome still has an ID.
export const backgroundStart = Symbol('backgroundStart')

export function backgroundNotice(content) {
  if (typeof content !== 'string') return ''
  content = content.trim()
  if (content.startsWith('Created At:')) {
    const newline = content.indexOf('\n')
    content = newline < 0 ? '' : content.slice(newline + 1).trimStart()
  }
  return content.startsWith(startPrefix) ? content.slice(startPrefix.length) : ''
}

export function priorBackgroundNotice(step) {
  if (step[backgroundStart]) return step[backgroundStart]
  if (liveStep.has(String(step.status).trim().toUpperCase()) && backgroundNotice(step.content)) return step.content
  return ''
}

function toolString(value) {
  if (typeof value !== 'string') return ''
  if (value.startsWith('"')) {
    try { return JSON.parse(value) } catch { /* The argument was already decoded. */ }
  }
  return value
}

function parseTime(value) {
  if (typeof value !== 'string' || !value) return Number.NaN
  return Date.parse(value)
}

function qualifiedTaskId(conversation, id) {
  return taskName.test(id) ? `${conversation}/${id}` : id
}

function toolOutput(content) {
  if (typeof content !== 'string') return ''
  if (content.startsWith('Created At: ')) {
    const match = genericHeaderRe.exec(content)
    if (match) return content.slice(match[0].length).replace(/^Output:\r?\n/, '')
  }
  const outIdx = content.indexOf('\nOutput:\n')
  return outIdx >= 0 && content.startsWith('Created At:') ? content.slice(outIdx + '\nOutput:\n'.length) : content
}

function systemMessageBody(content) {
  let trimmed = String(content ?? '').trim()
  const open = trimmed.lastIndexOf('<SYSTEM_MESSAGE>')
  if (open >= 0) trimmed = trimmed.slice(open + '<SYSTEM_MESSAGE>'.length)
  return trimmed.trim().replace(/<\/SYSTEM_MESSAGE>\s*$/, '').trim()
}

function serverRestartEvent(content, message, createdAt, order) {
  if (message) {
    if (message[2] !== 'system') return null
    const body = message[3].trim().replace(/<\/SYSTEM_MESSAGE>\s*$/, '').trim()
    if (!serverRestartNotice.test(body)) return null
    const at = parseTime(createdAt)
    if (Number.isFinite(at)) return { at, rawAt: createdAt, order }
    const fallback = parseTime(message[1])
    return { at: fallback, rawAt: Number.isFinite(fallback) ? message[1] : createdAt, order }
  }
  if (!serverRestartNotice.test(systemMessageBody(content))) return null
  return { at: parseTime(createdAt), rawAt: createdAt, order }
}

// Accepts complete, unfiltered manage_task(Action="list") results for this conversation.
function taskInventory(step, slot, calledAt, at, conversation) {
  const call = slot?.tool
  if (call?.name !== 'manage_task' || step.error || String(step.status ?? '').toUpperCase() !== 'DONE' ||
    !Number.isInteger(step.step_index) || step.step_index < 0 ||
    !Number.isInteger(slot.planner?.step_index) || slot.planner.step_index < 0 ||
    !Number.isFinite(calledAt) || !Number.isFinite(at) || at < calledAt ||
    step.truncated_fields?.includes('content') || slot.planner.truncated_fields?.includes('tool_calls')) {
    return null
  }
  const args = call.args
  if (!args || typeof args !== 'object' || Array.isArray(args) ||
    toolString(args.Action).toLowerCase() !== 'list') {
    return null
  }
  // A nonempty selector or an unknown option may make the inventory a subset.
  for (const [key, value] of Object.entries(args)) {
    if (key === 'Action') continue
    if (key === 'TaskId' && (value == null || (typeof value === 'string' && toolString(value) === ''))) continue
    return null
  }
  // The tool's timestamp can predate the result step when an old result is exported late.
  if (typeof step.content === 'string' && step.content.startsWith('Created At: ')) {
    const newline = step.content.indexOf('\n')
    const created = (newline < 0 ? step.content : step.content.slice(0, newline)).slice('Created At: '.length).trim()
    const started = parseTime(created)
    if (!Number.isFinite(started) || started < calledAt) return null
  }
  const output = toolOutput(step.content).trim()
  if (output === emptyTaskList) return new Set()
  const newline = output.indexOf('\n')
  if (newline < 0) return null
  const header = taskListHeader.exec(output.slice(0, newline).trim())
  if (!header) return null
  const count = Number(header[1])
  const prefix = conversation + '/'
  const ids = new Set()
  for (const line of output.slice(newline + 1).split('\n')) {
    const match = taskListRow.exec(line.trim())
    if (!match) return null
    const id = qualifiedTaskId(conversation, match[1])
    if (!id.startsWith(prefix) || !taskName.test(id.slice(prefix.length))) return null
    if (ids.has(id)) return null
    ids.add(id)
  }
  return ids.size === count ? ids : null
}

function recordSeen(task, at, order) {
  if (Number.isFinite(at)) {
    if (!Number.isFinite(task.lastSeen) || at > task.lastSeen) {
      task.lastSeen = at
      task.lastSeenOrder = order
    } else if (at === task.lastSeen && order > task.lastSeenOrder) {
      task.lastSeenOrder = order
    }
  } else if (!Number.isFinite(task.lastSeen) && order > task.lastSeenOrder) {
    task.lastSeenOrder = order
  }
}

function compareEvents(a, b) {
  const aTimed = Number.isFinite(a.at)
  const bTimed = Number.isFinite(b.at)
  if (aTimed && bTimed && a.at !== b.at) return a.at - b.at
  return a.order - b.order
}

function restartAfterTask(restart, lastSeen, lastSeenOrder) {
  if (Number.isFinite(restart.at) && Number.isFinite(lastSeen)) {
    return restart.at > lastSeen || (restart.at === lastSeen && restart.order > lastSeenOrder)
  }
  return restart.order > lastSeenOrder
}

function finish(task, status, at, inferred = false) {
  if (!task || (task.status && (!task.inferred || inferred))) return
  task.status = status
  task.ended_at = at
  task.inferred = inferred
}

export function extractBackground(steps, conversation) {
  const tasks = new Map()
  const prefix = conversation + '/'
  const pending = new Map()
  const restarts = []
  const inventories = []
  const inventorySeen = new Map()
  let index = -1
  let order = -1
  for (const step of steps) {
    order += 1
    index = Number.isInteger(step?.step_index) && step.step_index >= 0 ? step.step_index : index + 1
    const at = parseTime(step?.created_at)
    const slot = pending.get(index)
    pending.delete(index)
    switch (step?.type) {
      case 'PLANNER_RESPONSE': {
        const calls = Array.isArray(step.tool_calls) ? step.tool_calls : []
        for (let offset = 0; offset < calls.length; offset += 1) {
          const call = calls[offset]
          // Late results retain their native step index. An overlapping claim makes the slot ambiguous.
          const target = index + 1 + offset
          if (pending.has(target)) pending.set(target, null)
          else pending.set(target, { planner: step, plannerOrder: order, tool: call })
          if (call?.name === 'manage_task' && toolString(call.args?.Action).toLowerCase() === 'kill') {
            finish(tasks.get(qualifiedTaskId(conversation, toolString(call.args?.TaskId))), 'killed', step.created_at)
          }
        }
        break
      }
      case 'SYSTEM_MESSAGE': {
        const content = step.content ?? ''
        const notice = endNotice.exec(content)
        if (notice) {
          const verb = notice[2].toLowerCase()
          const status = verb === 'finished' ? 'completed' : verb === 'failed' ? 'failed' : 'killed'
          finish(tasks.get(qualifiedTaskId(conversation, notice[1])), status, step.created_at)
        }
        const message = taskMessage.exec(content)
        const restart = serverRestartEvent(content, message, step.created_at, order)
        if (restart) {
          restarts.push(restart)
          break
        }
        if (message) {
          const sender = qualifiedTaskId(conversation, message[2])
          const task = tasks.get(sender)
          if (task) {
            recordSeen(task, at, order)
            if (!notice && task.oneShot) {
              const body = message[3].trim().replace(/<\/SYSTEM_MESSAGE>\s*$/, '').trim()
              if (cronIteration.test(body)) task.oneShot = false
              else if (!heartbeatNotice.test(body)) finish(task, 'completed', step.created_at)
            }
          }
        }
        break
      }
      case 'GENERIC': {
        let call
        if (slot?.planner) {
          const calledAt = parseTime(slot.planner.created_at)
          if (!Number.isFinite(at) || !Number.isFinite(calledAt) || at >= calledAt) {
            call = slot.tool
          }
          const ids = taskInventory(step, slot, calledAt, at, conversation)
          if (ids) {
            inventories.push({ at: calledAt, rawAt: slot.planner.created_at, order: slot.plannerOrder })
            for (const id of ids) {
              const prev = inventorySeen.get(id)
              if (!prev || calledAt > prev.at || (calledAt === prev.at && slot.plannerOrder > prev.order)) {
                inventorySeen.set(id, { at: calledAt, order: slot.plannerOrder })
              }
            }
          }
        }
        const notice = backgroundNotice(step[backgroundStart] || step.content)
        if (!notice) break
        const [first, ...details] = notice.split('\n')
        const id = first.trim()
        if (!id.startsWith(prefix) || !taskName.test(id.slice(prefix.length))) break
        let task = tasks.get(id)
        if (!task) {
          const description = details.map(line => line.trim()).find(line => line.startsWith('Task Description:'))
          task = {
            id,
            kind: kinds.get(call?.name) ?? 'shell',
            started_at: step.created_at,
            lastSeen: Number.NaN,
            lastSeenOrder: order,
          }
          if (description) task.description = description.slice('Task Description:'.length).trim()
          const schedule = call?.name === 'schedule' ? call.args : undefined
          task.oneShot = !task.description?.startsWith('Cron:') && !toolString(schedule?.CronExpression).trim() &&
            (schedule?.DurationSeconds != null || task.description?.startsWith('Timer:'))
          if (call?.name === 'run_command') task.command = toolString(call.args?.CommandLine) || toolString(call.args?.command)
          tasks.set(id, task)
        }
        recordSeen(task, at, order)
        const updatedAt = parseTime(step.updated_at)
        if (Number.isFinite(updatedAt)) recordSeen(task, updatedAt, order)
        const status = terminalStatuses.get(String(step.status).toUpperCase())
        if (status) finish(task, status, step.updated_at || step.created_at, true)
        break
      }
    }
  }
  inventories.sort(compareEvents)
  const running = [], ended = []
  for (const record of tasks.values()) {
    if (!record.status) {
      let lastSeen = record.lastSeen
      let lastSeenOrder = record.lastSeenOrder
      const listed = inventorySeen.get(record.id)
      if (listed && (!Number.isFinite(lastSeen) || listed.at > lastSeen ||
        (listed.at === lastSeen && listed.order > lastSeenOrder))) {
        lastSeen = listed.at
        lastSeenOrder = listed.order
      }
      let restart
      for (const ev of restarts) {
        if (!restartAfterTask(ev, lastSeen, lastSeenOrder)) continue
        if (!restart || compareEvents(ev, restart) < 0) restart = ev
      }
      const startedAt = parseTime(record.started_at)
      const inventory = Number.isFinite(startedAt) && Number.isFinite(lastSeen)
        ? inventories.find(ev => ev.at > lastSeen)
        : undefined
      if (restart && inventory) {
        if (compareEvents(inventory, restart) < 0) finish(record, 'completed', inventory.rawAt, true)
        else finish(record, 'killed', restart.rawAt, true)
      } else if (restart) {
        finish(record, 'killed', restart.rawAt, true)
      } else if (inventory) {
        finish(record, 'completed', inventory.rawAt, true)
      }
    }
    const { inferred, oneShot, lastSeen, lastSeenOrder, ...task } = record
    if (task.status) {
      if (Number.isFinite(Date.parse(task.ended_at))) ended.push(task)
    } else if (running.length < runningLimit) {
      running.push(task)
    }
  }
  ended.sort((a, b) => Date.parse(a.ended_at) - Date.parse(b.ended_at))
  return { running, ended: ended.slice(-endedLimit) }
}

// The node attaches log paths from its own local transcript after validating
// that each path belongs to the conversation and task.
export function backgroundRow(tasks, live, now = Date.now()) {
  const ended = tasks.ended.filter(task => now - Date.parse(task.ended_at) < retainedForMs)
  return {
    ...(live && tasks.running.length ? { background: tasks.running } : {}),
    ...(ended.length ? { background_ended: ended } : {}),
  }
}
