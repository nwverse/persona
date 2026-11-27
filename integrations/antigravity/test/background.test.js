import assert from 'node:assert/strict'
import { test } from 'node:test'
import { extractBackground } from '../lib/background.js'

const conversation = '11111111-2222-4333-8444-555555555555'
const id = `${conversation}/task-1`
const started = '2026-06-01T10:00:00Z'
const fired = '2026-06-01T10:01:01Z'

function bgStep(step_index, status, created_at, taskNum, description, extra = {}) {
  return {
    step_index,
    type: 'GENERIC',
    status,
    created_at,
    content: `Created At: ${created_at}\nTool is running as a background task with task id: ${conversation}/task-${taskNum}\nTask Description: ${description}`,
    ...extra,
  }
}

function systemMessage(step_index, created_at, content) {
  return { step_index, type: 'SYSTEM_MESSAGE', created_at, content }
}

const cases = [
  { name: 'timer duration', args: { DurationSeconds: 60 }, description: 'Check status', completed: true },
  { name: 'timer description', completed: true },
  { name: 'cron expression', args: { CronExpression: '0 * * * *' } },
  { name: 'cron description', args: { DurationSeconds: '60' }, description: 'Cron: 0 * * * *' },
  { name: 'cron iteration', args: { DurationSeconds: '60' }, message: 'Check again (iteration 1)' },
  { name: 'heartbeat', args: { DurationSeconds: '60' }, message: 'Task is still running' },
  { name: 'different sender', sender: `${id}0` },
]

for (const { name, args, description = 'Timer: 60s', sender = id, message = 'Check again', completed } of cases) {
  test(`AGY schedule message: ${name}`, () => {
    const { running, ended } = extractBackground([
      { type: 'PLANNER_RESPONSE', tool_calls: [{ name: 'schedule', args }] },
      { type: 'GENERIC', status: 'RUNNING', created_at: started,
        content: `Tool is running as a background task with task id: ${id}\nTask Description: ${description}` },
      { type: 'SYSTEM_MESSAGE', created_at: fired,
        content: `<SYSTEM_MESSAGE>\n[Message] timestamp=${started} sender=${sender} priority=MESSAGE_PRIORITY_HIGH content=${message}\n</SYSTEM_MESSAGE>` },
    ], conversation)
    const task = { id, kind: 'monitor', description, started_at: started }
    assert.deepEqual(running, completed ? [] : [task])
    assert.deepEqual(ended, completed ? [{ ...task, status: 'completed', ended_at: fired }] : [])
  })
}

test('AGY server restart notice kills prior running tasks and keeps later tasks running', () => {
  const completedAt = '2026-06-01T10:01:00Z'
  const restartedAt = '2026-06-01T10:05:00Z'
  const resumedAt = '2026-06-01T10:06:00Z'
  const notice = `The following is a <SYSTEM_MESSAGE> not actually sent by the user.\n\n<SYSTEM_MESSAGE>\n[Message] timestamp=${restartedAt} sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>`
  const { running, ended } = extractBackground([
    {
      step_index: 0,
      type: 'PLANNER_RESPONSE',
      created_at: started,
      tool_calls: [
        { name: 'run_command', args: { CommandLine: 'npm test' } },
        { name: 'schedule', args: { CronExpression: '*/5 * * * *' } },
        { name: 'run_workflow' },
      ],
    },
    bgStep(1, 'RUNNING', started, 1, 'Run tests'),
    bgStep(2, 'RUNNING', started, 2, 'Cron: */5 * * * * Poll'),
    bgStep(3, 'DONE', started, 3, 'Already completed', { updated_at: completedAt }),
    systemMessage(4, restartedAt, notice),
    {
      step_index: 5,
      type: 'PLANNER_RESPONSE',
      created_at: resumedAt,
      tool_calls: [{ name: 'run_command', args: { CommandLine: 'npm run build' } }],
    },
    bgStep(6, 'RUNNING', resumedAt, 4, 'Build after restart'),
  ], conversation)
  assert.deepEqual(running.map(t => t.id), [`${conversation}/task-4`])
  assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [
    [`${conversation}/task-3`, 'completed', completedAt],
    [`${conversation}/task-1`, 'killed', restartedAt],
    [`${conversation}/task-2`, 'killed', restartedAt],
  ])
})

