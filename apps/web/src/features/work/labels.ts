export const states = [
  { id: 'backlog', label: '待规划' },
  { id: 'todo', label: '待处理' },
  { id: 'in_progress', label: '进行中' },
  { id: 'in_review', label: '待审查' },
  { id: 'done', label: '已完成' },
  { id: 'cancelled', label: '已取消' },
] as const

export const statusLabel = (status: string) => states.find((state) => state.id === status)?.label ?? status
export const priorityLabel: Record<string, string> = {
  urgent: '紧急', high: '高', medium: '中', low: '低',
}
