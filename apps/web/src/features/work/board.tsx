import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router'
import {
  ArrowRight, Bug, Check, ClipboardCheck, FolderKanban, LayoutGrid,
  List, Plus, RefreshCw, Search, SlidersHorizontal, X,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useSession } from '@/features/auth/session'
import { useWorkActions, useWorkItems } from '@/features/work/api'
import { useMyAccess } from '@/features/access/api'
import { principalLabel, useProjectPrincipals, type ProjectPrincipalSummary } from '@/features/work/principals'
import { filterLoadedWorkItems, workDetailHref } from '@/features/work/navigation'
import type { WorkItem, WorkItemKind } from '@/lib/qual-api'
import { states, statusLabel, priorityLabel } from '@/features/work/labels'
import { cn } from '@/lib/utils'

const statusTone: Record<string, string> = {
  backlog: 'bg-muted text-muted-foreground',
  todo: 'bg-slate-100 text-slate-700 dark:bg-slate-800 dark:text-slate-200',
  in_progress: 'bg-blue-50 text-blue-700 dark:bg-blue-950 dark:text-blue-200',
  in_review: 'bg-amber-50 text-amber-800 dark:bg-amber-950 dark:text-amber-200',
  done: 'bg-emerald-50 text-emerald-800 dark:bg-emerald-950 dark:text-emerald-200',
  cancelled: 'bg-muted text-muted-foreground',
}

// Put actionable work first on wide boards; still show Backlog and Cancelled.
const boardStatusOrder = ['todo', 'in_progress', 'in_review', 'done', 'backlog', 'cancelled']

const priorityTone: Record<string, string> = {
  urgent: 'text-rose-700 dark:text-rose-300',
  high: 'text-orange-700 dark:text-orange-300',
  medium: 'text-muted-foreground',
  low: 'text-muted-foreground',
}

function ErrorMessage({ message }: { message: string }) {
  return <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{message}</p>
}

function StatusPill({ status }: { status: string }) {
  return <span className={cn('inline-flex min-h-6 items-center rounded-md px-2 py-0.5 text-[11px] font-medium', statusTone[status] ?? 'bg-muted text-muted-foreground')}>{statusLabel(status)}</span>
}

function KindLabel({ kind }: { kind: WorkItemKind }) {
  return <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
    {kind === 'bug' ? <Bug aria-hidden="true" className="size-3.5 text-rose-600" /> : <ClipboardCheck aria-hidden="true" className="size-3.5 text-primary/70" />}
    {kind === 'bug' ? '缺陷' : '任务'}
  </span>
}

export function ProjectsPage() {
  const { data, isPending, isError, error } = useSession()
  return (
    <section className="flex flex-col gap-7">
      <header>
        <p className="text-xs font-semibold tracking-wider text-primary">WORKSPACE</p>
        <h1 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">项目</h1>
        <p className="mt-2 text-sm leading-6 text-muted-foreground">选择一个已授权的项目，查看人类与 Agent 共同记录的真实工作。</p>
      </header>
      {isPending && <p role="status" className="text-sm text-muted-foreground">正在读取项目…</p>}
      {isError && <ErrorMessage message={error.message} />}
      {data?.projects.length === 0 && (
        <Card>
          <CardHeader>
            <CardTitle>没有可访问的项目</CardTitle>
            <CardDescription>当前身份尚未获得项目访问权限，请联系 Workspace 管理员。</CardDescription>
          </CardHeader>
        </Card>
      )}
      {Boolean(data?.projects.length) && (
        <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {data?.projects.map((project) => (
            <Link key={project.id} to={`/projects/${project.id}`}
              className="group flex min-h-44 flex-col justify-between rounded-2xl border border-border bg-card p-5 transition-colors hover:border-primary/35 hover:bg-accent/25 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
              <div className="flex items-start justify-between">
                <span className="flex size-11 items-center justify-center rounded-xl bg-secondary text-primary"><FolderKanban aria-hidden="true" className="size-5" /></span>
                <ArrowRight aria-hidden="true" className="size-4 text-muted-foreground transition-transform group-hover:translate-x-1" />
              </div>
              <div>
                <h2 className="text-lg font-semibold">{project.key}</h2>
                <p className="mt-1 text-xs text-muted-foreground">进入工作项、测试与项目活动</p>
              </div>
            </Link>
          ))}
        </div>
      )}
    </section>
  )
}

