import { useQuery } from '@tanstack/react-query'
import {
  Activity, ArrowRight, ChevronRight, ClipboardCheck, ClipboardList,
  FolderKanban, LockKeyhole, RefreshCw, ShieldCheck,
} from 'lucide-react'
import { Link } from 'react-router'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useSession } from '@/features/auth/session'
import { useWorkItems, useProjectActivity } from '@/features/work/api'
import { useMyAccess } from '@/features/access/api'
import { selectLoadedUnclaimed } from '@/features/overview/unclaimed'
import { statusLabel } from '@/features/work/labels'
import { api } from '@/lib/qual-api'

async function fetchHealth({ signal }: { signal: AbortSignal }) {
  const { data, error } = await api.GET('/healthz', { signal })
  if (error || data?.status !== 'ok') throw new Error('Health endpoint unavailable')
  return true
}
async function fetchReadiness({ signal }: { signal: AbortSignal }) {
  const { data, error } = await api.GET('/readyz', { signal })
  if (error || data?.status !== 'ready') throw new Error('Readiness endpoint unavailable')
  return true
}

function ConnectionCheck() {
  const check = useQuery({ queryKey: ['health'], queryFn: fetchHealth, enabled: false, retry: false })
  const readiness = useQuery({ queryKey: ['readiness'], queryFn: fetchReadiness, enabled: false, retry: false })
  const message = check.isFetching || readiness.isFetching ? '正在检查服务与依赖…' :
    !check.isFetched ? '尚未检查连接' :
    check.isError ? 'Go HTTP 服务不可达，请检查本机进程。' :
    readiness.isSuccess ? 'Go 服务正常；PostgreSQL 与已配置的 S3 已就绪。' :
    'Go 进程仍存活，但数据库/迁移/S3 未就绪，请查看本机运维日志。'
  return (
    <div className="flex flex-wrap items-center justify-between gap-4">
      <div>
        <p className="text-sm font-medium">本机服务状态</p>
        <p role="status" aria-live="polite" className="mt-1 text-xs text-muted-foreground">{message}</p>
      </div>
      <Button type="button" variant="outline" size="sm" disabled={check.isFetching || readiness.isFetching}
        onClick={() => { void check.refetch(); void readiness.refetch() }}>
        <RefreshCw aria-hidden="true" className="size-4" />检查连接
      </Button>
    </div>
  )
}

function QuickAction({ to, icon: Icon, title, description }: {
  to: string
  icon: typeof ClipboardList
  title: string
  description: string
}) {
  return (
    <Link to={to}
      className="group flex items-start gap-3 rounded-2xl border border-border bg-card px-4 py-4 transition-colors hover:border-primary/35 hover:bg-accent/30 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
      <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-secondary text-primary">
        <Icon aria-hidden="true" className="size-5" />
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1 text-sm font-semibold">{title}<ChevronRight aria-hidden="true" className="size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" /></span>
        <span className="mt-1 block text-xs leading-5 text-muted-foreground">{description}</span>
      </span>
    </Link>
  )
}

