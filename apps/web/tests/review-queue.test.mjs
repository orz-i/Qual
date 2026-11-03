import test from 'node:test'
import assert from 'node:assert/strict'
import { filterReviewQueue, reviewSummary, reviewLane, reviewStatusMismatch, reviewerIsIndependent } from '../src/features/reviews/rules.ts'

const records = [
  { status: 'in_review', review_lane: 'awaiting', resubmitted_after_review: false, latest_review: undefined },
  { status: 'in_progress', review_lane: 'needs_work', resubmitted_after_review: false, latest_review: { outcome: 'needs_work', self_review: false } },
  { status: 'in_review', review_lane: 'needs_work', resubmitted_after_review: false, latest_review: { outcome: 'needs_work', self_review: true } },
  { status: 'in_review', review_lane: 'ready_to_close', resubmitted_after_review: false, latest_review: { outcome: 'passed', self_review: false } },
  { status: 'in_review', review_lane: 'awaiting', resubmitted_after_review: false, latest_review: { outcome: 'passed', self_review: true } },
  { status: 'in_review', review_lane: 'awaiting', resubmitted_after_review: true, latest_review: { outcome: 'needs_work', self_review: false } },
]
test('server-derived lane does not mistakenly treat stale in_review as awaiting', () => {
  assert.equal(reviewLane(records[0]), 'awaiting')
  assert.equal(reviewLane(records[1]), 'needs_work')
  assert.equal(reviewLane(records[2]), 'needs_work')
  assert.equal(reviewLane(records[3]), 'ready_to_close')
  assert.deepEqual(reviewSummary(records), { loaded: 6, awaiting: 3, needsWork: 2, readyToClose: 1 })
  assert.equal(filterReviewQueue(records, 'needs_work').length, 2)
  assert.equal(filterReviewQueue(records, 'ready_to_close').length, 1)
  assert.equal(filterReviewQueue(records, 'all').length, 6)
  assert.equal(filterReviewQueue(records, 'awaiting').length, 3)
  assert.equal(records[5].resubmitted_after_review, true)
  assert.equal(records[5].latest_review.outcome, 'needs_work')
  assert.equal(reviewStatusMismatch(records[5]), false)
})
test('stale state and self-review remain explicit, never auto-mark Done', () => {
  assert.equal(reviewStatusMismatch(records[2]), true)
  assert.equal(reviewStatusMismatch(records[1]), false)
  assert.equal(reviewStatusMismatch(records[3]), false)
  assert.equal(reviewerIsIndependent(records[0].latest_review), null)
  assert.equal(reviewerIsIndependent(records[1].latest_review), true)
  assert.equal(reviewerIsIndependent(records[2].latest_review), false)
  assert.equal(reviewerIsIndependent(records[4].latest_review), false)
  assert.equal(records[3].status, 'in_review')
  assert.equal(records[3].latest_review.outcome, 'passed')
})
