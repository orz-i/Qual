import test from 'node:test'
import assert from 'node:assert/strict'
import { canGrantIntegrationPermission } from '../src/features/access/permission-rules.ts'

test('integration reporters can receive only explicit telemetry, test and artifact grants', () => {
  for (const permission of ['event:write','run:report','test:read','test:write','artifact:read','artifact:write']) {
    assert.equal(canGrantIntegrationPermission(permission), true, permission)
  }
  for (const permission of ['work:read','work:write','work:claim','work:review','audit:read','archive:export','webhook:manage','access:manage']) {
    assert.equal(canGrantIntegrationPermission(permission), false, permission)
  }
})
