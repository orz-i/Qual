export type TaskGuideKind = 'goal' | 'entry' | 'drive' | 'verify' | 'evidence' | 'recovery' | 'details'

export type TaskGuideSection = {
  id: string
  title: string
  content: string
  kind: TaskGuideKind
}

function classify(label: string): TaskGuideKind {
  const text = label.trim().toLowerCase()
  if (/^(目标|goal|scope|non-goal)/.test(text)) return 'goal'
  if (/^(用户入口|现有入口|到达|路径|entry|how to|get to|precondition)/.test(text)) return 'entry'
  if (/^(驱动|操作|执行|实施|步骤|drive|command|steps|how to use)/.test(text)) return 'drive'
  if (/^(验证|验收|预期|测试|verify|check|expected|acceptance)/.test(text)) return 'verify'
  if (/^(证据|记录|引用|归档|evidence|artifact|references|results)/.test(text)) return 'evidence'
  if (/^(恢复|回滚|失败|重试|风险|注意|边界|权限|gotchas|recovery|retries|failures|safety)/.test(text)) return 'recovery'
  return 'details'
}

/**
 * Read a Task's own plain-text Description as a navigation/verification
 * contract. This is only a presentation projection: preserve every line,
 * never render arbitrary Markdown, execute shell or create a second source.
 * Fence handling prevents shell examples containing # from becoming sections.
 */
export function taskGuideSections(description: string): TaskGuideSection[] {
  if (!description.trim()) return []
  const sections: TaskGuideSection[] = []
  let lines: string[] = []
  let label = '目标与工作说明'
  let kind: TaskGuideKind = 'goal'
  let fence: string | null = null
  const flush = () => {
    const content = lines.join('\n').trim()
    if (content) {
      sections.push({ id: `task-guide-${sections.length}`, title: label, kind, content })
    }
    lines = []
  }
  for (const line of description.split(/\r?\n/)) {
    const fenceMatch = /^\s*(`{3,}|~{3,})/.exec(line)
    if (fenceMatch) {
      if (fence === null) fence = fenceMatch[1]!.charAt(0)
      else if (fence === fenceMatch[1]!.charAt(0)) fence = null
      lines.push(line)
      continue
    }
    if (fence === null) {
      const markdownHeading = /^#{2,4}\s+(.{1,96})$/.exec(line)
      const namedLine = /^(目标|用户入口|现有入口|前置条件|操作|执行|验证|验收|预期|证据|恢复|回滚|失败与重试|风险|权限|Goal|Non-goal|Scope|Entry|Preconditions|Drive|Verify|Evidence|Recovery|Gotchas|Expected|Acceptance Criteria|Results)\s*[:：]\s*(.*)$/i.exec(line)
      if (markdownHeading || namedLine) {
        flush()
        label = (markdownHeading?.[1] ?? namedLine?.[1] ?? '工作说明').trim()
        kind = classify(label)
        if (namedLine?.[2]) lines.push(namedLine[2])
        continue
      }
    }
    lines.push(line)
  }
  flush()
  return sections
}

export function canReadTaskPrivate(
  capabilities: { work_item_id: string; work_version: number; project_mode: string; allowed_actions: string[] } | undefined,
  itemID: string,
  version: number,
  legacyCanRead = false,
): boolean {
  if (!capabilities || capabilities.work_item_id !== itemID || capabilities.work_version !== version) return false
  if (capabilities.project_mode === 'legacy') return legacyCanRead
  // Read permission is a current TASK relationship, not a project-wide
  // work:read or artifact:read grant. Never show cached private data on 403.
  return capabilities.allowed_actions.some((action) =>
    action === 'task.read_test' || action === 'task.read_evidence' || action === 'task.comment')
}

export function cleanedAcceptanceCriteria(criteria: string[]): string[] {
  const cleaned = criteria.map((text) => text.trim()).filter(Boolean)
  if (cleaned.length > 30 || cleaned.some((text) => text.length > 2000)) {
    throw new Error('验收标准最多 30 条，每条最多 2000 字符')
  }
  return cleaned
}
