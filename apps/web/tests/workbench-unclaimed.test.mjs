import test from 'node:test'
import assert from 'node:assert/strict'
import { selectLoadedUnclaimed } from '../src/features/overview/unclaimed.ts'

test('only unassigned backlog/todo work can be suggested for self-claim', () => {
  const rows = [
    { key: 'QUAL-1', assignee_id: null, status: 'todo' },
    { key: 'QUAL-2', assignee_id: 'real-principal', status: 'todo' },
    { key: 'QUAL-3', assignee_id: null, status: 'in_progress' },
    { key: 'QUAL-4', assignee_id: null, status: 'backlog' },
    { key: 'QUAL-5', assignee_id: null, status: 'in_review' },
    { key: 'QUAL-6', assignee_id: null, status: 'done' },
    { key: 'QUAL-7', assignee_id: undefined, status: 'todo' },
  ]
  assert.deepEqual(selectLoadedUnclaimed(rows).map(item => item.key), ['QUAL-1', 'QUAL-4'])
  assert.equal(rows.length, 7)
})

test('preview remains bounded by actually loaded records without inventing a project total', () => {
  const items = Array.from({ length: 6 }, (_, i) => ({ key: `T-${i}`, assignee_id: null, status: 'todo' }))
  assert.deepEqual(selectLoadedUnclaimed(items, 2).map(item => item.key), ['T-0', 'T-1'])
  assert.deepEqual(selectLoadedUnclaimed(items, 0), [])
  assert.deepEqual(selectLoadedUnclaimed(items, -1), [])
  assert.deepEqual(selectLoadedUnclaimed(items, 0.5), [])
  assert.deepEqual(selectLoadedUnclaimed([], 3), [])
})
