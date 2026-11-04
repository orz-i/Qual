import { useState } from 'react'
import { Activity, RefreshCw } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useAgentRunReports, useWorkAgentRuns } from '@/features/work/run-observability-api'
import { externalReportPresentation, externalRunPresentation } from '@/features/work/run-observability-utils'
import { readableFactRows } from '@/features/work/read-facts'

function Problem({ message }: { message?: string }) {
  return message ? <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{message}</p> : null
}

function RunReports({ projectID, runID, canReadEvidence }: {
  projectID: string; runID: string; canReadEvidence: boolean
}) {
  const query = useAgentRunReports(projectID, runID)
  const reports = readableFactRows(true, query.status, query.data?.pages)
  return (
    <div className="mt-3 rounded-lg border bg-muted/25 p-3">
      <h4 className="text-sm font-semibold">历史报告（已加载 {reports.length} 条）</h4>
      <p className="mb-3 text-xs text-muted-foreground">按不可变 sequence 倒序。乱序状态报告可能只归档而不改变当前投影；心跳不等于实际任务完成。</p>
      {query.isPending && <p role="status" className="text-sm text-muted-foreground">正在读取外部运行报告…</p>}
      {query.isError && <Problem message={query.error.message} />}
      {query.isSuccess && !reports.length && <p className="text-xs text-muted-foreground">尚无外部 Run 报告。</p>}
      <ol className="space-y-3">
        {reports.map((report) => (
          <li key={report.id} className="min-w-0 rounded-lg border bg-card p-3 text-sm">
            <p className="break-words font-medium">#{report.sequence} · {externalReportPresentation(report)}</p>
            <dl className="mt-1 flex flex-col gap-1 break-words text-xs text-muted-foreground">
              <div><dt className="inline">来源上报：</dt><dd className="inline"><time dateTime={report.occurred_at}>{new Date(report.occurred_at).toLocaleString()}</time></dd></div>
              <div><dt className="inline">Qual 接收：</dt><dd className="inline"><time dateTime={report.received_at}>{new Date(report.received_at).toLocaleString()}</time></dd></div>
              <div><dt className="inline">报告主体：</dt><dd className="inline break-all">{report.reporter_principal_id}</dd></div>
              {report.error_reason && <div><dt className="inline">外部错误描述：</dt><dd className="inline whitespace-pre-wrap break-words">{report.error_reason}</dd></div>}
            </dl>
            {canReadEvidence && (report.evidence_artifact_id || report.external_reference_id) && (
              <div className="mt-2 flex flex-wrap gap-3 text-xs">
                {report.evidence_artifact_id && <a className="rounded text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  href={"#artifact-" + report.evidence_artifact_id}>查看关联证据（如在本工作项）</a>}
                {report.external_reference_id && <a className="rounded text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                  href={"#external-ref-" + report.external_reference_id}>查看外部引用（如在本工作项）</a>}
              </div>
            )}
          </li>
        ))}
      </ol>
      {query.isSuccess && query.hasNextPage && <Button type="button" size="sm" variant="outline" className="mt-3"
        disabled={query.isFetchingNextPage} onClick={() => void query.fetchNextPage()}>
        {query.isFetchingNextPage ? '读取中…' : '加载更早报告'}
      </Button>}
    </div>
  )
}

export function WorkRunObservability({ projectID, itemID, canRead, canReadEvidence }: {
  projectID: string; itemID: string; canRead: boolean; canReadEvidence: boolean
}) {
  const query = useWorkAgentRuns(projectID, itemID, canRead)
  const [expandedRun, setExpandedRun] = useState<string | null>(null)
  const runs = readableFactRows(canRead, query.status, query.data?.pages)
  return (
    <Card id="work-agent-runs" className="scroll-mt-24">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><Activity aria-hidden="true" className="size-4" />外部 AgentRun 观测</CardTitle>
        <CardDescription>仅显示外部 Host 已上报的事实。Qual 不启动或控制 Agent；Run 的成功/失败不更改 WorkItem、TestAttempt 或 Reviewer 状态。</CardDescription>
      </CardHeader>
      <CardContent className="min-w-0 space-y-3">
        {!canRead ? <p className="text-sm text-muted-foreground">尚未确认 work:read 权限，未读取 Run 数据。</p> : (
          <>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <p className="text-xs text-muted-foreground">状态/失联判断以 Qual 本次读取时的服务端投影为准，不代表当前外部进程实时存活。</p>
              <Button type="button" size="sm" variant="outline" disabled={query.isFetching} onClick={() => void query.refetch()}>
                <RefreshCw aria-hidden="true" className="size-3.5" />刷新观测
              </Button>
            </div>
            {query.isPending && <p role="status" className="text-sm text-muted-foreground">正在读取外部 Run…</p>}
            {query.isError && <Problem message={query.error.message} />}
            {query.isSuccess && !runs.length && <p className="rounded-lg bg-muted/35 p-4 text-sm text-muted-foreground">尚无 AgentRun 报告。这不表示 Agent 没有执行，也不能推测运行结果。</p>}
            <ul className="space-y-3">
              {runs.map((run) => {
                const presentation = externalRunPresentation(run)
                const expanded = expandedRun === run.id
                return (
                  <li key={run.id} className="min-w-0 rounded-xl border p-3 sm:p-4">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <Badge variant="outline">{run.source}</Badge>
                      <span className="break-all text-sm font-medium">{run.external_run_id}</span>
                      <Badge variant="secondary">{presentation.status}</Badge>
                    </div>
                    <p className="mt-2 text-sm font-medium">{presentation.observation}</p>
                    <p className="mt-1 text-xs text-muted-foreground">{presentation.note}</p>
                    <dl className="mt-3 grid min-w-0 gap-2 text-xs text-muted-foreground sm:grid-cols-2">
                      <div><dt className="font-medium">外部 Host</dt><dd className="break-all">{run.host}</dd></div>
                      <div><dt className="font-medium">报告主体</dt><dd className="break-all">{run.reporter_principal_id}</dd></div>
                      <div><dt className="font-medium">上次接收心跳或状态</dt><dd>{run.last_seen_at ? <time dateTime={run.last_seen_at}>{new Date(run.last_seen_at).toLocaleString()}</time> : '未上报（Unknown）'}</dd></div>
                      <div><dt className="font-medium">最近来源状态时间</dt><dd>{run.last_status_occurred_at ? <time dateTime={run.last_status_occurred_at}>{new Date(run.last_status_occurred_at).toLocaleString()}</time> : '无'}</dd></div>
                    </dl>
                    <div className="mt-3 flex flex-wrap items-center gap-3">
                      <Button type="button" variant="outline" size="sm" aria-expanded={expanded}
                        onClick={() => setExpandedRun(expanded ? null : run.id)}>
                        {expanded ? '收起历史报告' : '查看历史报告'}
                      </Button>
                      <a href="#work-external-timeline" className="rounded text-xs text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">查看工作项 Event（非 Run 直接关联）</a>
                      <a href="#work-evidence" className="rounded text-xs text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">前往证据与测试引用</a>
                    </div>
                    {expanded && <RunReports projectID={projectID} runID={run.id} canReadEvidence={canReadEvidence} />}
                  </li>
                )
              })}
            </ul>
            {query.isSuccess && query.hasNextPage && <Button variant="outline" type="button" size="sm" disabled={query.isFetchingNextPage}
              onClick={() => void query.fetchNextPage()}>{query.isFetchingNextPage ? '读取中…' : '加载更多 Run'}</Button>}
            {query.isSuccess && <p className="text-xs text-muted-foreground">已加载 {runs.length} 条 Run，非全量证明；报告来源需独立审查。</p>}
          </>
        )}
      </CardContent>
    </Card>
  )
}
