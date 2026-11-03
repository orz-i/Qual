import { useState, type FormEvent } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ArrowLeft, CheckCircle2, FilePlus2, FolderPlus, Link2, Plus, Search } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useSession } from '@/features/auth/session'
import { ErrorText, SpecificationForm, TestingNavigation } from '@/features/tests/components'
import { useCaseCoverage, useSuites, useTestActions, useTestCase, useTestCases, useVersions } from '@/features/tests/api'
import type { TestSpecification } from '@/features/tests/api'

export function TestsHomePage(){
  const {data:session}=useSession()
  return (
    <section className="flex max-w-4xl flex-col gap-5">
      <div className="flex flex-col gap-2">
        <h1 className="text-2xl font-semibold">测试管理</h1>
        <p className="text-sm text-muted-foreground">测试规范、外部执行结果与缺陷历史分离保存。Qual 不执行测试。</p>
      </div>
      <div className="grid gap-3 sm:grid-cols-2">
        {session?.projects.map(project=>(
          <Link key={project.id} to={`/projects/${project.id}/tests/cases`}
            className="rounded-xl border p-5 hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-ring">
            <span className="font-medium">{project.key}</span>
            <p className="mt-2 text-sm text-muted-foreground">用例库、测试运行与质量追踪</p>
          </Link>
        ))}
      </div>
      {!session?.projects.length && <p className="text-sm text-muted-foreground">没有可访问项目，请由本机管理员授权。</p>}
    </section>
  )
}

