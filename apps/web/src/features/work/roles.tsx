import { useState, type FormEvent } from 'react'
import { Link } from 'react-router'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { ListChecks, ShieldAlert } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectTrigger, SelectContent, SelectItem, SelectValue } from '@/components/ui/select'
import { api, apiErrorMessage, QualAPIError, requestKey } from '@/lib/qual-api'
import { useProjectPrincipals, principalLabel } from '@/features/work/principals'
import { useSession } from '@/features/auth/session'
import { useMyAccess } from '@/features/access/api'
import { workKey } from '@/features/work/api'

type Capacity = components['schemas']['RoleCapacity']
type Outcome = components['schemas']['RoleOutcome']
type ReferenceKind = components['schemas']['RoleReferenceKind']

const capacityNames:Record<Capacity,string>={
  executor:'执行',tester:'测试记录',reviewer:'复核',auditor:'审计记录',
}
const outcomeNames:Record<Outcome,string>={
  observed:'观察记录',passed:'核验通过',failed:'核验失败',needs_work:'需要修改',
}
const referenceNames:Record<ReferenceKind,string>={
  none:'无',git_commit:'Git Commit',test_run:'Qual TestRun',artifact:'Qual Artifact',
}
const key=(projectID:string,itemID:string)=>[...workKey(projectID),'roles',itemID] as const
const uuidPattern=/^[a-f0-9]{8}(?:-[a-f0-9]{4}){3}-[a-f0-9]{12}$/

