// External metadata is untrusted even when a Qual Adapter has accepted its source.
export function safeThreadURL(raw: string | null | undefined): string | null {
  if (!raw || raw.length > 2048) return null
  try {
    const url = new URL(raw)
    if (url.protocol !== 'https:' || url.username || url.password || url.search || url.hash || (url.port && url.port !== '443')) return null
    const host = url.hostname.toLowerCase()
    const labels = host.split('.')
    if (labels.length < 2 || labels.some((label) => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label))) return null
    if (labels.some((label) => label.length > 63) || /^\d+$/.test(labels[labels.length - 1])) return null
    if (['local', 'localhost', 'internal', 'test', 'invalid'].includes(labels[labels.length - 1])) return null
    return url.href
  } catch {
    return null
  }
}

// Source clock is evidence, not a trustworthy ordering key.
export function externalEventTimeHint(event: { occurred_at: string; received_at: string }): string | null {
  const occurred = Date.parse(event.occurred_at)
  const received = Date.parse(event.received_at)
  if (!Number.isFinite(occurred) || !Number.isFinite(received)) return '来源时间不可验证'
  if (occurred > received + 5 * 60_000) return '来源时间晚于接收时间，可能存在时钟偏差'
  if (received - occurred > 24 * 60 * 60_000) return '来源时间与接收时间相差超过一天，可能延迟或乱序'
  return null
}
