/** The only query keys that may flow from the board to work-item detail. */
const states = new Set(['backlog', 'todo', 'in_progress', 'in_review', 'done', 'cancelled'])
const allowedKeys = ['view', 'q', 'kind', 'status'] as const

export function safeWorkFilters(params: URLSearchParams) {
  const safe = new URLSearchParams()
  for (const name of allowedKeys) {
    const value = params.get(name)
    if (!value) continue
    if (name === 'view' && value !== 'board' && value !== 'list') continue
    if (name === 'kind' && value !== 'task' && value !== 'bug') continue
    if (name === 'status' && !states.has(value)) continue
    if (name === 'q' && value.length > 120) safe.set(name, value.slice(0, 120))
    else safe.set(name, value)
  }
  return safe
}

export function workDetailHref(projectID: string, itemID: string, filters: URLSearchParams) {
  const from = safeWorkFilters(filters).toString()
  if (!from) return `/projects/${projectID}/items/${itemID}`
  const query = new URLSearchParams({ from })
  return `/projects/${projectID}/items/${itemID}?${query.toString()}`
}

export function workReturnHref(projectID: string, detailSearch: string) {
  const from = new URLSearchParams(detailSearch).get('from')
  // A very long externally supplied query is ignored rather than propagated.
  if (!from || from.length > 768) return `/projects/${projectID}`
  const safe = safeWorkFilters(new URLSearchParams(from)).toString()
  return `/projects/${projectID}${safe ? '?' + safe : ''}`
}

export function filterLoadedWorkItems<T extends { key: string, title: string }>(items: readonly T[], query: string): T[] {
  const term = query.trim().toLocaleLowerCase()
  if (!term) return [...items]
  return items.filter((item) => (item.key + ' ' + item.title).toLocaleLowerCase().includes(term))
}
