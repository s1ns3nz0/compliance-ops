import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { loadAuth, onUnauthorized, saveAuth, type AuthState } from './api'

interface AuthContextValue {
  auth: AuthState | null
  connect: (auth: AuthState) => void
  disconnect: () => void
}

const AuthContext = createContext<AuthContextValue | null>(null)

export function AuthProvider({ children }: { children: ReactNode }) {
  const [auth, setAuth] = useState<AuthState | null>(() => loadAuth())
  const queryClient = useQueryClient()

  const disconnect = useCallback(() => {
    saveAuth(null)
    setAuth(null)
    queryClient.clear()
  }, [queryClient])

  const connect = useCallback(
    (next: AuthState) => {
      saveAuth(next)
      setAuth(next)
      queryClient.clear()
    },
    [queryClient],
  )

  useEffect(() => onUnauthorized(() => disconnect()), [disconnect])

  const value = useMemo(() => ({ auth, connect, disconnect }), [auth, connect, disconnect])
  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext)
  if (!ctx) throw new Error('useAuth must be used inside AuthProvider')
  return ctx
}