function WorkCard({ item, projectID, filters, principal }: { item: WorkItem; projectID: string; filters: URLSearchParams; principal?: ProjectPrincipalSummary }) {
  return (
    <Link to={workDetailHref(projectID, item.id, filters)}
      className="group block rounded-xl border border-border bg-card px-3.5 py-3 shadow-[0_1px_2px_rgba(35,30,70,0.025)] transition-colors hover:border-primary/40 hover:bg-accent/30 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
      <div className="flex items-center justify-between gap-2">
        <span className="font-mono text-xs font-semibold text-primary">{item.key}</span>
        <span className={cn('text-xs font-medium', priorityTone[item.priority] ?? 'text-muted-foreground')}>{priorityLabel[item.priority] ?? item.priority}</span>
      </div>
      <h3 className="mt-2 line-clamp-3 break-words text-sm font-semibold leading-5 group-hover:text-primary">{item.title}</h3>
      <div className="mt-4 flex flex-wrap items-center gap-x-2 gap-y-1.5 border-t border-border/60 pt-3">
        <KindLabel kind={item.kind} />
        <span className="ml-auto min-w-0 max-w-[175px] truncate text-right text-[11px] text-muted-foreground" title={item.assignee_id ?? undefined} aria-label={item.assignee_id ? `负责人 ${principalLabel(item.assignee_id, principal)}，主体 UUID ${item.assignee_id}` : '负责人未分配'}>{principalLabel(item.assignee_id, principal, true)}</span>
      </div>
    </Link>
  )
}

