import React, { lazy, Suspense } from 'react'
import { Center, Loader } from '@mantine/core'
import { createPath, Navigate, type RouteObject } from 'react-router-dom'
import { type Client } from '~/api'
import { DefaultLayout } from '~/screens'
import { NotFoundScreen } from '~/screens/not-found'

// Each screen is its own dynamic import so Vite/Rollup can split it into a separate chunk.
// The layout shell (DefaultLayout) stays eager because it is always rendered.
const HomeScreen = lazy(() => import('~/screens/home').then((m) => ({ default: m.HomeScreen })))
const DashboardScreen = lazy(() =>
  import('~/screens/dashboard').then((m) => ({ default: m.DashboardScreen }))
)
const SessionAndRequestScreen = lazy(() =>
  import('~/screens/session').then((m) => ({ default: m.SessionAndRequestScreen }))
)
const SessionsListScreen = lazy(() =>
  import('~/screens/sessions').then((m) => ({ default: m.SessionsListScreen }))
)

/** Lightweight loading state shown while a lazy chunk is being fetched. */
const ScreenFallback: React.FC = () => (
  <Center h="60vh">
    <Loader size="sm" color="dimmed" />
  </Center>
)

export enum RouteIDs {
  Home = 'home',
  Dashboard = 'dashboard',
  SessionAndRequest = 'session-and-request',
  SessionsList = 'sessions-list',
}

export const createRoutes = (apiClient: Client): RouteObject[] => [
  {
    path: '/',
    element: <DefaultLayout api={apiClient} />,
    errorElement: <NotFoundScreen />,
    children: [
      {
        index: true,
        element: (
          <Suspense fallback={<ScreenFallback />}>
            <HomeScreen />
          </Suspense>
        ),
        id: RouteIDs.Home,
      },
      {
        path: 'dashboard',
        id: RouteIDs.Dashboard,
        element: (
          <Suspense fallback={<ScreenFallback />}>
            <DashboardScreen />
          </Suspense>
        ),
      },
      {
        // redirect to the home screen if the path is just `/s/`
        path: 's/',
        element: <Navigate to={pathTo(RouteIDs.Home)} />,
      },
      {
        // please note that `sID` and `rID` accessed via `useParams` hook, and changing this will break the app
        path: 's/:sID/:rID?',
        id: RouteIDs.SessionAndRequest,
        element: (
          <Suspense fallback={<ScreenFallback />}>
            <SessionAndRequestScreen />
          </Suspense>
        ),
      },
      {
        path: 'sessions',
        id: RouteIDs.SessionsList,
        element: (
          <Suspense fallback={<ScreenFallback />}>
            <SessionsListScreen />
          </Suspense>
        ),
      },
    ],
  },
]

type RouteParams<T extends RouteIDs> = T extends RouteIDs.SessionAndRequest
  ? [string /* sID */, string? /* rID (optional) */]
  : [] // no params

/**
 * Converts a route ID to a path to use in a link.
 *
 * @example
 * ```tsx
 * <Link to={pathTo(RouteIDs.Home)}>Go to home</Link>
 * ```
 */
export function pathTo<T extends RouteIDs>(
  path: RouteIDs,
  ...params: T extends RouteIDs ? RouteParams<T> : never
): string {
  switch (path) {
    case RouteIDs.Home:
      return createPath({ pathname: '/' })
    case RouteIDs.Dashboard:
      return createPath({ pathname: '/dashboard' })
    case RouteIDs.SessionsList:
      return createPath({ pathname: '/sessions' })
    case RouteIDs.SessionAndRequest: {
      const [sID, rID] = [params[0] ?? 'no-session', params[1]]

      if (!rID) {
        return createPath({ pathname: `/s/${encodeURIComponent(sID)}` })
      }

      return createPath({ pathname: `/s/${encodeURIComponent(sID)}/${encodeURIComponent(rID)}` })
    }
    default:
      throw new Error(`Unknown route: ${path}`) // will never happen because of the type guard
  }
}
