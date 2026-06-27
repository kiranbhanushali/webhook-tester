/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react'
import type { Client } from '~/api'
import { RequestEventAction } from '~/api'
import type { Database } from '~/db'
import { DataProvider, useData } from './data'

// Build a captured-request page item in the shape the Client returns (camelCase, parsed types).
const mkItem = (uuid: string, seq: number) => ({
  seq,
  uuid,
  clientAddress: '1.2.3.4',
  method: 'POST' as const,
  requestPayload: new Uint8Array(),
  headers: [] as ReadonlyArray<{ name: string; value: string }>,
  url: new URL('https://example.com/' + uuid),
  capturedAt: new Date(seq), // monotonic so newest-first ordering is deterministic
  authorized: true,
})

// A minimal Database stub: the paginated data flow only touches these methods.
const makeDb = () =>
  ({
    getSessionIDs: vi.fn().mockResolvedValue([]),
    getSession: vi.fn().mockResolvedValue({
      sID: 'sess-1',
      responseCode: 200,
      responseDelay: 0,
      responseHeaders: [],
      responseBody: new Uint8Array(),
      createdAt: new Date(),
      slug: 'sess-1',
      group: null,
      responseScript: null,
      securityHeaders: [],
      forwardUrl: null,
      longLived: false,
      inboundAuthHeader: undefined,
      inboundAuthValue: undefined,
    }),
    getSessionRequests: vi.fn().mockResolvedValue([]), // empty Dexie cache -> server is source of truth
    getRequest: vi.fn().mockResolvedValue({ payload: null }), // lazy payload getter resolves to null (no payload cached)
    putRequest: vi.fn().mockResolvedValue(undefined),
    deleteRequest: vi.fn().mockResolvedValue(undefined),
    deleteAllRequests: vi.fn().mockResolvedValue(undefined),
    putSession: vi.fn().mockResolvedValue(undefined),
    deleteSession: vi.fn().mockResolvedValue(undefined),
    getSessionBySlug: vi.fn().mockResolvedValue(null),
  }) as unknown as Database

// Capture the WebSocket onUpdate listener so a test can push a live request through the same boundary.
const wsRef: { onUpdate: ((e: unknown) => void) | null } = { onUpdate: null }

const makeApi = () =>
  ({
    checkSessionExists: vi.fn().mockResolvedValue({}),
    getSession: vi.fn(),
    getSessionRequests: vi.fn(async (_sID: string, opts?: { before?: number; limit?: number }) => {
      if (!opts?.before) {
        // newest page: r3, r2 (newest first), more remains, cursor at seq=2 (oldest in page)
        return { items: [mkItem('r3', 3), mkItem('r2', 2)], nextBefore: 2, hasMore: true }
      }
      // older page (before=2): r1, nothing older remains
      return { items: [mkItem('r1', 1)], nextBefore: 1, hasMore: false }
    }),
    subscribeToSessionRequests: vi.fn(async (_sID: string, { onUpdate }: { onUpdate: (e: unknown) => void }) => {
      wsRef.onUpdate = onUpdate
      return () => {}
    }),
  }) as unknown as Client

// A tiny consumer that surfaces the data-context state and lets the test drive its actions.
const Consumer = (): React.JSX.Element => {
  const { requests, hasMoreRequests, switchToSession, loadMoreRequests } = useData()

  return (
    <div>
      <button onClick={() => switchToSession('sess-1').then((slow) => slow())}>switch</button>
      <button onClick={() => loadMoreRequests()}>more</button>
      <div data-testid="count">{requests.length}</div>
      <div data-testid="hasMore">{String(hasMoreRequests)}</div>
      <ol>
        {requests.map((r) => (
          <li key={r.rID} data-testid="row">
            {r.rID}
          </li>
        ))}
      </ol>
    </div>
  )
}

let api: Client
let db: Database