function CurrentProjectWork({ projectID }: { projectID: string }) {
  const query = useWorkItems(projectID, '', '')
  const access = useMyAccess(projectID)
  const items = query.data?.pages[0]?.items ?? []
  const unclaimed = selectLoadedUnclaimed(items)
  const canClaim = access.data?.effective_permissions.includes('work:claim') ?? false
  return (
    <section className="min-w-0 overflow-hidden rounded-2xl border border-border bg-card">
      <header className="flex flex-wrap items-center justify-between gap-3 border-b border-border px-5 py-4">
        <div>
          <h2 className="text-sm font-semibold">当前项目工作项</h2>
          <p className="mt-1 text-xs text-muted-foreground">仅展示已加载的首批工作项，不代表项目全部统计</p>
        </div>
        <Link to={`/projects/${projectID}`} className="text-xs font-semibold text-primary hover:underline">查看全部 <ArrowRight aria-hidden="true" className="inline size-3.5" /></Link>
      </header>
      {query.isPending && <p role="status" className="px-5 py-8 text-sm text-muted-foreground">正在读取工作项…</p>}
      {query.isError && <p role="alert" className="px-5 py-6 text-sm text-destructive">{query.error.message}</p>}
      {query.isSuccess && items.length === 0 && <p className="px-5 py-8 text-sm text-muted-foreground">当前已加载的首批工作项为空。请前往项目查看实际数据和权限。</p>}
      {query.isSuccess && unclaimed.length > 0 && (
        <div className="border-b border-border bg-secondary/30 px-5 py-4" aria-label="待自领取工作项">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="text-sm font-semibold">待自领取 · 已加载首批</h3>
            <span className="text-xs text-muted-foreground">最多展示 {unclaimed.length} 项，不是全项目数量</span>
          </div>
          <p className="mt-1 text-xs text-muted-foreground">
            {access.isPending ? '正在核验领取权限…' : access.isError
              ? '无法核验当前领取权限；请刷新或联系管理员，暂不展示领取操作。'
              : canClaim
                ? '打开工作项详情，可使用当前真实身份自领取；不能直接指派给其他成员。'
                : '当前身份没有 work:claim 权限，仅能查看；请联系有权限的成员。'}
          </p>
          <ul className="mt-3 space-y-1.5">
            {unclaimed.map((item) => (
              <li key={item.id}>
                <Link to={`/projects/${projectID}/items/${item.id}`}
                  className="flex min-w-0 items-center gap-2 rounded-lg border border-border bg-card px-3 py-2 text-xs transition-colors hover:bg-accent/60 focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
                  <span className="shrink-0 font-mono font-semibold text-primary">{item.key}</span>
                  <span className="min-w-0 flex-1 truncate">{item.title}</span>
                  <span className="shrink-0 text-muted-foreground">{statusLabel(item.status)}</span>
                  <ArrowRight aria-hidden="true" className="size-3.5 shrink-0 text-muted-foreground" />
                </Link>
              </li>
            ))}
          </ul>
          {query.hasNextPage && <p className="mt-2 text-xs text-muted-foreground">可能存在尚未加载的待领取工作，请在项目工作项页面继续分页查看。</p>}
        </div>
      )}
      <ul className="divide-y divide-border/70">
        {query.isSuccess && items.slice(0, 6).map((item) => (
          <li key={item.id}>
            <Link
              to={`/projects/${projectID}/items/${item.id}`}
              className="flex min-h-16 items-center gap-3 px-5 py-3 transition-colors hover:bg-muted/50 focus-visible:outline-2 focus-visible:-outline-offset-2"
            >
              <span className="w-20 shrink-0 font-mono text-xs font-medium text-primary">{item.key}</span>
              <span className="min-w-0 flex-1">
                <span className="block truncate text-sm font-medium">{item.title}</span>
                <span className="mt-1 block text-xs text-muted-foreground">{item.kind === 'bug' ? '缺陷' : '任务'} · {item.assignee_id ? '已分配' : '未分配'}</span>
              </span>
              <Badge variant="outline" className="max-w-24 shrink-0 truncate text-[11px]">{statusLabel(item.status)}</Badge>
            </Link>
          </li>
        ))}
      </ul>
      {query.isSuccess && query.hasNextPage && (
        <p className="border-t px-5 py-3 text-xs text-muted-foreground">还有未加载的工作项，请进入项目查看或加载下一页。</p>
      )}
    </section>
  )
}

