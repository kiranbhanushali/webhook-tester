/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, act } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import type { Request as RequestData } from '~/shared'

type TinyRequest = Omit<RequestData, 'payload'>

// A single mutable mock of the data context returned by useData; each test tweaks the fields it needs.
const { dataMock } = vi.hoisted(() => ({
  dataMock: {
    session: { sID: 'sess-1' } as { sID: string } | null,
    request: null as { rID: string } | null,
    requests: [] as Array<TinyRequest>,
    removeAllRequests: vi.fn(),
    removeRequest: vi.fn(),
    loadMoreRequests: vi.fn(),
    hasMoreRequests: false,
    searchIdentifiers: vi.fn().mockResolvedValue([]),
  },
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return { ...mod, useData: () => dataMock }
})

import { SideBar } from './sidebar'

// Capture the IntersectionObserver callback so a test can simulate the sentinel scrolling into view,
// and count instances/observe calls so a test can prove the observer is created ONCE (not per page).
let ioCallback: IntersectionObserverCallback | null = null
let ioInstances = 0
let ioObserveCalls = 0
class MockIntersectionObserver {
  constructor(cb: IntersectionObserverCallback) {
    ioCallback = cb
    ioInstances++
  }
  observe = vi.fn(() => {
    ioObserveCalls++
  })
  unobserve = vi.fn()
  disconnect = vi.fn()
  takeRecords = vi.fn(() => [])
  root = null
  rootMargin = ''
  thresholds = []
}

const fireSentinelVisible = () =>
  act(() => {
    ioCallback?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
  })

const mkReq = (rID: string): TinyRequest => ({
  rID,
  clientAddress: '10.0.0.' + rID.replace(/\D/g, ''),
  method: 'POST',
  headers: [],
  url: new URL('https://example.com/' + rID),
  capturedAt: new Date(),
  authorized: true,
})

const renderSidebar = () =>
  render(
    <MantineProvider>
      <MemoryRouter>
        <SideBar />
      </MemoryRouter>
    </MantineProvider>
  )

beforeEach(() => {
  vi.stubGlobal('IntersectionObserver', MockIntersectionObserver as unknown as typeof IntersectionObserver)
  ioCallback = null
  ioInstances = 0
  ioObserveCalls = 0
  dataMock.session = { sID: 'sess-1' }
  dataMock.request = null
  dataMock.requests = [mkReq('r3'), mkReq('r2')]
  dataMock.hasMoreRequests = false
  dataMock.removeAllRequests = vi.fn().mockResolvedValue(() => Promise.resolve())
  dataMock.removeRequest = vi.fn().mockResolvedValue(() => Promise.resolve())
  dataMock.loadMoreRequests = vi.fn().mockResolvedValue(undefined)
})

afterEach(() => vi.unstubAllGlobals())

describe('SideBar — infinite scroll', () => {
  test('renders the first page of requests', () => {
    renderSidebar()

    expect(screen.getByText('10.0.0.3')).toBeInTheDocument()
    expect(screen.getByText('10.0.0.2')).toBeInTheDocument()
  })

  test('shows the load-more sentinel and fetches the next page when it scrolls into view', () => {
    dataMock.hasMoreRequests = true
    renderSidebar()

    expect(screen.getByTestId('requests-load-more')).toBeInTheDocument()
    expect(dataMock.loadMoreRequests).not.toHaveBeenCalled()

    // simulate the sentinel entering the viewport
    fireSentinelVisible()

    expect(dataMock.loadMoreRequests).toHaveBeenCalledTimes(1)
  })

  test('stops at hasMore=false: no sentinel and no further loads', () => {
    dataMock.hasMoreRequests = false
    renderSidebar()

    expect(screen.queryByTestId('requests-load-more')).toBeNull()
    expect(ioCallback).toBeNull() // no observer is created when there is nothing more to load
    expect(dataMock.loadMoreRequests).not.toHaveBeenCalled()
  })

  test('does NOT start a second load while one is in flight (sentinel staying visible fires twice)', () => {
    dataMock.hasMoreRequests = true
    // a never-settling promise keeps the load "in flight" so the re-entrancy guard stays engaged
    dataMock.loadMoreRequests = vi.fn().mockReturnValue(new Promise<void>(() => {}))
    renderSidebar()

    // the IntersectionObserver re-fires for the still-visible sentinel (the #185 loop trigger)
    fireSentinelVisible()
    fireSentinelVisible()
    fireSentinelVisible()

    // ...but the synchronous ref guard collapses them into a single fetch
    expect(dataMock.loadMoreRequests).toHaveBeenCalledTimes(1)
  })

  test('releases the guard after the in-flight load settles, allowing the next page', async () => {
    dataMock.hasMoreRequests = true
    let resolveLoad: () => void = () => {}
    const pending = new Promise<void>((res) => {
      resolveLoad = res
    })
    dataMock.loadMoreRequests = vi.fn().mockReturnValue(pending)
    renderSidebar()

    fireSentinelVisible()
    expect(dataMock.loadMoreRequests).toHaveBeenCalledTimes(1)

    // still in flight → ignored
    fireSentinelVisible()
    expect(dataMock.loadMoreRequests).toHaveBeenCalledTimes(1)

    // settle the load and flush the .finally microtask that clears the guard
    await act(async () => {
      resolveLoad()
      await pending
    })

    fireSentinelVisible()
    expect(dataMock.loadMoreRequests).toHaveBeenCalledTimes(2)
  })

  test('observes the sentinel ONCE — appending a page does not recreate the observer', () => {
    dataMock.hasMoreRequests = true
    dataMock.requests = [mkReq('r3'), mkReq('r2')]
    const { rerender } = renderSidebar()

    expect(ioInstances).toBe(1)
    expect(ioObserveCalls).toBe(1)

    // a loaded page appends requests (requests.length changes) — the observer must NOT be recreated,
    // which is what previously re-fired the callback for the still-visible sentinel → infinite loop
    dataMock.requests = [mkReq('r5'), mkReq('r4'), mkReq('r3'), mkReq('r2')]
    rerender(
      <MantineProvider>
        <MemoryRouter>
          <SideBar />
        </MemoryRouter>
      </MantineProvider>
    )

    expect(ioInstances).toBe(1)
    expect(ioObserveCalls).toBe(1)
  })
})
