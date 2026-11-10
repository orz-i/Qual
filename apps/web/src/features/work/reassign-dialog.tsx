import { useState, type FormEvent } from 'react'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, DialogTrigger } from '@/components/ui/dialog'
import { Field, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useWorkActions } from '@/features/work/api'
import { QualAPIError } from '@/lib/qual-api'

const uuidPattern = /^[a-f\d]{8}(?:-[a-f\d]{4}){3}-[a-f\d]{12}$/i

export function ReassignWorkDialog({ projectID, itemID, version, onRefresh }: {
  projectID: string
  itemID: string
  version: number
  onRefresh: () => Promise<void>
}) {
  const [open, setOpen] = useState(false)
  const [assignee, setAssignee] = useState('')
  const actions = useWorkActions(projectID)
  const error = actions.reassign.error
  const conflict = error instanceof QualAPIError &&
    (error.code === 'VERSION_CONFLICT' || error.code === 'CONFLICT' || error.code === 'ALREADY_CLAIMED')
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!uuidPattern.test(assignee)) return
    try {
      await actions.reassign.mutateAsync({
        itemID, version, newAssigneeID: assignee.toLowerCase(),
      })
      setOpen(false)
      setAssignee('')
    } catch {
      // Keep the intended target visible so the user can inspect a 403/409.
    }
  }
  return (
    <Dialog open={open} onOpenChange={(next) => { setOpen(next); if (next) actions.reassign.reset() }}>
      <DialogTrigger asChild><Button size="sm" variant="outline">转交任务</Button></DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>转交当前任务</DialogTitle>
          <DialogDescription>指定本项目中真实、具备参与资格的 Human 或 Agent。服务器核实接手者的凭证与项目权限；转交不会启动 Agent。</DialogDescription>
        </DialogHeader>
        <form onSubmit={event => { void submit(event) }} className="space-y-4">
          <Field>
            <FieldLabel htmlFor="transfer-assignee">接手者 Principal UUID</FieldLabel>
            <Input id="transfer-assignee" value={assignee} required
              placeholder="xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx"
              aria-describedby="transfer-version"
              maxLength={36} onChange={event => setAssignee(event.target.value.trim())} />
          </Field>
          <p id="transfer-version" className="text-xs text-muted-foreground">
            以任务版本 v{version} 转交，先完成当前版本校验。不得使用另一个人的令牌。
          </p>
          {error && <p role="alert" className="text-sm text-destructive">{error.message}</p>}
          {conflict && <Button type="button" variant="outline" size="sm"
            onClick={() => { void onRefresh().then(() => actions.reassign.reset()) }}>
            刷新版本并核对接手者
          </Button>}
          <DialogFooter>
            <Button type="submit" disabled={!uuidPattern.test(assignee) || actions.reassign.isPending || conflict}>
              {actions.reassign.isPending ? '正在核验转交…' : '确认转交'}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  )
}
