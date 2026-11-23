const startPrefix = 'Tool is running as a background task with task id:'
const endNotice = /\bTask id "([^"\n]+)" (?:was )?(finished|failed|canceled|cancelled|killed|stopped)\b/i
const terminalStatuses = new Map([
  ['DONE', 'completed'], ['ERROR', 'failed'], ['CANCELED', 'killed'], ['CANCELLED', 'killed'],
])
const kinds = new Map([['schedule', 'monitor'], ['run_workflow', 'workflow'], ['invoke_subagent', 'agent']])
const retainedForMs = 24 * 60 * 60 * 1000
const endedLimit = 8
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

function finish(task, status, at, inferred = false) {
  if (!task || (task.status && (!task.inferred || inferred))) return
  task.status = status
  task.ended_at = at
  task.inferred = inferred
}

export function extractBackground(steps, conversation) {
  const tasks = new Map()
  const prefix = conversation + '/'
  let planner, plannerIndex
  let index = -1
  for (const step of steps) {
    index = Number.isInteger(step?.step_index) && step.step_index >= 0 ? step.step_index : index + 1
    switch (step?.type) {
      case 'PLANNER_RESPONSE':
        planner = Array.isArray(step.tool_calls) ? step.tool_calls : undefined
        plannerIndex = index
        for (const call of planner ?? []) {
          if (call?.name === 'manage_task' && toolString(call.args?.Action).toLowerCase() === 'kill') {
            finish(tasks.get(toolString(call.args?.TaskId)), 'killed', step.created_at)
          }
        }
        break
      case 'USER_INPUT':
        planner = undefined
        break
      case 'SYSTEM_MESSAGE': {
        const notice = endNotice.exec(step.content ?? '')
        if (notice) {
          const verb = notice[2].toLowerCase()
          const status = verb === 'finished' ? 'completed' : verb === 'failed' ? 'failed' : 'killed'
          finish(tasks.get(notice[1]), status, step.created_at)
        }
        break
      }
      case 'GENERIC': {
        const notice = backgroundNotice(step[backgroundStart] || step.content)
        if (!notice) break
        const [first, ...details] = notice.split('\n')
        const id = first.trim()
        if (!id.startsWith(prefix) || !taskName.test(id.slice(prefix.length))) break
        let task = tasks.get(id)
        if (!task) {
          const call = planner?.[index - plannerIndex - 1]
          const description = details.map(line => line.trim()).find(line => line.startsWith('Task Description:'))
          task = { id, kind: kinds.get(call?.name) ?? 'shell', started_at: step.created_at }
          if (description) task.description = description.slice('Task Description:'.length).trim()
          if (call?.name === 'run_command') task.command = toolString(call.args?.CommandLine) || toolString(call.args?.command)
          tasks.set(id, task)
        }
        const status = terminalStatuses.get(String(step.status).toUpperCase())
        if (status) finish(task, status, step.updated_at || step.created_at, true)
        break
      }
    }
  }
  const running = [], ended = []
  for (const { inferred, ...task } of tasks.values()) {
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
