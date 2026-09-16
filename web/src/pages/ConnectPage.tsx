import { useState, type FormEvent } from 'react'
import { Shield } from 'lucide-react'
import { useAuth } from '@/auth'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

export function ConnectPage() {
  const { connect } = useAuth()
  const [token, setToken] = useState('')

  const submit = (e: FormEvent) => {
    e.preventDefault()
    if (!token.trim()) return
    connect({ token: token.trim() })
  }

  return (
    <div className="flex min-h-full items-center justify-center p-6">
      <Card className="w-full max-w-md">
        <CardHeader>
          <div className="mb-2 flex items-center gap-2 text-slate-700">
            <Shield className="size-6" aria-hidden />
            <span className="text-sm font-medium uppercase tracking-wide">Compliance Ops</span>
          </div>
          <CardTitle className="text-xl">Connect</CardTitle>
          <CardDescription>
            Enter the API bearer token. The token stays in this tab until you close it or disconnect.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="space-y-4">
            <div className="space-y-1.5">
              <Label htmlFor="token">API token</Label>
              <Input
                id="token"
                type="password"
                autoComplete="off"
                required
                value={token}
                onChange={(e) => setToken(e.target.value)}
                placeholder="Bearer token"
              />
            </div>
            <Button type="submit" className="w-full" disabled={!token.trim()}>
              Connect
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  )
}
