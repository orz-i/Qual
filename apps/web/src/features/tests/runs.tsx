import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ArrowRight, FilePlay, Plus, ClipboardCheck, Search } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Checkbox } from '@/components/ui/checkbox'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { ErrorText, TestingNavigation } from '@/features/tests/components'
import { getLatestVersion, useTestActions, useTestCases, useTestRuns } from '@/features/tests/api'
import { useMyAccess } from '@/features/access/api'
import { qualityCounts } from '@/features/tests/quality'
import { QualityBar, QualityCompactCounts } from '@/features/tests/quality-view'

function CreateRunDialog({projectID}:{projectID:string}){
  const cases=useTestCases(projectID)
  const {createRun}=useTestActions(projectID)
  const [open,setOpen]=useState(false)
  const [name,setName]=useState('')
  const [environment,setEnvironment]=useState('')
  const [buildRef,setBuildRef]=useState('')
  const [selected,setSelected]=useState<string[]>([])
  const [localError,setLocalError]=useState('')
  const navigate=useNavigate()
  const ready=(cases.data??[]).filter(c=>c.published_version>0)
  const toggle=(id:string,checked:boolean)=>{
    setSelected(old=>checked?[...old,id]:old.filter(x=>x!==id))
  }
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    if(!selected.length){setLocalError('请选择至少一个已发布的用例。');return}
    setLocalError('')
    try{
      const versions=await Promise.all(selected.map(caseID=>getLatestVersion(projectID,caseID)))
      const run=await createRun.mutateAsync({
        name:name.trim(),source_system:'manual',environment,build_ref:buildRef,
        case_version_ids:versions.map(v=>v.id),
      })
      setOpen(false)
      navigate(`/projects/${projectID}/tests/runs/${run.id}`)
    }catch(error){
      setLocalError(error instanceof Error?error.message:'创建运行失败')
    }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button><Plus data-icon="inline-start" aria-hidden="true"/> 新建运行记录</Button></DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-xl">
        <DialogHeader><DialogTitle>登记 TestRun</DialogTitle><DialogDescription>
          创建一份用于接收外部测试结果的运行记录。不会启动或执行任何测试。
        </DialogDescription></DialogHeader>
        <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-5">
          <FieldGroup>
            <Field><FieldLabel htmlFor="run-name">运行名称</FieldLabel>
              <Input id="run-name" required maxLength={240} value={name} onChange={e=>setName(e.target.value)}
                placeholder="例如：Sprint 4 回归验证" />
            </Field>
            <Field><FieldLabel htmlFor="run-env">环境（可选）</FieldLabel>
              <Input id="run-env" maxLength={2000} value={environment} onChange={e=>setEnvironment(e.target.value)}
                placeholder="例如：staging / Linux" />
            </Field>
            <Field><FieldLabel htmlFor="run-build">Commit / Build（可选）</FieldLabel>
              <Input id="run-build" maxLength={240} value={buildRef} onChange={e=>setBuildRef(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>选择用例版本</FieldLabel>
              <FieldDescription>提交时绑定各已选用例的最新发布版本；历史版本后续不会被覆盖。</FieldDescription>
              <div className="max-h-60 overflow-y-auto rounded-lg border">
                {ready.map(item=>(
                  <label key={item.id} className="flex cursor-pointer items-center gap-3 border-b p-3 text-sm last:border-b-0">
                    <Checkbox aria-label={`选择 ${item.key}`} checked={selected.includes(item.id)}
                      onCheckedChange={value=>toggle(item.id,value===true)}/>
                    <span className="min-w-0 flex-1 truncate">{item.key} · {item.draft.title}</span>
                    <Badge variant="outline">v{item.published_version}</Badge>
                  </label>
                ))}
                {!ready.length&&<p className="p-4 text-sm text-muted-foreground">暂无已发布用例，请先去用例库发布。</p>}
              </div>
            </Field>
          </FieldGroup>
          <ErrorText message={localError||createRun.error?.message}/>
          <Button disabled={!name.trim()||!selected.length||createRun.isPending} type="submit">
            {createRun.isPending?'正在登记…':`创建运行记录（${selected.length} 项）`}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function TestRunsPage(){
  const {projectID=''}=useParams()
  const runs=useTestRuns(projectID)
  const access=useMyAccess(projectID)
  const canWrite=access.data?.effective_permissions.includes('test:write')??false
  const [focus,setFocus]=useState<'all'|'failed'|'unreported'>('all')
  const displayed=(runs.data??[]).filter(run=>{
    const counts=qualityCounts(run.items)
    return focus==='all'||counts[focus]>0
  })
  return (
    <section className="flex flex-col gap-6">
      <TestingNavigation projectID={projectID}/>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex flex-col gap-2">
          <p className="text-xs font-semibold tracking-wider text-primary">QUALITY / RUNS</p>
          <h1 className="text-2xl font-semibold tracking-tight sm:text-3xl">测试运行</h1>
          <p className="text-sm leading-6 text-muted-foreground">从外部报告的最新结果查看质量信号，并追溯不可变的 Attempt 和失败证据。Qual 不执行测试。</p>
        </div>
        {canWrite&&<CreateRunDialog projectID={projectID}/>}
        {access.isSuccess&&!canWrite&&<p className="self-center text-xs text-muted-foreground">只读 · 登记测试运行需要 test:write 权限</p>}
      </header>
      {runs.isPending&&<p role="status" className="text-sm text-muted-foreground">正在读取 TestRun…</p>}
      {runs.isError&&<ErrorText message={runs.error.message}/>}
      {runs.isSuccess&&!runs.data.length&&(
        <Card><CardHeader><CardTitle>尚无测试运行</CardTitle>
          <CardDescription>请先发布用例，然后创建一份运行记录供外部报告使用。</CardDescription>
        </CardHeader></Card>
      )}
      {runs.isSuccess&&runs.data.length>0&&(
        <div className="flex flex-wrap items-center justify-between gap-3 rounded-2xl border border-border bg-card px-4 py-3 sm:px-5">
          <div className="flex items-center gap-2 text-xs text-muted-foreground">
            <ClipboardCheck aria-hidden="true" className="size-4 text-primary" />
            已加载 {runs.data.length} 次运行
          </div>
          <div role="group" aria-label="过滤已加载的测试运行" className="flex flex-wrap items-center gap-1.5">
            {([
              {key:'all',label:'全部'},
              {key:'failed',label:'有失败'},
              {key:'unreported',label:'有未上报'},
            ] as const).map(option=>(
              <Button type="button" size="sm" key={option.key}
                variant={focus===option.key?'secondary':'ghost'}
                aria-pressed={focus===option.key}
                onClick={()=>setFocus(option.key)}>{option.label}</Button>
            ))}
          </div>
        </div>
      )}
      {runs.isSuccess&&runs.data.length>0&&displayed.length===0&&(
        <div className="flex flex-col items-center gap-2 rounded-2xl border border-border bg-card px-5 py-10 text-center">
          <Search aria-hidden="true" className="size-5 text-primary"/>
          <p className="text-sm font-semibold">已加载运行中没有匹配项</p>
          <p className="text-xs text-muted-foreground">仅按当前最新状态筛选，不代表历史上没有失败。</p>
          <Button variant="outline" size="sm" onClick={()=>setFocus('all')}>查看全部</Button>
        </div>
      )}
      <div className="grid gap-3">
        {displayed.map(run=>{
          const counts=qualityCounts(run.items)
          return <Link key={run.id} to={`/projects/${projectID}/tests/runs/${run.id}`}
            className="group flex min-w-0 flex-col gap-4 rounded-2xl border border-border bg-card px-4 py-4 transition-colors hover:border-primary/30 hover:bg-accent/25 focus-visible:outline-2 focus-visible:outline-ring sm:px-5">
            <div className="flex min-w-0 items-start gap-3">
              <span className="flex size-10 shrink-0 items-center justify-center rounded-xl bg-secondary text-primary">
                <FilePlay aria-hidden="true" className="size-5"/>
              </span>
              <div className="min-w-0 flex-1">
                <h2 className="break-words text-sm font-semibold group-hover:text-primary">{run.name}</h2>
                <p className="mt-1 text-xs leading-5 text-muted-foreground">
                  {run.source_system} · {run.environment||'环境未登记'} · {new Date(run.created_at).toLocaleString()}
                </p>
              </div>
              {counts.failed>0&&<Badge variant="destructive" className="shrink-0">最新失败 {counts.failed}</Badge>}
              <ArrowRight aria-hidden="true" className="size-4 shrink-0 text-muted-foreground"/>
            </div>
            <QualityBar counts={counts}/>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <QualityCompactCounts counts={counts}/>
              <span className="text-xs text-muted-foreground">已上报 {counts.reported}/{counts.total}</span>
            </div>
          </Link>
        })}
      </div>
      <p className="text-xs leading-5 text-muted-foreground">仅显示服务端最近 100 次运行；筛选只作用于已加载记录。每个统计依据 RunItem 的最新上报状态，历史失败仍保留在 Attempt 中。导入报告不会触发 CI 或测试进程。</p>
    </section>
  )
}