test('AGY server restart notice handles bare notices, message timestamp fallback, same-timestamp order, and ignores non-restart messages', () => {
  const restartedAt = '2026-06-01T10:05:00Z'
  const secondRestartAt = '2026-06-01T10:10:00Z'
  const afterSecondAt = '2026-06-01T10:11:00Z'

  // Bare notice with preamble wrapper.
  {
    const bare = 'The following is a <SYSTEM_MESSAGE> not actually sent by the user.\n\n<SYSTEM_MESSAGE>\n[Notice] All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>'
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Prior task'),
      systemMessage(1, restartedAt, bare),
    ], conversation)
    assert.deepEqual(running, [])
    assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [[`${conversation}/task-1`, 'killed', restartedAt]])
  }

  // Fallback to [Message] timestamp when step.created_at is omitted.
  {
    const wrapped = `<SYSTEM_MESSAGE>\n[Message] timestamp=${restartedAt} sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>`
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Prior task'),
      { step_index: 1, type: 'SYSTEM_MESSAGE', content: wrapped },
    ], conversation)
    assert.deepEqual(running, [])
    assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [[`${conversation}/task-1`, 'killed', restartedAt]])
  }

  // Same-timestamp tie-break and multiple restarts preserve earliest restart per task.
  {
    const msg = (ts) => `<SYSTEM_MESSAGE>\n[Message] timestamp=${ts} sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>`
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', restartedAt, 1, 'Before first restart same ts'),
      systemMessage(1, restartedAt, msg(restartedAt)),
      bgStep(2, 'RUNNING', restartedAt, 2, 'After first restart same ts'),
      systemMessage(3, secondRestartAt, msg(secondRestartAt)),
      bgStep(4, 'RUNNING', afterSecondAt, 3, 'After second restart'),
    ], conversation)
    assert.deepEqual(running.map(t => t.id), [`${conversation}/task-3`])
    assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [
      [`${conversation}/task-1`, 'killed', restartedAt],
      [`${conversation}/task-2`, 'killed', secondRestartAt],
    ])
  }

  // Quoted restart text from a task sender or a subagent idle report must not kill running tasks.
  {
    const fromTask = `<SYSTEM_MESSAGE>\n[Message] timestamp=${restartedAt} sender=${conversation}/task-1 priority=MESSAGE_PRIORITY_HIGH content=[Notice] All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>`
    const fromSubagentReport = `<SYSTEM_MESSAGE>\n[Message] timestamp=${restartedAt} sender=system priority=MESSAGE_PRIORITY_HIGH content=Subagent 00000000-0000-4000-8000-000000000002 has gone idle. This was the subagent's most recent response: All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>`
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Echo test output'),
      bgStep(1, 'RUNNING', started, 2, 'Still alive'),
      systemMessage(2, restartedAt, fromTask),
      systemMessage(3, restartedAt, fromSubagentReport),
    ], conversation)
    assert.deepEqual(running.map(t => t.id), [`${conversation}/task-1`, `${conversation}/task-2`])
    assert.deepEqual(ended, [])
  }
})

