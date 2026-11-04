import { useState } from 'react'
import { Link } from 'react-router'
import { AlertCircle, ArrowRight, RefreshCw } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useProjectPrincipals, principalLabel } from '@/features/work/principals'
import { statusLabel } from '@/features/work/labels'
import { useAttentionInbox, type AttentionItem } from '@/features/reviews/attention-api'
import {
  attentionLabels, attentionLoadedCounts, attentionNextStep,
  filterAttention, uniqueLoadedAttention, type AttentionLane,
} from '@/features/reviews/attention-rules'

function Entry({ projectID, item, canReview, principals }: {
  projectID: string
  item: AttentionItem
  canReview: boolean
  principals: ReturnType<typeof useProjectPrincipals>['byID']
}) {
  const review = item.source_kind === 'review'
  return (
    <article className="flex min-w-0 flex-col gap-3 rounded-xl border border-border bg-card p-3 sm:p-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className="font-mono text-xs font-semibold text-primary">{item.key}</span>
        <Badge variant={review ? 'secondary' : 'outline'}>{attentionLabels[item.reason]}</Badge>
        {item.hint_only && <span className="text-xs text-muted-foreground">外部线索 · 未核实</span>}
        <span className="ml-auto text-xs text-muted-foreground">{statusLabel(item.status)}</span>
      </div>
      <Link to={"/projects/" + projectID + "/items/" + item.work_item_id}
        className="max-w-3xl break-words rounded text-sm font-semibold leading-6 underline-offset-4 hover:text-primary hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
        {item.title}
      </Link>
      <p className="break-words text-sm text-muted-foreground">{attentionNextStep(item, canReview)}</p>
      <dl className="grid min-w-0 gap-2 text-xs text-muted-foreground sm:grid-cols-2">
        <div><dt className="font-medium">来源</dt><dd className="break-all">{item.source_kind} · {item.source_label}</dd></div>
        <div><dt className="font-medium">负责主体</dt><dd className="break-all">{principalLabel(item.assignee_id, principals.get(item.assignee_id ?? ''))}</dd></div>
        <div><dt className="font-medium">Qual 观察到</dt><dd><time dateTime={item.observed_at}>{new Date(item.observed_at).toLocaleString()}</time></dd></div>
        {item.source_at && <div><dt className="font-medium">外部来源时间（未验证）</dt><dd><time dateTime={item.source_at}>{new Date(item.source_at).toLocaleString()}</time></dd></div>}
      </dl>
      <details className="text-xs text-muted-foreground">
        <summary className="w-fit cursor-pointer rounded focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">查看事实来源 ID</summary>
        <p className="mt-2 break-all font-mono">{item.source_id}</p>
      </details>
      <Button type="button" variant="outline" size="sm" className="self-start" asChild>
        <Link to={"/projects/" + projectID + "/items/" + item.work_item_id + (review ? '#review-evidence-history' : '#work-evidence')}>
          {review ? '核对审核与证据' : '查看事实与证据'} <ArrowRight aria-hidden="true" className="size-4"/>
        </Link>
      </Button>
    </article>
  )
}

export function AttentionInboxPanel({ projectID, canRead, canReview }: {
  projectID: string; canRead: boolean; canReview: boolean
}) {
  const query = useAttentionInbox(projectID, canRead)
  const [lane, setLane] = useState<AttentionLane>('all')
  // A previously cached successful page must not stay visible after an
  // authorization failure. Showing errors is safer than stale private data.
  const loaded = canRead && !query.isError
    ? uniqueLoadedAttention(query.data?.pages.flatMap(p => p.items) ?? [])
    : []
  const principals = useProjectPrincipals(projectID, loaded.map(item => item.assignee_id))
  const visible = filterAttention(loaded, lane)
  const counts = attentionLoadedCounts(loaded)
  const tabs: { value: AttentionLane; title: string }[] = [
    { value: 'all', title: '全部' },
    { value: 'review', title: '待审查' },
    { value: 'needs_work', title: '整改' },
    { value: 'blocked', title: '外部阻塞' },
    { value: 'stale', title: '失联/未知' },
    { value: 'input', title: '需输入' },
  ]
  const pagingLimited = query.data?.pages.some(page => page.paging_limited) ?? false

  return (
    <section className="flex min-w-0 flex-col gap-4" aria-label="Attention Inbox 待处理线索">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <h2 className="flex items-center gap-2 text-lg font-semibold">
            <AlertCircle aria-hidden="true" className="size-5 text-primary" />需要关注
          </h2>
          <p className="mt-1 max-w-3xl text-xs leading-6 text-muted-foreground">
            仅汇总授权范围的真实 Reviewer 决策和外部来源线索，每个工作项默认只显示一次。
            外部 Run/Event 不能自动批准任务或认定测试通过，所有动作仍在既有工作项详情执行。
          </p>
        </div>
        <Button type="button" size="sm" variant="outline" disabled={!canRead || query.isFetching}
          onClick={() => void query.refetch()}>
          <RefreshCw aria-hidden="true" className="size-4" />刷新线索
        </Button>
      </div>
      <div role="group" aria-label="筛选需要关注的线索" className="flex flex-wrap gap-2">
        {tabs.map(tab => (
          <Button key={tab.value} type="button" size="sm" variant={lane===tab.value?'secondary':'ghost'}
            aria-pressed={lane===tab.value} onClick={() => setLane(tab.value)}>
            {tab.title} <span className="ml-1 tabular-nums">{counts[tab.value]}</span>
          </Button>
        ))}
      </div>
      {query.isPending && canRead && <p role="status" className="text-sm text-muted-foreground">正在读取授权的待处理事实…</p>}
      {query.isError && canRead && <p role="alert" className="rounded-xl border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
        读取 Attention Inbox 失败：{query.error.message}。旧服务可能尚未部署此只读端点；可展开下方原有审核队列。
      </p>}
      {query.isSuccess && loaded.length===0 && <p className="rounded-xl bg-muted/35 p-5 text-sm text-muted-foreground">
        当前已读取范围内没有需关注记录；不代表所有 WorkItem、测试和外部执行均已完成。
      </p>}
      {query.isSuccess && loaded.length>0 && visible.length===0 && <p className="rounded-xl border p-5 text-sm text-muted-foreground">
        已加载的 {loaded.length} 项中此分类为空。切换其他筛选或加载下一批查看。
      </p>}
      <div className="grid gap-3">
        {query.isSuccess && visible.map(item => <Entry key={item.work_item_id} projectID={projectID}
          item={item} canReview={canReview} principals={principals.byID}/>)}
      </div>
      {query.hasNextPage && <Button type="button" variant="outline" size="sm"
        className="self-center" disabled={query.isFetchingNextPage}
        onClick={() => void query.fetchNextPage()}>
        {query.isFetchingNextPage ? '读取中…' : '加载下一批关注项'}
      </Button>}
      {pagingLimited && <p className="text-xs text-muted-foreground">
        有界读取已到达前 500 项上限；需要更多历史时请按工作项查询或使用项目归档，不应据此断言无其他事项。
      </p>}
      {query.isSuccess && <p className="text-xs leading-5 text-muted-foreground">
        仅统计已加载 {loaded.length} 项；offset 分页非原子快照，并发更改可能重排，刷新后核对。原始 Audit 仍须单独 audit:read 权限。
      </p>}
    </section>
  )
}
