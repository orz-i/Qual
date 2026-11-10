import { useState, type FormEvent } from 'react'
import { Link, useLocation, useParams } from 'react-router'
import { ArrowLeft, ArrowRight, Bug, CheckCircle2, Clipboard, Clock3, MessageSquareText, RefreshCw, ShieldCheck, UserRound } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Checkbox } from '@/components/ui/checkbox'
import { Textarea } from '@/components/ui/textarea'
import { useComments, useProjectActivity, useWorkActions, useWorkCapabilities, useWorkItem } from '@/features/work/api'
import { useWorkTestCoverage } from '@/features/tests/api'
import { useSession } from '@/features/auth/session'
import { useMyAccess } from '@/features/access/api'
import { EvidencePanel } from '@/features/evidence/panel'
import { WorkExternalTimeline } from '@/features/work/external-timeline'
import { WorkRunObservability } from '@/features/work/run-observability'
import { TaskEvidenceSummary } from '@/features/work/task-evidence-summary'
import { WorkRoleHistory } from '@/features/work/roles'
import { ReviewGovernancePanel } from '@/features/reviews/governance-panel'
import { priorityLabel, statusLabel } from '@/features/work/labels'
import { useProjectPrincipals, principalLabel } from '@/features/work/principals'
import { workReturnHref } from '@/features/work/navigation'
import { ReassignWorkDialog } from '@/features/work/reassign-dialog'
import { allowsTaskAction, taskTransitionAction, displayTaskCapacities, displayTaskDenial, type TaskAction } from '@/features/work/capabilities'
import { QualAPIError, type WorkItem, type WorkItemStatus } from '@/lib/qual-api'
import { taskGuideSections, cleanedAcceptanceCriteria, canReadTaskPrivate } from '@/features/work/task-contract'

const nextStatuses: Partial<Record<WorkItemStatus, WorkItemStatus[]>> = {
  backlog: ['todo', 'cancelled'],
  todo: ['in_progress', 'cancelled'],
  in_progress: ['in_review', 'cancelled'],
  in_review: ['in_progress'], // Done belongs to the explicit governance panel, not unguarded transitions.
}

function MutationProblem({ message }: { message?: string }) {
  if (!message) return null
  return <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{message}</p>
}

function TaskExecutionGuide({ description }: { description: string }) {
  const sections = taskGuideSections(description)
  if (!sections.length) return <p id="work-task-contract" className="scroll-mt-24 text-sm text-muted-foreground">尚无执行说明；可在当前有权时补充目标、用户路径和可观察的验证条件。</p>
  return (
    <div id="work-task-contract" className="min-w-0 scroll-mt-24 space-y-4">
      {sections.length > 1 && (
        <nav aria-label="Task 执行说明导航" className="flex flex-wrap gap-2 rounded-lg bg-muted/35 p-3">
          {sections.map((section) => (
            <a key={section.id} href={`#${section.id}`}
              className="inline-flex min-h-9 items-center rounded-md border border-border bg-background px-3 text-xs font-medium hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">
              {section.title}
            </a>
          ))}
        </nav>
      )}
      {sections.map((section) => (
        <section key={section.id} id={section.id} aria-label={section.title}
          className="scroll-mt-24 rounded-lg border border-border/70 p-3 sm:p-4">
          <h3 className="mb-2 text-sm font-semibold">{section.title}</h3>
          <p className="whitespace-pre-wrap break-words text-sm leading-6">{section.content}</p>
        </section>
      ))}
      <p className="text-xs leading-5 text-muted-foreground">以上内容直接来自当前 Task 原始描述；命令只是可审查文本，不会在浏览器或 Qual Server 执行。</p>
    </div>
  )
}

