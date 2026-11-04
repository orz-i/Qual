import type { components } from '@qual/sdk-ts'

type Run = Pick<components['schemas']['AgentRun'], 'source_status' | 'observability'>

const statusText: Record<Run['source_status'], string> = {
  queued: '排队中（外部声明）',
  running: '执行中（外部声明）',
  blocked: '阻塞（外部声明）',
  succeeded: '完成（外部声明）',
  failed: '失败（外部声明）',
  cancelled: '取消（外部声明）',
}

export function externalRunPresentation(run: Run): { status: string; observation: string; note: string } {
  const status = statusText[run.source_status]
  switch (run.observability) {
    case 'stale':
      return { status, observation: '失联 / Stale', note: '最近上报已过期，无法确认外部进程是否仍在运行；不等于执行失败。' }
    case 'unknown':
      return { status, observation: '未知 / Unknown', note: '无可信的最近观测，不能判断外部执行状态。' }
    case 'terminal':
      return { status, observation: '终态（仅外部报告）', note: '终态只是来源声明，不代表 WorkItem Done、审核通过或 TestAttempt Passed。' }
    case 'current':
      return { status, observation: '最近有上报', note: '此状态来自外部 Host 的报告，不代表工作项或测试结果。' }
  }
}

export function externalReportPresentation(report: {
  kind: 'status' | 'heartbeat'
  status?: Run['source_status']
  applied: boolean
}): string {
  if (report.kind === 'heartbeat') return '心跳（外部观测）'
  const status = report.status ? statusText[report.status] : '状态未提供'
  return status + (report.applied ? ' · 已纳入运行投影' : ' · 仅留存历史（未更新运行投影）')
}
