import { NavLink, Outlet } from 'react-router'
import { AlertTriangle, ClipboardCheck, FileCheck2, LayoutDashboard, ListChecks, LogOut, Shield } from 'lucide-react'
import { useAuth } from '@/auth'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import { ASSESSMENT_NOTICE_HEADING, ASSESSMENT_NOTICE_ITEMS } from '@/lib/uiCopy'

const NAV = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard, end: true },
  { to: '/frameworks', label: 'Frameworks', icon: Shield },
  { to: '/requirements', label: 'Requirements', icon: ListChecks },
  { to: '/evidence', label: 'Evidence', icon: FileCheck2 },
  { to: '/assessment', label: 'Assessment', icon: ClipboardCheck },
]

export function Layout() {
  const { disconnect } = useAuth()
  return (
    <div className="flex min-h-full flex-col">
      <header className="sticky top-0 z-20 border-b border-slate-200 bg-white">
        <div className="mx-auto flex h-14 max-w-7xl items-center gap-6 px-4 sm:px-6">
          <NavLink to="/" className="flex items-center gap-2 font-semibold tracking-tight">
            <Shield className="size-5 text-slate-700" aria-hidden />
            Compliance Ops
          </NavLink>
          <nav className="flex items-center gap-1" aria-label="Primary">
            {NAV.map(({ to, label, icon: Icon, end }) => (
              <NavLink
                key={to}
                to={to}
                end={end}
                className={({ isActive }) =>
                  cn(
                    'flex items-center gap-1.5 rounded-md px-3 py-1.5 text-sm transition-colors',
                    isActive ? 'bg-slate-900 text-white' : 'text-slate-600 hover:bg-slate-100 hover:text-slate-900',
                  )
                }
                title={label}
              >
                <Icon className="size-4" aria-hidden />
                <span>{label}</span>
              </NavLink>
            ))}
          </nav>
          <div className="ml-auto flex items-center gap-3 text-sm">
            <Button variant="outline" size="sm" onClick={disconnect}>
              <LogOut /> Disconnect
            </Button>
          </div>
        </div>
      </header>
      <div
        role="alert"
        aria-labelledby="assessment-notice-heading"
        className="sticky top-14 z-10 w-full border-y border-red-400 bg-red-100 text-red-900"
      >
        <div className="mx-auto flex max-w-7xl items-start gap-3 px-4 py-3 text-sm leading-5 sm:px-6">
          <AlertTriangle className="mt-0.5 size-5 shrink-0 text-red-700" aria-hidden />
          <div>
            <p id="assessment-notice-heading" className="font-semibold">{ASSESSMENT_NOTICE_HEADING}</p>
            <ul className="mt-1 list-disc space-y-0.5 pl-5">
              {ASSESSMENT_NOTICE_ITEMS.map((item) => <li key={item}>{item}</li>)}
            </ul>
          </div>
        </div>
      </div>
      <main className="mx-auto w-full max-w-7xl flex-1 px-4 py-6 sm:px-6">
        <Outlet />
      </main>
    </div>
  )
}

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: string
  subtitle?: string
  actions?: React.ReactNode
}) {
  return (
    <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        {subtitle && <p className="mt-1 text-sm text-slate-500">{subtitle}</p>}
      </div>
      {actions && <div className="flex items-center gap-2">{actions}</div>}
    </div>
  )
}
