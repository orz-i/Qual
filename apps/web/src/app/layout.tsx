import { useCallback, useEffect, useLayoutEffect, useRef, useState } from 'react'
import { NavLink, Outlet, useLocation } from 'react-router'
import {
  Activity, Archive, ChevronDown, ClipboardCheck, ClipboardList, FolderKanban,
  LayoutDashboard, LogIn, LogOut, Menu, PlugZap, Scale, ShieldCheck, SunMoon, X,
  type LucideIcon,
} from 'lucide-react'
import { Button } from '@/components/ui/button'
import { useLogout, useSession } from '@/features/auth/session'
import { isInCollapsedMenuContent, nextMobileMenuFocusIndex } from '@/app/mobile-menu-focus'
import { applyThemePreference, parseThemePreference, readThemePreference, saveThemePreference, THEME_STORAGE_KEY } from '@/app/theme'
import { cn } from '@/lib/utils'

type NavigationLinkProps = {
  to: string
  label: string
  icon: LucideIcon
  end?: boolean
  onNavigate?: () => void
}

function NavigationLink({ to, label, icon: Icon, end, onNavigate }: NavigationLinkProps) {
  return (
    <NavLink
      to={to}
      end={end}
      onClick={onNavigate}
      className={({ isActive }) => cn(
        'flex min-h-10 items-center gap-3 rounded-xl px-3 py-2 text-sm font-medium transition-colors',
        'focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring',
        isActive
          ? 'bg-sidebar-accent text-sidebar-accent-foreground'
          : 'text-sidebar-foreground/75 hover:bg-sidebar-accent/60 hover:text-sidebar-foreground',
      )}
    >
      <Icon aria-hidden="true" className="size-[18px] shrink-0" />
      <span>{label}</span>
    </NavLink>
  )
}

const workspaceLinks = [
  { to: '/', label: '工作台', icon: LayoutDashboard, end: true },
  { to: '/projects', label: '项目', icon: FolderKanban, end: true },
  { to: '/tests', label: '测试', icon: ClipboardCheck, end: true },
] as const

