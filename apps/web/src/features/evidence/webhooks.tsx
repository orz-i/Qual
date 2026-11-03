import { useState, type FormEvent } from 'react'
import { Link, useParams } from 'react-router'
import { ArrowLeft, PlugZap, Plus, RefreshCw, ShieldAlert, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { useWebhookActions, useWebhookDeliveries, useWebhookSubscriptions } from '@/features/evidence/api'

function RegisterWebhook({projectID}:{projectID:string}){
  const [open,setOpen]=useState(false)
  const [endpoint,setEndpoint]=useState('')
  const [types,setTypes]=useState('work_item.created')
  const [firstSecret,setFirstSecret]=useState<string|null>(null)
  const actions=useWebhookActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    try{
      const input=types.split(',').map(v=>v.trim()).filter(Boolean)
      const created=await actions.register.mutateAsync({endpoint:endpoint.trim(),event_types:input})
      setFirstSecret(created.secret??null)
    }catch{
      // Details appear in the dialog, not in logs.
    }
  }
  const changeOpen=(next:boolean)=>{
    setOpen(next)
    if(!next){
      setFirstSecret(null)
      setEndpoint('')
      actions.register.reset()
    }
  }
  return (
    <Dialog open={open} onOpenChange={changeOpen}>
      <DialogTrigger asChild><Button><Plus data-icon="inline-start" aria-hidden="true"/> 创建订阅</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>注册出站 Webhook</DialogTitle>
          <DialogDescription>只投递已持久化的工程事件；不能让 Qual 启动 Agent、测试或 CI。</DialogDescription>
        </DialogHeader>
        {firstSecret ? (
          <div className="flex flex-col gap-4 text-sm">
            <p className="flex items-center gap-2 font-medium"><ShieldCheck aria-hidden="true" className="size-4"/> 订阅已创建</p>
            <p className="text-muted-foreground">以下签名密钥只展示一次。请立即放入目的端的密钥管理系统，不要保存到仓库、截图或聊天记录。</p>
            <div className="break-all rounded-lg border border-border bg-muted/35 p-3 font-mono text-xs" aria-label="一次性签名密钥">{firstSecret}</div>
            <Button variant="outline" className="self-start" onClick={()=>void navigator.clipboard.writeText(firstSecret)}>复制密钥</Button>
            <Button onClick={()=>changeOpen(false)}>完成</Button>
          </div>
        ):(
          <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="webhook-endpoint">HTTPS 目的地</FieldLabel>
                <Input id="webhook-endpoint" type="url" maxLength={2048} required
                  placeholder="https://hooks.example.com/qual" value={endpoint}
                  onChange={e=>setEndpoint(e.target.value)} />
                <FieldDescription>目的地主机必须由 Qual 管理员在环境 allowlist 中明确允许；内网和重定向禁止。</FieldDescription>
              </Field>
              <Field>
                <FieldLabel htmlFor="webhook-events">事件类型（逗号分隔）</FieldLabel>
                <Input id="webhook-events" value={types} maxLength={500}
                  onChange={e=>setTypes(e.target.value)} required />
                <FieldDescription>例如 work_item.created、test_attempt.recorded。可用 * 订阅全部项目事件。</FieldDescription>
              </Field>
            </FieldGroup>
            {actions.register.isError&&<p role="alert" className="text-sm text-destructive">{actions.register.error.message}</p>}
            <Button type="submit" disabled={actions.register.isPending||!endpoint.trim()||!types.trim()}>
              {actions.register.isPending?'正在注册…':'注册并显示一次密钥'}
            </Button>
          </form>
        )}
      </DialogContent>
    </Dialog>
  )
}

