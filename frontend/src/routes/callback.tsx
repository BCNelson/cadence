import { createFileRoute } from '@tanstack/react-router'
import { useAuth } from 'react-oidc-context'

// /callback is the OIDC redirect_uri. AuthProvider (in main.tsx) parses
// the auth-code response automatically; we render a brief placeholder
// while that happens. onSigninCallback (configured on AuthProvider)
// replaces the URL back to "/" once the code is exchanged.
export const Route = createFileRoute('/callback')({
  component: CallbackPage,
})

function CallbackPage() {
  const auth = useAuth()
  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4">
      <div className="w-full max-w-md rounded-lg bg-white p-8 text-center text-sm text-slate-600 shadow-sm">
        {auth.error ? `Sign-in failed: ${auth.error.message}` : 'Signing you in…'}
      </div>
    </main>
  )
}