function EditWorkDialog({ projectID, item, editable, onRefresh }: { projectID: string; item: WorkItem; editable: boolean; onRefresh: () => Promise<boolean> }) {
  const [open, setOpen] = useState(false)
  const [title, setTitle] = useState(item.title)
  const [description, setDescription] = useState(item.description)
  const [criteria, setCriteria] = useState<string[]>([...item.acceptance_criteria])
  const [validationError, setValidationError] = useState('')
  const [discardPrompt, setDiscardPrompt] = useState(false)
  const [requiresComparison, setRequiresComparison] = useState(false)
  const [confirmedComparison, setConfirmedComparison] = useState(false)
  const [refreshError, setRefreshError] = useState('')
  const actions = useWorkActions(projectID)
  const isConflict = actions.update.error instanceof QualAPIError && actions.update.error.code === 'VERSION_CONFLICT'
  const dirty = title !== item.title || description !== item.description ||
    JSON.stringify(criteria) !== JSON.stringify(item.acceptance_criteria)
  const toggleOpen = (nextOpen: boolean) => {
    if (nextOpen && !editable) return
    if (!nextOpen && open && dirty) {
      // Escape, overlay clicks and browser interaction races must not silently
      // discard an external Agent's unsaved Task contract/acceptance drafts.
      setDiscardPrompt(true)
      return
    }
    if (nextOpen) {
      setDiscardPrompt(false)
      setTitle(item.title)
      setDescription(item.description)
      setCriteria([...item.acceptance_criteria])
      setValidationError('')
      setRequiresComparison(false)
      setConfirmedComparison(false)
      setRefreshError('')
      actions.update.reset()
    }
    setOpen(nextOpen)
  }
  const reloadAfterConflict = async () => {
    setRefreshError('')
    if (!await onRefresh()) {
      setRefreshError('最新内容读取失败；草稿仍保留，请检查连接后重试。')
      return
    }
    // Never overwrite unsaved title/description; require an explicit comparison.
    setRequiresComparison(true)
    setConfirmedComparison(false)
    actions.update.reset()
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (isConflict || (requiresComparison && !confirmedComparison)) return
    let acceptanceCriteria: string[]
    try { acceptanceCriteria = cleanedAcceptanceCriteria(criteria) }
    catch (error) { setValidationError(error instanceof Error ? error.message : '验收条目不符合规范'); return }
    try {
      await actions.update.mutateAsync({ itemID: item.id, version: item.version, title: title.trim(), description, acceptanceCriteria })
      setOpen(false)
    } catch { /* Display error in dialog. */ }
  }
  return (
    <Dialog open={open} onOpenChange={toggleOpen}>
      <DialogTrigger asChild><Button variant="outline" size="sm" disabled={!editable} className={!editable ? 'hidden' : undefined}>编辑内容</Button></DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>编辑 {item.key}</DialogTitle>
          <DialogDescription>更新基于版本 {item.version}，不会覆盖其他人或 Agent 的并发更改。</DialogDescription>
        </DialogHeader>
        <form onSubmit={(event) => { void submit(event) }}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="edit-title">标题</FieldLabel>
              <Input id="edit-title" value={title} maxLength={240} required onChange={(event) => setTitle(event.target.value)} />
            </Field>
            <Field>
              <FieldLabel htmlFor="edit-description">描述</FieldLabel>
              <Textarea id="edit-description" value={description} maxLength={20000} rows={6} onChange={(event) => setDescription(event.target.value)} />
            </Field>
            <Field>
              <FieldLabel>验收标准（最多 30 条，每条最多 2000 字符）</FieldLabel>
              <div className="flex flex-col gap-2">
                {criteria.map((criterion, index) => (
                  <div key={index} className="flex min-w-0 flex-col gap-2 rounded-lg border p-2 sm:flex-row sm:items-start">
                    <Textarea aria-label={`验收标准 ${index + 1}`} value={criterion} maxLength={2000} rows={2}
                      className="min-w-0 flex-1" onChange={(event) => {
                        setCriteria((prev) => prev.map((value, i) => i === index ? event.target.value : value))
                        setValidationError('')
                      }} />
                    <Button type="button" size="sm" variant="outline" className="shrink-0"
                      onClick={() => setCriteria((prev) => prev.filter((_, i) => i !== index))}
                      aria-label={`移除验收标准 ${index + 1}`}>移除</Button>
                  </div>
                ))}
                {criteria.length === 0 && <p className="text-xs text-muted-foreground">尚无结构化验收条目。可以添加可观察的成功、失败或边界条件。</p>}
                <Button type="button" size="sm" variant="outline" className="self-start" disabled={criteria.length >= 30}
                  onClick={() => setCriteria((prev) => [...prev, ''])}>添加验收标准</Button>
              </div>
            </Field>
            <MutationProblem message={validationError} />
            <MutationProblem message={actions.update.error?.message} />
            {isConflict && <Button type="button" size="sm" variant="outline" className="self-start"
              onClick={() => void reloadAfterConflict()}>刷新最新版本并核对草稿</Button>}
            {refreshError && <MutationProblem message={refreshError} />}
            {discardPrompt && <div role="alert" className="flex flex-wrap items-center gap-2 rounded-lg border p-3 text-sm">
              <span className="flex-1">有未保存的任务说明或验收草稿。请选择继续编辑或明确放弃。</span>
              <Button type="button" size="sm" variant="outline" onClick={() => setDiscardPrompt(false)}>继续编辑</Button>
              <Button type="button" size="sm" variant="destructive" onClick={() => { setDiscardPrompt(false); setOpen(false) }}>放弃草稿并关闭</Button>
            </div>}
            {requiresComparison && <div className="space-y-3 rounded-xl border border-amber-300 bg-amber-50 p-3 text-sm text-amber-950 dark:bg-amber-950 dark:text-amber-100">
              <p className="font-semibold">已读取服务器版本 v{item.version}；你尚未提交的标题、描述和验收条目仍保留在上面的输入框。</p>
              <p className="break-words">服务器当前标题：{item.title}</p>
              <p className="max-h-24 overflow-y-auto whitespace-pre-wrap break-words">服务器当前描述：{item.description || '无'}</p>
              <div><p>服务器当前验收标准：</p><ol className="mt-1 list-decimal pl-5">{item.acceptance_criteria.map((criterion, index) => <li key={index} className="break-words">{criterion}</li>)}</ol></div>
              <label className="flex items-start gap-2">
                <Checkbox checked={confirmedComparison} onCheckedChange={(v) => setConfirmedComparison(v === true)} />
                <span>我已核对最新内容，确认用当前草稿更新 v{item.version}。</span>
              </label>
            </div>}
            <DialogFooter><Button type="submit" disabled={actions.update.isPending || !title.trim() || isConflict || (requiresComparison && !confirmedComparison)}>
              {actions.update.isPending ? '正在保存…' : '保存更改'}
            </Button></DialogFooter>
          </FieldGroup>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function Comments({ projectID, item, canComment, onRefresh }: { projectID: string; item: WorkItem; canComment: boolean; onRefresh: () => Promise<boolean> }) {
  const { data: session } = useSession()
  const canWrite = canComment
  const query = useComments(projectID, item.id)
  const principals = useProjectPrincipals(projectID, query.data?.map((entry) => entry.author_id) ?? [])
  const [text, setText] = useState('')
  const actions = useWorkActions(projectID)
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    try {
      await actions.comment.mutateAsync({ itemID: item.id, version: item.version, body: text.trim() })
      setText('')
    } catch { /* Display error below. */ }
  }
  return (
    <section className="flex flex-col gap-4" aria-label="评论与进展">
      <h2 className="flex items-center gap-2 text-base font-semibold"><MessageSquareText aria-hidden="true" className="size-4" /> 评论与进展</h2>
      {query.isPending && <p className="text-sm text-muted-foreground">读取评论中…</p>}
      {query.isError && <MutationProblem message={query.error.message} />}
      {query.isSuccess && !query.data.length && <p className="rounded-lg bg-muted/35 p-5 text-sm text-muted-foreground">暂无评论。你或 Agent 可以通过相同权限记录进展。</p>}
      {query.data?.map((comment) => (
        <div key={comment.id} className="border-b border-border pb-3">
          <div className="mb-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
            <span className="break-all" title={`完整主体 UUID: ${comment.author_id}`}>{comment.author_id === session?.principal.id ? `${session.principal.display_name} · Human` : principalLabel(comment.author_id, principals.byID.get(comment.author_id))}</span>
            <time dateTime={comment.created_at}>{new Date(comment.created_at).toLocaleString()}</time>
          </div>
          <p className="whitespace-pre-wrap break-words text-sm leading-6">{comment.body}</p>
        </div>
      ))}
      {canWrite ? <form onSubmit={(event) => { void submit(event) }} className="flex flex-col gap-3">
        <Field>
          <FieldLabel htmlFor="comment-body">追加评论</FieldLabel>
          <Textarea id="comment-body" value={text} maxLength={10000} required rows={3}
            placeholder="记录进展、决策或阻塞原因，不包含密码或访问密钥"
            onChange={(event) => setText(event.target.value)} />
        </Field>
        <MutationProblem message={actions.comment.error?.message} />
        {actions.comment.error instanceof QualAPIError && actions.comment.error.code === 'VERSION_CONFLICT' && (
          <div className="space-y-2 rounded-lg border border-amber-300 p-3 text-xs">
            <p>评论未提交；文本已保留。请先刷新工作项版本，检查最新进展后再重新提交。</p>
            <Button type="button" variant="outline" size="sm" onClick={() => void onRefresh().then((ok) => { if (ok) actions.comment.reset() })}>刷新最新版本（不清空评论）</Button>
          </div>
        )}
        <Button type="submit" disabled={!text.trim() || actions.comment.isPending || (actions.comment.error instanceof QualAPIError && actions.comment.error.code === 'VERSION_CONFLICT')} className="self-end">
          {actions.comment.isPending ? '正在记录…' : '提交评论'}
        </Button>
      </form> : <p className="text-xs leading-5 text-muted-foreground">当前任务的状态或职责不允许追加评论。</p>}
    </section>
  )
}

export function WorkDetailPage() {
  const { projectID = '', itemID = '' } = useParams()
  const location = useLocation()
  const query = useWorkItem(projectID, itemID)
  const capabilities = useWorkCapabilities(projectID, itemID)
  const coverage = useWorkTestCoverage(projectID, itemID)
  const actions = useWorkActions(projectID)
  const { data: session } = useSession()
  const access = useMyAccess(projectID)
  const permissions = access.data?.effective_permissions ?? []
  const item = query.data
  const caps = capabilities.data
  const can = (action: TaskAction) => item ? allowsTaskAction(caps, item.version, action) : false
  const canWrite = can('task.update')
  const canClaim = can('task.claim')
  const canReview = can('task.complete_independent') || can('task.complete_owner')
  // Project governance is not derived from task capacity.
  const canManage = permissions.includes('access:manage')
  const matchesCapabilityVersion = Boolean(item && caps?.work_version === item.version)
  const canReadPrivate = Boolean(item && canReadTaskPrivate(caps, item.id, item.version, permissions.includes('work:read')))
  const refreshTask = async () => {
    const [work, currentCaps] = await Promise.all([query.refetch(), capabilities.refetch()])
    return work.isSuccess && currentCaps.isSuccess
  }
  const principals = useProjectPrincipals(projectID, item ? [item.assignee_id, item.created_by, item.accountable_owner_id] : [])
  const pending = actions.transition.isPending || actions.claim.isPending ||
    actions.release.isPending || actions.reviewClaim.isPending || actions.reviewRelease.isPending
  const displayPrincipal = (id: string) => id === session?.principal.id
    ? `${session.principal.display_name} · Human`
    : principalLabel(id, principals.byID.get(id))
  const transition = async (status: WorkItemStatus) => {
    if (!item) return
    try { await actions.transition.mutateAsync({ itemID, version: item.version, status }) }
    catch { /* Expose error in page. */ }
  }
  const lifecycle = async (kind: 'claim' | 'release' | 'reviewClaim' | 'reviewRelease') => {
    if (!item) return
    try { await actions[kind].mutateAsync({ itemID, version: item.version }) }
    catch { /* The error and current capability state remain visible. */ }
  }

  return (
    <article className="flex min-w-0 flex-col gap-6">
      <Link to={workReturnHref(projectID, location.search)} className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft aria-hidden="true" className="size-4" /> 返回工作项（保留筛选）
      </Link>
      {query.isPending && <p role="status" className="text-sm text-muted-foreground">正在载入工作项…</p>}
      {query.isError && <MutationProblem message={query.error.message} />}
      {item && (
        <>
          <header className="flex flex-wrap items-start justify-between gap-3">
            <div className="flex min-w-0 flex-1 flex-col gap-3">
              <div className="flex flex-wrap items-center gap-2">
                <span className="font-mono text-sm font-semibold text-primary">{item.key}</span>
                <Badge variant="secondary">{item.kind === 'bug' ? '缺陷' : '任务'}</Badge>
                <Badge variant="outline">{statusLabel(item.status)}</Badge>
              </div>
              <h1 className="max-w-4xl break-words text-2xl font-semibold leading-tight tracking-tight sm:text-3xl">{item.title}</h1>
              <p className="text-xs leading-5 text-muted-foreground">所有操作均以真实主体和当前版本为基础记录，不会触发 Agent 或测试执行。</p>
            </div>
            <div className="flex items-center gap-2">
              <Button size="sm" variant="ghost" aria-label="复制工作项编号"
                onClick={() => void navigator.clipboard.writeText(item.key)}>
                <Clipboard aria-hidden="true" />
              </Button>
              <EditWorkDialog projectID={projectID} item={item} editable={canWrite} onRefresh={refreshTask} />
            </div>
          </header>
          <div className="flex flex-wrap items-center gap-2">
            <a href="#work-task-contract" className="inline-flex min-h-9 items-center rounded-lg border border-input px-3 text-xs font-medium hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">目标与执行导航</a>
            <a href="#work-acceptance" className="inline-flex min-h-9 items-center rounded-lg border border-input px-3 text-xs font-medium hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">验收标准</a>
            <a href="#work-task-source" className="inline-flex min-h-9 items-center rounded-lg border border-input bg-background px-3 text-xs font-medium text-foreground hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">测试与历史来源</a>
            <a href="#work-evidence" className="inline-flex min-h-9 items-center rounded-lg border border-input bg-background px-3 text-xs font-medium text-foreground hover:bg-accent focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">查看已归档证据</a>
            {canClaim && (
              <Button variant="outline" size="sm" disabled={pending}
                onClick={() => void lifecycle('claim')}>
                <UserRound data-icon="inline-start" aria-hidden="true" /> 领取任务
              </Button>
            )}
            {can('task.release') && <Button variant="outline" size="sm" disabled={pending}
              onClick={() => void lifecycle('release')}>释放执行职责</Button>}
            {can('task.reassign') && <ReassignWorkDialog projectID={projectID} itemID={itemID}
              version={item.version} onRefresh={async () => { await refreshTask() }} />}
            {can('task.claim_review') && <Button size="sm" variant="outline" disabled={pending}
              onClick={() => void lifecycle('reviewClaim')}>领取独立审核</Button>}
            {can('task.release_review') && <Button size="sm" variant="outline" disabled={pending}
              onClick={() => void lifecycle('reviewRelease')}>退出当前审核</Button>}
            {nextStatuses[item.status]?.filter((next) => {
              const action = taskTransitionAction(item.status, next)
              return action && can(action)
            }).map((next) => (
              <Button key={next} size="sm" disabled={pending}
                variant={next === 'cancelled' || next === 'in_progress' ? 'outline' : 'default'}
                onClick={() => void transition(next)}>
                {next === 'done' && <CheckCircle2 data-icon="inline-start" aria-hidden="true" />}
                {next === 'done' ? '审核完成' : `转为${statusLabel(next)}`}
              </Button>
            ))}
            {pending && <span role="status" className="text-xs text-muted-foreground">正在提交…</span>}
            {item.status === 'in_review' && !canReview && <span className="inline-flex items-center gap-1.5 rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-900 dark:bg-amber-950 dark:text-amber-200"><ShieldCheck aria-hidden="true" className="size-4" />完成审核需要当前任务对应的审核职责及真实证据</span>}
          </div>
          <MutationProblem message={actions.claim.error?.message ?? actions.release.error?.message ??
            actions.reviewClaim.error?.message ?? actions.reviewRelease.error?.message ?? actions.transition.error?.message} />
          {(actions.claim.error || actions.release.error || actions.reviewClaim.error ||
            actions.reviewRelease.error || actions.transition.error) && (
            <Button type="button" variant="outline" size="sm" className="self-start"
              onClick={() => void refreshTask().then(() => {
                actions.claim.reset(); actions.release.reset(); actions.reviewClaim.reset()
                actions.reviewRelease.reset(); actions.transition.reset()
              })}>
              <RefreshCw aria-hidden="true" className="size-4" /> 刷新最新工作项与可执行状态
            </Button>
          )}
          {access.isError && <MutationProblem message="当前项目权限读取失败，项目治理动作已隐藏；请检查连接后重试。" />}
          {capabilities.isError && <MutationProblem message="当前任务可执行动作读取失败；已隐藏任务写操作，请刷新。" />}
          {!matchesCapabilityVersion && capabilities.isSuccess && (
            <div className="flex flex-wrap items-center gap-2" role="status">
              <p className="text-xs text-muted-foreground">任务版本已变化，旧能力快照失效，操作已隐藏。</p>
              <Button type="button" size="sm" variant="outline" onClick={() => void refreshTask()}>
                <RefreshCw aria-hidden="true" className="size-4" /> 刷新任务及权限
              </Button>
            </div>
          )}
          <section aria-label="当前任务职责与可执行动作" className="space-y-1 rounded-xl border border-border bg-muted/20 p-3 text-xs">
            <p className="font-medium">当前任务职责：{matchesCapabilityVersion ? displayTaskCapacities(caps) : '正在校验'}</p>
            {matchesCapabilityVersion && <p className="text-muted-foreground">
              {caps?.project_mode === 'legacy' ? '当前为旧项目权限模式' : '任务驱动授权模式'}
              {' · '}v{caps?.work_version}{' · '}当前允许 {caps?.allowed_actions.length ?? 0} 类任务操作
              {' · '}操作前服务器仍将重新核验状态、证据及权限
            </p>}
            {matchesCapabilityVersion && !canClaim && !canWrite && (
              <p className="text-muted-foreground">暂不可领取/编辑：
                {displayTaskDenial(caps?.denied_reasons?.['task.claim'] ||
                  caps?.denied_reasons?.['task.update'])}</p>
            )}
          </section>
          <div className="grid min-w-0 items-start gap-5 xl:grid-cols-[minmax(0,1fr)_280px]">
            <div className="flex min-w-0 flex-col gap-5">
              <Card>
                <CardHeader>
                  <CardTitle>概述与验收</CardTitle>
                  <CardDescription>明确工作范围与预期结果；所有写入保留身份、版本与审计记录。</CardDescription>
                </CardHeader>
                <CardContent>
                  <TaskExecutionGuide description={item.description} />
                  {item.acceptance_criteria.length > 0 && (
                    <section id="work-acceptance" aria-label="验收标准" className="mt-5 flex scroll-mt-24 flex-col gap-2">
                      <h2 className="text-sm font-semibold">验收标准</h2>
                      <ul className="list-disc pl-5 text-sm leading-6">{item.acceptance_criteria.map((text, index) => <li key={index} className="whitespace-pre-wrap break-words">{text}</li>)}</ul>
                    </section>
                  )}
                  {item.acceptance_criteria.length === 0 && (
                    <section id="work-acceptance" aria-label="验收标准" className="mt-5 scroll-mt-24 border-t border-border/70 pt-4">
                      <h2 className="text-sm font-semibold">验收标准</h2>
                      <p className="mt-2 text-xs leading-6 text-muted-foreground">未登记结构化验收标准。请结合真实工作记录和测试证据判断，不将任务状态视为测试通过。</p>
                    </section>
                  )}
                </CardContent>
              </Card>
              {item.kind === 'bug' && item.bug_details && (
                <Card>
                  <CardHeader><CardTitle className="flex items-center gap-2"><Bug aria-hidden="true" className="size-4 text-destructive" />缺陷信息</CardTitle></CardHeader>
                  <CardContent className="flex flex-col gap-4 text-sm">
                    <p>严重程度：{item.bug_details.severity}</p>
                    {item.bug_details.reproduction_steps && <p className="whitespace-pre-wrap">复现步骤：{item.bug_details.reproduction_steps}</p>}
                    {item.bug_details.actual_behavior && <p className="whitespace-pre-wrap">实际行为：{item.bug_details.actual_behavior}</p>}
                  </CardContent>
                </Card>
              )}
              <Card>
                <CardHeader><CardTitle className="flex flex-wrap items-center justify-between gap-2">测试覆盖 <Link to={`/projects/${projectID}/tests/runs`} className="text-xs font-medium text-primary hover:underline">查看测试运行 <ArrowRight aria-hidden="true" className="inline size-3.5" /></Link></CardTitle><CardDescription>仅显示关联用例；真实通过/失败须查看 TestRun / Attempt，历史不可被新版本覆盖。</CardDescription></CardHeader>
                <CardContent className="flex flex-col gap-2">
                  {coverage.isError&&<MutationProblem message={coverage.error.message}/>}
                  {coverage.data?.map(test=>(
                    <Link key={test.id} to={`/projects/${projectID}/tests/cases/${test.id}`}
                      className="rounded-lg border p-2.5 text-sm hover:bg-muted/40">
                      {test.key} · {test.title} <span className="text-xs text-muted-foreground">v{test.published_version||'草稿'}</span>
                    </Link>
                  ))}
                  {coverage.isPending && <p role="status" className="text-sm text-muted-foreground">正在读取测试覆盖…</p>}
                  {coverage.data?.length===0&&<p className="text-sm text-muted-foreground">尚未建立测试覆盖关系；没有记录不代表测试通过。</p>}
                </CardContent>
              </Card>
              <WorkExternalTimeline projectID={projectID} itemID={itemID} canRead={access.isSuccess && permissions.includes('work:read')} />
              <TaskEvidenceSummary projectID={projectID} itemID={itemID} version={item.version} authorized={canReadPrivate} />
              <WorkRunObservability projectID={projectID} itemID={itemID} canRead={canReadPrivate} canReadEvidence={canReadPrivate} />
              <ReviewGovernancePanel projectID={projectID} item={item} canReview={canReview} canManage={canManage} onLegacyDone={() => transition('done')} />
              {canReadPrivate && <WorkRoleHistory projectID={projectID} itemID={itemID} version={item.version} />}
              {canReadPrivate ? <EvidencePanel projectID={projectID} targetKind="work_item" targetID={itemID} /> :
                <div id="work-evidence" role="status" className="rounded-xl border border-border bg-muted/30 p-4 text-sm text-muted-foreground">当前没有本任务的私有证据读取关系。可阅读公开任务目标；领取或获得合法审核关系后才能检查 TestRun/Ready Artifact。</div>}
              <section id="work-comments" className="scroll-mt-24 rounded-2xl border border-border bg-card p-5 sm:p-6">
                {canReadPrivate ? <Comments projectID={projectID} item={item} canComment={can('task.comment')} onRefresh={refreshTask} /> :
                  <p className="text-sm text-muted-foreground">评论与 Checkpoint 是任务私有上下文，当前主体尚无读取关系；它们不代表空历史。</p>}
              </section>
            </div>
            <aside aria-label="工作项属性" className="flex flex-col gap-4 text-sm xl:sticky xl:top-24">
              <div className="rounded-2xl border border-border bg-card p-5">
                <h2 className="mb-5 text-base font-semibold">属性</h2>
                <dl className="flex flex-col gap-4">
                  <div className="flex justify-between gap-2"><dt className="text-muted-foreground">优先级</dt><dd>{priorityLabel[item.priority]}</dd></div>
                  <div className="flex justify-between gap-2"><dt className="text-muted-foreground">状态</dt><dd>{statusLabel(item.status)}</dd></div>
                  <div className="flex justify-between gap-2"><dt className="text-muted-foreground">版本</dt><dd className="tabular-nums">{item.version}</dd></div>
                  <div className="flex flex-col gap-1 border-t border-border pt-4"><dt className="text-muted-foreground">负责人</dt><dd className="break-all font-medium" title={item.assignee_id ?? undefined}>{item.assignee_id ? displayPrincipal(item.assignee_id) : '未分配'}{item.assignee_id && <Button type="button" variant="ghost" size="sm" className="mt-1 flex px-0 text-xs" onClick={() => void navigator.clipboard.writeText(item.assignee_id!)}>复制负责人完整 UUID</Button>}</dd></div>
                  <div className="flex flex-col gap-1"><dt className="text-muted-foreground">创建者</dt><dd className="break-all font-medium" title={item.created_by}>{displayPrincipal(item.created_by)}<Button type="button" variant="ghost" size="sm" className="mt-1 flex px-0 text-xs" onClick={() => void navigator.clipboard.writeText(item.created_by)}>复制创建者完整 UUID</Button></dd></div>
                </dl>
              </div>
              <p className="flex items-center gap-2 px-2 text-xs text-muted-foreground"><Clock3 aria-hidden="true" className="size-3.5 shrink-0" />更新于 {new Date(item.updated_at).toLocaleString()}</p>
              <p className="px-2 text-xs leading-5 text-muted-foreground">身份名称和 Human/Agent 类型仅来自项目成员目录；失效或不可解析的主体仍保留完整 UUID 可核验。</p>
              {principals.isError && <p role="alert" className="px-2 text-xs text-destructive">成员身份查询失败，仅显示可追溯 UUID。</p>}
            </aside>
          </div>
        </>
      )}
    </article>
  )
}

export function ProjectActivityPage() {
  const { projectID = '' } = useParams()
  const query = useProjectActivity(projectID)
  return (
    <section className="flex max-w-3xl flex-col gap-5">
      <Link to={`/projects/${projectID}`} className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft aria-hidden="true" className="size-4" /> 返回看板
      </Link>
      <h1 className="text-2xl font-semibold tracking-tight">项目活动</h1>
      <p className="text-sm text-muted-foreground">工作项变更记录按时间排序；本页最多展示最近 50 条。</p>
      {query.isPending && <p role="status" className="text-sm text-muted-foreground">读取活动中…</p>}
      {query.isError && <MutationProblem message={query.error.message} />}
      {query.data?.length === 0 && <p className="rounded-lg bg-muted/40 p-8 text-center text-sm text-muted-foreground">此项目还没有活动记录。</p>}
      <div className="divide-y rounded-xl border">
        {query.data?.map((event) => (
          <div key={event.id} className="flex flex-col gap-1.5 p-4 sm:flex-row sm:items-center sm:justify-between">
            <div className="flex flex-col gap-1">
              <span className="text-sm font-medium">{event.operation}</span>
              <span className="text-xs text-muted-foreground">Actor: {event.actor_id.slice(0, 8)} · WorkItem: {event.entity_id.slice(0, 8)}</span>
            </div>
            <time className="text-xs text-muted-foreground" dateTime={event.occurred_at}>{new Date(event.occurred_at).toLocaleString()}</time>
          </div>
        ))}
      </div>
    </section>
  )
}