const renderProvider = () =>
  render(
    <DataProvider api={api} db={db} errHandler={() => {}}>
      <Consumer />
    </DataProvider>
  )

const rowOrder = () => screen.getAllByTestId('row').map((el) => el.textContent)

// A simple in-memory Storage so useStorage works under Node's experimental localStorage.
const makeStorage = (): Storage => {
  const m = new Map<string, string>()
  return {
    get length() {
      return m.size
    },
    clear: () => m.clear(),
    getItem: (k: string) => (m.has(k) ? (m.get(k) as string) : null),
    key: (i: number) => Array.from(m.keys())[i] ?? null,
    removeItem: (k: string) => void m.delete(k),
    setItem: (k: string, v: string) => void m.set(k, v),
  }
}

beforeEach(() => {
  wsRef.onUpdate = null
  api = makeApi()
  db = makeDb()
  vi.stubGlobal('localStorage', makeStorage())
  vi.stubGlobal('sessionStorage', makeStorage())
})

describe('DataProvider — paginated requests + live WS', () => {
  test('loads the first (newest) page on switchToSession', async () => {
    renderProvider()

    await act(async () => {
      fireEvent.click(screen.getByText('switch'))
    })

    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('2'))
    expect(screen.getByTestId('hasMore')).toHaveTextContent('true')
    expect(rowOrder()).toEqual(['r3', 'r2']) // newest first
    expect(api.getSessionRequests).toHaveBeenCalledWith('sess-1', { limit: 50 })
  })

  test('loadMore fetches the next page with the right `before` cursor and appends (older) results', async () => {
    renderProvider()

    await act(async () => {
      fireEvent.click(screen.getByText('switch'))
    })
    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('2'))

    await act(async () => {
      fireEvent.click(screen.getByText('more'))
    })

    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('3'))
    // the older page was requested with before = nextBefore (2) from the first page
    expect(api.getSessionRequests).toHaveBeenCalledWith('sess-1', { before: 2, limit: 50 })
    expect(rowOrder()).toEqual(['r3', 'r2', 'r1']) // older appended at the bottom
    expect(screen.getByTestId('hasMore')).toHaveTextContent('false') // stops at hasMore=false
  })

  test('loadMore is a no-op once hasMore is false', async () => {
    renderProvider()

    await act(async () => {
      fireEvent.click(screen.getByText('switch'))
    })
    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('2'))

    await act(async () => {
      fireEvent.click(screen.getByText('more'))
    })
    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('3'))

    const callsAfterFirstMore = (api.getSessionRequests as ReturnType<typeof vi.fn>).mock.calls.length

    // cursor is exhausted (hasMore=false) — further loads must not hit the server
    await act(async () => {
      fireEvent.click(screen.getByText('more'))
    })

    expect((api.getSessionRequests as ReturnType<typeof vi.fn>).mock.calls.length).toBe(callsAfterFirstMore)
  })

  test('a live WS request is prepended at the top and de-duped by id', async () => {
    renderProvider()

    await act(async () => {
      fireEvent.click(screen.getByText('switch'))
    })
    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('2'))
    expect(wsRef.onUpdate).not.toBeNull()

    const liveEvent = {
      action: RequestEventAction.create,
      request: {
        uuid: 'r4',
        clientAddress: '9.9.9.9',
        method: 'POST',
        headers: [],
        url: new URL('https://example.com/r4'),
        capturedAt: new Date(4),
        authorized: true,
      },
    }

    await act(async () => {
      wsRef.onUpdate?.(liveEvent)
    })

    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('3'))
    expect(rowOrder()).toEqual(['r4', 'r3', 'r2']) // newest live request at the top

    // pushing the same request again must NOT duplicate it
    await act(async () => {
      wsRef.onUpdate?.(liveEvent)
    })

    await waitFor(() => expect(screen.getByTestId('count')).toHaveTextContent('3'))
    expect(rowOrder()).toEqual(['r4', 'r3', 'r2'])
  })
})
