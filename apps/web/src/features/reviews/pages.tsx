import { useState } from 'react'
import { Link, useParams } from 'react-router'
import {
  ArrowRight, ClipboardCheck, ClipboardList, History, LockKeyhole,
  RefreshCw, Scale, ShieldAlert, ShieldCheck, Users,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useMyAccess } from '@/features/access/api'
import { statusLabel, priorityLabel } from '@/features/work/labels'
import { useProjectPrincipals, principalLabel } from '@/features/work/principals'
import { useReviewQueue, useProjectAudit, type ReviewQueueEntry, type ReviewQueueLane } from './api'
import { filterReviewQueue, reviewSummary, reviewLane, reviewStatusMismatch } from './rules'
import { AttentionInboxPanel } from './attention-panel'

const outcomeNames: Record<string,string> = {
  observed:'观察记录',passed:'核验通过',failed:'核验失败',needs_work:'需要整改',
}
const sourceNames: Record<string,string> = {
  git_commit:'Git Commit',test_run:'Qual TestRun',artifact:'Qual Artifact',none:'未关联证据',
}

function QueueItemCard({projectID,item,canReview,principals}:{
 projectID:string
 item:ReviewQueueEntry
 canReview:boolean
 principals:ReturnType<typeof useProjectPrincipals>['byID']
}){
 const review=item.latest_review
 const lane=reviewLane(item)
 const needsWork=lane==='needs_work'
 const readyToClose=lane==='ready_to_close'
 const resubmitted=item.resubmitted_after_review && lane==='awaiting'
 const mismatch=reviewStatusMismatch(item)
 return (
  <article className="flex min-w-0 flex-col gap-4 rounded-2xl border border-border bg-card p-4 sm:p-5">
   <div className="flex flex-wrap items-center gap-2 text-xs">
    <span className="font-mono font-semibold text-primary">{item.key}</span>
    <span className={needsWork
      ?'rounded-lg bg-rose-50 px-2 py-1 font-semibold text-rose-900 dark:bg-rose-950 dark:text-rose-200'
      :readyToClose
        ?'rounded-lg bg-emerald-50 px-2 py-1 font-semibold text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200'
        :'rounded-lg bg-amber-50 px-2 py-1 font-semibold text-amber-900 dark:bg-amber-950 dark:text-amber-200'}>
      {resubmitted?'整改后重新提交 · 待复审':needsWork?'最新审核要求整改':readyToClose?'独立核验已通过 · 待关闭':review?.self_review&&review.outcome==='passed'?'等待独立复核':'等待审查'}
    </span>
    <span className="text-muted-foreground">{item.kind==='bug'?'缺陷':'任务'} · {priorityLabel[item.priority]}</span>
    <span className="ml-auto text-muted-foreground">v{item.version} · {statusLabel(item.status)}</span>
   </div>
   <Link to={`/projects/${projectID}/items/${item.work_item_id}`}
    className="max-w-4xl break-words text-sm font-semibold leading-6 hover:text-primary hover:underline sm:text-base">
    {item.title}
   </Link>
   <p className="break-all text-xs text-muted-foreground" title={item.assignee_id??undefined}>
    负责人：{principalLabel(item.assignee_id,principals.get(item.assignee_id??''))}
   </p>
   {mismatch && <p role="alert" className="rounded-xl border border-destructive/40 bg-destructive/5 p-3 text-xs leading-6 text-destructive">
     状态不一致：最新 Reviewer 要求整改，但工作项仍为「待审查」。负责人应核对版本后按授权流程退回进行中；不能将其视为待批准。
   </p>}
   {resubmitted && <p className="rounded-xl border border-amber-300 bg-amber-50/60 p-3 text-xs leading-6 text-amber-900 dark:bg-amber-950 dark:text-amber-200">
     执行者已在上次 Reviewer 决策之后重新提交待审。旧审核结论仍永久保留，需有权 Reviewer 重新核验本次版本及证据；这不是独立批准。
   </p>}
   {readyToClose && <p className="rounded-xl border border-emerald-300 bg-emerald-50/60 p-3 text-xs leading-6 text-emerald-900 dark:bg-emerald-950 dark:text-emerald-200">
     独立 Reviewer 已记录 passed，但当前工作项仍未 Done；只有有效 work:review 授权者可以在核实证据后执行独立状态流转。
   </p>}
   {review ? (
    <div className="space-y-3 rounded-xl bg-muted/50 p-4">
     <div className="flex flex-wrap items-center gap-2">
      <p className="text-xs font-semibold">{resubmitted?'上次 Reviewer 历史 · ':'最近 Reviewer 记录 · '}{outcomeNames[review.outcome]??review.outcome}</p>
      <time className="text-xs text-muted-foreground" dateTime={review.recorded_at}>
       {new Date(review.recorded_at).toLocaleString()}
      </time>
     </div>
     <p className="whitespace-pre-wrap break-words text-sm leading-6">{review.summary}</p>
     <p className="break-all text-xs text-muted-foreground" title={review.actor_id}>
      Reviewer：{principalLabel(review.actor_id,principals.get(review.actor_id))}
     </p>
     <p className="text-xs text-muted-foreground">记录于工作版本 v{review.work_version}；并非当前状态的自动批准。</p>
     {review.self_review && (
       <p className="flex items-start gap-2 text-xs font-medium text-destructive">
         <ShieldAlert aria-hidden="true" className="size-4 shrink-0"/>
         同一 Principal 自我复核 · 非独立批准
       </p>
     )}
     {review.reference_kind!=='none' && (
      <p className="break-all text-xs leading-5 text-muted-foreground">
       证据引用 · {sourceNames[review.reference_kind]??review.reference_kind}：
       {review.reference_kind==='test_run'
        ? <Link className="font-mono text-primary underline underline-offset-2"
            to={`/projects/${projectID}/tests/runs/${review.reference_value}`}>
            {review.reference_value}
          </Link>
        : <span className="font-mono">{review.reference_value}</span>}
       {review.reference_kind==='git_commit'&&'（Qual 不自动校验 Git 提交）'}
      </p>
     )}
    </div>
   ):(
    <p className="rounded-xl bg-muted/35 px-4 py-3 text-xs text-muted-foreground">
      尚无 Reviewer 决策。WorkItem 状态等待审查，但并不表示已经核验通过。
    </p>
   )}
   <div className="flex flex-wrap items-center justify-between gap-2">
    <span className="text-xs text-muted-foreground">
     {resubmitted
       ?canReview?'已重新提交，必须重新审核当前版本与证据。':'已重新提交，等待有权 Reviewer 核验。'
       :needsWork
       ?'最近审核结论要求整改；请执行者修改并重新提交审查。'
       :readyToClose
         ?canReview?'有权处理状态，但仍须按最新版本核验并执行显式流转。':'已有独立核验记录；当前身份不能关闭任务。'
         :canReview
           ?'已获得 work:review；请在详情核对身份、证据和自审标记。'
           :'当前身份无 work:review，队列仅供阅读。'}
    </span>
    <Button size="sm" variant={!needsWork&&canReview?'default':'outline'} asChild>
     <Link to={`/projects/${projectID}/items/${item.work_item_id}${!needsWork&&canReview?'#review-evidence-history':''}`}>
      {!needsWork&&canReview?'核对审核与证据':'查看详情与证据'}
      <ArrowRight aria-hidden="true" className="size-4"/>
     </Link>
    </Button>
   </div>
  </article>
 )
}