export function WebhookPage(){
  const {projectID=''}=useParams()
  const subscriptions=useWebhookSubscriptions(projectID)
  const deliveries=useWebhookDeliveries(projectID)
  const actions=useWebhookActions(projectID)
  const [confirmID,setConfirmID]=useState<string|null>(null)
  const disable=async(id:string)=>{
    try{
      await actions.disable.mutateAsync(id)
      setConfirmID(null)
    }catch{
      // Render operation error below.
    }
  }
  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <Link className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
        to={`/projects/${projectID}`}><ArrowLeft aria-hidden="true" className="size-4"/> 返回工作看板</Link>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="flex items-center gap-2 text-2xl font-semibold"><PlugZap aria-hidden="true" className="size-6"/> Webhook 投递</h1>
          <p className="max-w-2xl text-sm text-muted-foreground">获项目授权的 Human/Agent 登记 HTTPS 回调目的地，Qual 维护 HMAC 签名、重试及可审计投递状态。独立 Worker 由管理员显式启动。</p>
        </div>
        <RegisterWebhook projectID={projectID}/>
      </header>
      <div className="flex items-start gap-3 rounded-lg border border-border bg-muted/30 p-4 text-sm">
        <ShieldAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0"/>
        <p className="text-muted-foreground">本机模式要求预先设置 QUAL_WEBHOOK_ENCRYPTION_KEY（随机 32 字节）和 QUAL_WEBHOOK_ALLOWED_HOSTS。创建订阅不会自行启动 Worker；使用 qual-maintenance deliver-loop 才会投递 Outbox。</p>
      </div>
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">订阅</h2>
        <Button variant="ghost" size="sm" onClick={()=>void subscriptions.refetch()} disabled={subscriptions.isFetching}>
          <RefreshCw data-icon="inline-start" aria-hidden="true"/> 刷新订阅
        </Button>
      </div>
      {subscriptions.isPending&&<p role="status" className="text-sm text-muted-foreground">正在加载订阅…</p>}
      {subscriptions.isError&&<p role="alert" className="text-sm text-destructive">
        {subscriptions.error.message}。需要有效 webhook:manage Scope 与项目授权。
      </p>}
      {actions.disable.isError&&<p role="alert" className="text-sm text-destructive">{actions.disable.error.message}</p>}
      {subscriptions.data?.length===0&&(
        <Card><CardHeader><CardTitle>尚无 Webhook</CardTitle>
          <CardDescription>仅允许已登记的 HTTPS hostname，不接受任意地址。</CardDescription>
        </CardHeader></Card>
      )}
      <div className="grid gap-3">
        {subscriptions.data?.map(item=>(
          <Card key={item.id}>
            <CardHeader>
              <CardTitle className="flex flex-wrap items-center justify-between gap-3 text-sm">
                <span className="min-w-0 break-all font-mono">{item.endpoint}</span>
                <Badge variant={item.disabled_at?'outline':'secondary'}>{item.disabled_at?'已禁用':'已启用'}</Badge>
              </CardTitle>
              <CardDescription>Events: {item.event_types.join(', ')} · 创建于 {new Date(item.created_at).toLocaleString()}</CardDescription>
            </CardHeader>
            {!item.disabled_at&&<CardContent className="flex flex-wrap items-center gap-2">
              {confirmID!==item.id ? (
                <Button size="sm" variant="outline" onClick={()=>setConfirmID(item.id)}>禁用订阅</Button>
              ):(
                <>
                  <p className="text-sm text-muted-foreground">确定禁用吗？待发送事件将取消。</p>
                  <Button variant="destructive" size="sm" disabled={actions.disable.isPending}
                    onClick={()=>void disable(item.id)}>确认禁用</Button>
                  <Button variant="ghost" size="sm" onClick={()=>setConfirmID(null)}>取消</Button>
                </>
              )}
            </CardContent>}
          </Card>
        ))}
      </div>
      <div className="flex items-center justify-between gap-3">
        <h2 className="text-base font-semibold">投递历史</h2>
        <Button variant="ghost" size="sm" onClick={()=>void deliveries.refetch()} disabled={deliveries.isFetching}>
          <RefreshCw data-icon="inline-start" aria-hidden="true"/> 刷新投递
        </Button>
      </div>
      {deliveries.isPending&&<p role="status" className="text-sm text-muted-foreground">加载投递状态…</p>}
      {deliveries.isError&&<p role="alert" className="text-sm text-destructive">{deliveries.error.message}</p>}
      {deliveries.data?.length===0&&<p className="text-sm text-muted-foreground">没有待投递或历史记录。</p>}
      <div className="divide-y rounded-xl border">
        {deliveries.data?.map(item=>(
          <div key={item.id} className="flex flex-wrap items-center gap-3 p-3">
            <div className="min-w-0 flex-1">
              <p className="font-mono text-xs">{item.id.slice(0,8)} · {item.event_id.slice(0,8)}</p>
              <p className="mt-1 text-xs text-muted-foreground">
                {item.attempt_count} / 5 次 · {item.last_http_status??'无 HTTP 响应'} · {item.last_error_code??'无错误'}
              </p>
              <p className="text-xs text-muted-foreground">下次尝试：{new Date(item.next_attempt_at).toLocaleString()}</p>
            </div>
            <Badge variant={item.status==='dead'?'destructive':'outline'}>{item.status}</Badge>
          </div>
        ))}
      </div>
      <p className="text-xs text-muted-foreground">Webhook 是至少一次投递；外部消费端需依据 Event/Delivery ID 做幂等。它不是 Agent 编排器。</p>
    </section>
  )
}
