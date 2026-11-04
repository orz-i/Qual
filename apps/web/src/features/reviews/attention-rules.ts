import type { AttentionItem, AttentionReason } from './attention-api'

export type AttentionLane = 'all' | 'review' | 'needs_work' | 'blocked' | 'stale' | 'input'

export const attentionLabels: Record<AttentionReason, string> = {
  review_needs_work: '审核要求整改',
  review_awaiting: '待审核 / 复核',
  review_ready: '已核验，待显式关闭',
  run_blocked: '外部 Run 报告阻塞',
  run_stale: '外部 Run 失联（Stale）',
  run_unknown: '外部 Run 状态未知',
  event_needs_input: '外部 Event 声明需输入',
}

export function attentionLaneOf(reason: AttentionReason): AttentionLane {
  switch (reason) {
    case 'review_needs_work': return 'needs_work'
    case 'review_awaiting':
    case 'review_ready': return 'review'
    case 'run_blocked': return 'blocked'
    case 'run_stale':
    case 'run_unknown': return 'stale'
    case 'event_needs_input': return 'input'
  }
}

// Offset pages can overlap after concurrent updates; never render the same
// WorkItem twice or claim a project total from the loaded subset.
export function uniqueLoadedAttention(items: AttentionItem[]): AttentionItem[] {
  const seen = new Set<string>()
  return items.filter(item => {
    if (seen.has(item.work_item_id)) return false
    seen.add(item.work_item_id)
    return true
  })
}

export function filterAttention(items: AttentionItem[], lane: AttentionLane) {
  return lane === 'all' ? items : items.filter(item => attentionLaneOf(item.reason) === lane)
}

export function attentionLoadedCounts(items: AttentionItem[]): Record<AttentionLane, number> {
  const counts = { all: items.length, review: 0, needs_work: 0, blocked: 0, stale: 0, input: 0 }
  for (const item of items) counts[attentionLaneOf(item.reason)]++
  return counts
}

export function attentionNextStep(item: AttentionItem, canReview: boolean) {
  switch (item.reason) {
    case 'review_awaiting':
    case 'review_ready':
      return canReview ? '进入详情核验现有 Reviewer/Acceptance 与证据' : '查看详情；当前主体没有审核权限'
    case 'review_needs_work':
      return '查看上次 Reviewer 要求及整改证据'
    case 'run_blocked':
    case 'run_stale':
    case 'run_unknown':
      return '查看工作项与真实外部 Run 历史；不要推断任务失败'
    case 'event_needs_input':
      return '查看来源 Thread/Event，并人工判定是否需要行动'
  }
}