function AuditPanel({projectID,enabled,canReadWork}:{
 projectID:string;enabled:boolean;canReadWork:boolean
}){
 const audit=useProjectAudit(projectID,enabled)
 const actors=useProjectPrincipals(projectID,enabled&&canReadWork
  ?(audit.data?.map(e=>e.actor_id)??[]):[])
 return (
  <section className="space-y-3 rounded-2xl border border-border bg-card p-4 sm:p-5" aria-label="项目审计记录">
   <div className="flex flex-wrap items-start justify-between gap-3">
    <div>
     <h2 className="flex items-center gap-2 text-base font-semibold">
      <ShieldCheck aria-hidden="true" className="size-4 text-primary"/> 项目审计记录
     </h2>
     <p className="mt-1 text-xs leading-5 text-muted-foreground">
      与工作履历独立，读取原始审计元数据要求 audit:read，有效权限可随时撤销。
     </p>
    </div>
    {enabled&&<Button size="sm" type="button" variant="outline" disabled={audit.isFetching} onClick={()=>void audit.refetch()}>
      <RefreshCw aria-hidden="true" className="size-4"/>刷新审计
    </Button>}
   </div>
   {!enabled?(
    <div className="flex items-center gap-2 rounded-xl bg-muted/55 px-4 py-4 text-xs text-muted-foreground">
     <LockKeyhole aria-hidden="true" className="size-4 shrink-0"/>
     当前身份未获 audit:read；不会尝试获取受限 Audit 数据。项目活动和 Reviewer 业务履历与原始审计不是同一权限。
    </div>
   ):(
    <>
     {audit.isPending&&<p role="status" className="text-sm text-muted-foreground">正在验证权限并读取审计…</p>}
     {audit.isError&&<p role="alert" className="text-sm text-destructive">{audit.error.message}</p>}
     {audit.isSuccess&&audit.data.length===0&&<p className="text-xs text-muted-foreground">没有返回审计记录。</p>}
     <ol className="divide-y divide-border/70">
      {audit.data?.map(row=>(
       <li key={row.id} className="flex flex-col gap-1.5 py-3 first:pt-0 last:pb-0">
        <div className="flex flex-wrap items-center justify-between gap-2">
         <p className="break-words text-sm font-medium">{row.operation}</p>
         <time className="shrink-0 text-xs text-muted-foreground" dateTime={row.occurred_at}>
          {new Date(row.occurred_at).toLocaleString()}
         </time>
        </div>
        <p title={row.actor_id} className="break-all text-xs text-muted-foreground">
          操作者：{canReadWork?principalLabel(row.actor_id,actors.byID.get(row.actor_id)):row.actor_id}
        </p>
        <p className="break-all text-xs text-muted-foreground">
          对象 UUID：{row.entity_id} · 版本 {row.old_version??'—'} → {row.new_version}
        </p>
       </li>
      ))}
     </ol>
     {audit.isSuccess&&<p className="text-xs leading-5 text-muted-foreground">
      仅显示最近 {audit.data.length} 条授权审计元数据；不展示凭证 ID，不表示完整项目历史。
     </p>}
    </>
   )}
  </section>
 )
}

