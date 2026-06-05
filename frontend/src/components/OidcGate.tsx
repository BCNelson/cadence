import { type ReactNode } from 'react'
import { useAuth } from 'react-oidc-context'

// OidcGate blocks rendering of children until the user has completed an
// OIDC sign-in. The AuthProvider in main.tsx drives the auth-code + PKCE
// flow; this component only surfaces UI state (loading, error, signed-out,
// signed-in).
export function OidcGate({ children }: { children: ReactNode }) {
  const auth = useAuth()

  if (auth.isLoading) {
    return <Splash>Signing in…</Splash>
  }
  if (auth.error) {
    return (
      <Splash>
        <div className="text-rose-700">
          Sign-in failed: {auth.error.message}
        </div>
        <button
          type="button"
          onClick={() => void auth.signinRedirect()}
          className="mt-4 rounded bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700"
        >
          Try again
        </button>
      </Splash>
    )
  }
  if (auth.isAuthenticated) return <>{children}</>

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4">
      <div className="w-full max-w-md rounded-lg bg-white p-8 shadow-sm">
        <h1 className="text-2xl font-bold text-slate-900">cadence</h1>
        <p className="mt-2 text-sm text-slate-600">
          Sign in with your organization's identity provider to view checks.
        </p>
        <button
          type="button"
          onClick={() => void auth.signinRedirect()}
          className="mt-6 w-full rounded bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700"
        >
          Sign in with SSO
        </button>
      </div>
    </main>
  )
}

function Splash({ children }: { children: ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4">
      <div className="w-full max-w-md rounded-lg bg-white p-8 text-center text-sm text-slate-600 shadow-sm">
        {children}
      </div>
    </main>
  )
}
