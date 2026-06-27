/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { MemoryRouter } from 'react-router-dom'
import { type FirehoseEvent, FirehoseEventAction, type SessionSummary } from '~/api'

type FirehoseListeners = {
  onConnected?: () => void
  onEvent: (e: FirehoseEvent) => void
  onError?: (err: Error) => void
}

type RecentEventsPage = { items: ReadonlyArray<FirehoseEvent>; nextBefore: number; hasMore: boolean }

// Hoisted mocks so the vi.mock factory can reference them; fhRef captures the firehose listeners so a
// test can push events through the SAME boundary the dashboard subscribed to (no real socket).
const {
  mockListAllSessions,
  mockSubscribeFirehose,
  mockSwitchToSession,
  mockSwitchToRequest,
  mockGetRecentEvents,
  fhRef,
} = vi.hoisted(() => ({
  mockListAllSessions: vi.fn<() => Promise<ReadonlyArray<SessionSummary>>>(),
  mockSubscribeFirehose: vi.fn<(l: FirehoseListeners) => Promise<() => void>>(),
  mockSwitchToSession: vi.fn<(sID: string) => Promise<() => Promise<void>>>(),
  mockSwitchToRequest: vi.fn<(sID: string, rID: string | null) => Promise<() => Promise<void>>>(),
  mockGetRecentEvents: vi.fn<(opts?: { session?: string; group?: string; before?: number }) => Promise<RecentEventsPage>>(),
  fhRef: { current: null as FirehoseListeners | null },
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      listAllSessions: mockListAllSessions,
      subscribeFirehose: mockSubscribeFirehose,
      switchToSession: mockSwitchToSession,
      switchToRequest: mockSwitchToRequest,
      getRecentEvents: mockGetRecentEvents,
    }),
  }
})

// Stub the reused RequestDetails so the dashboard test stays at its own boundary.
vi.mock('~/screens/session/components', () => ({
  RequestDetails: ({ loading }: { loading?: boolean }): React.JSX.Element => (
    <div data-testid="request-details">detail-loading={String(!!loading)}</div>
  ),
}))

// Stub the reused NewSessionModal (it pulls settings/data internals we don't exercise here).
vi.mock('~/screens/components/header/components', () => ({
  NewSessionModal: ({ opened }: { opened: boolean }): React.JSX.Element | null =>
    opened ? <div data-testid="new-session-modal" /> : null,
}))

// Import after mocks (vi.mock is hoisted).
import { DashboardScreen } from './screen'

const SESSIONS: ReadonlyArray<SessionSummary> = [
  {
    uuid: 'uuid-alpha',
    slug: 'session-alpha',
    group: 'team-a',
    statusCode: 200,
    requestsCount: 3,
    lastRequestAt: null,
    createdAt: new Date(0),
    expiresAt: new Date(0),
    longLived: false,
  },
  {
    uuid: 'uuid-beta',
    slug: 'session-beta',
    group: null,
    statusCode: 404,
    requestsCount: 0,
    lastRequestAt: null,
    createdAt: new Date(0),
    expiresAt: new Date(0),
    longLived: false,
  },
]

const makeEvent = (over: {
  sessionUUID: string
  sessionSlug: string
  rID: string
  method?: string
  path?: string
  authorized?: boolean
}): FirehoseEvent =>
  Object.freeze({
    sessionUUID: over.sessionUUID,
    sessionSlug: over.sessionSlug,
    action: FirehoseEventAction.create,
    request: Object.freeze({
      uuid: over.rID,
      clientAddress: '203.0.113.7',
      method: over.method ?? 'POST',
      url: Object.freeze(new URL(`http://localhost${over.path ?? '/w/' + over.sessionSlug + '/x'}`)),
      capturedAt: new Date(),
      authorized: over.authorized ?? true,
    }),
  })

const renderScreen = () =>
  render(
    <MantineProvider>
      <Notifications />
      <MemoryRouter>
        <DashboardScreen />
      </MemoryRouter>
    </MantineProvider>
  )

/** Push a firehose event through the captured boundary (wrapped in act for the state update). */
const pushEvent = (event: FirehoseEvent): void => {
  act(() => {
    fhRef.current?.onEvent(event)
  })
}