export function ReviewCenterPage(){
 const {projectID=''}=useParams()
 const access=useMyAccess(projectID)
 const perms=access.data?.effective_permissions??[]
 const canRead=perms.includes('work:read')
 const canReview=canRead&&perms.includes('work:review')
 const canAudit=perms.includes('audit:read')
 const canManage=perms.includes('access:manage')
 const [showReviewDetails,setShowReviewDetails]=useState(false)
 const queue=useReviewQueue(projectID,canRead&&showReviewDetails)
 const items=queue.data?.pages.flatMap(p=>p.items)??[]
 const principals=useProjectPrincipals(projectID,canRead
  ?items.flatMap(item=>[item.assignee_id,item.latest_review?.actor_id]):[])
 const [lane,setLane]=useState<ReviewQueueLane>('all')
 const summary=reviewSummary(items)
 const visible=filterReviewQueue(items,lane)
 const tabs=[
  {value:'all',label:'全部',count:summary.loaded},
  {value:'awaiting',label:'等待审查',count:summary.awaiting},
  {value:'needs_work',label:'审核要求整改',count:summary.needsWork},
  {value:'ready_to_close',label:'已核验待关闭',count:summary.readyToClose},
 ] as const

 return (
  <section className="flex min-w-0 flex-col gap-6">
   <header className="flex flex-wrap items-start justify-between gap-4">
    <div>
     <p className="text-xs font-semibold tracking-wider text-primary">HUMAN + AGENT / REVIEW</p>
     <h1 className="mt-2 text-2xl font-semibold tracking-tight sm:text-3xl">协作审核</h1>
     <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">
      先呈现 Reviewer 待决策与外部 AgentRun/Event 的注意力线索，仍通过现有工作项执行审核。
      Qual 不自动批准、不运行 Agent，Reviewer 与 Auditor 权限始终独立。
     </p>
    </div>
    {canManage&&<Button size="sm" variant="outline" asChild>
      <Link to={`/projects/${projectID}/access`}><Users aria-hidden="true" className="size-4"/>成员与权限</Link>
    </Button>}
   </header>

   {access.isPending&&<p role="status" className="text-sm text-muted-foreground">正在确认项目权限…</p>}
   {access.isError&&<p role="alert" className="rounded-xl bg-destructive/5 p-4 text-sm text-destructive">
     当前权限读取失败；为避免越权，审核与审计资料均未加载：{access.error.message}
   </p>}
   {access.isSuccess&&(
    <>
     <div className="grid gap-3 sm:grid-cols-3">
      {[
       {label:'工作履历',granted:canRead,permission:'work:read'},
       {label:'审核与完成',granted:canReview,permission:'work:review + work:read'},
       {label:'原始审计',granted:canAudit,permission:'audit:read'},
      ].map(cap=>(
       <div key={cap.label} className="rounded-xl border border-border bg-card px-4 py-3">
        <p className="text-xs text-muted-foreground">{cap.label}</p>
        <p className="mt-1 flex items-center gap-2 text-sm font-semibold">
         {cap.granted?<ShieldCheck aria-hidden="true" className="size-4 text-emerald-600"/>:
          <LockKeyhole aria-hidden="true" className="size-4 text-muted-foreground"/>}
         {cap.granted?'已授权':'未授权'}
        </p>
        <p className="mt-1 text-[11px] text-muted-foreground">{cap.permission}</p>
       </div>
      ))}
     </div>

     {canRead?(
      <>
       <AttentionInboxPanel projectID={projectID} canRead={canRead} canReview={canReview}/>
       <details onToggle={event=>setShowReviewDetails(event.currentTarget.open)} className="min-w-0 rounded-xl border border-border px-4 py-3 sm:px-5">
        <summary className="cursor-pointer rounded py-2 text-sm font-semibold focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring">展开现有 Reviewer 审核明细与原始历史（按需）</summary>
        <section className="mt-4 flex flex-col gap-4" aria-label="工作项审查与整改队列">
       <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
         <h2 className="flex items-center gap-2 text-lg font-semibold"><Scale aria-hidden="true" className="size-5 text-primary"/>项目审查队列</h2>
         <p className="mt-1 text-xs leading-5 text-muted-foreground">
          同时核对工作状态、历史 Reviewer 决策与整改后的显式重新提交（旧结论不可变，自审不算独立通过）；以下仅统计已加载的 {summary.loaded} 项。
          {queue.hasNextPage?' 还有未加载记录。':''}
         </p>
        </div>
        <Button variant="outline" size="sm" type="button" disabled={queue.isFetching}
          onClick={()=>void queue.refetch()}>
          <RefreshCw aria-hidden="true" className="size-4"/>刷新记录
        </Button>
       </div>
       <div role="group" aria-label="筛选审核队列" className="flex flex-wrap gap-2">
        {tabs.map(tab=>(
         <Button key={tab.value} type="button" size="sm"
          variant={lane===tab.value?'secondary':'ghost'}
          aria-pressed={lane===tab.value}
          onClick={()=>setLane(tab.value)}>{tab.label} <span className="ml-1 tabular-nums">{tab.count}</span></Button>
        ))}
       </div>
       {queue.isPending&&<p role="status" className="text-sm text-muted-foreground">正在读取待审核与整改记录…</p>}
       {queue.isError&&<p role="alert" className="rounded-xl bg-destructive/5 p-4 text-sm text-destructive">{queue.error.message}</p>}
       {queue.isSuccess&&items.length===0&&(
        <div className="flex items-start gap-3 rounded-2xl border border-border bg-card p-6">
         <ClipboardCheck aria-hidden="true" className="mt-1 size-5 shrink-0 text-primary"/>
         <div>
          <p className="text-sm font-semibold">当前已加载范围中没有待审核或 Reviewer 要求整改的任务</p>
          <p className="mt-1 text-xs leading-6 text-muted-foreground">并不意味着任务全部已完成，也不表示审核通过。</p>
         </div>
        </div>
       )}
       {queue.isSuccess&&items.length>0&&visible.length===0&&(
        <div className="rounded-xl border border-border p-6 text-center text-sm text-muted-foreground">
         已加载范围中此分类为空；仍可切回全部或继续加载后续记录。
        </div>
       )}
       <div className="grid gap-3">
        {visible.map(item=>(
         <QueueItemCard key={item.work_item_id} projectID={projectID} item={item}
          canReview={canReview} principals={principals.byID}/>
        ))}
       </div>
       {queue.hasNextPage&&<Button type="button" size="sm" variant="outline"
        className="self-center" disabled={queue.isFetchingNextPage}
        onClick={()=>void queue.fetchNextPage()}>
         {queue.isFetchingNextPage?'读取中…':'加载下一批审核记录'}
       </Button>}
       <p className="text-xs leading-5 text-muted-foreground">
        队列使用有界 offset 分页，刷新后可能因并发状态变化而重排；所有审批操作须在工作项详情按当前版本完成。
        原始 Audit 不属于 work:read 可见信息。
       </p>
        </section>
       </details>
      </>
     ):(
      <p className="flex items-center gap-2 rounded-xl border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
       <ClipboardList aria-hidden="true" className="size-4 shrink-0"/>
       此身份没有 work:read，不加载项目审核队列。Audit-only 主体不能由此获得工作项读取权限。
      </p>
     )}
     <AuditPanel projectID={projectID} enabled={canAudit} canReadWork={canRead}/>
     <p className="flex items-start gap-2 text-xs leading-6 text-muted-foreground">
      <History aria-hidden="true" className="mt-1 size-4 shrink-0"/>
      Review RoleEntry 与 Audit 都是实际操作者、版本、来源和事件的存档记录。
      Reviewer: passed 不等于 WorkItem: done；同人复核不得称为独立批准。
     </p>
    </>
   )}
  </section>
 )
}
