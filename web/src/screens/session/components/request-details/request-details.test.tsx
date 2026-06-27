/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import type { Request as RequestData, Session } from '~/shared'

const BASE_SESSION: Session = {
  sID: 'uuid-session-1',
  responseCode: 200,
  responseHeaders: [],
  responseDelay: 0,
  responseBody: new Uint8Array(),
  slug: 'test-session',
  group: null,
  responseScript: null,
  securityHeaders: [],
  forwardUrl: null,
  longLived: false,
  inboundAuthHeader: null,
  inboundAuthValue: null,
}

const BASE_REQUEST: RequestData = {
  rID: 'uuid-request-1',
  clientAddress: '127.0.0.1',
  method: 'POST',
  headers: [{ name: 'Content-Type', value: 'application/json' }],
  url: new URL('http://localhost/uuid-session-1'),
  capturedAt: new Date('2025-01-01T12:00:00Z'),
  payload: null,
  authorized: undefined,
}

// Hoisted holder so the vi.mock factory can read a per-test request
const { state } = vi.hoisted(() => ({ state: { request: null as RequestData | null } }))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      session: BASE_SESSION,
      request: state.request,
      replayRequest: vi.fn(),
    }),
    useSettings: () => ({
      showRequestDetails: true,
      maxRequestBodySize: 0,
    }),
  }
})

// Import after the mock is defined (vi.mock is hoisted)
import { RequestDetails } from './request-details'

const renderDetails = (request: RequestData) => {
  state.request = request

  return render(
    <MantineProvider>
      <MemoryRouter>
        <RequestDetails />
      </MemoryRouter>
    </MantineProvider>
  )
}

describe('RequestDetails — unauthorized badge', () => {
  beforeEach(() => {
    state.request = null
  })

  test('does NOT show the Unauthorized badge when authorized is undefined (no auth configured)', () => {
    renderDetails({ ...BASE_REQUEST, authorized: undefined })
    expect(screen.queryByText(/unauthorized/i)).toBeNull()
  })

  test('does NOT show the Unauthorized badge when authorized is true', () => {
    renderDetails({ ...BASE_REQUEST, authorized: true })
    expect(screen.queryByText(/unauthorized/i)).toBeNull()
  })

  test('shows the Unauthorized badge when authorized is false', () => {
    renderDetails({ ...BASE_REQUEST, authorized: false })
    expect(screen.getByText(/unauthorized/i)).toBeInTheDocument()
  })
})
