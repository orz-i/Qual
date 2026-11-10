import { useState } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft, KeyRound, ShieldCheck, ShieldAlert, RefreshCw } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Card, CardHeader, CardContent, CardTitle, CardDescription } from '@/components/ui/card'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle, DialogDescription } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import {
  allPermissions, permissionLabels, useMyAccess, useAccessMembers, useChangeAccess,
  type PermissionName, type PermissionEffect,
} from '@/features/access/api'
import { canGrantIntegrationPermission } from '@/features/access/permission-rules'

const effectLabels:Record<PermissionEffect,string>={inherit:'继承角色',allow:'明确允许',deny:'明确拒绝'}
const principalTypeLabels:Record<string,string>={human:'Human',agent:'Agent',integration:'Integration'}
const roleLabels:Record<string,string>={
  workspace_admin:'Workspace Admin',project_maintainer:'Maintainer',
  project_member:'Member',project_viewer:'Viewer',
}

function ErrorLine({message}:{message?:string}){
  return message?<p role="alert" className="rounded-lg border border-destructive/25 p-3 text-sm text-destructive">{message}</p>:null
}

export function ProjectAccessPage(){
  const {projectID=''}=useParams()
  const my=useMyAccess(projectID)
  const canManage=my.data?.effective_permissions.includes('access:manage')??false
  const members=useAccessMembers(projectID,canManage)
  const change=useChangeAccess(projectID)

  const [principalID,setPrincipalID]=useState('')
  const [permission,setPermission]=useState<PermissionName>('work:review')
  const [effect,setEffect]=useState<PermissionEffect>('allow')
  const [reason,setReason]=useState('')
  const [confirmOpen,setConfirmOpen]=useState(false)
  const [success,setSuccess]=useState('')

  const chosen=members.data?.find(m=>m.principal_id===principalID)
  const setting=chosen?.permissions.find(p=>p.permission===permission)
  const selfChange=principalID!==''&&principalID===my.data?.principal_id
  const lockedAdmin=chosen?.role==='workspace_admin'
  const managementChange=permission==='access:manage'&&my.data?.role!=='workspace_admin'
  const reporterRestricted=chosen?.kind==='integration'&&
    !canGrantIntegrationPermission(permission)
  const unchanged=setting?.effect===effect
  const canSubmit=!!chosen&&!!setting&&!selfChange&&!lockedAdmin&&!managementChange&&!reporterRestricted
    &&!unchanged&&reason.trim().length>=3&&reason.trim().length<=500&&!change.isPending

  async function submit(){
    if(!canSubmit||!setting)return
    try{
      await change.mutateAsync({
        principalID,permission,effect,expectedVersion:setting.version,reason,
      })
      setSuccess(`${chosen?.display_name} 的 ${permission} 已更新为 ${effectLabels[effect]}`)
      setConfirmOpen(false)
      setReason('')
    }catch{
      // Keep the dialog open with the actionable server error.
    }
  }

  return (
    <section className="flex max-w-6xl flex-col gap-6">
      <Link className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
        to={`/projects/${projectID}`}>
        <ArrowLeft className="size-4" aria-hidden="true"/> 返回工作看板
      </Link>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="space-y-2">
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight">
            <KeyRound className="size-6" aria-hidden="true"/> 项目权限
          </h1>
          <p className="max-w-3xl text-sm text-muted-foreground">
            Human 和 Agent 可拥有不同的操作能力。Agent A 可承担执行/审核职责，
            Agent B 可被授予审计读取能力。Qual 只鉴权与保存事实，不启动 Agent 或测试。
          </p>
        </div>
        <Button variant="outline" size="sm" onClick={()=>{void my.refetch();void members.refetch()}}>
          <RefreshCw className="size-4" data-icon="inline-start" aria-hidden="true"/> 刷新
        </Button>
      </header>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-base"><ShieldCheck className="size-4" aria-hidden="true"/> 我的当前权限</CardTitle>
          <CardDescription>当前 Credential Scope 与项目 Role/覆盖值同时允许才会生效。令牌失效或显式 Deny 会立即拒绝下一次请求。</CardDescription>
        </CardHeader>
        <CardContent>
          {my.isPending&&<p role="status" className="text-sm">正在读取当前权限…</p>}
          <ErrorLine message={my.error?.message}/>
          {my.data&&(
            <div className="space-y-3">
              <p className="text-sm">{principalTypeLabels[my.data.kind]??my.data.kind} · {roleLabels[my.data.role]??my.data.role}
                <span className="ml-2 font-mono text-xs text-muted-foreground">{my.data.principal_id.slice(0,8)}</span>
              </p>
              <div className="flex flex-wrap gap-2">
                {my.data.effective_permissions.map(p=><Badge variant="secondary" key={p}>{permissionLabels[p]}</Badge>)}
                {my.data.effective_permissions.length===0&&<span className="text-sm text-muted-foreground">本凭证没有有效项目操作权限</span>}
              </div>
            </div>
          )}
        </CardContent>
      </Card>
      <div className="rounded-lg border border-border bg-muted/30 p-4 text-sm text-muted-foreground">
        <p className="flex items-center gap-2 font-medium text-foreground"><ShieldAlert className="size-4" aria-hidden="true"/> 无人值守与授权边界</p>
        <p className="mt-2">
          不再限制“只有 Human 可审核”。授权 Agent 同样可操作；但新权限必须同时存在于
          Agent 的 Credential Scope 和本项目的明确 Allow / 角色默认权限中。
          项目 Deny 立即覆盖旧 Scope。权限变更会记录不可变 Audit/Activity/Outbox。
          同一 macOS 用户下的 Agent 仍不等同于系统级隔离。
        </p>
      </div>
      {my.isSuccess&&!canManage&&(
        <p role="status" className="rounded-lg border p-4 text-sm">
          当前身份不含 access:manage；可查看自己的有效权限，但不能枚举其他成员或修改权限。
        </p>
      )}
      {canManage&&(
        <>
          <section className="space-y-3">
            <h2 className="text-lg font-semibold">成员权限</h2>
            {members.isPending&&<p role="status" className="text-sm">正在读取项目成员…</p>}
            <ErrorLine message={members.error?.message}/>
            <div className="grid gap-3 md:grid-cols-2">
              {members.data?.map(m=>(
                <Card key={m.principal_id}>
                  <CardHeader className="gap-1.5">
                    <CardTitle className="flex flex-wrap items-center justify-between gap-2 text-base">
                      {m.display_name}
                      <Badge variant="outline">{principalTypeLabels[m.kind]??m.kind}</Badge>
                    </CardTitle>
                    <CardDescription>{roleLabels[m.role]??m.role} · {m.principal_id}</CardDescription>
                  </CardHeader>
                  <CardContent className="space-y-2">
                    <div className="flex flex-wrap gap-1.5">
                      {m.permissions.filter(p=>p.allowed_by_project).map(p=>(
                        <Badge variant="secondary" key={p.permission}>{permissionLabels[p.permission]}</Badge>
                      ))}
                    </div>
                    <p className="text-xs text-muted-foreground">
                      以上为项目允许能力；其 Credential 仍需包含相同 Scope。
                    </p>
                    <Button size="sm" variant="outline" onClick={()=>{
                      setPrincipalID(m.principal_id);setSuccess('');change.reset()
                    }} disabled={m.principal_id===my.data?.principal_id||m.role==='workspace_admin'}>
                      配置此成员
                    </Button>
                  </CardContent>
                </Card>
              ))}
            </div>
          </section>
          <Card>
            <CardHeader>
              <CardTitle className="text-base">明确授权或拒绝</CardTitle>
              <CardDescription>选择成员、权限与效果。修改需要版本匹配、变更原因和确认；不会修改或生成现有 Agent PAT。</CardDescription>
            </CardHeader>
            <CardContent className="space-y-4">
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="space-y-2">
                  <Label htmlFor="access-principal">成员</Label>
                  <Select value={principalID} onValueChange={setPrincipalID}>
                    <SelectTrigger id="access-principal" className="w-full"><SelectValue placeholder="选择成员"/></SelectTrigger>
                    <SelectContent>{members.data?.map(m=>(
                      <SelectItem key={m.principal_id} value={m.principal_id}
                        disabled={m.principal_id===my.data?.principal_id||m.role==='workspace_admin'}>
                        {m.display_name} ({principalTypeLabels[m.kind]})
                      </SelectItem>
                    ))}</SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="access-permission">权限</Label>
                  <Select value={permission} onValueChange={value=>setPermission(value as PermissionName)}>
                    <SelectTrigger id="access-permission" className="w-full"><SelectValue/></SelectTrigger>
                    <SelectContent>{allPermissions.map(p=>(
                      <SelectItem key={p} value={p} disabled={p==='access:manage'&&my.data?.role!=='workspace_admin'}>
                        {permissionLabels[p]} · {p}
                      </SelectItem>
                    ))}</SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="access-effect">配置效果</Label>
                  <Select value={effect} onValueChange={v=>setEffect(v as PermissionEffect)}>
                    <SelectTrigger id="access-effect" className="w-full"><SelectValue/></SelectTrigger>
                    <SelectContent>
                      <SelectItem value="allow">明确允许 Allow</SelectItem>
                      <SelectItem value="deny">明确拒绝 Deny</SelectItem>
                      <SelectItem value="inherit">继承角色 Inherit</SelectItem>
                    </SelectContent>
                  </Select>
                </div>
                <div className="space-y-2">
                  <Label htmlFor="access-version">当前版本（CAS）</Label>
                  <Input id="access-version" readOnly value={setting?.version??'—'}/>
                </div>
              </div>
              <div className="space-y-2">
                <Label htmlFor="access-reason">变更原因（审计记录）</Label>
                <Textarea id="access-reason" value={reason} maxLength={500}
                  onChange={e=>setReason(e.target.value)} placeholder="例如：委托 Agent A 审核低风险项目工作项" rows={3}/>
              </div>
              {setting&&<p className="text-sm text-muted-foreground">现有效果：{effectLabels[setting.effect]} · 项目策略当前{setting.allowed_by_project?'允许':'拒绝'}（还受令牌 Scope 约束）。</p>}
              {managementChange&&<ErrorLine message="只有 Human Workspace Admin 可委托 access:manage。"/>}
              {reporterRestricted&&<ErrorLine message="Integration 仅可获得测试、证据或显式授权的 Event/AgentRun 上报权限。"/>}
              {unchanged&&chosen&&<p className="text-sm text-muted-foreground">目标效果与现有记录相同，不需要重复写入。</p>}
              <ErrorLine message={change.error?.message}/>
              {success&&<p role="status" className="text-sm">{success}</p>}
              <Button disabled={!canSubmit} onClick={()=>setConfirmOpen(true)}>
                核对并提交权限变更
              </Button>
            </CardContent>
          </Card>
        </>
      )}
      <Dialog open={confirmOpen} onOpenChange={setConfirmOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>确认项目权限变更</DialogTitle>
            <DialogDescription>更新后立即影响新请求。此操作写入不可变审计历史。</DialogDescription>
          </DialogHeader>
          <div className="space-y-2 text-sm">
            <p><strong>成员：</strong>{chosen?.display_name} ({chosen?.kind})</p>
            <p><strong>权限：</strong>{permissionLabels[permission]} · {permission}</p>
            <p><strong>效果：</strong>{effectLabels[effect]}</p>
            <p><strong>版本：</strong>{setting?.version}</p>
            <p className="whitespace-pre-wrap break-words"><strong>原因：</strong>{reason.trim()}</p>
          </div>
          <ErrorLine message={change.error?.message}/>
          <DialogFooter>
            <Button variant="outline" onClick={()=>setConfirmOpen(false)}>取消</Button>
            <Button disabled={!canSubmit} onClick={()=>void submit()}>
              {change.isPending?'更新中…':'确认提交'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </section>
  )
}
