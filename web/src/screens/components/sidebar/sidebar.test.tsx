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
  },
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return { ...mod, useData: () => dataMock }
})

import { SideBar } from './sidebar'

// Capture the IntersectionObserver callback so a test can simulate the sentinel scrolling into view.
let ioCallback: IntersectionObserverCallback | null = null
class MockIntersectionObserver {
  constructor(cb: IntersectionObserverCallback) {
    ioCallback = cb
  }
  observe = vi.fn()
  unobserve = vi.fn()
  disconnect = vi.fn()
  takeRecords = vi.fn(() => [])
  root = null
  rootMargin = ''
  thresholds = []
}

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
    act(() => {
      ioCallback?.([{ isIntersecting: true } as IntersectionObserverEntry], {} as IntersectionObserver)
    })

    expect(dataMock.loadMoreRequests).toHaveBeenCalledTimes(1)
  })

  test('stops at hasMore=false: no sentinel and no further loads', () => {
    dataMock.hasMoreRequests = false
    renderSidebar()

    expect(screen.queryByTestId('requests-load-more')).toBeNull()
    expect(ioCallback).toBeNull() // no observer is created when there is nothing more to load
    expect(dataMock.loadMoreRequests).not.toHaveBeenCalled()
  })
})