test('AGY manage_task(list) reconciliation validates complete inventories and ignores unsafe outputs', () => {
  const listed = '2026-06-01T10:00:02Z'
  const later = '2026-06-01T10:00:03Z'
  const empty = 'No background tasks are currently running.'
  const partial = 'You have 1 background task(s) currently running:\n- Check status (`task-2`)'
  const full = `You have 2 background task(s) currently running:\n- Wait for input (\`${conversation}/task-1\`)\n- Check status (\`task-2\`)`
  const listCases = [
    { name: 'empty inventory with Output header', args: { Action: 'list' }, status: 'DONE', content: `Created At: ${listed}\nCompleted At: ${listed}\nOutput:\n${empty}`, wantEnded: 2 },
    { name: 'empty inventory without Output header', args: { Action: 'list' }, status: 'DONE', content: `Created At: ${listed}\nCompleted At: ${listed}\n${empty}`, wantEnded: 2 },
    { name: 'listed task stays running (JSON-encoded action)', args: { Action: JSON.stringify('list') }, status: 'DONE', content: `Created At: ${listed}\nCompleted At: ${listed}\nOutput:\n${partial}`, wantEnded: 1 },
    { name: 'all tasks listed', args: { Action: 'list' }, status: 'DONE', content: `Created At: ${listed}\nCompleted At: ${listed}\nOutput:\n${full}`, wantEnded: 0 },
    { name: 'status is not a list', args: { Action: 'status' }, status: 'DONE', content: empty, wantEnded: 0 },
    { name: 'failed list status', args: { Action: 'list' }, status: 'ERROR', content: empty, wantEnded: 0 },
    { name: 'failed list error field', args: { Action: 'list' }, status: 'DONE', error: 'tool failed', content: empty, wantEnded: 0 },
    { name: 'truncated content', args: { Action: 'list' }, status: 'DONE', content: partial, truncated_fields: ['content'], wantEnded: 0 },
    { name: 'truncated planner tool_calls', args: { Action: 'list' }, status: 'DONE', content: empty, plannerTruncated: ['tool_calls'], wantEnded: 0 },
    { name: 'missing row', args: { Action: 'list' }, status: 'DONE', content: partial.replace('have 1', 'have 2'), wantEnded: 0 },
    { name: 'duplicate row', args: { Action: 'list' }, status: 'DONE', content: `${partial}\n- Check status (\`task-2\`)`, wantEnded: 0 },
    { name: 'foreign inventory', args: { Action: 'list' }, status: 'DONE', content: partial.replace('task-2', 'another-conversation/task-2'), wantEnded: 0 },
    { name: 'mixed inventory', args: { Action: 'list' }, status: 'DONE', content: full.replace('(`task-2`)', '(`another-conversation/task-2`)'), wantEnded: 0 },
    { name: 'filtered TaskId', args: { Action: 'list', TaskId: 'task-2' }, status: 'DONE', content: partial, wantEnded: 0 },
    { name: 'unknown selector', args: { Action: 'list', Scope: 'current-turn' }, status: 'DONE', content: empty, wantEnded: 0 },
    { name: 'empty TaskId string', args: { Action: 'list', TaskId: '' }, status: 'DONE', content: empty, wantEnded: 2 },
    { name: 'null TaskId', args: { Action: 'list', TaskId: null }, status: 'DONE', content: empty, wantEnded: 2 },
    { name: 'unknown output', args: { Action: 'list' }, status: 'DONE', content: 'Unable to list tasks', wantEnded: 0 },
  ]

  for (const tc of listCases) {
    const planner = {
      step_index: 2,
      type: 'PLANNER_RESPONSE',
      created_at: listed,
      tool_calls: [{ name: 'run_command' }, { name: 'manage_task', args: tc.args }],
      ...(tc.plannerTruncated ? { truncated_fields: tc.plannerTruncated } : {}),
    }
    const result = {
      step_index: 4,
      type: 'GENERIC',
      status: tc.status,
      created_at: listed,
      content: tc.content,
      ...(tc.error ? { error: tc.error } : {}),
      ...(tc.truncated_fields ? { truncated_fields: tc.truncated_fields } : {}),
    }
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Wait for input'),
      bgStep(1, 'RUNNING', started, 2, 'Check status'),
      planner,
      result,
      bgStep(3, 'RUNNING', later, 3, 'Started after list'),
    ], conversation)
    assert.equal(ended.length, tc.wantEnded, `${tc.name}: ended count`)
    assert.equal(running.length, 3 - tc.wantEnded, `${tc.name}: running count`)
    assert.equal(running[running.length - 1].id, `${conversation}/task-3`, `${tc.name}: later task kept running`)
    for (let i = 0; i < ended.length; i += 1) {
      assert.equal(ended[i].id, `${conversation}/task-${i + 1}`, `${tc.name}: ended id`)
      assert.equal(ended[i].status, 'completed', `${tc.name}: ended status`)
      assert.equal(ended[i].ended_at, listed, `${tc.name}: ended_at`)
    }
  }
})