function CurrentProjectActivity({ projectID }: { projectID: string }) {
  const query = useProjectActivity(projectID)
  return (
    <section className="rounded-2xl border border-border bg-card">
      <header className="flex items-center justify-between gap-3 border-b border-border px-5 py-4">
        <div>
          <h2 className="text-sm font-semibold">最近项目活动</h2>
          <p className="mt-1 text-xs text-muted-foreground">真实记录，不代表 Agent 正在运行</p>
        </div>
        <Link to={`/projects/${projectID}/activity`} className="text-xs font-semibold text-primary hover:underline">全部</Link>
      </header>
      {query.isPending && <p role="status" className="px-5 py-8 text-sm text-muted-foreground">正在读取活动…</p>}
      {query.isError && <p role="alert" className="px-5 py-6 text-sm text-destructive">{query.error.message}</p>}
      {query.isSuccess && query.data.length === 0 && <p className="px-5 py-8 text-sm text-muted-foreground">暂无活动记录</p>}
      <ol className="divide-y divide-border/70">
        {query.data?.slice(0, 5).map((event) => (
          <li key={event.id} className="flex gap-3 px-5 py-3">
            <span aria-hidden="true" className="mt-1 flex size-7 shrink-0 items-center justify-center rounded-lg bg-secondary text-primary">
              <Activity className="size-3.5" />
            </span>
            <div className="min-w-0">
              <p className="break-words text-sm font-medium">{event.operation}</p>
              <time dateTime={event.occurred_at} className="mt-1 block text-xs text-muted-foreground">{new Date(event.occurred_at).toLocaleString()}</time>
            </div>
          </li>
        ))}
      </ol>
    </section>
  )
}

