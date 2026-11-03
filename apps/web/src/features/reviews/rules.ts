import type { ReviewQueueEntry, ReviewQueueLane } from './api'

// The Go application derives this from immutable Reviewer history and actual
// WorkItem state. Never recalculate authorization or approval from UI status.
export function reviewLane(entry: Pick<ReviewQueueEntry, 'review_lane'>): Exclude<ReviewQueueLane, 'all'> {
  return entry.review_lane
}

export function filterReviewQueue<T extends Pick<ReviewQueueEntry, 'review_lane'>>(
  items: readonly T[], lane: ReviewQueueLane,
): T[] {
  return lane === 'all' ? [...items] : items.filter(item => reviewLane(item) === lane)
}

export function reviewSummary(items: readonly Pick<ReviewQueueEntry, 'review_lane'>[]) {
  return {
    loaded: items.length,
    awaiting: items.filter(item => item.review_lane === 'awaiting').length,
    needsWork: items.filter(item => item.review_lane === 'needs_work').length,
    readyToClose: items.filter(item => item.review_lane === 'ready_to_close').length,
  }
}

export function reviewStatusMismatch(entry: Pick<ReviewQueueEntry, 'status' | 'review_lane'>) {
  return entry.status === 'in_review' && entry.review_lane === 'needs_work'
}

export function reviewerIsIndependent(decision: Pick<NonNullable<ReviewQueueEntry['latest_review']>, 'self_review'> | undefined): boolean | null {
  return decision ? !decision.self_review : null
}
