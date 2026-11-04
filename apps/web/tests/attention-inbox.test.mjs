import test from 'node:test'
import assert from 'node:assert/strict'
import {
  attentionLoadedCounts, attentionLabels, attentionNextStep,
  uniqueLoadedAttention, filterAttention, attentionLaneOf,
} from '../src/features/reviews/attention-rules.ts'

function item(id, reason) {
  return { work_item_id: id, reason, source_kind: reason.startsWith('review') ? 'review' : 'agent_run' }
}

test('attention filters only loaded real rows; keeps review and external signals separate', () => {
  const items = [
    item('a','review_needs_work'), item('b','review_awaiting'), item('c','review_ready'),
    item('d','run_blocked'), item('e','run_stale'), item('f','run_unknown'),
    item('g','event_needs_input'),
  ]
  assert.deepEqual(attentionLoadedCounts(items), { all: 7, review: 2, needs_work: 1, blocked: 1, stale: 2, input: 1 })
  assert.equal(filterAttention(items,'review').length, 2)
  assert.equal(filterAttention(items,'needs_work')[0].work_item_id, 'a')
  assert.equal(attentionLaneOf('run_unknown'), 'stale')
  assert.match(attentionLabels.run_stale, /失联/)
})

test('duplicate across offset pages never multiplies the same WorkItem notification', () => {
  const items = [item('a','review_awaiting'), item('a','run_blocked'), item('b','run_blocked')]
  const unique = uniqueLoadedAttention(items)
  assert.deepEqual(unique.map(item => item.work_item_id), ['a','b'])
  assert.deepEqual(attentionLoadedCounts(unique), { all: 2, review: 1, needs_work: 0, blocked: 1, stale: 0, input: 0 })
})

test('viewer has no implicit approval and external telemetry never asserts a real task failure', () => {
  assert.match(attentionNextStep(item('b','review_ready'), false), /没有审核权限/)
  assert.match(attentionNextStep(item('b','review_ready'), true), /核验/)
  assert.match(attentionNextStep(item('d','run_stale'), true), /不要推断任务失败/)
  assert.match(attentionNextStep(item('g','event_needs_input'), true), /人工判定/)
})