function CreateSuiteDialog({projectID}:{projectID:string}){
  const [open,setOpen]=useState(false)
  const [name,setName]=useState('')
  const {createSuite}=useTestActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    try{await createSuite.mutateAsync(name.trim());setOpen(false);setName('')}catch{ /* Error shown below. */ }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button size="sm" variant="outline"><FolderPlus data-icon="inline-start" aria-hidden="true"/> 新建套件</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>新建测试套件</DialogTitle><DialogDescription>用于组织项目内测试用例，不执行测试。</DialogDescription></DialogHeader>
        <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
          <FieldGroup><Field><FieldLabel htmlFor="suite-name">套件名称</FieldLabel>
            <Input id="suite-name" required maxLength={160} value={name} onChange={e=>setName(e.target.value)} />
          </Field></FieldGroup>
          <ErrorText message={createSuite.error?.message}/>
          <Button disabled={createSuite.isPending||!name.trim()} type="submit">创建套件</Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function CreateCaseDialog({projectID}:{projectID:string}){
  const [open,setOpen]=useState(false)
  const {data:suites=[]}=useSuites(projectID)
  const {createCase}=useTestActions(projectID)
  const navigate=useNavigate()
  const submit=async(spec:TestSpecification,suiteID:string|null)=>{
    const result=await createCase.mutateAsync({specification:spec,suite_id:suiteID})
    setOpen(false)
    navigate(`/projects/${projectID}/tests/cases/${result.id}`)
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button size="sm"><Plus data-icon="inline-start" aria-hidden="true"/> 新建用例</Button></DialogTrigger>
      <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>新建测试用例</DialogTitle>
          <DialogDescription>创建可编辑草稿，需明确发布版本后才能加入 TestRun。</DialogDescription>
        </DialogHeader>
        <SpecificationForm suites={suites} onSubmit={submit} pending={createCase.isPending}
          error={createCase.error?.message} submitLabel="创建草稿"/>
      </DialogContent>
    </Dialog>
  )
}

export function TestCasesPage(){
  const {projectID=''}=useParams()
  const [filter,setFilter]=useState('')
  const cases=useTestCases(projectID)
  const suites=useSuites(projectID)
  const rows=(cases.data??[]).filter(item=>(item.key+' '+item.draft.title).toLowerCase().includes(filter.toLowerCase()))
  return (
    <section className="flex flex-col gap-6">
      <TestingNavigation projectID={projectID}/>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-semibold tracking-tight">测试用例库</h1>
          <p className="text-sm text-muted-foreground">可编辑草稿与不可变发布版本分离。仅记录测试规范。</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <CreateSuiteDialog projectID={projectID}/>
          <CreateCaseDialog projectID={projectID}/>
        </div>
      </header>
      <div className="relative max-w-sm">
        <Search aria-hidden="true" className="pointer-events-none absolute left-2.5 top-2.5 size-4 text-muted-foreground"/>
        <Input aria-label="筛选当前用例" placeholder="查找用例编号或标题" className="pl-9"
          value={filter} onChange={e=>setFilter(e.target.value)}/>
      </div>
      {suites.data?.length ? <p className="text-xs text-muted-foreground">套件：{suites.data.map(s=>s.name).join(' · ')}</p>:null}
      {cases.isPending&&<p role="status" className="text-sm text-muted-foreground">正在读取用例库…</p>}
      {cases.isError&&<ErrorText message={cases.error.message}/>}
      {cases.isSuccess&&rows.length===0&&(
        <Card><CardHeader><CardTitle>暂无匹配的用例</CardTitle>
          <CardDescription>请创建并发布测试规范，再从外部测试工具导入结果。</CardDescription>
        </CardHeader></Card>
      )}
      {rows.length>0&&(
        <div className="divide-y overflow-hidden rounded-xl border">
          {rows.map(item=>(
            <Link to={`/projects/${projectID}/tests/cases/${item.id}`} key={item.id}
              className="flex flex-wrap items-center gap-3 p-3.5 hover:bg-muted/40 focus-visible:outline-2 focus-visible:outline-ring">
              <span className="w-25 shrink-0 font-mono text-xs text-muted-foreground">{item.key}</span>
              <span className="min-w-35 flex-1 text-sm font-medium">{item.draft.title}</span>
              <Badge variant={item.published_version>0?'secondary':'outline'}>
                {item.published_version>0?`已发布 v${item.published_version}`:'草稿未发布'}
              </Badge>
              <span className="text-xs text-muted-foreground">修订 {item.revision}</span>
            </Link>
          ))}
        </div>
      )}
      <p className="text-xs text-muted-foreground">当前列表最多返回 500 条。完整分页和高级搜索属于后续增强。</p>
    </section>
  )
}

function LinkWorkDialog({projectID,caseID}:{projectID:string;caseID:string}){
  const {linkCoverage}=useTestActions(projectID)
  const [open,setOpen]=useState(false)
  const [id,setID]=useState('')
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    try{await linkCoverage.mutateAsync({caseID,workID:id.trim()});setOpen(false);setID('')}catch{ /* Display error. */ }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button size="sm" variant="outline"><Link2 data-icon="inline-start" aria-hidden="true"/> 关联工作项</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>建立用例覆盖关系</DialogTitle>
          <DialogDescription>关联当前项目中已有 Task/Bug 的 UUID，保持来源可追踪。</DialogDescription></DialogHeader>
        <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
          <FieldGroup><Field><FieldLabel htmlFor="coverage-work-item">WorkItem UUID</FieldLabel>
            <Input id="coverage-work-item" value={id} onChange={e=>setID(e.target.value)}
              required placeholder="从工作项详情 URL 复制 UUID" />
          </Field></FieldGroup>
          <ErrorText message={linkCoverage.error?.message}/>
          <Button type="submit" disabled={linkCoverage.isPending||!id.trim()}>{linkCoverage.isPending?'正在关联…':'保存关联'}</Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function TestCaseDetailPage(){
  const {projectID='',caseID=''}=useParams()
  const query=useTestCase(projectID,caseID)
  const {data:suites=[]}=useSuites(projectID)
  const versions=useVersions(projectID,caseID)
  const coverage=useCaseCoverage(projectID,caseID)
  const {updateCase,publishCase}=useTestActions(projectID)
  const [editOpen,setEditOpen]=useState(false)
  const [selectedVersion,setSelectedVersion]=useState<string|null>(null)
  const c=query.data
  const latest=versions.data?.[0]
  const selected=versions.data?.find(v=>v.id===selectedVersion)??latest
  const canPublish=!!c&&(!latest||latest.source_revision<c.revision)
  const save=async(spec:TestSpecification,suiteID:string|null)=>{
    if(!c)return
    await updateCase.mutateAsync({caseID,revision:c.revision,specification:spec,suiteID})
    setEditOpen(false)
  }
  return (
    <section className="flex flex-col gap-6">
      <TestingNavigation projectID={projectID}/>
      <Link to={`/projects/${projectID}/tests/cases`} className="flex items-center gap-2 text-sm text-muted-foreground hover:underline">
        <ArrowLeft aria-hidden="true" className="size-4"/> 返回用例库
      </Link>
      {query.isPending&&<p role="status">加载用例中…</p>}
      {query.isError&&<ErrorText message={query.error.message}/>}
      {c&&(
        <>
          <header className="flex flex-wrap items-start justify-between gap-4">
            <div className="flex flex-col gap-2">
              <span className="font-mono text-xs text-muted-foreground">{c.key}</span>
              <h1 className="text-2xl font-semibold tracking-tight">{c.draft.title}</h1>
              <p className="text-sm text-muted-foreground">当前草稿修订 {c.revision} · {c.published_version>0?`已发布 v${c.published_version}`:'未发布'}</p>
            </div>
            <div className="flex flex-wrap gap-2">
              <Button size="sm" variant="outline" onClick={()=>setEditOpen(true)}><FilePlus2 data-icon="inline-start" aria-hidden="true"/> 编辑草稿</Button>
              <Button size="sm" disabled={!canPublish||publishCase.isPending}
                onClick={()=>void publishCase.mutateAsync({caseID,revision:c.revision}).catch(()=>{})}>
                <CheckCircle2 data-icon="inline-start" aria-hidden="true"/>
                {publishCase.isPending?'发布中…':canPublish?'发布新版本':'已是最新发布'}
              </Button>
            </div>
          </header>
          <ErrorText message={publishCase.error?.message}/>
          <Dialog open={editOpen} onOpenChange={setEditOpen}>
            <DialogContent className="max-h-[90vh] overflow-y-auto sm:max-w-2xl">
              <DialogHeader><DialogTitle>编辑草稿 {c.key}</DialogTitle>
                <DialogDescription>旧的已发布版本不会受影响。</DialogDescription></DialogHeader>
              <SpecificationForm key={c.revision} initial={c.draft} initialSuiteID={c.suite_id}
                suites={suites} onSubmit={save} pending={updateCase.isPending} error={updateCase.error?.message} />
            </DialogContent>
          </Dialog>
          <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_270px]">
            <div className="flex min-w-0 flex-col gap-5">
              <Card>
                <CardHeader><CardTitle>当前草稿</CardTitle><CardDescription>只修改草稿；发布会创建新的只读规范。</CardDescription></CardHeader>
                <CardContent className="flex flex-col gap-4 text-sm">
                  <p className="whitespace-pre-wrap">{c.draft.preconditions||'无前置条件'}</p>
                  <ol className="flex list-decimal flex-col gap-3 pl-5">
                    {c.draft.steps?.map((step,i)=><li key={i}>
                      <p className="font-medium">{step.action}</p>
                      {step.expected&&<p className="mt-1 text-muted-foreground">预期：{step.expected}</p>}
                    </li>)}
                  </ol>
                  <p className="whitespace-pre-wrap text-muted-foreground">{c.draft.expected_result||'未填写整体预期'}</p>
                </CardContent>
              </Card>
              <Card>
                <CardHeader><CardTitle>不可变发布历史</CardTitle>
                  <CardDescription>运行记录固定引用具体 CaseVersion ID 和 SHA-256。</CardDescription></CardHeader>
                <CardContent className="flex flex-col gap-3">
                  {versions.isPending&&<p className="text-sm text-muted-foreground">读取历史版本…</p>}
                  {versions.isError&&<ErrorText message={versions.error.message}/>}
                  {!versions.data?.length&&<p className="text-sm text-muted-foreground">尚未发布版本</p>}
                  <div className="flex flex-wrap gap-2">
                    {versions.data?.map(v=><Button key={v.id} variant={selected?.id===v.id?'secondary':'outline'}
                      size="sm" onClick={()=>setSelectedVersion(v.id)}>v{v.version}</Button>)}
                  </div>
                  {selected&&<div className="rounded-lg bg-muted/30 p-4 text-sm">
                    <h3 className="font-medium">{selected.specification.title}</h3>
                    <p className="mt-2 text-muted-foreground">源修订 {selected.source_revision} · 发布于 {new Date(selected.published_at).toLocaleString()}</p>
                    <p className="mt-2 break-all font-mono text-xs text-muted-foreground">SHA-256: {selected.sha256}</p>
                    <p className="mt-3 whitespace-pre-wrap">{selected.specification.expected_result}</p>
                    <ol className="mt-2 list-decimal pl-5">{selected.specification.steps?.map((step,i)=><li key={i}>{step.action} — {step.expected}</li>)}</ol>
                  </div>}
                </CardContent>
              </Card>
            </div>
            <aside className="flex flex-col gap-4">
              <Card>
                <CardHeader><CardTitle>需求覆盖</CardTitle><CardDescription>关联当前项目的任务或缺陷。</CardDescription></CardHeader>
                <CardContent className="flex flex-col gap-3">
                  <LinkWorkDialog projectID={projectID} caseID={caseID}/>
                  {coverage.isError&&<ErrorText message={coverage.error.message}/>}
                  {coverage.data?.map(item=><Link key={item.id}
                    className="rounded-lg border p-2.5 text-sm hover:bg-muted/40"
                    to={`/projects/${projectID}/items/${item.id}`}>
                    <span className="text-xs text-muted-foreground">{item.key}</span>
                    <p className="mt-1 font-medium">{item.title}</p>
                  </Link>)}
                  {!coverage.data?.length&&<p className="text-xs text-muted-foreground">暂无关联工作项</p>}
                </CardContent>
              </Card>
            </aside>
          </div>
        </>
      )}
    </section>
  )
}