export function WorkRoleHistory({projectID,itemID,version}:{
 projectID:string;itemID:string;version:number
}){
 const queryClient=useQueryClient()
 const {data:session}=useSession()
 const access=useMyAccess(projectID)
 const perms=access.data?.effective_permissions??[]
 const allowedCapacities:Capacity[]=[
  ...(perms.includes('work:write')?['executor' as Capacity]:[]),
  ...(perms.includes('test:write')&&perms.includes('work:read')?['tester' as Capacity]:[]),
  ...(perms.includes('work:review')&&perms.includes('work:read')?['reviewer' as Capacity]:[]),
  ...(perms.includes('audit:read')&&perms.includes('work:read')&&perms.includes('work:write')?['auditor' as Capacity]:[]),
 ]
 const [capacity,setCapacity]=useState<Capacity>('executor')
 const chosenCapacity=allowedCapacities.includes(capacity)?capacity:allowedCapacities[0]
 const [outcome,setOutcome]=useState<Outcome>('observed')
 const [summary,setSummary]=useState('')
 const [referenceKind,setReferenceKind]=useState<ReferenceKind>('none')
 const [referenceValue,setReferenceValue]=useState('')
 const entries=useQuery({
  queryKey:key(projectID,itemID),
  queryFn:async()=>{
   const {data,error}=await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/role-entries',{
    params:{path:{projectID,itemID}},
   })
   if(error||!data)throw new Error(apiErrorMessage(error))
   return data.items
  },
 })
 const principals=useProjectPrincipals(projectID,entries.data?.map(e=>e.actor_id)??[])
 const record=useMutation({
  mutationFn:async()=>{
   if(!session?.csrf_token)throw new Error('Human Browser Session 已失效；请重新登录。')
   if(!chosenCapacity)throw new Error('当前身份没有追加该职责的项目权限。')
   const {data,error}=await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/role-entries',{
    params:{path:{projectID,itemID},header:{'Idempotency-Key':requestKey()}},
    headers:{'X-CSRF-Token':session.csrf_token},
    body:{
     capacity:chosenCapacity,outcome,summary:summary.trim(),reference_kind:referenceKind,
     reference_value:referenceKind==='none'?'':referenceValue.trim(),
     expected_version:version,
    },
   })
   if(error||!data)throw new QualAPIError(error)
   return data
  },
  onSuccess:async()=>{
   setSummary('');setReferenceKind('none');setReferenceValue('')
   await Promise.all([
    queryClient.invalidateQueries({queryKey:key(projectID,itemID)}),
    queryClient.invalidateQueries({queryKey:workKey(projectID)}),
   ])
  },
 })
 const needsEvidence=(chosenCapacity==='reviewer'||chosenCapacity==='tester')&&(outcome==='passed'||outcome==='failed')
 const canSubmit=!!chosenCapacity&&summary.trim().length>=1&&summary.trim().length<=5000&&
   (!needsEvidence||referenceKind!=='none')&&(referenceKind==='none'||referenceValue.trim().length>0)
 const submit=(event:FormEvent<HTMLFormElement>)=>{
  event.preventDefault()
  if(!canSubmit||record.isPending)return
  void record.mutateAsync().catch(()=>{})
 }
 return (
  <Card id="review-evidence-history" className="scroll-mt-24">
   <CardHeader>
    <CardTitle className="flex items-center gap-2 text-base"><ListChecks className="size-4" aria-hidden="true"/> 职责与证据履历</CardTitle>
    <CardDescription>
     同一真实 Human/Agent 可分别承担执行、测试、复核或审计职责；标记不代表不同的人，
     更不会授予额外权限。所有记录只追加，并纳入 Audit/Activity/Outbox 和项目 ZIP。
    </CardDescription>
   </CardHeader>
   <CardContent className="flex flex-col gap-5">
    {entries.isPending&&<p role="status" className="text-sm">正在读取职责记录…</p>}
    {entries.isError&&<p role="alert" className="text-sm text-destructive">{entries.error.message}</p>}
    {entries.data?.length===0&&<p className="text-sm text-muted-foreground">尚无职责记录；旧评论仍保留，不会被改写。</p>}
    <div className="flex flex-col gap-3">
     {entries.data?.map(entry=>(
      <div key={entry.id} className="space-y-2 rounded-lg border p-3">
       <div className="flex flex-wrap items-center gap-2">
        <Badge variant="secondary">{capacityNames[entry.capacity]}</Badge>
        <Badge variant="outline">{outcomeNames[entry.outcome]}</Badge>
        {entry.self_review&&<Badge variant="destructive">同一 Principal 自我复核 · 非独立批准</Badge>}
       </div>
       <p className="whitespace-pre-wrap break-words text-sm">{entry.summary}</p>
       <p className="break-all text-xs text-muted-foreground" title={entry.actor_id}>
        {entry.actor_id===session?.principal.id?`${session.principal.display_name} · Human`:principalLabel(entry.actor_id, principals.byID.get(entry.actor_id))} · v{entry.work_version} · {entry.work_status} ·
        {' '}{new Date(entry.recorded_at).toLocaleString()}
       </p>
       <p className="break-all font-mono text-xs text-muted-foreground">主体 UUID: {entry.actor_id}</p>
       {entry.reference_kind!=='none'&&<p className="break-all text-xs text-muted-foreground">
        {referenceNames[entry.reference_kind]}: {' '}
        {entry.reference_kind==='test_run' && uuidPattern.test(entry.reference_value) ?
          <Link to={`/projects/${projectID}/tests/runs/${entry.reference_value}`} className="font-medium text-primary underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2">{entry.reference_value} · 查看测试运行</Link> :
          entry.reference_kind==='artifact' && uuidPattern.test(entry.reference_value) ?
            <a href={`#artifact-${entry.reference_value}`} className="font-medium text-primary underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2">{entry.reference_value} · 查看工作项附件</a> :
            <span className="font-mono">{entry.reference_value}</span>}
       </p>}
      </div>
     ))}
    </div>
    {access.isPending&&<p role="status" className="border-t pt-4 text-xs text-muted-foreground">正在确认追加职责所需权限…</p>}
    {access.isError&&<p role="alert" className="border-t pt-4 text-xs text-destructive">当前权限读取失败，已隐藏职责追加表单。</p>}
    {access.isSuccess&&allowedCapacities.length===0&&<p className="border-t pt-4 text-xs text-muted-foreground">当前身份可以查阅职责履历，但没有追加履历的权限。</p>}
    {access.isSuccess&&allowedCapacities.length>0&&<form className="flex flex-col gap-3 border-t pt-4" onSubmit={submit}>
     <h3 className="font-semibold text-sm">追加一条真实履历</h3>
     <div className="grid gap-3 sm:grid-cols-2">
      <Field>
       <FieldLabel htmlFor="role-capacity">本次职责</FieldLabel>
       <Select value={chosenCapacity} onValueChange={v=>setCapacity(v as Capacity)}>
        <SelectTrigger id="role-capacity" className="w-full"><SelectValue/></SelectTrigger>
        <SelectContent>{allowedCapacities.map(v=><SelectItem key={v} value={v}>{capacityNames[v]}</SelectItem>)}</SelectContent>
       </Select>
      </Field>
      <Field>
       <FieldLabel htmlFor="role-outcome">本次结论</FieldLabel>
       <Select value={outcome} onValueChange={v=>setOutcome(v as Outcome)}>
        <SelectTrigger id="role-outcome" className="w-full"><SelectValue/></SelectTrigger>
        <SelectContent>{Object.entries(outcomeNames).map(([v,label])=><SelectItem key={v} value={v}>{label}</SelectItem>)}</SelectContent>
       </Select>
      </Field>
      <Field>
       <FieldLabel htmlFor="role-reference-kind">证据来源类型</FieldLabel>
       <Select value={referenceKind} onValueChange={v=>setReferenceKind(v as ReferenceKind)}>
        <SelectTrigger id="role-reference-kind" className="w-full"><SelectValue/></SelectTrigger>
        <SelectContent>{Object.entries(referenceNames).map(([v,label])=><SelectItem key={v} value={v}>{label}</SelectItem>)}</SelectContent>
       </Select>
      </Field>
      <Field>
       <FieldLabel htmlFor="role-reference">来源标识（SHA / Qual UUID）</FieldLabel>
       <Input id="role-reference" value={referenceValue} maxLength={128}
        disabled={referenceKind==='none'}
        onChange={e=>setReferenceValue(e.target.value)}
        placeholder="只使用真实 Git SHA 或同项目 TestRun/Artifact UUID"/>
      </Field>
     </div>
     <Field>
      <FieldLabel htmlFor="role-summary">已实际完成或观察到的事实</FieldLabel>
      <Textarea id="role-summary" value={summary} maxLength={5000} rows={3}
       onChange={e=>setSummary(e.target.value)}
       placeholder="附实际提交、测试命令、观察结果；请勿伪造多个 Agent 或上传凭证"/>
     </Field>
     <p className="flex gap-2 text-xs text-muted-foreground">
      <ShieldAlert className="size-4 shrink-0" aria-hidden="true"/>
      Tester/Reviewer 的通过或失败结论必须引用证据。即使是 Reviewer: passed，也不会自动把任务变为 done；
      同人复核由服务端标记，最终状态转换仍需 work:review 权限。
     </p>
     {record.isError&&<p role="alert" className="text-sm text-destructive">{record.error.message}</p>}
     {record.error instanceof QualAPIError && record.error.code === 'VERSION_CONFLICT' &&
       <div className="space-y-2 rounded-lg border border-amber-300 p-3 text-xs">
         <p>职责记录未追加；你的说明和证据引用仍保留。读取新版本后请核对，再提交。</p>
         <Button type="button" size="sm" variant="outline" onClick={() => void queryClient.invalidateQueries({queryKey:workKey(projectID)}).then(() => record.reset())}>刷新工作项版本并核对</Button>
       </div>}
     {record.isSuccess&&<p role="status" className="text-sm">已追加 v{record.data.work_version} 职责记录。</p>}
     <Button type="submit" className="self-end" disabled={!canSubmit||record.isPending||(record.error instanceof QualAPIError && record.error.code==='VERSION_CONFLICT')}>
      {record.isPending?'记录中…':'追加职责记录'}
     </Button>
    </form>}
   </CardContent>
  </Card>
 )
}
