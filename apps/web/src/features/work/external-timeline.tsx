import { useState } from 'react'
import { Link } from 'react-router'
import { ArrowUpRight, GitBranch, History, Link2 } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { useThreadWorkItems, useWorkExternalEvents, useWorkThreads } from '@/features/work/external-timeline-api'
import { externalEventTimeHint, safeThreadURL } from '@/features/work/external-timeline-utils'

function Problem({ message }: { message?: string }) {
  return message ? <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{message}</p> : null
}

function ThreadBacklinks({ projectID, threadID }: { projectID: string; threadID: string }) {
  const query = useThreadWorkItems(projectID, threadID, true)
  const items = query.data?.pages.flatMap((page) => page.items) ?? []
  return (
    <div className="mt-3 rounded-lg border bg-muted/30 p-3">
      <p className="mb-2 text-xs font-medium">此 Thread 在本项目的工作项关联（已加载 {items.length} 条）</p>
      {query.isPending && <p role="status" className="text-xs text-muted-foreground">正在读取关联工作项…</p>}
      {query.isError && <Problem message={query.error.message} />}
      {query.isSuccess && !items.length && <p className="text-xs text-muted-foreground">当前没有可读取的工作项关联。</p>}
      <ul className="flex flex-col gap-2">
        {items.map((item) => (
          <li key={item.link.id}>
            <Link className="inline-flex max-w-full flex-wrap items-center gap-2 rounded text-sm text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
              to={"/projects/" + projectID + "/items/" + item.work_item.id}>
              {item.work_item.key} <span className="text-xs text-muted-foreground">状态：{item.work_item.status}</span>
            </Link>
          </li>
        ))}
      </ul>
      {query.hasNextPage && <Button size="sm" variant="outline" type="button" disabled={query.isFetchingNextPage}
        className="mt-3" onClick={() => void query.fetchNextPage()}>
        {query.isFetchingNextPage ? '读取中…' : '加载更多关联工作项'}
      </Button>}
    </div>
  )
}

