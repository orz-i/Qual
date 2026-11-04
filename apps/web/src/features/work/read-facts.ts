// React Query can retain previously successful pages after a background
// 403/404/network error. Never render cached protected facts after denial or
// any read failure; the server remains the authority for authorization.
export function readableFactRows<T>(
  canRead: boolean,
  status: 'pending' | 'error' | 'success',
  pages: ReadonlyArray<{ items: ReadonlyArray<T> }> | undefined,
): T[] {
  return canRead && status === 'success' ? (pages?.flatMap(page => page.items) ?? []) : []
}
