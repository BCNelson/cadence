import { createFileRoute, Outlet } from '@tanstack/react-router'
import { AuthGate } from '../components/AuthGate'

// Layout for /checks/$slug/*. The leaf at /checks/$slug lives in
// checks.$slug.index.tsx and the per-ping page in
// checks.$slug.pings.$pingId.tsx; this file just gates access and
// renders the matched child via Outlet.
export const Route = createFileRoute('/checks/$slug')({
  component: RouteComponent,
})

function RouteComponent() {
  return (
    <AuthGate>
      <Outlet />
    </AuthGate>
  )
}
