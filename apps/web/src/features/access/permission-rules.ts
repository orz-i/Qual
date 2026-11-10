import type { PermissionName } from './api'

// Match identity/domain.policy integrationGrantable exactly. A Project Allow
// still requires a matching credential Scope; neither is implicit.
const integrationGrantable: ReadonlySet<PermissionName> = new Set([
  'test:read', 'test:write', 'artifact:read', 'artifact:write',
  'run:report', 'event:write',
])

export function canGrantIntegrationPermission(permission: PermissionName): boolean {
  return integrationGrantable.has(permission)
}