describe('DashboardScreen', () => {
  beforeEach(() => {
    mockListAllSessions.mockResolvedValue(SESSIONS)
    mockSubscribeFirehose.mockImplementation((listeners: FirehoseListeners) => {
      fhRef.current = listeners
      listeners.onConnected?.()

      return Promise.resolve(() => {})
    })
    mockSwitchToSession.mockResolvedValue(() => Promise.resolve())
    mockSwitchToRequest.mockResolvedValue(() => Promise.resolve())
    // recent backfill is empty by default; individual tests override as needed
    mockGetRecentEvents.mockResolvedValue({ items: [], nextBefore: 0, hasMore: false })
  })

  afterEach(() => {
    fhRef.current = null
    vi.clearAllMocks()
  })

  test('renders the endpoint rail and an empty live stream', async () => {
    renderScreen()

    // rail lists the endpoints from listAllSessions()
    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })
    expect(screen.getByText('session-beta')).toBeInTheDocument()
    expect(screen.getByText('All endpoints')).toBeInTheDocument()

    // recent backfill ran (empty here), then the empty-stream prompt + live indicator (onConnected fired)
    await waitFor(() => expect(mockGetRecentEvents).toHaveBeenCalled())
    await waitFor(() => expect(screen.getByText(/waiting for incoming webhooks/i)).toBeInTheDocument())
    await waitFor(() => expect(screen.getByText('Live')).toBeInTheDocument())
  })

  test('a firehose event prepends a row with the slug, method and an Unauthorized badge', async () => {
    renderScreen()

    await waitFor(() => expect(screen.getByText('session-alpha')).toBeInTheDocument())

    pushEvent(
      makeEvent({ sessionUUID: 'uuid-alpha', sessionSlug: 'session-alpha', rID: 'req-1', method: 'POST', authorized: false })
    )

    // the row carries the method, the derived 401 status and the Unauthorized badge
    await waitFor(() => expect(screen.getByText('POST')).toBeInTheDocument())
    expect(screen.getByText('Unauthorized')).toBeInTheDocument()
    expect(screen.getByText('401')).toBeInTheDocument()
    // the slug appears both in the rail and in the new stream row
    expect(screen.getAllByText('session-alpha').length).toBeGreaterThan(1)
  })

  test('selecting a session in the rail refetches the backfill and filters live events to it', async () => {
    renderScreen()

    await waitFor(() => expect(screen.getByText('session-beta')).toBeInTheDocument())

    // filter to session-beta: triggers a fresh backfill scoped to that session (empty here)
    fireEvent.click(screen.getByRole('button', { name: /filter stream to session-beta/i }))

    await waitFor(() =>
      expect(mockGetRecentEvents).toHaveBeenCalledWith(expect.objectContaining({ session: 'uuid-beta' }))
    )

    // under the beta filter, a beta live event shows and an alpha live event is filtered out
    pushEvent(makeEvent({ sessionUUID: 'uuid-beta', sessionSlug: 'session-beta', rID: 'req-b', method: 'GET' }))
    pushEvent(makeEvent({ sessionUUID: 'uuid-alpha', sessionSlug: 'session-alpha', rID: 'req-a', method: 'POST' }))

    await waitFor(() => expect(screen.getByText('GET')).toBeInTheDocument())
    expect(screen.queryByText('POST')).not.toBeInTheDocument()
  })

  test('clicking a stream row triggers the detail fetch (switchToSession + switchToRequest)', async () => {
    renderScreen()

    await waitFor(() => expect(screen.getByText('session-alpha')).toBeInTheDocument())

    pushEvent(makeEvent({ sessionUUID: 'uuid-alpha', sessionSlug: 'session-alpha', rID: 'req-1', method: 'POST' }))

    const row = await screen.findByRole('button', { name: /open post request to session-alpha/i })
    fireEvent.click(row)

    await waitFor(() => {
      expect(mockSwitchToSession).toHaveBeenCalledWith('uuid-alpha')
      expect(mockSwitchToRequest).toHaveBeenCalledWith('uuid-alpha', 'req-1')
    })

    // the reused RequestDetails renders inside the detail drawer
    expect(await screen.findByTestId('request-details')).toBeInTheDocument()
  })
})
