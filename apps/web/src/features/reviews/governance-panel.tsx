import { useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { CheckCircle2, ShieldAlert } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { useSession } from '@/features/auth/session'
import { useProjectReviewPolicy, useWorkReviewPolicy, useChangeWorkReviewPolicy, useWorkAcceptanceHistory, useCompleteWorkItem } from '@/features/reviews/governance-api'
import type { WorkItem } from '@/lib/qual-api'

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i

export function ReviewGovernancePanel({ projectID, item, canReview, canManage, onLegacyDone }: {
  projectID: string
  item: WorkItem
  canReview: boolean
  canManage: boolean
  onLegacyDone: () => Promise<void>
}) {
  const policy = useProjectReviewPolicy(projectID)
  const history = useWorkAcceptanceHistory(projectID, item.id)
  const effective = useWorkReviewPolicy(projectID, item.id)
  const complete = useCompleteWorkItem(projectID, item.id)
  const changeOverride = useChangeWorkReviewPolicy(projectID, item.id)
  const { data: session } = useSession()
  const [referenceKind, setReferenceKind] = useState<'test_run' | 'artifact'>('test_run')
  const [reference, setReference] = useState('')
  const [reason, setReason] = useState('')
  const [acknowledged, setAcknowledged] = useState(false)
  const [overrideMode, setOverrideMode] = useState<'independent_review' | 'owner_acceptance'>('independent_review')
  const [overrideReason, setOverrideReason] = useState('')

  const mode = effective.data?.mode
  const isOwnerMode = mode === 'owner_acceptance'
  // A signer is the recorded accountable owner, or the claimed assignee when
  // no separate owner exists. The server independently checks work:review,
  // project policy, evidence and CAS for both Human and Agent callers.
  const isResponsibleSigner = Boolean(session?.principal.id && (
    item.accountable_owner_id === session.principal.id ||
    (!item.accountable_owner_id && item.assignee_id === session.principal.id)
  ))
  const canSubmit = item.status === 'in_review' && canReview &&
    Boolean(mode) && uuidPattern.test(reference) &&
    (!isOwnerMode || (isResponsibleSigner && acknowledged && reason.trim() === reason && [...reason].length >= 16))
  const submitOverride = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!canManage || !effective.data || overrideReason.trim() !== overrideReason || [...overrideReason].length < 16) return
    try {
      await changeOverride.mutateAsync({
        mode: overrideMode,
        expected_version: item.version,
        expected_policy_version: effective.data.override_version,
        reason: overrideReason,
      })
      setOverrideReason('')
    } catch { /* Server policy/CAS/authorization message remains visible. */ }
  }
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!canSubmit || !mode) return
    try {
      await complete.mutateAsync({
        acceptance_mode: mode,
        expected_version: item.version,
        reference_kind: referenceKind,
        reference_value: reference,
        reason,
        acknowledge_self_review: isOwnerMode && acknowledged,
      })
    } catch { /* Error stays visible; never imply acceptance on failure. */ }
  }

  const accepted = history.data?.decision
  const existingMode = history.data?.acceptance_mode
  return (
    <Card role="region" aria-label="审核签收策略与真实历史" id="review-acceptance">
      <CardHeader>
        <CardTitle className="flex flex-wrap items-center gap-2">
          <CheckCircle2 aria-hidden="true" className="size-4" /> 审核与签收
          {existingMode === 'legacy_unclassified' && <Badge variant="outline">历史未分类</Badge>}
          {existingMode === 'owner_acceptance' && <Badge variant="outline">Owner 自审 · 非独立</Badge>}
          {existingMode === 'independent_review' && <Badge variant="outline">独立审核</Badge>}
        </CardTitle>
        <CardDescription>审核决策来自服务端真实主体、项目政策、证据和 WorkItem 版本。测试 Passed 不等于审核通过。</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-4 text-sm">
        {history.isPending && <p role="status" className="text-muted-foreground">读取历史签收记录…</p>}
        {history.isError && <p role="alert" className="text-destructive">签收历史不可用；不能推断已存在独立批准。</p>}
        {existingMode === 'legacy_unclassified' && <p className="rounded-lg border border-amber-400/50 p-3 leading-6">
          此工作项是在结构化签收机制启用前完成的。只证明历史状态为 Done，不证明已通过独立 Reviewer，也不自动回填 Owner 自审决定。
        </p>}
        {accepted && <dl className="grid min-w-0 gap-2 rounded-lg bg-muted/50 p-4 text-xs leading-6">
          <div><dt className="font-medium">签收方式</dt><dd>{accepted.acceptance_mode === 'owner_acceptance' ? 'Owner 明确风险接受（非独立审核）' : '独立 Reviewer 通过'}</dd></div>
          <div><dt className="font-medium">签收主体</dt><dd className="break-all font-mono">{accepted.actor_id}</dd></div>
          <div><dt className="font-medium">工作版本</dt><dd>v{accepted.work_version_before} → v{accepted.work_version_after}</dd></div>
          <div><dt className="font-medium">证据</dt><dd className="break-all">{accepted.reference_kind} · {accepted.reference_kind === 'test_run' ?
            <Link className="text-primary underline" to={`/projects/${projectID}/tests/runs/${accepted.reference_value}`}>{accepted.reference_value}</Link> :
            <span className="font-mono">{accepted.reference_value}</span>}</dd></div>
          {accepted.reason && <div><dt className="font-medium">风险/签收理由</dt><dd className="whitespace-pre-wrap break-words">{accepted.reason}</dd></div>}
          <div><dt className="font-medium">可审计政策</dt><dd>{accepted.policy_source} v{accepted.policy_version}</dd></div>
        </dl>}
        {item.status === 'in_review' && <>
          {policy.isPending && <p role="status" className="text-muted-foreground">正在查询项目有效签收策略…</p>}
          {(policy.isError || effective.isError) && <p role="alert" className="text-destructive">无法确认有效审核策略，已禁用完成操作。请核对服务版本与权限。</p>}
          {effective.isPending && policy.data && <p role="status">读取工作项实际有效审核模式…</p>}
          {policy.isSuccess && policy.data === null && <div className="space-y-3 rounded-lg border border-amber-400/50 p-3">
            <p className="leading-6">当前仍为 Goose12 兼容流程，没有结构化签收政策。旧版 Done 不会产生 independent_review 或 owner_acceptance 决策。</p>
            {canReview && <Button size="sm" variant="outline" type="button" onClick={() => void onLegacyDone()}>
              旧版审核完成（不产生结构化签收）
            </Button>}
          </div>}
          {policy.data && effective.data && <div className="space-y-3">
            <p className="leading-6">服务器当前有效模式：<strong>{isOwnerMode ? 'Owner 自审风险接受' : '独立 Reviewer 审核'}</strong>（来源 {effective.data.source}，项目政策 v{effective.data.project_policy_version}，工作项覆盖 v{effective.data.override_version}）。
              {effective.data.forced_independent && ' 此项触发强制独立审核上限。'}</p>
            {isOwnerMode && <div className="rounded-lg border border-amber-400/60 bg-amber-50/50 p-3 text-amber-950 dark:bg-amber-950 dark:text-amber-100">
              <ShieldAlert aria-hidden="true" className="mr-1 inline size-4" />负责人签收不是独立批准。Human 和 Agent 均可按授权承担签收：服务端核对登记的 Accountable Owner，或未另设 Owner 时实际认领的负责人，以及 work:review、项目策略和真实证据。
              {!isResponsibleSigner && <p className="mt-2 font-semibold">当前主体不是登记的 Owner 或合法认领的负责人，不能代签。</p>}
            </div>}
            {canReview ? <form onSubmit={(e) => { void submit(e) }} className="space-y-3">
              <FieldGroup>
                <Field>
                  <FieldLabel htmlFor="decision-evidence-kind">证据类型</FieldLabel>
                  <select id="decision-evidence-kind" value={referenceKind}
                    onChange={e => setReferenceKind(e.target.value as 'test_run' | 'artifact')}
                    className="min-h-10 rounded-lg border border-input bg-background px-3 text-sm">
                    <option value="test_run">实际 TestRun（有 Attempt 且覆盖本工作项）</option>
                    <option value="artifact">私有 Ready Artifact（直接绑定本工作项）</option>
                  </select>
                </Field>
                <Field>
                  <FieldLabel htmlFor="decision-evidence-uuid">项目内证据 UUID</FieldLabel>
                  <Input id="decision-evidence-uuid" value={reference} placeholder="真实 TestRun 或 Ready Artifact UUID"
                    onChange={e => setReference(e.target.value.trim())} required />
                </Field>
                {isOwnerMode && <>
                  <Field>
                    <FieldLabel htmlFor="decision-risk-reason">本人明确接受的风险与依据（至少 16 字）</FieldLabel>
                    <Textarea id="decision-risk-reason" rows={3} maxLength={2000} required value={reason} onChange={e => setReason(e.target.value)} />
                  </Field>
                  <label className="flex items-start gap-2 leading-6">
                    <Checkbox checked={acknowledged} onCheckedChange={v => setAcknowledged(v === true)} />
                    <span>我确认这是 Owner 自审签收，不是独立 Reviewer 通过，并接受上述风险。</span>
                  </label>
                </>}
              </FieldGroup>
              {complete.error && <p role="alert" className="text-destructive">{complete.error.message}。如版本冲突，请刷新当前工作项和审核证据。</p>}
              {complete.isSuccess && <p role="status" className="text-emerald-700">服务端已确认完成决策，可查看上方最新签收历史。</p>}
              <Button type="submit" disabled={!canSubmit || complete.isPending}>
                {complete.isPending ? '提交服务器核验…' : isOwnerMode ? '明确自审签收并请求 Done' : '根据最新独立 Reviewer 记录请求 Done'}
              </Button>
            </form> : <p>当前主体无 work:review 权限，无法执行任何签收。可以查看历史和证据。</p>}
            {canManage && <form className="space-y-3 rounded-lg border border-border p-3" onSubmit={(e) => { void submitOverride(e) }}>
              <h3 className="font-semibold">调整此工作项审核模式（需 access:manage）</h3>
              <p className="text-xs leading-5 text-muted-foreground">任何修改都追加审计并增加 WorkItem 版本。放宽为 Owner 自审必须由 Human Workspace Admin 执行；高风险任务仍不能绕过独立审核。</p>
              <Field>
                <FieldLabel htmlFor="override-mode">期望审核模式</FieldLabel>
                <select id="override-mode" value={overrideMode}
                  onChange={e => setOverrideMode(e.target.value as 'independent_review' | 'owner_acceptance')}
                  className="min-h-10 w-full rounded-lg border border-input bg-background px-3 text-sm">
                  <option value="independent_review">强制独立 Reviewer</option>
                  <option value="owner_acceptance">Owner 明确风险接受（非独立审核）</option>
                </select>
              </Field>
              <Field>
                <FieldLabel htmlFor="override-reason">变更原因（至少 16 字）</FieldLabel>
                <Textarea id="override-reason" value={overrideReason} onChange={e => setOverrideReason(e.target.value)}
                  rows={2} maxLength={2000} required />
              </Field>
              {changeOverride.error && <p role="alert" className="text-destructive">{changeOverride.error.message}。请核对当前 CAS 版本与 Human 管理权限。</p>}
              <Button variant="outline" size="sm" type="submit" disabled={changeOverride.isPending ||
                overrideMode === effective.data.mode || [...overrideReason].length < 16}>
                {changeOverride.isPending ? '提交权限与版本校验…' : '提交工作项策略变更'}
              </Button>
            </form>}
          </div>}
        </>}
      </CardContent>
    </Card>
  )
}
