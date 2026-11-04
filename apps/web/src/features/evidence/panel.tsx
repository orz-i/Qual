import { useState, type FormEvent } from 'react'
import { Link2, Download, FileCheck2, FileUp, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { Badge } from '@/components/ui/badge'
import { WorkEvidencePacketPreview } from '@/features/evidence/work-packet-preview'
import { isEvidencePacketFilename } from '@/features/evidence/work-packet'
import { useMyAccess } from '@/features/access/api'
import {
  type ArtifactTargetKind,
  useArtifacts, useReferences, useEvidenceActions,
} from '@/features/evidence/api'

function Problem({message}:{message?:string}){
  return message?<p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/5 p-3 text-sm text-destructive">{message}</p>:null
}

function UploadDialog({projectID,targetKind,targetID}:{
  projectID:string;targetKind:ArtifactTargetKind;targetID:string
}){
  const [open,setOpen]=useState(false)
  const [file,setFile]=useState<File|null>(null)
  const actions=useEvidenceActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    if(!file)return
    try{
      await actions.upload.mutateAsync({file,targetKind,targetID})
      setFile(null)
      setOpen(false)
    }catch{
      // The mutation error stays visible in the dialog.
    }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button variant="outline" size="sm"><FileUp data-icon="inline-start" aria-hidden="true"/> 上传证据</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>上传私有附件</DialogTitle>
          <DialogDescription>三阶段上传，经 Qual SHA-256 完整性验证后保存到私有 S3。不会执行或解析文件内容。</DialogDescription>
        </DialogHeader>
        <form onSubmit={(event)=>{void submit(event)}} className="flex flex-col gap-4">
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="evidence-file">附件文件（最大 10 MiB）</FieldLabel>
              <Input id="evidence-file" type="file" required
                onChange={event=>{setFile(event.target.files?.[0]??null);actions.upload.reset()}} />
              <FieldDescription>只存储文件与校验值；不要上传秘密令牌或未经许可的数据。</FieldDescription>
            </Field>
          </FieldGroup>
          <Problem message={actions.upload.error?.message}/>
          <Button type="submit" disabled={!file||actions.upload.isPending}>
            {actions.upload.isPending?'上传并核验中…':'保存附件'}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

function ReferenceDialog({projectID,targetKind,targetID}:{
  projectID:string;targetKind:ArtifactTargetKind;targetID:string
}){
  const [open,setOpen]=useState(false)
  const [provider,setProvider]=useState('github')
  const [kind,setKind]=useState('pull_request')
  const [externalID,setExternalID]=useState('')
  const [url,setURL]=useState('')
  const actions=useEvidenceActions(projectID)
  const submit=async(event:FormEvent<HTMLFormElement>)=>{
    event.preventDefault()
    try{
      await actions.addReference.mutateAsync({
        target_kind:targetKind,target_id:targetID,provider:provider.trim(),
        kind:kind.trim(),external_id:externalID.trim(),url:url.trim(),
      })
      setExternalID('')
      setURL('')
      setOpen(false)
    }catch{
      // Surface server errors without leaking credentials.
    }
  }
  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild><Button variant="outline" size="sm"><Link2 data-icon="inline-start" aria-hidden="true"/> 添加外部引用</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader><DialogTitle>记录外部来源</DialogTitle>
          <DialogDescription>只保存外部 Git/CI 链接及来源标识，Qual 不会请求此 URL 或触发外部动作。</DialogDescription>
        </DialogHeader>
        <form className="flex flex-col gap-4" onSubmit={(event)=>{void submit(event)}}>
          <FieldGroup>
            <Field>
              <FieldLabel htmlFor="evidence-provider">Provider</FieldLabel>
              <Input id="evidence-provider" value={provider} onChange={e=>setProvider(e.target.value)} maxLength={60} required />
            </Field>
            <Field>
              <FieldLabel htmlFor="evidence-ref-kind">类型</FieldLabel>
              <Input id="evidence-ref-kind" value={kind} onChange={e=>setKind(e.target.value)} maxLength={60} required />
            </Field>
            <Field>
              <FieldLabel htmlFor="evidence-external-id">外部 ID</FieldLabel>
              <Input id="evidence-external-id" value={externalID} onChange={e=>setExternalID(e.target.value)} maxLength={240} required />
            </Field>
            <Field>
              <FieldLabel htmlFor="evidence-url">HTTPS URL</FieldLabel>
              <Input id="evidence-url" type="url" value={url} onChange={e=>setURL(e.target.value)} required
                placeholder="https://github.com/org/repo/pull/123" maxLength={2048} />
            </Field>
          </FieldGroup>
          <Problem message={actions.addReference.error?.message}/>
          <Button type="submit" disabled={actions.addReference.isPending||!provider.trim()||!kind.trim()||!externalID.trim()||!url.trim()}>
            {actions.addReference.isPending?'保存中…':'记录来源链接'}
          </Button>
        </form>
      </DialogContent>
    </Dialog>
  )
}

export function EvidencePanel({projectID,targetKind,targetID}:{
  projectID:string;targetKind:ArtifactTargetKind;targetID:string
}){
  const artifacts=useArtifacts(projectID,targetKind,targetID)
  const references=useReferences(projectID,targetKind,targetID)
  const access=useMyAccess(projectID)
  const permissions=access.data?.effective_permissions??[]
  const canUpload=permissions.includes('artifact:write')
  const canReference=permissions.includes(targetKind==='work_item'?'work:write':'test:write')
  return (
    <Card id="work-evidence" className="scroll-mt-24">
      <CardHeader>
        <CardTitle className="flex items-center gap-2"><ShieldCheck aria-hidden="true" className="size-4"/> 证据与外部引用</CardTitle>
        <CardDescription>每个附件由私有对象存储保存，下载需鉴权并复核哈希。外部链接仅作为来源事实记录。</CardDescription>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        {(canUpload||canReference)&&<div className="flex flex-wrap gap-2">
          {canUpload&&<UploadDialog projectID={projectID} targetKind={targetKind} targetID={targetID}/>}
          {canReference&&<ReferenceDialog projectID={projectID} targetKind={targetKind} targetID={targetID}/>}
        </div>}
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-semibold">附件</h3>
          {artifacts.isPending&&<p role="status" className="text-xs text-muted-foreground">正在读取附件…</p>}
          <Problem message={artifacts.error?.message}/>
          {artifacts.data?.map(item=>(
            <div key={item.id} id={`artifact-${item.id}`} className="flex scroll-mt-24 flex-wrap items-center gap-2 rounded-lg border p-3">
              <FileCheck2 aria-hidden="true" className="size-4 text-muted-foreground" />
              <div className="min-w-0 flex-1">
                <p className="break-all text-sm font-medium">{item.filename}</p>
                <p className="break-all font-mono text-xs text-muted-foreground">SHA-256: {item.sha256}</p>
                <p className="text-xs text-muted-foreground">{item.size_bytes} bytes · {new Date(item.created_at).toLocaleString()}</p>
              </div>
              <Badge variant={item.state==='ready'?'secondary':'outline'}>{item.state}</Badge>
              {item.state==='ready' && targetKind==='work_item' &&
                isEvidencePacketFilename(item.filename,item.media_type) &&
                <WorkEvidencePacketPreview projectID={projectID} workItemID={targetID} artifact={item} />}
              {item.state==='ready'&&(
                <Button size="sm" variant="ghost" asChild>
                  <a href={`/api/v1/projects/${projectID}/artifacts/${item.id}/content`} download={item.filename}>
                    <Download data-icon="inline-start" aria-hidden="true" /> 下载
                  </a>
                </Button>
              )}
            </div>
          ))}
          {artifacts.data?.length===0&&<p className="text-sm text-muted-foreground">暂无证据附件。</p>}
        </section>
        <section className="flex flex-col gap-2">
          <h3 className="text-sm font-semibold">外部引用</h3>
          {references.isPending&&<p role="status" className="text-xs text-muted-foreground">正在读取来源链接…</p>}
          <Problem message={references.error?.message}/>
          {references.data?.map(ref=>(
            <div key={ref.id} id={'external-ref-' + ref.id} className="scroll-mt-24 rounded-lg border p-3 text-sm">
              <p className="font-medium">{ref.provider} · {ref.kind} · {ref.external_id}</p>
              <a className="mt-1 block break-all text-xs text-muted-foreground underline underline-offset-4 hover:text-foreground"
                href={ref.url} rel="noopener noreferrer" target="_blank">
                {ref.url}
              </a>
            </div>
          ))}
          {references.data?.length===0&&<p className="text-sm text-muted-foreground">尚无外部引用。</p>}
        </section>
        <p className="text-xs text-muted-foreground">列表仅显示最近 100 条；生产保留策略及完整归档导入仍需额外设计。</p>
      </CardContent>
    </Card>
  )
}