test('AGY manage_task(list) respects slot ownership, call time, and newer task updates', () => {
  const first = '2026-06-01T10:00:01Z'
  const second = '2026-06-01T10:00:02Z'
  const written = '2026-06-01T10:00:03Z'
  const empty = 'No background tasks are currently running.'
  const present = 'You have 1 background task(s) currently running:\n- Wait for input (`task-1`)'
  const listCall = { name: 'manage_task', args: { Action: 'list' } }
  const planner = (step_index, created_at, tool_calls) => ({ step_index, type: 'PLANNER_RESPONSE', created_at, tool_calls })
  const result = (step_index, created_at, content) => ({ step_index, type: 'GENERIC', status: 'DONE', created_at, content })

  const ownershipCases = [
    {
      name: 'late result keeps its native slot',
      steps: [planner(1, first, [listCall]), planner(3, second, [listCall]), result(2, written, empty)],
      endedAt: first,
    },
    {
      name: 'late result after user input',
      steps: [planner(1, first, [listCall]), { step_index: 3, type: 'USER_INPUT' }, result(2, written, empty)],
      endedAt: first,
    },
    {
      name: 'old result in newer planner slot',
      steps: [planner(1, first, [listCall]), planner(3, written, [listCall]), result(4, second, empty)],
      endedAt: '',
    },
    {
      name: 'old tool timestamp in newer planner slot',
      steps: [
        planner(1, first, [listCall]),
        planner(3, second, [listCall]),
        result(4, written, `Created At: ${first}\nCompleted At: ${written}\nOutput:\n${empty}`),
      ],
      endedAt: '',
    },
    {
      name: 'overlapping planner slots',
      steps: [planner(1, first, [{ name: 'run_command' }, listCall]), planner(2, second, [listCall]), result(3, written, empty)],
      endedAt: '',
    },
    {
      name: 'newer snapshot wins over later export',
      steps: [planner(1, first, [listCall]), planner(3, second, [listCall]), result(4, second, present), result(2, written, empty)],
      endedAt: '',
    },
    {
      name: 'repeated omission preserves first completion',
      steps: [planner(1, first, [listCall]), planner(3, second, [listCall]), result(4, second, empty), result(2, written, empty)],
      endedAt: first,
    },
  ]

  for (const tc of ownershipCases) {
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Wait for input'),
      ...tc.steps,
    ], conversation)
    if (!tc.endedAt) {
      assert.equal(running.length, 1, `${tc.name}: expected running`)
      assert.equal(ended.length, 0, `${tc.name}: expected no ended`)
    } else {
      assert.equal(running.length, 0, `${tc.name}: expected no running`)
      assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [[`${conversation}/task-1`, 'completed', tc.endedAt]], tc.name)
    }
  }

  // Call time and later heartbeat / updated_at keep active tasks running.
  const heartbeat = `[Message] timestamp=${second} sender=${conversation}/task-3 priority=MESSAGE_PRIORITY_HIGH content=Task is still running`
  const { running, ended } = extractBackground([
    bgStep(0, 'RUNNING', started, 1, 'Omitted older task'),
    bgStep(1, 'RUNNING', started, 2, 'Task with later update', { updated_at: second }),
    bgStep(2, 'RUNNING', started, 3, 'Task with later heartbeat'),
    planner(3, first, [{ name: 'run_command' }, listCall]),
    bgStep(4, 'RUNNING', second, 4, 'Started after the list call'),
    result(5, written, empty),
    systemMessage(6, second, heartbeat),
  ], conversation)
  assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [[`${conversation}/task-1`, 'completed', first]])
  assert.deepEqual(running.map(t => t.id), [
    `${conversation}/task-2`,
    `${conversation}/task-3`,
    `${conversation}/task-4`,
  ])
})

test('AGY reconciles earliest event between manage_task(list) and server restart', () => {
  const listTime = '2026-06-01T10:02:00Z'
  const afterList = '2026-06-01T10:03:00Z'
  const restartTime = '2026-06-01T10:05:00Z'
  const laterListTime = '2026-06-01T10:06:00Z'
  const listCall = { name: 'manage_task', args: { Action: 'list' } }
  const restartMsg = `<SYSTEM_MESSAGE>\n[Message] timestamp=${restartTime} sender=system priority=MESSAGE_PRIORITY_LOW content=[Notice] All your subagents and background tasks have been stopped due to server restart.\n</SYSTEM_MESSAGE>`

  // List omission before restart ends task-1 as completed; restart ends task-2 as killed.
  {
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Already gone before list'),
      { step_index: 1, type: 'PLANNER_RESPONSE', created_at: listTime, tool_calls: [listCall] },
      { step_index: 2, type: 'GENERIC', status: 'DONE', created_at: listTime, content: 'No background tasks are currently running.' },
      bgStep(3, 'RUNNING', afterList, 2, 'Running at restart'),
      systemMessage(4, restartTime, restartMsg),
    ], conversation)
    assert.deepEqual(running, [])
    assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [
      [`${conversation}/task-1`, 'completed', listTime],
      [`${conversation}/task-2`, 'killed', restartTime],
    ])
  }

  // Server restart before empty list preserves killed status at restart time.
  {
    const { running, ended } = extractBackground([
      bgStep(0, 'RUNNING', started, 1, 'Killed by restart'),
      systemMessage(1, restartTime, restartMsg),
      { step_index: 2, type: 'PLANNER_RESPONSE', created_at: laterListTime, tool_calls: [listCall] },
      { step_index: 3, type: 'GENERIC', status: 'DONE', created_at: laterListTime, content: 'No background tasks are currently running.' },
    ], conversation)
    assert.deepEqual(running, [])
    assert.deepEqual(ended.map(t => [t.id, t.status, t.ended_at]), [
      [`${conversation}/task-1`, 'killed', restartTime],
    ])
  }
})
