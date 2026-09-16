import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button } from '@/components/ui/button'

interface ErrorBoundaryProps {
  children: ReactNode
}

interface ErrorBoundaryState {
  error: Error | null
}

function sanitizedErrorMessage(error: Error): string {
  const message = error.message.trim()
    .replace(/Bearer\s+\S+/gi, 'Bearer [redacted]')
    .replace(/\b(authorization|token|request headers?)\s*[:=]\s*[^,;\n]+/gi, '$1: [redacted]')
    .slice(0, 180)
  return message || 'An unexpected application error occurred.'
}

export class ErrorBoundary extends Component<ErrorBoundaryProps, ErrorBoundaryState> {
  state: ErrorBoundaryState = { error: null }

  static getDerivedStateFromError(error: Error): ErrorBoundaryState {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('Unhandled application error', error.name, info.componentStack)
  }

  render() {
    if (!this.state.error) return this.props.children
    return (
      <main className="mx-auto flex min-h-screen max-w-xl items-center px-6 py-16">
        <section role="alert" className="w-full rounded-xl border border-red-200 bg-white p-6 shadow-sm">
          <h1 className="text-xl font-semibold text-slate-900">Something went wrong</h1>
          <p className="mt-2 text-sm text-slate-600">{sanitizedErrorMessage(this.state.error)}</p>
          <Button className="mt-5" onClick={() => window.location.reload()}>Reload page</Button>
        </section>
      </main>
    )
  }
}