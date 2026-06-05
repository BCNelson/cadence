import type { ReactNode } from 'react'
import {
  createMemoryHistory,
  createRootRoute,
  createRoute,
  createRouter,
  Outlet,
  RouterProvider,
} from '@tanstack/react-router'

// withRouter renders `children` inside a minimal TanStack Router context.
// Use it from component tests so anything that calls `Link` / `useNavigate`
// has a router to talk to without pulling in the real route tree.
//
// Implementation note: TanStack Router's RouterProvider renders an empty
// tree on first paint until `router.load()` resolves. We invoke load()
// eagerly and return a synchronously-renderable provider — the memory
// router with a single matching path resolves on the next microtask, so
// callers using RTL's `waitFor` see the rendered output. For tests that
// assert synchronously after render(), use `withRouterReady` below.
export function withRouter(children: ReactNode): ReactNode {
  return <RouterProvider router={makeRouter(children)} />
}

// withRouterReady awaits the router's initial load so callers can assert
// synchronously after render(). Use from `await` test bodies.
export async function withRouterReady(children: ReactNode): Promise<ReactNode> {
  const router = makeRouter(children)
  await router.load()
  return <RouterProvider router={router} />
}

function makeRouter(children: ReactNode) {
  const rootRoute = createRootRoute({ component: () => <Outlet /> })
  const childRoute = createRoute({
    getParentRoute: () => rootRoute,
    path: '/',
    component: () => <>{children}</>,
  })
  return createRouter({
    routeTree: rootRoute.addChildren([childRoute]),
    history: createMemoryHistory({ initialEntries: ['/'] }),
  })
}
