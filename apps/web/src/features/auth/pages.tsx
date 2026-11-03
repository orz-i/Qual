import { useState, type FormEvent } from 'react'
import { Link, Navigate, Outlet, useNavigate } from 'react-router'
import { KeyRound, ShieldCheck } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Field, FieldDescription, FieldGroup, FieldLabel } from '@/components/ui/field'
import { Input } from '@/components/ui/input'
import { useLogin, useSession } from '@/features/auth/session'

export function RequireSession() {
  const { data, isPending, isError, error, refetch } = useSession()
  if (isPending) return <p role="status" className="p-8 text-sm text-muted-foreground">正在验证本地会话…</p>
  if (isError) return (
    <div className="flex flex-col gap-3 p-8">
      <h2 className="text-lg font-semibold">身份服务不可用</h2>
      <p role="alert" className="text-sm text-muted-foreground">{error.message}</p>
      <Button variant="outline" onClick={() => void refetch()} className="self-start">重新连接</Button>
    </div>
  )
  if (!data) return <Navigate to="/login" replace />
  return <Outlet />
}

export function LoginPage() {
  const { data: session, isPending } = useSession()
  const [code, setCode] = useState('')
  const login = useLogin()
  const navigate = useNavigate()
  if (isPending) return <p className="p-8 text-sm text-muted-foreground">正在检查会话…</p>
  if (session) return <Navigate to="/projects" replace />
  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    try {
      const result = await login.mutateAsync(code.trim())
      setCode('')
      navigate(result.projects[0] ? `/projects/${result.projects[0].id}` : '/projects', { replace: true })
    } catch {
      // Error is shown by the form; never log the one-time code.
    }
  }

  return (
    <div className="mx-auto flex max-w-lg flex-col gap-7 py-8 sm:py-14">
      <div className="flex flex-col gap-3">
        <div className="flex size-10 items-center justify-center rounded-xl bg-secondary"><ShieldCheck aria-hidden="true" /></div>
        <h1 className="text-3xl font-semibold tracking-tight">登录 Qual</h1>
        <p className="text-sm leading-6 text-muted-foreground">
          使用本机管理员生成的一次性 Human 登录码。登录码五分钟内有效且只能使用一次；
          登录后使用 HttpOnly Cookie，无需在浏览器保存 PAT。
        </p>
      </div>
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2"><KeyRound aria-hidden="true" className="size-4" /> 一次性登录码</CardTitle>
          <CardDescription>仅供本机受控开发环境，不是生产 OIDC 登录。</CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={(event) => { void submit(event) }}>
            <FieldGroup>
              <Field>
                <FieldLabel htmlFor="one-time-code">登录码</FieldLabel>
                <Input id="one-time-code" type="password" value={code}
                  onChange={(event) => setCode(event.target.value)} autoComplete="off"
                  placeholder="qlc_…" required minLength={68} />
                <FieldDescription>由 `qual-admin browser-code -principal &lt;UUID&gt;` 离线生成。</FieldDescription>
              </Field>
              {login.isError && <p role="alert" className="text-sm text-destructive">{login.error.message}</p>}
              <Button type="submit" disabled={login.isPending || code.trim().length < 68} className="w-full">
                {login.isPending ? '正在验证…' : '使用登录码进入'}
              </Button>
            </FieldGroup>
          </form>
        </CardContent>
      </Card>
      <p className="text-xs text-muted-foreground">请勿使用 Agent PAT 登录，也不要把登录码粘贴进聊天或代码仓库。<Link to="/" className="underline underline-offset-4">返回工作台</Link></p>
    </div>
  )
}