export function AppLayout() {
  const { data: session, isPending: sessionPending } = useSession()
  const logout = useLogout(session?.csrf_token ?? '')
  const { pathname } = useLocation()
  const [menuOpen, setMenuOpen] = useState(false)
  const [themePreference, setThemePreference] = useState(readThemePreference)
  const [systemDark, setSystemDark] = useState(() => window.matchMedia('(prefers-color-scheme: dark)').matches)
  const menuButtonRef = useRef<HTMLButtonElement>(null)
  const closeButtonRef = useRef<HTMLButtonElement>(null)
  const sidebarRef = useRef<HTMLDivElement>(null)
  const previousPath = useRef(pathname)
  const restoreAfterClose = useRef<'trigger' | 'main' | null>(null)

  const closeMenu = useCallback((target: 'trigger' | 'main' = 'trigger') => {
    if (!menuOpen) return
    restoreAfterClose.current = target
    setMenuOpen(false)
  }, [menuOpen])
  const closeAfterNavigate = () => closeMenu('main')

  // Re-resolve when macOS appearance changes or another tab updates the choice.
  // Explicit light/dark choices take precedence over OS preference.
  useEffect(() => {
    const media = window.matchMedia('(prefers-color-scheme: dark)')
    const syncSystem = () => setSystemDark(media.matches)
    const syncStorage = (event: StorageEvent) => {
      if (event.key === THEME_STORAGE_KEY || event.key === null) {
        setThemePreference(parseThemePreference(event.key === null ? null : event.newValue))
      }
    }
    media.addEventListener('change', syncSystem)
    window.addEventListener('storage', syncStorage)
    return () => {
      media.removeEventListener('change', syncSystem)
      window.removeEventListener('storage', syncStorage)
    }
  }, [])

  useEffect(() => applyThemePreference(themePreference, systemDark), [themePreference, systemDark])

  // Focus the new route's content without adding a keyboard Tab stop.
  useEffect(() => {
    if (previousPath.current !== pathname) {
      previousPath.current = pathname
      document.getElementById('content')?.focus()
    }
  }, [pathname])

  useEffect(() => {
    if (menuOpen || !restoreAfterClose.current) return
    const target = restoreAfterClose.current
    restoreAfterClose.current = null
    if (target === 'main') document.getElementById('content')?.focus()
    else menuButtonRef.current?.focus()
  }, [menuOpen])

  useLayoutEffect(() => {
    if (!menuOpen) return
    const sidebar = sidebarRef.current
    const desktop = window.matchMedia('(min-width: 1024px)')
    const closeOnDesktop = () => {
      // The mobile trigger is hidden at desktop width; return focus to main.
      if (desktop.matches) closeMenu('main')
    }
    desktop.addEventListener('change', closeOnDesktop)
    closeOnDesktop()

    // Move focus into the dialog during the opening commit, before the
    // trigger becomes inert and the browser can blur it to BODY. Retry once
    // after paint only if focus is still outside; never steal a user's Tab.
    const focusInside = () => {
      if (!sidebar?.contains(document.activeElement)) {
        closeButtonRef.current?.focus({ preventScroll: true })
      }
    }
    focusInside()
    const focusFrame = window.requestAnimationFrame(focusInside)
    // During the 200ms translate-in transition Chrome may still consider the
    // formerly invisible sidebar unfocusable. Retry after it becomes visible.
    // The guard leaves any already focused navigation control untouched.
    const revealFocusTask = window.setTimeout(focusInside, 240)
    const controls = () => [...(sidebar?.querySelectorAll<HTMLElement>(
      'a[href], button:not([disabled]), summary, [tabindex]:not([tabindex="-1"])',
    ) ?? [])].filter(node => !isInCollapsedMenuContent(node)
      && !node.closest('[hidden], [inert], [aria-hidden="true"]')
      && getComputedStyle(node).visibility === 'visible'
      && node.getClientRects().length > 0)
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        closeMenu()
        return
      }
      if (event.key !== 'Tab') return
      const all = controls()
      if (!all.length) return
      // Explicitly cycle every Tab rather than relying on Chrome's default
      // tabbing at the document boundary (which can focus the page behind).
      const currentIndex = all.indexOf(document.activeElement as HTMLElement)
      const nextIndex = nextMobileMenuFocusIndex(currentIndex, all.length, event.shiftKey)
      event.preventDefault()
      all[nextIndex]?.focus()
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => {
      window.cancelAnimationFrame(focusFrame)
      window.clearTimeout(revealFocusTask)
      desktop.removeEventListener('change', closeOnDesktop)
      document.removeEventListener('keydown', onKeyDown, true)
    }
  }, [menuOpen, closeMenu])

  const activeProjectID = pathname.match(/^\/projects\/([^/]+)/)?.[1]
  const project = session?.projects.find((entry) => entry.id === activeProjectID) ??
    (session?.projects.length === 1 ? session.projects[0] : undefined)
  const projectPath = project ? `/projects/${project.id}` : ''
  const settingsPage = Boolean(
    projectPath && /\/(access|archive|webhooks)$/.test(pathname),
  )

  return (
    <div className="min-h-svh bg-background text-foreground">
      {menuOpen && (
        <button
          type="button"
          aria-label="关闭导航菜单"
          className="fixed inset-0 z-20 bg-foreground/20 lg:hidden"
          onClick={() => closeMenu()}
        />
      )}
      <div className="min-h-svh lg:grid lg:grid-cols-[232px_minmax(0,1fr)]">
        <div
          id="qual-sidebar"
          ref={sidebarRef}
          role={menuOpen ? 'dialog' : 'complementary'}
          aria-modal={menuOpen ? true : undefined}
          aria-label={menuOpen ? '导航菜单' : undefined}
          className={cn(
            'fixed inset-y-0 left-0 z-30 flex w-64 flex-col overflow-y-auto border-r border-sidebar-border bg-sidebar px-4 py-5 transition-transform duration-200',
            'lg:sticky lg:top-0 lg:z-auto lg:h-svh lg:w-auto lg:translate-x-0',
            menuOpen ? 'visible translate-x-0' : 'invisible -translate-x-full lg:visible',
          )}
        >
          <div className="mb-8 flex items-center justify-between gap-2 px-2">
            <NavLink to="/" onClick={closeAfterNavigate} className="flex items-center gap-2.5 rounded-lg">
              <span aria-hidden="true" className="flex size-9 items-center justify-center rounded-xl bg-primary text-lg font-bold text-primary-foreground">Q</span>
              <span className="flex flex-col">
                <span className="text-lg font-semibold leading-5 tracking-tight">Qual</span>
                <span className="text-[11px] text-muted-foreground">Engineering workspace</span>
              </span>
            </NavLink>
            <Button ref={closeButtonRef} variant="ghost" size="icon-sm" className="lg:hidden" onClick={() => closeMenu()} aria-label="关闭菜单">
              <X aria-hidden="true" />
            </Button>
          </div>

          <nav aria-label="工作空间导航" className="flex flex-col gap-1">
            <p className="mb-1 px-3 text-[11px] font-semibold tracking-wide text-muted-foreground">工作空间</p>
            {workspaceLinks.map(({ to, label, icon, end }) => (
              <NavigationLink key={to} to={to} label={label} icon={icon} end={end} onNavigate={closeAfterNavigate} />
            ))}
          </nav>

          {project && (
            <nav aria-label="当前项目导航" className="mt-8 flex flex-col gap-1">
              <div className="mb-2 flex items-center justify-between px-3">
                <span className="text-[11px] font-semibold tracking-wide text-muted-foreground">当前项目</span>
                <span className="truncate rounded-md bg-card px-1.5 py-0.5 text-[11px] font-semibold text-primary">{project.key}</span>
              </div>
              <NavigationLink to={projectPath} label="工作项" icon={ClipboardList} end onNavigate={closeAfterNavigate} />
              <NavigationLink to={`${projectPath}/reviews`} label="协作审核" icon={Scale} onNavigate={closeAfterNavigate} />
              <NavigationLink to={`${projectPath}/tests/cases`} label="测试用例" icon={ClipboardCheck} onNavigate={closeAfterNavigate} />
              <NavigationLink to={`${projectPath}/tests/runs`} label="测试运行" icon={ClipboardList} onNavigate={closeAfterNavigate} />
              <NavigationLink to={`${projectPath}/activity`} label="项目活动" icon={Activity} onNavigate={closeAfterNavigate} />
              <details key={project.id} className="group mt-1 rounded-xl" open={settingsPage ? true : undefined}>
                <summary className="flex min-h-10 cursor-pointer list-none items-center justify-between rounded-xl px-3 py-2 text-sm font-medium text-sidebar-foreground/75 hover:bg-sidebar-accent/60 focus-visible:outline-2">
                  <span className="flex items-center gap-3"><ShieldCheck aria-hidden="true" className="size-[18px]" />项目管理</span>
                  <ChevronDown aria-hidden="true" className="size-4 transition-transform group-open:rotate-180" />
                </summary>
                <div className="ml-3 flex flex-col gap-1 border-l border-sidebar-border pl-3">
                  <NavigationLink to={`${projectPath}/access`} label="成员与权限" icon={ShieldCheck} onNavigate={closeAfterNavigate} />
                  <NavigationLink to={`${projectPath}/archive`} label="证据归档" icon={Archive} onNavigate={closeAfterNavigate} />
                  <NavigationLink to={`${projectPath}/webhooks`} label="Webhook" icon={PlugZap} onNavigate={closeAfterNavigate} />
                </div>
              </details>
            </nav>
          )}
          <div className="mt-auto pt-8">
            <div className="rounded-xl border border-sidebar-border bg-card/65 p-3">
              <p className="text-xs font-semibold">{project?.key ?? 'Qual Workspace'}</p>
              <p className="mt-1 text-xs leading-5 text-muted-foreground">任务 · 测试 · 证据 · 审计</p>
            </div>
          </div>
        </div>

        <div className="min-w-0" inert={menuOpen}>
          <header className="sticky top-0 z-10 flex min-h-16 items-center justify-between gap-3 border-b border-border bg-card/95 px-4 backdrop-blur-sm sm:px-7">
            <div className="flex min-w-0 items-center gap-3">
              <Button
                ref={menuButtonRef}
                variant="ghost" size="icon-sm" className="lg:hidden"
                aria-controls="qual-sidebar" aria-expanded={menuOpen} aria-label="打开导航菜单"
                onClick={() => setMenuOpen(true)}
              >
                <Menu aria-hidden="true" />
              </Button>
              <div className="flex min-w-0 items-center gap-2 text-sm">
                <span className="hidden text-muted-foreground sm:inline">工作空间</span>
                <span className="hidden text-muted-foreground/60 sm:inline">/</span>
                <span className="truncate font-medium">{project?.key ?? (pathname === '/' ? '工作台' : 'Qual')}</span>
              </div>
            </div>
            <div className="flex shrink-0 items-center gap-2">
              <label className="inline-flex min-h-9 items-center gap-1 text-xs text-muted-foreground">
                <SunMoon aria-hidden="true" className="size-4 shrink-0" />
                <span className="sr-only">外观模式</span>
                <select
                  aria-label="外观模式"
                  value={themePreference}
                  onChange={(event) => {
                    const next = parseThemePreference(event.target.value)
                    setThemePreference(next)
                    saveThemePreference(next)
                  }}
                  className="min-h-9 max-w-24 rounded-lg border border-input bg-background px-1.5 text-xs text-foreground focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-ring"
                >
                  <option value="system">跟随系统</option>
                  <option value="light">浅色</option>
                  <option value="dark">深色</option>
                </select>
              </label>
              {session && (
                <span className="hidden max-w-[240px] truncate rounded-full bg-secondary px-3 py-1.5 text-xs font-medium text-secondary-foreground sm:inline">
                  Human · {session.principal.display_name}
                </span>
              )}
              {session ? (
                <Button size="sm" variant="ghost" disabled={logout.isPending}
                  onClick={() => void logout.mutateAsync().catch(() => {})}>
                  <LogOut aria-hidden="true" className="size-4" />
                  <span className="hidden sm:inline">{logout.isPending ? '退出中…' : '退出'}</span>
                  <span className="sr-only sm:hidden">退出登录</span>
                </Button>
              ) : !sessionPending ? (
                <Button size="sm" variant="outline" asChild>
                  <NavLink to="/login"><LogIn aria-hidden="true" className="size-4" />登录</NavLink>
                </Button>
              ) : <span role="status" className="text-xs text-muted-foreground">读取会话…</span>}
            </div>
          </header>
          {logout.isError && (
            <p role="alert" className="mx-auto max-w-7xl px-5 py-2 text-sm text-destructive">{logout.error.message}</p>
          )}
          <main id="content" tabIndex={-1} className="mx-auto min-h-[calc(100svh-4rem)] w-full max-w-[1500px] px-4 py-7 pb-16 sm:px-7 lg:px-9 lg:py-9">
            <Outlet />
          </main>
        </div>
      </div>
    </div>
  )
}