export function HomePage() {
  const { data: session, isPending, isError, error } = useSession()
  const firstProject = session?.projects[0]

  return (
    <div className="flex flex-col gap-7">
      <div className="flex items-center justify-between gap-3">
        <div>
          <p className="text-xs font-semibold tracking-wider text-primary">QUAL WORKSPACE</p>
          <h1 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">工作台</h1>
          <p className="mt-2 text-sm text-muted-foreground">人类与 Agent 共享的任务、测试与质量证据空间。</p>
        </div>
        {session && <Badge variant="secondary" className="hidden sm:inline-flex">已登录 · Human</Badge>}
      </div>

      <section className="relative overflow-hidden rounded-3xl border border-[#e2dfff] bg-[#f2efff] px-5 py-7 text-[#2b255a] sm:px-8 sm:py-8">
        <span className="inline-flex items-center rounded-full bg-white/75 px-3 py-1 text-[11px] font-semibold text-[#5145ab]">
          Human + Agent · Engineering
        </span>
        <h2 className="mt-4 max-w-[650px] text-2xl font-semibold tracking-tight sm:text-[29px] sm:leading-[1.25]">
          把工作进展与质量证据，放在同一条可追溯的链路上。
        </h2>
        <p className="mt-3 max-w-2xl text-sm leading-7 text-[#655f88]">
          管理真实任务与缺陷，关联外部测试结果，查看执行与审核记录。Qual 负责管理和存档，不替代 Agent、测试执行器或 CI。
        </p>
        <div className="mt-6 flex flex-wrap gap-2.5">
          {firstProject ? (
            <>
              <Button asChild><Link to={`/projects/${firstProject.id}`}>打开 {firstProject.key} 工作项 <ArrowRight aria-hidden="true" className="size-4" /></Link></Button>
              <Button asChild variant="outline" className="border-[#d8d3f8] bg-white/80"><Link to={`/projects/${firstProject.id}/tests/runs`}>查看测试运行</Link></Button>
            </>
          ) : session ? (
            <Button asChild variant="outline" className="bg-white/80"><Link to="/projects">查看已授权项目</Link></Button>
          ) : (
            <Button asChild><Link to="/login">登录并进入工作空间 <ArrowRight aria-hidden="true" className="size-4" /></Link></Button>
          )}
        </div>
      </section>

      {isPending && <p role="status" className="text-sm text-muted-foreground">正在读取会话及项目…</p>}
      {isError && <p role="alert" className="text-sm text-destructive">读取浏览器会话失败：{error.message}</p>}
      {!session && !isPending && !isError && (
        <section className="rounded-2xl border border-border bg-card p-6 sm:p-7">
          <div className="flex items-start gap-4">
            <span className="flex size-11 shrink-0 items-center justify-center rounded-xl bg-secondary text-primary"><LockKeyhole aria-hidden="true" className="size-5" /></span>
            <div>
              <h2 className="text-base font-semibold">使用本机 Human 会话访问项目</h2>
              <p className="mt-2 text-sm leading-6 text-muted-foreground">当前尚未登录。请使用本机管理员签发的一次性登录码进入已授权的项目；浏览器不会保存 Agent PAT。</p>
              <Button asChild variant="outline" size="sm" className="mt-4"><Link to="/login">前往登录 <ArrowRight aria-hidden="true" className="size-4" /></Link></Button>
            </div>
          </div>
        </section>
      )}

      {session && !firstProject && (
        <section className="rounded-2xl border border-border bg-card p-6">
          <h2 className="text-base font-semibold">暂时没有可访问的项目</h2>
          <p className="mt-2 text-sm text-muted-foreground">请联系 Workspace 管理员，为当前 Human Principal 授予项目访问权限。</p>
        </section>
      )}

      {firstProject && (
        <>
          <div>
            <div className="mb-3 flex items-center justify-between">
              <h2 className="text-base font-semibold">开始处理</h2>
              {session && <span className="text-xs text-muted-foreground">当前项目 · {firstProject.key}</span>}
            </div>
            <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-4">
              <QuickAction to={`/projects/${firstProject.id}`} icon={FolderKanban} title="工作项" description="查看任务与缺陷，更新真实状态" />
              <QuickAction to={`/projects/${firstProject.id}/reviews`} icon={ShieldCheck} title="协作审核" description="按当前授权查看待复核、需整改的工作" />
              <QuickAction to={`/projects/${firstProject.id}/tests/runs`} icon={ClipboardCheck} title="测试运行" description="检查已登记的外部测试结果" />
              <QuickAction to={`/projects/${firstProject.id}/activity`} icon={Activity} title="项目活动" description="追溯操作、参与者和状态变更" />
            </div>
          </div>
          <div className="grid items-start gap-5 xl:grid-cols-[minmax(0,1.45fr)_minmax(290px,0.75fr)]">
            <CurrentProjectWork projectID={firstProject.id} />
            <CurrentProjectActivity projectID={firstProject.id} />
          </div>
          {session.projects.length > 1 && (
            <section>
              <h2 className="mb-3 text-sm font-semibold">其他已授权项目</h2>
              <div className="grid gap-2 sm:grid-cols-2">
                {session.projects.slice(1).map((project) => (
                  <Link key={project.id} to={`/projects/${project.id}`} className="flex items-center justify-between rounded-xl border bg-card px-4 py-3 text-sm font-medium hover:bg-muted/50">{project.key}<ArrowRight aria-hidden="true" className="size-4 text-primary" /></Link>
                ))}
              </div>
            </section>
          )}
        </>
      )}

      <details className="rounded-xl border border-border bg-card/70 px-4 py-3 text-sm">
        <summary className="cursor-pointer text-xs font-semibold text-muted-foreground focus-visible:outline-2">运维信息 · 本机环境</summary>
        <div className="pt-4"><ConnectionCheck /></div>
      </details>
    </div>
  )
}

export function PlaceholderPage({ title, phase, description }: { title: string; phase: string; description: string }) {
  return (
    <section className="flex max-w-2xl flex-col gap-4">
      <p className="text-sm text-muted-foreground">{phase} · 尚未实现</p>
      <h1 className="text-3xl font-semibold tracking-tight">{title}</h1>
      <p className="text-sm leading-6 text-muted-foreground">{description}</p>
      <Button asChild variant="outline" className="self-start">
        <Link to="/"><ArrowRight className="size-4 rotate-180" aria-hidden="true" />返回工作台</Link>
      </Button>
    </section>
  )
}
export function NotFoundPage() {
  return <PlaceholderPage title="页面不存在" phase="404" description="当前路径无对应页面，请返回工作台。" />
}
