import { Link, useParams } from 'react-router'
import { Archive, ArrowLeft, Download, RefreshCw, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { useProjectExportActions, useProjectExports } from '@/features/evidence/api'

export function ArchivePage(){
  const {projectID=''}=useParams()
  const exports=useProjectExports(projectID)
  const create=useProjectExportActions(projectID)
  return (
    <section className="flex max-w-5xl flex-col gap-6">
      <Link className="flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground"
        to={`/projects/${projectID}`}><ArrowLeft aria-hidden="true" className="size-4"/> 返回工作看板</Link>
      <header className="flex flex-wrap items-start justify-between gap-4">
        <div className="flex flex-col gap-2">
          <h1 className="flex items-center gap-2 text-2xl font-semibold tracking-tight"><Archive aria-hidden="true" className="size-6"/> 项目归档</h1>
          <p className="max-w-2xl text-sm text-muted-foreground">人类管理员可创建项目级一致性快照：JSONL、附件字节、Manifest 与 SHA-256。只负责导出，不执行任务，也不支持恢复导入。</p>
        </div>
        <Button disabled={create.isPending}
          onClick={()=>void create.mutateAsync().catch(()=>{})}>
          <ShieldCheck data-icon="inline-start" aria-hidden="true"/>
          {create.isPending?'生成并核验中…':'生成验证型 ZIP'}
        </Button>
      </header>
      {create.isError&&<p role="alert" className="rounded-lg border border-destructive/25 p-3 text-sm text-destructive">{create.error.message}</p>}
      {create.isSuccess&&<p role="status" className="text-sm">新归档已生成：SHA-256 {create.data.sha256}。可在下方下载。</p>}
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="text-base font-semibold">已保存归档</h2>
        <Button variant="ghost" size="sm" onClick={()=>void exports.refetch()} disabled={exports.isFetching}>
          <RefreshCw data-icon="inline-start" aria-hidden="true"/> 刷新
        </Button>
      </div>
      {exports.isPending&&<p role="status" className="text-sm text-muted-foreground">正在读取已授权导出记录…</p>}
      {exports.isError&&<p role="alert" className="text-sm text-destructive">
        {exports.error.message}。本页要求有效 archive:export Credential Scope + 项目 Allow/角色权限。
      </p>}
      {exports.isSuccess&&exports.data.length===0&&(
        <Card><CardHeader><CardTitle>暂无归档</CardTitle>
          <CardDescription>在本机受控环境创建第一个包含数据和哈希清单的项目快照。</CardDescription>
        </CardHeader></Card>
      )}
      <div className="flex flex-col gap-3">
        {exports.data?.map(item=>{
          const files=item.manifest.files
          return (
            <Card key={item.id}>
              <CardHeader>
                <CardTitle className="flex flex-wrap items-center justify-between gap-3">
                  <span className="font-mono text-sm">{item.id.slice(0,8)} · {new Date(item.created_at).toLocaleString()}</span>
                  <Badge variant="secondary">Manifest v{item.manifest.archive_schema_version}</Badge>
                </CardTitle>
                <CardDescription>一致性水位：{new Date(item.snapshot_at).toLocaleString()} · {item.size_bytes} bytes · {files.length} 份文件</CardDescription>
              </CardHeader>
              <CardContent className="flex flex-col gap-4">
                <p className="break-all font-mono text-xs text-muted-foreground">归档 SHA-256: {item.sha256}</p>
                <Button asChild variant="outline" className="self-start">
                  <a href={`/api/v1/projects/${projectID}/exports/${item.id}/content`} download={`qual-${projectID.slice(0,8)}-${item.id.slice(0,8)}.zip`}>
                    <Download data-icon="inline-start" aria-hidden="true"/> 下载已验证 ZIP
                  </a>
                </Button>
                <details className="rounded-lg border p-3">
                  <summary className="cursor-pointer text-sm font-medium">查看 Manifest 文件清单</summary>
                  <div className="mt-3 flex flex-col gap-2">
                    {files.map(file=>(
                      <div key={file.path} className="border-b pb-2 text-xs last:border-b-0">
                        <p className="break-all font-medium">{file.path} · {file.size_bytes} bytes</p>
                        <p className="break-all font-mono text-muted-foreground">{file.sha256}</p>
                        {file.records!==undefined&&<p className="text-muted-foreground">{file.records} records</p>}
                      </div>
                    ))}
                  </div>
                </details>
              </CardContent>
            </Card>
          )
        })}
      </div>
      <p className="text-xs text-muted-foreground">本机 MVP 上限：ZIP 32 MiB、每表 1000 行、最多 100 个 ready 附件。超限不会静默生成不完整归档。</p>
    </section>
  )
}
