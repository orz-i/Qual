import { useState, type FormEvent } from 'react'
import { Link, NavLink } from 'react-router'
import { Plus, Trash2 } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Field, FieldGroup, FieldLabel, FieldDescription } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import type { ResultStatus, TestSpecification, TestSuite } from '@/features/tests/api'

export function TestingNavigation({ projectID }: { projectID: string }) {
  const base=`/projects/${projectID}/tests`
  return (
    <nav aria-label="测试管理" className="flex flex-wrap items-center gap-2 border-b pb-4">
      <NavLink to={`${base}/cases`} className={({isActive})=>isActive
        ? 'rounded-lg bg-secondary px-3 py-2 text-sm font-medium'
        : 'rounded-lg px-3 py-2 text-sm text-muted-foreground hover:bg-muted'}>测试用例</NavLink>
      <NavLink to={`${base}/runs`} className={({isActive})=>isActive
        ? 'rounded-lg bg-secondary px-3 py-2 text-sm font-medium'
        : 'rounded-lg px-3 py-2 text-sm text-muted-foreground hover:bg-muted'}>测试运行</NavLink>
      <Link className="ml-auto text-xs text-muted-foreground hover:underline" to={`/projects/${projectID}`}>返回工作看板</Link>
    </nav>
  )
}
export function ErrorText({message}:{message?:string}){
  return message?<p role="alert" className="rounded-lg border border-destructive/25 bg-destructive/5 p-3 text-sm text-destructive">{message}</p>:null
}
export function ResultBadge({status}:{status:ResultStatus|null|undefined}){
  const label:Record<ResultStatus,string>={passed:'通过',failed:'失败',blocked:'阻塞',skipped:'跳过'}
  if(!status)return <Badge variant="outline">未上报</Badge>
  return <Badge variant={status==='failed'?'destructive':status==='passed'?'secondary':'outline'}>{label[status]}</Badge>
}

export function SpecificationForm({
  initial,initialSuiteID,suites,pending,error,onSubmit,submitLabel='保存草稿',
}:{
  initial?:TestSpecification
  initialSuiteID?:string|null
  suites:TestSuite[]
  pending?:boolean
  error?:string
  submitLabel?:string
  onSubmit:(spec:TestSpecification,suiteID:string|null)=>Promise<void>
}){
  const [title,setTitle]=useState(initial?.title??'')
  const [preconditions,setPreconditions]=useState(initial?.preconditions??'')
  const [expected,setExpected]=useState(initial?.expected_result??'')
  const [steps,setSteps]=useState<{action:string;expected:string}[]>(initial?.steps?.length
    ? initial.steps.map(step=>({action:step.action,expected:step.expected}))
    : [{action:'',expected:''}])
  const [suiteID,setSuiteID]=useState(initialSuiteID??'none')
  const change=(index:number,key:'action'|'expected',value:string)=>{
    setSteps(current=>current.map((step,i)=>i===index?{...step,[key]:value}:step))
  }
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    const meaningful=steps.filter(step=>step.action.trim()).map(step=>({
      action:step.action.trim(),expected:step.expected,
    }))
    await onSubmit({title:title.trim(),preconditions,expected_result:expected,steps:meaningful},suiteID==='none'?null:suiteID)
  }
  return (
    <form className="flex flex-col gap-5" onSubmit={(event)=>{void submit(event).catch(()=>{})}}>
      <FieldGroup>
        <Field>
          <FieldLabel htmlFor="case-title">用例标题</FieldLabel>
          <Input id="case-title" value={title} onChange={event=>setTitle(event.target.value)}
            maxLength={240} required placeholder="描述可重复验证的行为" />
        </Field>
        <Field>
          <FieldLabel htmlFor="case-suite">测试套件</FieldLabel>
          <Select value={suiteID} onValueChange={setSuiteID}>
            <SelectTrigger id="case-suite" className="w-full"><SelectValue /></SelectTrigger>
            <SelectContent><SelectGroup>
              <SelectItem value="none">无套件</SelectItem>
              {suites.map(s=><SelectItem key={s.id} value={s.id}>{s.name}</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
        </Field>
        <Field>
          <FieldLabel htmlFor="case-preconditions">前置条件</FieldLabel>
          <Textarea id="case-preconditions" rows={2} value={preconditions} maxLength={10000}
            onChange={event=>setPreconditions(event.target.value)} />
        </Field>
        <Field>
          <FieldLabel>测试步骤</FieldLabel>
          <FieldDescription>仅保存测试规范，不会执行步骤。</FieldDescription>
          <div className="flex flex-col gap-3">
            {steps.map((step,index)=>(
              <div key={index} className="grid gap-2 rounded-lg border border-border p-3 sm:grid-cols-[1fr_1fr_auto]">
                <Input aria-label={`步骤 ${index+1} 操作`} placeholder={`步骤 ${index+1}：操作`}
                  value={step.action} maxLength={2000} onChange={event=>change(index,'action',event.target.value)}/>
                <Input aria-label={`步骤 ${index+1} 预期`} placeholder="预期结果"
                  value={step.expected} maxLength={2000} onChange={event=>change(index,'expected',event.target.value)}/>
                <Button type="button" size="icon-sm" variant="ghost" aria-label={`删除步骤 ${index+1}`}
                  onClick={()=>setSteps(old=>old.filter((_,i)=>i!==index))}><Trash2 aria-hidden="true" /></Button>
              </div>
            ))}
          </div>
          <Button type="button" variant="outline" size="sm" className="self-start"
            disabled={steps.length>=100} onClick={()=>setSteps(old=>[...old,{action:'',expected:''}])}>
            <Plus data-icon="inline-start" aria-hidden="true"/> 添加步骤
          </Button>
        </Field>
        <Field>
          <FieldLabel htmlFor="case-expected">整体预期结果</FieldLabel>
          <Textarea id="case-expected" value={expected} rows={2} maxLength={10000}
            onChange={event=>setExpected(event.target.value)} />
        </Field>
      </FieldGroup>
      <ErrorText message={error}/>
      <Button type="submit" disabled={!title.trim()||pending} className="self-end">
        {pending?'保存中…':submitLabel}
      </Button>
    </form>
  )
}
