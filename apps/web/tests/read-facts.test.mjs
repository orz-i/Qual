import test from 'node:test'
import assert from 'node:assert/strict'
import { readableFactRows } from '../src/features/work/read-facts.ts'

test('a previously loaded protected page is not rendered after a 403, 404 or failed refetch', () => {
  const cached = [{ items: [{ id: 'private-event' }] }]
  assert.deepEqual(readableFactRows(true, 'success', cached), [{ id: 'private-event' }])
  assert.deepEqual(readableFactRows(true, 'error', cached), [])
  assert.deepEqual(readableFactRows(false, 'success', cached), [])
  assert.deepEqual(readableFactRows(false, 'error', cached), [])
  assert.deepEqual(readableFactRows(true, 'pending', cached), [])
})

test('successful paged reads keep order while a missing or denied result has no facts', () => {
  const pages = [{ items: ['one'] }, { items: ['two', 'three'] }]
  assert.deepEqual(readableFactRows(true, 'success', pages), ['one', 'two', 'three'])
  assert.deepEqual(readableFactRows(true, 'success', undefined), [])
  assert.deepEqual(readableFactRows(true, 'error', pages), [])
})
