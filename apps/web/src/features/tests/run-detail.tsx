import { useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft, ArrowRight, Bug, ClipboardList, FileUp, History, Plus } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { ErrorText, ResultBadge, TestingNavigation } from '@/features/tests/components'
import { useTestActions, useTestAttempts, useTestAttemptDefects, useTestRun } from '@/features/tests/api'
import { EvidencePanel } from '@/features/evidence/panel'
import type { ResultStatus, TestAttempt, TestRunItem } from '@/features/tests/api'
import { useMyAccess } from '@/features/access/api'
import { principalLabel, useProjectPrincipals } from '@/features/work/principals'
import { statusLabel } from '@/features/work/labels'
import { qualityCounts, filterRunItems, sortByLatestAttention, type QualityFilter } from '@/features/tests/quality'
import { QualityBar, QualityCountsSummary, QualityFilters } from '@/features/tests/quality-view'

function ReportResultDialog({projectID,item}:{projectID:string;item:TestRunItem}){
  const [open,setOpen]=useState(false)
  const [status,setStatus]=useState<ResultStatus>('passed')
  const [note,setNote]=useState('')
  const {recordAttempt}=useTestActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    try{
      await recordAttempt.mutateAsync({itemID:item.id,status,note})
      setNote('');setOpen(false)
    }catch{ /* Show mutation error. */ }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button size="sm" variant="outline"><Plus data-icon="inline-start" aria-hidden="true"/> 记录结果</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>追加测试结果</DialogTitle>
          <DialogDescription>{item.test_case_key} · 固定 v{item.case_version}。此操作只记录外部/人工观察，不执行测试。</DialogDescription>
        </DialogHeader>
        <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
          <FieldGroup>
            <Field><FieldLabel htmlFor="attempt-status">结果</FieldLabel>
              <Select value={status} onValueChange={value=>setStatus(value as ResultStatus)}>
                <SelectTrigger id="attempt-status" className="w-full"><SelectValue /></SelectTrigger>
                <SelectContent><SelectGroup>
                  <SelectItem value="passed">通过 Passed</SelectItem>
                  <SelectItem value="failed">失败 Failed</SelectItem>
                  <SelectItem value="blocked">阻塞 Blocked</SelectItem>
                  <SelectItem value="skipped">跳过 Skipped</SelectItem>
                </SelectGroup></SelectContent>
              </Select>
            </Field>
            <Field><FieldLabel htmlFor="attempt-note">结果描述（可选）</FieldLabel>
              <Textarea id="attempt-note" maxLength={20000} rows={4} value={note}
                onChange={e=>setNote(e.target.value)} placeholder="记录实际观察到的结果，不要填写凭证或机密日志" />
            </Field>
          </FieldGroup>
          <ErrorText message={recordAttempt.error?.message}/>
          <Button disabled={recordAttempt.isPending} type="submit">
            {recordAttempt.isPending?'保存中…':'追加 Attempt（保留历史）'}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function LinkBugDialog({projectID,attempt}:{projectID:string;attempt:TestAttempt}){
  const [open,setOpen]=useState(false)
  const [mode,setMode]=useState<'create'|'existing'>('create')
  const [title,setTitle]=useState('')
  const [existing,setExisting]=useState('')
  const [severity,setSeverity]=useState<'critical'|'major'|'minor'>('major')
  const [link,setLink]=useState<{bug_id:string;bug_key:string}|null>(null)
  const {linkDefect}=useTestActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    try{
      const result=await linkDefect.mutateAsync({
        attemptID:attempt.id,
        ...(mode==='existing'?{bugID:existing.trim()}:{title:title.trim(),severity}),
      })
      setLink(result)
    }catch{ /* Show error. */ }
  }
  return (
    <Dialog open={open} onOpenChange={next=>{setOpen(next);if(!next)setLink(null)}}>
      <DialogTrigger asChild><Button size="sm" variant="outline"><Bug data-icon="inline-start" aria-hidden="true"/> 关联缺陷</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>失败记录关联 Bug</DialogTitle>
          <DialogDescription>只有 Failed Attempt 可以关联 Bug。后续 Passed 不会抹除这条失败证据。</DialogDescription>
        </DialogHeader>
        {link?(
          <div className="flex flex-col gap-3 text-sm">
            <p>已关联缺陷：{link.bug_key}</p>
            <Link to={`/projects/${projectID}/items/${link.bug_id}`}
              className="font-medium underline underline-offset-4">打开 {link.bug_key}</Link>
          </div>
        ):(
          <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
            <FieldGroup>
              <Field><FieldLabel htmlFor="bug-link-mode">方式</FieldLabel>
                <Select value={mode} onValueChange={value=>setMode(value as typeof mode)}>
                  <SelectTrigger id="bug-link-mode" className="w-full"><SelectValue /></SelectTrigger>
                  <SelectContent><SelectGroup>
                    <SelectItem value="create">创建并关联新 Bug</SelectItem>
                    <SelectItem value="existing">关联已有 Bug UUID</SelectItem>
                  </SelectGroup></SelectContent>
                </Select>
              </Field>
              {mode==='create'?(
                <>
                  <Field><FieldLabel htmlFor="failed-bug-title">Bug 标题</FieldLabel>
                    <Input id="failed-bug-title" required maxLength={240} value={title}
                      onChange={e=>setTitle(e.target.value)} placeholder="描述检测到的实际缺陷" />
                  </Field>
                  <Field><FieldLabel htmlFor="failed-bug-severity">严重程度</FieldLabel>
                    <Select value={severity} onValueChange={value=>setSeverity(value as typeof severity)}>
                      <SelectTrigger id="failed-bug-severity" className="w-full"><SelectValue /></SelectTrigger>
                      <SelectContent><SelectGroup>
                        <SelectItem value="minor">一般</SelectItem>
                        <SelectItem value="major">主要</SelectItem>
                        <SelectItem value="critical">严重</SelectItem>
                      </SelectGroup></SelectContent>
                    </Select>
                  </Field>
                </>
              ):(
                <Field><FieldLabel htmlFor="existing-bug-id">已有 Bug UUID</FieldLabel>
                  <Input id="existing-bug-id" required value={existing} onChange={e=>setExisting(e.target.value)} />
                </Field>
              )}
            </FieldGroup>
            <ErrorText message={linkDefect.error?.message}/>
            <Button type="submit" disabled={linkDefect.isPending||(mode==='create'?!title.trim():!existing.trim())}>
              {linkDefect.isPending?'正在关联…':'保存缺陷关系'}
            </Button>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

function DefectTrace({projectID,attempt,canLink}:{projectID:string;attempt:TestAttempt;canLink:boolean}){
  const [open,setOpen]=useState(false)
  const query=useTestAttemptDefects(projectID,attempt.id,open)
  return (
    <div className="flex flex-col gap-2">
      <Button type="button" size="sm" variant="outline" className="self-start"
        aria-expanded={open} onClick={()=>setOpen(current=>!current)}>
        <Bug aria-hidden="true" className="size-4"/>
        {open?'收起缺陷关系':'查看此失败 Attempt 的关联 Bug'}
      </Button>
      {open&&(
        <div className="flex flex-col gap-3 rounded-xl border border-border bg-card p-3">
          {query.isPending&&<p role="status" className="text-xs text-muted-foreground">正在读取已归档的缺陷关系…</p>}
          {query.isError&&<ErrorText message={query.error.message}/>}
          {query.isSuccess&&query.data.length===0&&<p className="text-xs text-muted-foreground">此 Attempt 暂无关联 Bug；这不代表失败已解决。</p>}
          {query.data?.map(link=>(
            <Link to={`/projects/${projectID}/items/${link.bug_id}`} key={link.bug_id}
              className="flex flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border border-border px-3 py-2 text-sm hover:border-primary/35 hover:bg-accent/30">
              <Bug aria-hidden="true" className="size-4 text-rose-600"/>
              <span className="font-mono text-xs font-semibold text-primary">{link.bug_key}</span>
              <span className="min-w-0 flex-1 break-words">{link.bug_title}</span>
              <span className="text-xs text-muted-foreground">{statusLabel(link.bug_status)}</span>
              <ArrowRight aria-hidden="true" className="size-4 text-muted-foreground"/>
            </Link>
          ))}
          {canLink&&<LinkBugDialog projectID={projectID} attempt={attempt}/>}
          {query.isSuccess&&<p className="text-xs leading-5 text-muted-foreground">关联记录不可变；Bug 标题与状态来自当前工作项。稍后通过的 Attempt 不删除旧关联。</p>}
        </div>
      )}
    </div>
  )
}

function ResultHistory({projectID,item,canLink}:{projectID:string;item:TestRunItem;canLink:boolean}){
  const [expanded,setExpanded]=useState(false)
  const query=useTestAttempts(projectID,item.id,expanded)
  const principals=useProjectPrincipals(projectID,query.data?.map(attempt=>attempt.reported_by)??[])
  return (
    <div className="flex flex-col gap-3">
      <Button size="sm" type="button" variant="ghost" className="self-start" aria-expanded={expanded} onClick={()=>setExpanded(value=>!value)}>
        <History data-icon="inline-start" aria-hidden="true"/>
        {expanded?'收起历史':`查看 Attempt 历史（${item.attempt_count}）`}
      </Button>
      {expanded&&(
        <div className="flex flex-col gap-3 rounded-lg bg-muted/25 p-3">
          {query.isPending&&<p className="text-xs text-muted-foreground">读取上报历史…</p>}
          {query.isError&&<ErrorText message={query.error.message}/>}
          {query.isSuccess&&!query.data.length&&<p className="text-xs text-muted-foreground">尚未上报结果</p>}
          {query.data?.map(attempt=>(
            <div key={attempt.id} className="flex flex-col gap-2 border-b border-border pb-3 last:border-b-0 last:pb-0">
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
                <span>Attempt {attempt.attempt_no}</span>
                <ResultBadge status={attempt.status}/>
                <span>{attempt.source_kind} · {new Date(attempt.ingested_at).toLocaleString()}</span>
                <span title={attempt.reported_by}>上报者：{principalLabel(attempt.reported_by,principals.byID.get(attempt.reported_by),true)}</span>
              </div>
              {attempt.note&&<p className="whitespace-pre-wrap break-words text-sm">{attempt.note}</p>}
              {attempt.status==='failed'&&<DefectTrace projectID={projectID} attempt={attempt} canLink={canLink}/>}
            </div>
          ))}
        </div>
      )}
    </div>
  )
}

function ImportJUnitDialog({projectID,runID}:{projectID:string;runID:string}){
  const [open,setOpen]=useState(false)
  const [file,setFile]=useState<File|null>(null)
  const {importJUnit}=useTestActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    if(!file)return
    try{await importJUnit.mutateAsync({runID,file})}catch{ /* Show error. */ }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button variant="outline"><FileUp data-icon="inline-start" aria-hidden="true"/> 导入 JUnit XML</Button></DialogTrigger>
      <DialogContent className="sm:max-w-lg">
        <DialogHeader><DialogTitle>导入外部 JUnit XML</DialogTitle>
          <DialogDescription>只登记现有 RunItem 的外部结果，绝不启动 CI、Agent 或测试执行器。</DialogDescription>
        </DialogHeader>
        <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
          <FieldGroup><Field>
            <FieldLabel htmlFor="junit-file">报告文件</FieldLabel>
            <Input id="junit-file" type="file" accept=".xml,application/xml" required
              onChange={event=>{setFile(event.target.files?.[0]??null);importJUnit.reset()}} />
            <FieldDescription>最大 1 MiB、500 个 testcase。每个 testcase 需要属性 qual.case_key 对应已绑定的用例编号；
              未知标识将拒绝整份报告。原始 XML 暂存数据库，正式 Artifact 存储在后续阶段。</FieldDescription>
          </Field></FieldGroup>
          <ErrorText message={importJUnit.error?.message}/>
          {importJUnit.isSuccess&&(
            <p role="status" className="text-sm">
              已导入 {importJUnit.data.imported_count} 条：通过 {importJUnit.data.passed}、失败 {importJUnit.data.failed}、跳过 {importJUnit.data.skipped}
            </p>
          )}
          <Button type="submit" disabled={!file||importJUnit.isPending}>
            {importJUnit.isPending?'导入中…':'保存报告结果'}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function TestRunDetailPage(){
  const {projectID='',runID=''}=useParams()
  const query=useTestRun(projectID,runID)
  const access=useMyAccess(projectID)
  const permissions=access.data?.effective_permissions??[]
  const canRecord=permissions.includes('test:write')
  const canLink=permissions.includes('work:write')&&permissions.includes('test:read')&&access.data?.kind!=='integration'
  const [filter,setFilter]=useState<QualityFilter>('all')
  const run=query.data
  const counts=run?qualityCounts(run.items):null
  const shown=run?sortByLatestAttention(filterRunItems(run.items,filter)):[]
  return (
    <section className="flex flex-col gap-6">
      <TestingNavigation projectID={projectID}/>
      <Link className="flex items-center gap-2 text-sm text-muted-foreground hover:underline"
        to={`/projects/${projectID}/tests/runs`}>
        <ArrowLeft aria-hidden="true" className="size-4"/> 返回运行列表
      </Link>
      {query.isPending&&<p role="status" className="text-sm text-muted-foreground">读取 TestRun…</p>}
      {query.isError&&<ErrorText message={query.error.message}/>}
      {run&&(
        <>
          <header className="flex flex-wrap items-start justify-between gap-4">
            <div className="flex flex-col gap-2">
              <p className="text-xs font-semibold tracking-wider text-primary">QUALITY / TEST RUN</p>
              <h1 className="max-w-3xl break-words text-2xl font-semibold tracking-tight sm:text-3xl">{run.name}</h1>
              <p className="text-xs leading-6 text-muted-foreground">
                外部来源 {run.source_system} · {run.environment||'环境未登记'} · 创建于 {new Date(run.created_at).toLocaleString()}
              </p>
              <p className="text-xs text-muted-foreground">Qual 记录外部或人工上报的测试事实，不执行测试、Agent 或 CI。</p>
            </div>
            {canRecord&&<ImportJUnitDialog projectID={projectID} runID={runID}/>}
            {access.isSuccess&&!canRecord&&<p className="text-xs text-muted-foreground">只读 · 导入报告需要 test:write 权限</p>}
          </header>
          {counts&&(
            <section className="flex flex-col gap-4" aria-label="最新结果质量概览">
              <div className="flex flex-wrap items-end justify-between gap-3">
                <div>
                  <h2 className="text-base font-semibold">最新结果概览</h2>
                  <p className="mt-1 text-xs leading-5 text-muted-foreground">统计当前 Run 内全部 {counts.total} 个用例的最新上报状态；历史失败请展开 Attempt 查看。</p>
                </div>
                <span className="rounded-lg bg-secondary px-3 py-2 text-xs font-semibold text-secondary-foreground">已上报 {counts.reported}/{counts.total}</span>
              </div>
              <QualityCountsSummary counts={counts}/>
              <QualityBar counts={counts}/>
              <p className="text-xs text-muted-foreground">“未上报”并非“跳过”，后续新 Attempt 不会覆盖先前失败记录。</p>
            </section>
          )}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2 text-base">
                <ClipboardList aria-hidden="true" className="size-4"/> 用例结果与失败定位
              </CardTitle>
              <CardDescription>先处理失败、阻塞或未上报项；固定发布版本和 SHA-256 可以按需展开核验。</CardDescription>
            </CardHeader>
            <CardContent className="flex flex-col gap-5">
              {counts&&<QualityFilters value={filter} onChange={setFilter} counts={counts}/>}
              {shown.map(item=>(
                <div key={item.id} className="flex flex-col gap-3 rounded-xl border border-border bg-card p-4">
                  <div className="flex flex-wrap items-start justify-between gap-3">
                    <div className="flex min-w-0 flex-col gap-2">
                      <Link className="font-mono text-sm font-semibold text-primary hover:underline"
                        to={`/projects/${projectID}/tests/cases/${item.test_case_id}`}>
                        {item.test_case_key}
                      </Link>
                      <p className="text-xs text-muted-foreground">已发布用例 v{item.case_version} · {item.attempt_count} 次 Attempt</p>
                      <details className="text-xs text-muted-foreground">
                        <summary className="w-fit cursor-pointer py-1 hover:text-foreground">固定版本与 SHA-256</summary>
                        <p className="mt-2 break-all font-mono leading-5">CaseVersion ID: {item.case_version_id}</p>
                        <p className="mt-1 break-all font-mono leading-5">SHA-256: {item.snapshot_sha256}</p>
                      </details>
                    </div>
                    <div className="flex flex-wrap items-center gap-2">
                      <ResultBadge status={item.latest_status}/>
                      {canRecord&&<ReportResultDialog projectID={projectID} item={item}/>}
                    </div>
                  </div>
                  <ResultHistory projectID={projectID} item={item} canLink={canLink}/>
                </div>
              ))}
              {run.items.length===0&&<p className="text-sm text-muted-foreground">没有 RunItem。Qual 不会自动执行或生成结果。</p>}
              {run.items.length>0&&shown.length===0&&<p className="rounded-xl bg-muted/40 px-5 py-8 text-center text-sm text-muted-foreground">当前最新结果没有匹配项；旧失败仍可通过其他筛选条件展开历史核查。</p>}
            </CardContent>
          </Card>
          <EvidencePanel projectID={projectID} targetKind="test_run" targetID={runID}/>
          <details className="rounded-xl border border-border bg-card p-4 text-xs text-muted-foreground">
            <summary className="cursor-pointer font-medium">运行来源及技术标识</summary>
            <p className="mt-3 break-all">Run UUID：{run.id}</p>
            <p className="mt-2 break-all">Build / Commit：{run.build_ref||'未指定'}</p>
            <p className="mt-2 break-all">外部运行键：{run.external_run_key||'未指定'}</p>
          </details>
        </>
      )}
    </section>
  )
}
