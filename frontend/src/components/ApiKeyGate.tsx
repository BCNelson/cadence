import { useState, type ReactNode } from 'react'
import { getApiKey, setApiKey } from '../api'

// ApiKeyGate blocks rendering of children until the user has supplied an
// API key. The key is stored in localStorage so dev/operator sessions
// don't have to re-enter it constantly.
export function ApiKeyGate({ children }: { children: ReactNode }) {
  const [hasKey, setHasKey] = useState(() => Boolean(getApiKey()))
  const [draft, setDraft] = useState('')

  if (hasKey) return <>{children}</>

  return (
    <main className="flex min-h-screen items-center justify-center bg-slate-50 px-4">
      <div className="w-full max-w-md rounded-lg bg-white p-8 shadow-sm">
        <h1 className="text-2xl font-bold text-slate-900">cadence</h1>
        <p className="mt-2 text-sm text-slate-600">
          Enter an API key (from <code className="rounded bg-slate-100 px-1">server.api_keys</code>
          {' '}in your config) to view checks.
        </p>
        <form
          className="mt-6 space-y-3"
          onSubmit={(e) => {
            e.preventDefault()
            if (draft.trim() === '') return
            setApiKey(draft.trim())
            setHasKey(true)
          }}
        >
          <input
            type="password"
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            placeholder="X-Api-Key"
            className="w-full rounded border border-slate-300 px-3 py-2 text-sm focus:border-sky-500 focus:outline-none"
          />
          <button
            type="submit"
            className="w-full rounded bg-slate-900 px-3 py-2 text-sm font-medium text-white hover:bg-slate-700"
          >
            Continue
          </button>
        </form>
      </div>
    </main>
  )
}
