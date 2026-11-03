import type { TestRunItem } from './api'

export type QualityStatus = NonNullable<TestRunItem['latest_status']> | 'unreported'
export type QualityFilter = QualityStatus | 'all'

export const qualityLabels: Record<QualityStatus, string> = {
  passed: '通过',
  failed: '失败',
  blocked: '阻塞',
  skipped: '跳过',
  unreported: '未上报',
}

export const qualityOrder: QualityStatus[] = ['failed','blocked','unreported','passed','skipped']

export function itemStatus(item: Pick<TestRunItem, 'latest_status'>): QualityStatus {
  return item.latest_status ?? 'unreported'
}

export function qualityCounts(items: readonly Pick<TestRunItem,'latest_status'>[]) {
  const counts: Record<QualityStatus,number> = {
    passed:0, failed:0, blocked:0, skipped:0, unreported:0,
  }
  for (const item of items) {
    counts[itemStatus(item)] += 1
  }
  return {
    ...counts,
    total: items.length,
    reported: items.length - counts.unreported,
  }
}

export function filterRunItems<T extends Pick<TestRunItem,'latest_status'>>(
  items: readonly T[], filter: QualityFilter,
): T[] {
  return filter === 'all' ? [...items] : items.filter(item => itemStatus(item) === filter)
}

export function sortByLatestAttention<T extends Pick<TestRunItem,'latest_status'>>(items: readonly T[]): T[] {
  return items.map((item,index) => ({item,index}))
    .sort((a,b) => qualityOrder.indexOf(itemStatus(a.item))-qualityOrder.indexOf(itemStatus(b.item)) || a.index-b.index)
    .map(({item}) => item)
}
