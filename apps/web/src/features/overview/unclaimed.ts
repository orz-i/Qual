// The workbench is a preview of authorized, already-loaded WorkItems.
// Never treat this result as a server-wide query or a command to assign work.
export function selectLoadedUnclaimed<T extends { assignee_id?: string | null; status: string }>(
  items: readonly T[], limit = 3,
): T[] {
  if (!Number.isSafeInteger(limit) || limit <= 0) return []
  // Fail closed if the API field is missing: undefined is unknown, not unassigned.
  return items.filter(item => item.assignee_id === null &&
    (item.status === 'todo' || item.status === 'backlog')).slice(0, limit)
}