function CreateWorkDialog({ projectID }: { projectID: string }) {
  const [open, setOpen] = useState(false)
  const [kind, setKind] = useState<WorkItemKind>('task')
  const [priority, setPriority] = useState<'urgent' | 'high' | 'medium' | 'low'>('medium')
  const [severity, setSeverity] = useState<'critical' | 'major' | 'minor'>('major')
  const [title, setTitle] = useState('')
  const [description, setDescription] = useState('')
  const actions = useWorkActions(projectID)
  const navigate = useNavigate()
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    try {
      const item = await actions.create.mutateAsync({
        kind, title: title.trim(), description, priority,
        ...(kind === 'bug' ? { bug_details: { severity } } : {}),
      })
      setOpen(false)
      setTitle('')
      setDescription('')
      navigate(`/projects/${projectID}/items/${item.id}`)
    } catch {
      // The actionable mutation error is displayed in the dialog.
    }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button><Plus data-icon="inline-start" aria-hidden="true" /> 新建工作项</Button>
      </DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>创建任务或缺陷</DialogTitle>
          <DialogDescription>记录到 Qual 并保留操作审计，不会启动任何 Agent。</DialogDescription>
        </DialogHeader>
        <form onSubmit={(event) => { void submit(event) }}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="work-kind">类型</FieldLabel>
              <Select value={kind} onValueChange={(value) => setKind(value as WorkItemKind)}>
                <SelectTrigger id="work-kind" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent><SelectGroup>
                  <SelectItem value="task">任务 Task</SelectItem>
                  <SelectItem value="bug">缺陷 Bug</SelectItem>
                </SelectGroup></SelectContent>
              </Select>
            </Field>
            <Field>
              <FieldLabel htmlFor="work-title">标题</FieldLabel>
              <Input id="work-title" value={title} maxLength={240} required
                placeholder="明确描述需要完成或修复的工作" onChange={(event) => setTitle(event.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="work-description">描述（可选）</FieldLabel>
              <Textarea id="work-description" rows={4} maxLength={20000}
                value={description} onChange={(event) => setDescription(event.target.value)} />
            </Field>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <Field>
                <FieldLabel htmlFor="work-priority">优先级</FieldLabel>
                <Select value={priority} onValueChange={(value) => setPriority(value as typeof priority)}>
                  <SelectTrigger id="work-priority" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectGroup>
                    <SelectItem value="low">低</SelectItem>
                    <SelectItem value="medium">中</SelectItem>
                    <SelectItem value="high">高</SelectItem>
                    <SelectItem value="urgent">紧急</SelectItem>
                  </SelectGroup></SelectContent>
                </Select>
              </Field>
              {kind === 'bug' && (
                <Field>
                  <FieldLabel htmlFor="bug-severity">缺陷严重程度</FieldLabel>
                  <Select value={severity} onValueChange={value => setSeverity(value as typeof severity)}>
                    <SelectTrigger id="bug-severity" className="w-full"><SelectValue /></SelectTrigger>
                    <SelectContent><SelectGroup>
                      <SelectItem value="minor">一般</SelectItem>
                      <SelectItem value="major">主要</SelectItem>
                      <SelectItem value="critical">严重</SelectItem>
                    </SelectGroup></SelectContent>
                  </Select>
                </Field>
              )}
            </div>
            {actions.create.isError && <ErrorMessage message={actions.create.error.message} />}
            <DialogFooter>
              <Button type="submit" disabled={!title.trim() || actions.create.isPending}>
                {actions.create.isPending ? '正在创建…' : '创建工作项'}
              </Button>
            </DialogFooter>
          </FieldGroup>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function WorkBoardPage() {
  const { projectID = '' } = useParams()
  const [params, setParams] = useSearchParams()
  const { data: session } = useSession()
  const access = useMyAccess(projectID)
  const canCreate = access.data?.effective_permissions.includes('work:write') ?? false
  const project = session?.projects.find((p) => p.id === projectID)
  const kind = params.get('kind') || ''
  const status = params.get('status') || ''
  const query = params.get('q') || ''
  const view = params.get('view') === 'board' ? 'board' : 'list'
  const works = useWorkItems(projectID, status, kind)
  const loadedItems = works.data?.pages.flatMap((page) => page.items) ?? []
  const items = filterLoadedWorkItems(loadedItems, query)
  const principals = useProjectPrincipals(projectID, loadedItems.map((item) => item.assignee_id))
  const hasFilter = Boolean(kind || status || query.trim())

  const updateFilter = (name: string, value: string) => {
    setParams((current) => {
      const next = new URLSearchParams(current)
      if (value) next.set(name, value)
      else next.delete(name)
      return next
    }, { replace: true })
  }
  const clearFilters = () => {
    setParams((current) => {
      const next = new URLSearchParams(current)
      next.delete('q')
      next.delete('kind')
      next.delete('status')
      return next
    }, { replace: true })
  }

  return (
    <section className="flex min-w-0 flex-col gap-6">
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <nav aria-label="面包屑导航" className="flex items-center gap-2 text-xs text-muted-foreground">
            <Link to="/projects" className="hover:text-primary hover:underline">项目</Link>
            <span aria-hidden="true">/</span>
            <span className="truncate">{project?.key ?? '当前项目'}</span>
          </nav>
          <p className="mt-5 text-xs font-semibold tracking-wider text-primary">WORK ITEMS</p>
          <h1 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">工作项</h1>
          <p className="mt-2 text-sm leading-6 text-muted-foreground">统一管理任务与缺陷，保留 Human 和 Agent 的真实工作记录。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button asChild size="sm" variant="outline">
            <Link to={`/projects/${projectID}/tests/cases`}><ClipboardCheck data-icon="inline-start" aria-hidden="true" /> 测试用例</Link>
          </Button>
          {canCreate ? <CreateWorkDialog projectID={projectID} /> : <span className="self-center text-xs text-muted-foreground" role={access.isError ? 'alert' : 'status'}>{access.isPending ? '正在检查创建权限…' : access.isError ? '无法确认创建权限，已隐藏新建入口；请刷新或联系管理员。' : '只读模式 · 新建需要 work:write 权限'}</span>}
        </div>
      </header>

      <div className="overflow-hidden rounded-2xl border border-border bg-card">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-4 py-4 sm:px-5">
          <div className="flex items-center gap-2">
            <span className="text-sm font-semibold">项目工作项</span>
            {works.isSuccess && <span className="rounded-lg bg-secondary px-2 py-0.5 text-xs font-semibold tabular-nums text-secondary-foreground">{loadedItems.length} 已加载</span>}
          </div>
          <div role="group" aria-label="切换工作项视图" className="flex items-center gap-1 rounded-xl bg-muted/70 p-1">
            <Button type="button" variant={view === 'list' ? 'secondary' : 'ghost'} size="sm"
              className={cn('h-8 px-3 text-xs', view === 'list' && 'bg-card shadow-sm')}
              aria-pressed={view === 'list'} onClick={() => updateFilter('view', 'list')}>
              <List aria-hidden="true" className="size-4" /> 列表
            </Button>
            <Button type="button" variant={view === 'board' ? 'secondary' : 'ghost'} size="sm"
              className={cn('h-8 px-3 text-xs', view === 'board' && 'bg-card shadow-sm')}
              aria-pressed={view === 'board'} onClick={() => updateFilter('view', 'board')}>
              <LayoutGrid aria-hidden="true" className="size-4" /> 看板
            </Button>
          </div>
        </div>
        <div className="flex flex-wrap items-center gap-2 border-b border-border px-4 py-3 sm:px-5">
          <div className="relative w-full min-w-0 flex-none sm:min-w-[230px] sm:flex-1">
            <Search aria-hidden="true" className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input aria-label="搜索已加载工作项的标题或编号" type="search"
              placeholder="搜索已加载的编号或标题" autoComplete="off" maxLength={120} value={query}
              className="h-9 bg-background pl-9 pr-9 text-sm"
              onChange={(event) => updateFilter('q', event.target.value)} />
            {query && (
              <Button type="button" variant="ghost" size="icon-xs"
                aria-label="清除搜索" className="absolute right-2 top-1/2 -translate-y-1/2"
                onClick={() => updateFilter('q', '')}><X aria-hidden="true" /></Button>
            )}
          </div>
          <Select value={kind || 'all'} onValueChange={value => updateFilter('kind', value === 'all' ? '' : value)}>
            <SelectTrigger aria-label="按类型筛选工作项" className="h-9 min-w-29 bg-background"><SelectValue placeholder="全部类型" /></SelectTrigger>
            <SelectContent><SelectGroup>
              <SelectItem value="all">全部类型</SelectItem>
              <SelectItem value="task">任务</SelectItem>
              <SelectItem value="bug">缺陷</SelectItem>
            </SelectGroup></SelectContent>
          </Select>
          <Select value={status || 'all'} onValueChange={value => updateFilter('status', value === 'all' ? '' : value)}>
            <SelectTrigger aria-label="按状态筛选工作项" className="h-9 min-w-29 bg-background"><SelectValue placeholder="全部状态" /></SelectTrigger>
            <SelectContent><SelectGroup>
              <SelectItem value="all">全部状态</SelectItem>
              {states.map((state) => <SelectItem key={state.id} value={state.id}>{state.label}</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
          {hasFilter && (
            <Button type="button" variant="ghost" size="sm" onClick={clearFilters} className="text-xs">
              <SlidersHorizontal aria-hidden="true" className="size-3.5" /> 清除筛选
            </Button>
          )}
          <Button type="button" variant="ghost" size="icon-sm" disabled={works.isFetching}
            onClick={() => void works.refetch()} aria-label="刷新工作项" title="刷新工作项">
            <RefreshCw aria-hidden="true" className={cn('size-4', works.isFetching && 'animate-spin')} />
          </Button>
        </div>

        <div role="status" aria-live="polite" className="flex flex-wrap items-center justify-between gap-2 border-b border-border/70 bg-muted/25 px-4 py-2.5 text-xs text-muted-foreground sm:px-5">
          <span>{works.isPending ? '正在读取工作项…' : works.isSuccess
            ? `已加载 ${loadedItems.length} 项 · 当前匹配 ${items.length} 项`
            : '无法获取工作项'}</span>
          <span>编号与标题搜索仅限已加载数据；状态与类型在服务端过滤</span>
          {principals.isError && <span role="alert">成员身份查询失败；可打开详情核验完整主体 UUID。</span>}
        </div>

        {works.isPending && <p role="status" className="px-5 py-10 text-sm text-muted-foreground">正在读取工作项…</p>}
        {works.isError && <div className="p-5"><ErrorMessage message={works.error.message} /></div>}

        {works.isSuccess && items.length === 0 && (
          <div className="flex flex-col items-center gap-3 px-5 py-14 text-center">
            <span className="flex size-11 items-center justify-center rounded-xl bg-secondary text-primary">
              {hasFilter ? <Search aria-hidden="true" className="size-5" /> : <ClipboardCheck aria-hidden="true" className="size-5" />}
            </span>
            <h2 className="text-sm font-semibold">{hasFilter ? '已加载数据中没有匹配项' : '当前没有工作项'}</h2>
            <p className="max-w-md text-xs leading-6 text-muted-foreground">
              {works.hasNextPage
                ? '还有未加载的工作项。可以继续加载下一页后重新搜索，或调整筛选条件。'
                : hasFilter ? '请调整搜索词或筛选条件，再查看真实工作项。' : canCreate ? '可以新建一个真实任务或缺陷。' : '尚无工作项，当前身份只有阅读权限。'}
            </p>
            {hasFilter && <Button variant="outline" size="sm" onClick={clearFilters}>清除筛选</Button>}
          </div>
        )}

        {works.isSuccess && items.length > 0 && view === 'list' && (
          <div role="list" aria-label="工作项列表" className="divide-y divide-border/70">
            <div aria-hidden="true" className="hidden grid-cols-[92px_minmax(0,1fr)_76px_100px_60px_170px] gap-3 bg-muted/15 px-5 py-2.5 text-[11px] font-medium text-muted-foreground lg:grid">
              <span>编号</span><span>标题</span><span>类型</span><span>状态</span><span>优先级</span><span>负责人</span>
            </div>
            {items.map((item) => (
              <div role="listitem" key={item.id}>
                <Link to={workDetailHref(projectID, item.id, params)}
                  className="group grid min-h-[70px] grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 px-4 py-3 transition-colors hover:bg-accent/35 focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring sm:px-5 lg:grid-cols-[92px_minmax(0,1fr)_76px_100px_60px_170px]">
                  <span className="hidden font-mono text-xs font-semibold text-primary lg:block">{item.key}</span>
                  <div className="min-w-0">
                    <p className="mb-1 font-mono text-xs font-semibold text-primary lg:hidden">{item.key}</p>
                    <p className="line-clamp-2 break-words text-sm font-semibold leading-5 group-hover:text-primary">{item.title}</p>
                    <p className="mt-1 flex items-center gap-2 text-xs text-muted-foreground lg:hidden">
                      <KindLabel kind={item.kind} /> · {priorityLabel[item.priority] ?? item.priority} · <span title={item.assignee_id ?? undefined}>{principalLabel(item.assignee_id, principals.byID.get(item.assignee_id ?? ''), true)}</span>
                    </p>
                  </div>
                  <span className="hidden lg:inline"><KindLabel kind={item.kind} /></span>
                  <span className="justify-self-end lg:justify-self-start"><StatusPill status={item.status} /></span>
                  <span className={cn('hidden text-xs font-medium lg:block', priorityTone[item.priority] ?? 'text-muted-foreground')}>
                    {priorityLabel[item.priority] ?? item.priority}
                  </span>
                  <span className="hidden truncate text-xs text-muted-foreground lg:block" title={item.assignee_id ?? undefined} aria-label={item.assignee_id ? `负责人 ${principalLabel(item.assignee_id, principals.byID.get(item.assignee_id))}，主体 UUID ${item.assignee_id}` : '负责人未分配'}>{principalLabel(item.assignee_id, principals.byID.get(item.assignee_id ?? ''), true)}</span>
                </Link>
              </div>
            ))}
          </div>
        )}

        {works.isSuccess && items.length > 0 && view === 'board' && (
          <div role="region" aria-label="工作看板（可水平滚动）" tabIndex={0}
            className="overflow-x-auto overscroll-x-contain bg-muted/25 px-4 py-4 focus-visible:outline-2 focus-visible:-outline-offset-2 sm:px-5">
            <div className="flex w-max items-start gap-3">
              {states.filter((state) => !status || state.id === status)
                .sort((a, b) => boardStatusOrder.indexOf(a.id) - boardStatusOrder.indexOf(b.id))
                .map((state) => {
                const column = items.filter((item) => item.status === state.id)
                return (
                  <section key={state.id} aria-label={`${state.label}，当前匹配 ${column.length} 项`}
                    className="w-[258px] shrink-0 rounded-2xl border border-border/70 bg-muted/40 p-3 sm:w-[286px]">
                    <header className="mb-3 flex items-center justify-between gap-2 px-1">
                      <div className="flex items-center gap-2">
                        <span aria-hidden="true" className={cn('size-2 rounded-full', {
                          'bg-slate-400': state.id === 'backlog' || state.id === 'todo',
                          'bg-blue-500': state.id === 'in_progress',
                          'bg-amber-500': state.id === 'in_review',
                          'bg-emerald-500': state.id === 'done',
                          'bg-zinc-400': state.id === 'cancelled',
                        })} />
                        <h2 className="text-sm font-semibold">{state.label}</h2>
                      </div>
                      <span className="rounded-md bg-card px-2 py-0.5 text-xs font-semibold tabular-nums text-muted-foreground">{column.length}</span>
                    </header>
                    <div className="flex flex-col gap-2.5">
                      {column.map((item) => <WorkCard key={item.id} item={item} projectID={projectID} filters={params} principal={principals.byID.get(item.assignee_id ?? '')} />)}
                      {column.length === 0 && <p className="rounded-xl border border-dashed border-border p-5 text-center text-xs text-muted-foreground">当前没有匹配项</p>}
                    </div>
                  </section>
                )
              })}
            </div>
          </div>
        )}

        {works.hasNextPage && (
          <div className="flex justify-center border-t border-border p-4">
            <Button type="button" variant="outline" disabled={works.isFetchingNextPage}
              onClick={() => void works.fetchNextPage()}>
              {works.isFetchingNextPage ? '加载中…' : '加载下一页（扩大本地搜索范围）'}
            </Button>
          </div>
        )}
        <footer className="flex items-center justify-between gap-3 border-t border-border/70 px-4 py-3 text-xs text-muted-foreground sm:px-5">
          <span className="flex items-center gap-1"><Check aria-hidden="true" className="size-3.5 text-emerald-600" />操作由 Qual 记录与审计</span>
          <Link to={`/projects/${projectID}/activity`} className="font-medium text-primary hover:underline">查看项目活动 <ArrowRight aria-hidden="true" className="inline size-3.5" /></Link>
        </footer>
      </div>
    </section>
  )
}
