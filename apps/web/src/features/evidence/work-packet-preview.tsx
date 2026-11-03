import { useState } from 'react'
import { Link } from 'react-router'
import { CheckCircle2, FileSearch, ShieldAlert } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import type { Artifact } from '@/features/evidence/api'
import { verifyWorkEvidencePacket, type WorkEvidencePacketV1 } from '@/features/evidence/work-packet'

const statusLabel: Record<WorkEvidencePacketV1['case_versions'][number]['latest_status'], string> = {
  passed: '最新报告：通过',
  failed: '最新报告：失败',
  blocked: '最新报告：阻塞',
  skipped: '最新报告：跳过',
}

// This is an authorized private download only after a real user opens the
// detail. It does not issue Git/network calls to any untrusted URL in JSON.
export function WorkEvidencePacketPreview({ projectID, workItemID, artifact }: {
  projectID: string
  workItemID: string
  artifact: Artifact
}) {
  const [open, setOpen] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [verified, setVerified] = useState<{ packet: WorkEvidencePacketV1; sha256: string } | null>(null)

  const inspect = async () => {
    setOpen(true)
    if (loading || verified) return
    setLoading(true)
    setError('')
    try {
      if (artifact.state !== 'ready' || artifact.target_kind !== 'work_item' ||
        artifact.target_id !== workItemID || artifact.project_id !== projectID) {
        throw new Error('附件与当前工作项不匹配，无法打开证据包。')
      }
      const url = `/api/v1/projects/${encodeURIComponent(projectID)}/artifacts/${encodeURIComponent(artifact.id)}/content`
      const response = await fetch(url, { credentials: 'same-origin', cache: 'no-store' })
      if (!response.ok) throw new Error(`证据包下载失败（HTTP ${response.status}），请检查当前 Human 权限。`)
      const size = Number(response.headers.get('Content-Length'))
      if (response.headers.get('Content-Length') && size !== artifact.size_bytes) {
        throw new Error('下载长度与服务器附件记录不一致。')
      }
      const bytes = await response.arrayBuffer()
      const result = await verifyWorkEvidencePacket(bytes, {
        projectID, workItemID,
        artifactSHA256: artifact.sha256,
        headerSHA256: response.headers.get('X-Content-SHA256'),
        sizeBytes: artifact.size_bytes,
      })
      setVerified(result)
    } catch (problem) {
      setError(problem instanceof Error ? problem.message : '证据包核验失败；不展示未经验证的内容。')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button type="button" size="sm" variant="outline" onClick={() => void inspect()}>
          <FileSearch aria-hidden="true" data-icon="inline-start" /> 核验并查看
        </Button>
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>工作项证据包</DialogTitle>
          <DialogDescription>从当前项目私有 S3 下载，核对长度、SHA-256、项目和工作项标识后才展示内容。</DialogDescription>
        </DialogHeader>
        {loading && <p role="status" className="text-sm text-muted-foreground">正在下载并校验 SHA-256…</p>}
        {error && (
          <div className="space-y-3">
            <p role="alert" className="flex items-start gap-2 rounded-lg border border-destructive/30 p-3 text-sm text-destructive">
              <ShieldAlert className="mt-0.5 size-4 shrink-0" aria-hidden="true" /> {error}
            </p>
            <Button type="button" variant="outline" size="sm" onClick={() => void inspect()}>重新核验</Button>
          </div>
        )}
        {verified && (
          <div className="min-w-0 space-y-4 text-sm">
            <p role="status" className="flex items-start gap-2 rounded-lg border border-border bg-muted/40 p-3">
              <CheckCircle2 aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-emerald-600" />
              已核对私有附件内容与 Qual SHA-256；这不代表 Git 仓库或外部测试经过独立复核。
            </p>
            <dl className="grid min-w-0 gap-3 rounded-lg border p-3 sm:grid-cols-2">
              <div className="min-w-0"><dt className="text-xs text-muted-foreground">来源标记</dt><dd>外部 CLI · 本地 Git 对象核验</dd></div>
              <div className="min-w-0"><dt className="text-xs text-muted-foreground">固定 CaseVersion</dt><dd>{verified.packet.case_versions.length} 项</dd></div>
              <div className="min-w-0 sm:col-span-2"><dt className="text-xs text-muted-foreground">Git Commit（外部宿主报告）</dt><dd className="break-all font-mono text-xs">{verified.packet.git_commit}</dd></div>
              <div className="min-w-0 sm:col-span-2"><dt className="text-xs text-muted-foreground">附件 SHA-256（本次重新计算）</dt><dd className="break-all font-mono text-xs">{verified.sha256}</dd></div>
            </dl>
            <div className="space-y-2">
              <h3 className="font-semibold">关联测试运行</h3>
              <Link className="block break-all text-sm font-medium text-primary underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2" to={`/projects/${projectID}/tests/runs/${verified.packet.test_run_id}`}>
                查看 TestRun · {verified.packet.test_run_id}
              </Link>
              <ul className="space-y-2">
                {verified.packet.case_versions.map(item => (
                  <li key={item.case_version_id} className="min-w-0 rounded-lg border p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <Link className="font-medium text-primary underline underline-offset-4 focus-visible:outline-2 focus-visible:outline-offset-2" to={`/projects/${projectID}/tests/cases/${item.test_case_id}`}>
                        {item.test_case_key}
                      </Link>
                      <span className="text-xs text-muted-foreground">{statusLabel[item.latest_status]}</span>
                    </div>
                    <p className="mt-2 text-xs text-muted-foreground">已报告 Attempt：{item.attempt_count} 次。最新结果不覆盖过去的失败。</p>
                    <p className="mt-1 break-all font-mono text-xs text-muted-foreground">CaseVersion: {item.case_version_id}</p>
                    <p className="mt-1 break-all font-mono text-xs text-muted-foreground">Snapshot SHA-256: {item.snapshot_sha256}</p>
                  </li>
                ))}
              </ul>
            </div>
            <p className="text-xs leading-5 text-muted-foreground">这里的状态来自已归档的外部报告快照；请打开 TestRun 查看每次不可变 Attempt 与关联缺陷。附件校验成功不等于工作项验收通过。</p>
          </div>
        )}
      </DialogContent>
    </Dialog>
  )
}
