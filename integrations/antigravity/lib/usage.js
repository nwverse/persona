function windowLabel(minutes) {
  if (minutes % 1440 === 0) return `${minutes / 1440}d`
  if (minutes % 60 === 0) return `${minutes / 60}h`
  return `${minutes}m`
}

function quotaWindowMinutes(bucket) {
  if (typeof bucket.window === 'string') {
    const match = /^(\d+(?:\.\d+)?)s$/.exec(bucket.window)
    const seconds = match ? Number(match[1]) : 0
    if (Number.isFinite(seconds) && seconds > 0 && seconds % 60 === 0) return seconds / 60
  }
  const id = typeof bucket.bucketId === 'string' ? bucket.bucketId.toLowerCase().replaceAll('_', '-') : ''
  const compact = id.replaceAll('-', '')
  if (compact.includes('fivehour') || id.split('-').includes('5h')) return 300
  if (compact.includes('weekly') || id.split('-').some((token) => token === 'week' || token === '7d')) return 10080
  return 0
}

function resetTime(value, sampledAt) {
  if (value === undefined) return {}
  const at = new Date(value)
  if (!Number.isFinite(at.getTime())) return {}
  if (at <= sampledAt) return null
  return { resets_at: at.toISOString() }
}

/** Map the native RetrieveUserQuotaSummary response, never an account credential or status-line email. */
export function usageAccountFromQuota(result, sampledAt) {
  if (!result || typeof result.response !== 'object' || result.response === null || Array.isArray(result.response) ||
      !(sampledAt instanceof Date) || !Number.isFinite(sampledAt.getTime())) {
    throw new TypeError('quota needs a native response and a valid sample time')
  }
  const summary = result.response
  if ((summary.buckets !== undefined && !Array.isArray(summary.buckets)) ||
      (summary.groups !== undefined && !Array.isArray(summary.groups))) {
    throw new TypeError('quota bucket collections must be arrays')
  }
  const entries = []
  for (const bucket of summary.buckets || []) entries.push({ bucket, group: '' })
  for (const group of summary.groups || []) {
    if (group?.buckets !== undefined && !Array.isArray(group.buckets)) {
      throw new TypeError('quota group buckets must be an array')
    }
    const name = typeof group?.displayName === 'string' ? group.displayName : ''
    for (const bucket of group?.buckets || []) entries.push({ bucket, group: name })
  }
  const windows = new Map()
  for (const { bucket, group } of entries) {
    if (!bucket || typeof bucket !== 'object' || bucket.disabled === true ||
        typeof bucket.remainingFraction !== 'number' || !Number.isFinite(bucket.remainingFraction)) continue
    const minutes = quotaWindowMinutes(bucket)
    if (!minutes) continue
    const reset = resetTime(bucket.resetTime, sampledAt)
    if (reset === null) continue
    const model = typeof bucket.modelId === 'string' && bucket.modelId ? bucket.modelId : group
    const key = `${minutes}|${model}`
    if (windows.has(key)) continue
    windows.set(key, {
      label: windowLabel(minutes), window_minutes: minutes,
      used_percent: Math.min(100, Math.max(0, (1 - bucket.remainingFraction) * 100)),
      ...reset, ...(model ? { model } : {}),
    })
  }
  return {
    provider: 'agy', name: 'Antigravity', sampled_at: sampledAt.toISOString(),
    windows: [...windows.values()].sort((a, b) => a.window_minutes - b.window_minutes),
  }
}
