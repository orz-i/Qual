import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useTaskContextFacts } from '@/features/work/api'

const labels: Record<string, string> = {
  passed: 'Passed · 实际报告',
  failed: 'Failed · 失败历史保留',
  blocked: 'Blocked · 未完成验证',
  skipped: 'Skipped · 有依据跳过',
  needs_work: 'Needs work · 待整改',
}

export function TaskEvidenceSummary({ projectID, itemID, version, authorized }: {
  projectID: string; itemID: string; version: number; authorized: boolean
}) {
  const attempts = useTaskContextFacts(projectID, itemID, version, 'attempts', authorized)
  // The server advertises Goose21 history only when the immutable table is
  // actually installed. Keep old Goose17/20 Context v2 fully functional.
  const overview = useTaskContextFacts(projectID, itemID, version, 'overview', authorized)
  const historicalAvailable = overview.isSuccess &&
    overview.data.available_sections?.includes('historical_attributions') === true
  const historical = useTaskContextFacts(projectID, itemID, version, 'historical_attributions', historicalAvailable)
  const artifacts = useTaskContextFacts(projectID, itemID, version, 'artifacts', authorized)
  // Explicit source discrimination: these are persisted TestAttempt/Ready
  // facts, never inferred from an AgentRun or WorkItem Done badge.
  const observedAttempts = attempts.data?.items.filter((fact) => 'source' in fact && fact.source === 'test_attempts') ?? []
  const historicalRuns = historical.data?.items.filter((fact) => 'source' in fact && fact.source === 'historical_test_run_attributions') ?? []
  const ready = artifacts.data?.items.filter((fact) => 'source' in fact && fact.source === 'artifacts') ?? []
  return (
    <Card id="work-task-source" className="scroll-mt-24">
      <CardHeader>
        <CardTitle>真实验证来源与最近历史</CardTitle>
        <CardDescription>只展示本任务当前有权读取的 TestAttempt 和 Ready SHA。结果不可由任务状态或 AgentRun 推断；每个区段最多读取最近 8 条。</CardDescription>
      </CardHeader>
      <CardContent className="min-w-0 space-y-5 text-sm">
        {!authorized ? (
          <p role="status" className="text-muted-foreground">当前仅可阅读公开 Task。私有 TestAttempt、失败历史和附件不可读取，不能把这些信息标为不存在。</p>
        ) : (
          <>
            <section aria-label="最近测试结果" className="space-y-3">
              <h3 className="font-semibold">TestAttempt · 真实执行报告</h3>
              {attempts.isPending && <p role="status" className="text-muted-foreground">正在读取本任务的测试历史…</p>}
              {attempts.isError && <p role="alert" className="text-destructive">测试历史读取失败：{attempts.error.message}。不得据此推断 Passed。</p>}
              {attempts.isSuccess && observedAttempts.length === 0 && <p className="text-muted-foreground">当前读取范围暂无 TestAttempt；这不证明测试已通过或没有更早记录。</p>}
              <ol className="space-y-2">
                {observedAttempts.map((attempt) => (
                  <li key={attempt.source_id} className="min-w-0 space-y-1 rounded-lg border p-3">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant={attempt.status === 'failed' ? 'destructive' : attempt.status === 'passed' ? 'secondary' : 'outline'}>
                        {labels[attempt.status] ?? attempt.status}
                      </Badge>
                      <span className="text-xs text-muted-foreground">Attempt #{attempt.attempt_no}</span>
                    </div>
                    <p className="whitespace-pre-wrap break-words text-xs">{attempt.excerpt || '来源未提供具体观察文本'}</p>
                    <p className="break-all font-mono text-xs text-muted-foreground">Run: {attempt.run_id}</p>
                    <p className="break-all font-mono text-xs text-muted-foreground">Attempt: {attempt.source_id}</p>
                  </li>
                ))}
              </ol>
              {attempts.isSuccess && (attempts.data.has_more || attempts.data.paging_limited) &&
                <p className="text-xs text-muted-foreground">仍有更早的历史或已达到读取上限；当前列表不代表全量验收。</p>}
            </section>
            <section aria-label="历史 TestRun 来源索引" className="space-y-3 border-t pt-4">
              <h3 className="font-semibold">Goose21 · 历史 TestRun 归属来源</h3>
              <p className="text-xs leading-5 text-muted-foreground">仅展示不可变职责/签收引用能唯一归属本任务的历史 Run ID。这不是原 Attempt 结果，也不会赋予旧 Run 的独立读取权限；冲突与无依据历史不会列入。</p>
              {overview.isPending && <p role="status" className="text-muted-foreground">正在核对服务端历史索引可用性…</p>}
              {overview.isError && <p role="alert" className="text-destructive">无法确认历史索引是否可用：{overview.error.message}；不能据此断定不存在历史记录。</p>}
              {overview.isSuccess && !historicalAvailable && <p role="status" className="text-muted-foreground">当前服务端未提供 Goose21 历史来源索引；不代表不存在旧 Run 或旧 Attempt。</p>}
              {historicalAvailable && historical.isPending && <p role="status" className="text-muted-foreground">正在查询历史来源…</p>}
              {historicalAvailable && historical.isError && <p role="alert" className="text-destructive">历史来源索引读取失败：{historical.error.message}；不能推断历史记录不存在。</p>}
              {historicalAvailable && historical.isSuccess && !historicalRuns.length && <p className="text-muted-foreground">本次查询无可信历史归属，不代表不存在旧 Run 或旧 Attempt。</p>}
              <ol className="space-y-2">
                {historicalRuns.map((entry) => (
                  <li key={entry.run_id} className="min-w-0 rounded-lg border p-3 text-xs">
                    <p className="break-all font-medium">历史 Run：{entry.run_id}</p>
                    <p className="mt-1 text-muted-foreground">不可变引用：职责 {entry.role_entry_ids.length} 条，签收 {entry.acceptance_ids.length} 条</p>
                    <p className="mt-1 text-muted-foreground">这是来源索引，未核实原 Run 的 Passed/Failed。</p>
                  </li>
                ))}
              </ol>
              {historicalAvailable && historical.isSuccess && (historical.data.has_more || historical.data.paging_limited) &&
                <p className="text-xs text-muted-foreground">仍有更早的可信归属或查询分页上限；请使用有权的 Context cursor 继续读取。</p>}
            </section>
            <section aria-label="最近可验证附件" className="space-y-3 border-t pt-4">
              <h3 className="font-semibold">Ready Artifact · SHA 来源</h3>
              {artifacts.isPending && <p role="status" className="text-muted-foreground">正在读取本任务附件…</p>}
              {artifacts.isError && <p role="alert" className="text-destructive">附件读取失败：{artifacts.error.message}。不能推断证据已归档。</p>}
              {artifacts.isSuccess && ready.length === 0 && <p className="text-muted-foreground">当前读取范围尚无 Ready Artifact。</p>}
              <ul className="space-y-2">
                {ready.map((artifact) => (
                  <li key={artifact.source_id} className="min-w-0 space-y-1 rounded-lg border p-3">
                    <p className="break-all font-medium">{artifact.filename}</p>
                    <p className="break-all font-mono text-xs text-muted-foreground">SHA-256: {artifact.sha256}</p>
                    <p className="break-all font-mono text-xs text-muted-foreground">Artifact: {artifact.id}</p>
                  </li>
                ))}
              </ul>
              {artifacts.isSuccess && (artifacts.data.has_more || artifacts.data.paging_limited) &&
                <p className="text-xs text-muted-foreground">附件有更早分页；不将已加载条目当作完整归档。</p>}
              <a href="#work-evidence" className="inline-flex min-h-9 items-center text-xs font-medium text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
                前往附件列表和真实来源下载
              </a>
            </section>
          </>
        )}
      </CardContent>
    </Card>
  )
}