// This view only reads persisted facts: it never executes external instructions or infers WorkItem/Test/Review status.
export function WorkExternalTimeline({ projectID, itemID, canRead }: { projectID: string; itemID: string; canRead: boolean }) {
  const [expandedThread, setExpandedThread] = useState<string | null>(null)
  const threads = useWorkThreads(projectID, itemID, canRead)
  const events = useWorkExternalEvents(projectID, itemID, canRead)
  const threadRows = threads.data?.pages.flatMap((page) => page.items) ?? []
  const eventRows = events.data?.pages.flatMap((page) => page.items) ?? []

  return (
    <Card id="work-external-timeline" className="scroll-mt-24">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><GitBranch aria-hidden="true" className="size-4" />外部协作来源</CardTitle>
        <CardDescription>按持久化事实显示 Thread 与 Event。来源文字、外部时间及状态仅是报告，不代表 Qual 审核、测试通过或任务完成。</CardDescription>
      </CardHeader>
      <CardContent className="flex min-w-0 flex-col gap-6">
        {!canRead ? <p className="text-sm text-muted-foreground">尚未确认 work:read 权限，未读取外部协作记录。</p> : (
          <>
            <section aria-label="关联 Thread" className="min-w-0 space-y-3">
              <h3 className="flex items-center gap-2 text-sm font-semibold"><Link2 aria-hidden="true" className="size-4" />关联 Thread</h3>
              {threads.isPending && <p role="status" className="text-sm text-muted-foreground">正在读取来源 Thread…</p>}
              {threads.isError && <Problem message={threads.error.message} />}
              {threads.isSuccess && threadRows.length === 0 && <p className="rounded-lg bg-muted/35 p-4 text-sm text-muted-foreground">当前没有关联 Thread。外部来源未登记并不表示任务无活动。</p>}
              <ul className="space-y-3">
                {threadRows.map((entry) => {
                  const safeHref = safeThreadURL(entry.thread.url)
                  const expanded = expandedThread === entry.thread.id
                  return (
                    <li id={"external-thread-" + entry.thread.id} key={entry.link.id} className="min-w-0 scroll-mt-24 rounded-xl border p-3 sm:p-4">
                      <div className="flex min-w-0 flex-col gap-2 sm:flex-row sm:items-start sm:justify-between">
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-2">
                            <Badge variant="outline">{entry.thread.provider}</Badge>
                            <span className="text-xs text-muted-foreground">{entry.thread.thread_kind}</span>
                          </div>
                          <p className="mt-2 break-all text-sm font-medium">{entry.thread.external_thread_id}</p>
                          <p className="mt-1 text-xs text-muted-foreground">关联于 <time dateTime={entry.link.created_at}>{new Date(entry.link.created_at).toLocaleString()}</time></p>
                        </div>
                        {safeHref && <a href={safeHref} target="_blank" rel="noopener noreferrer" className="inline-flex shrink-0 items-center gap-1 self-start rounded text-xs text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
                          查看外部来源 <ArrowUpRight aria-hidden="true" className="size-3.5" />
                        </a>}
                      </div>
                      {entry.link.source_reference_id && <a className="mt-2 block w-fit rounded text-xs text-primary underline-offset-4 hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                        href={"#external-ref-" + entry.link.source_reference_id}>前往证据区来源引用</a>}
                      <Button variant="ghost" size="sm" type="button" className="mt-2"
                        aria-expanded={expanded} onClick={() => setExpandedThread(expanded ? null : entry.thread.id)}>
                        {expanded ? '收起关联工作项' : '反查此 Thread 的工作项'}
                      </Button>
                      {expanded && <ThreadBacklinks projectID={projectID} threadID={entry.thread.id} />}
                    </li>
                  )
                })}
              </ul>
              {threads.hasNextPage && <Button type="button" size="sm" variant="outline" disabled={threads.isFetchingNextPage}
                onClick={() => void threads.fetchNextPage()}>{threads.isFetchingNextPage ? '读取中…' : '加载更多 Thread'}</Button>}
              {threads.isSuccess && <p className="text-xs text-muted-foreground">已加载 {threadRows.length} 条关联；不代表项目中全部来源。</p>}
            </section>
            <section aria-label="外部 Event 时间线" className="min-w-0 space-y-3">
              <h3 className="flex items-center gap-2 text-sm font-semibold"><History aria-hidden="true" className="size-4" />Event 时间线</h3>
              <p className="text-xs text-muted-foreground">按 Qual 实际接收时间倒序；外部发生时间可能延迟、乱序或存在时钟误差。重复投递由服务端按来源事件 ID 去重。</p>
              {events.isPending && <p role="status" className="text-sm text-muted-foreground">正在读取外部事件…</p>}
              {events.isError && <Problem message={events.error.message} />}
              {events.isSuccess && eventRows.length === 0 && <p className="rounded-lg bg-muted/35 p-4 text-sm text-muted-foreground">暂无外部 Event 记录；不会由此推测任务状态。</p>}
              <ol className="space-y-3">
                {eventRows.map((event) => {
                  const hint = externalEventTimeHint(event)
                  return (
                    <li key={event.id} className="min-w-0 rounded-xl border p-3 sm:p-4">
                      <div className="flex flex-wrap items-center gap-2">
                        <Badge variant="secondary">{event.source}</Badge>
                        <span className="break-all text-xs text-muted-foreground">{event.event_type}</span>
                      </div>
                      <p className="mt-2 whitespace-pre-wrap break-words text-sm font-medium">{event.summary}</p>
                      <dl className="mt-3 grid min-w-0 gap-2 text-xs text-muted-foreground sm:grid-cols-2">
                        <div><dt className="font-medium">来源发生时间</dt><dd><time dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time></dd></div>
                        <div><dt className="font-medium">Qual 接收时间</dt><dd><time dateTime={event.received_at}>{new Date(event.received_at).toLocaleString()}</time></dd></div>
                      </dl>
                      {hint && <p className="mt-2 text-xs text-amber-700 dark:text-amber-300">{hint}</p>}
                      <details className="mt-2 text-xs text-muted-foreground">
                        <summary className="w-fit cursor-pointer rounded py-1 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">查看来源标识与内容哈希</summary>
                        <dl className="mt-1 space-y-1 break-all">
                          <div><dt className="inline font-medium">外部事件 ID：</dt><dd className="inline">{event.external_event_id}</dd></div>
                          <div><dt className="inline font-medium">Thread：</dt><dd className="inline">{event.thread_id}</dd></div>
                          <div><dt className="inline font-medium">SHA-256：</dt><dd className="inline font-mono">{event.payload_sha256}</dd></div>
                        </dl>
                      </details>
                    </li>
                  )
                })}
              </ol>
              {events.hasNextPage && <Button type="button" size="sm" variant="outline" disabled={events.isFetchingNextPage}
                onClick={() => void events.fetchNextPage()}>{events.isFetchingNextPage ? '读取中…' : '加载更多事件'}</Button>}
              {events.isSuccess && <p className="text-xs text-muted-foreground">已加载 {eventRows.length} 条不可变事件记录。</p>}
            </section>
          </>
        )}
      </CardContent>
    </Card>
  )
}
