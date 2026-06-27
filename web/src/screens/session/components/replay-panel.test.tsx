/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import type { Session } from '~/shared'
import type { ReplayResult } from '~/api'

// Hoist mock functions so they are available in vi.mock factory
const { mockReplayRequest } = vi.hoisted(() => ({
  mockReplayRequest: vi.fn<(ref: string, rID: string, targetUrl?: string) => Promise<ReplayResult>>(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      replayRequest: mockReplayRequest,
    }),
  }
})

// Import after mock is defined (vi.mock is hoisted, so this is fine)
import { ReplayPanel } from './replay-panel'

const ENCODER = new TextEncoder()

const BASE_SESSION: Session = {
  sID: 'uuid-session-1234',
  responseCode: 200,
  responseHeaders: [],
  responseDelay: 0,
  responseBody: new Uint8Array(),
  slug: 'test-session',
  group: null,
  responseScript: null,
  securityHeaders: [],
  forwardUrl: 'https://example.com/hook',
  longLived: false,
}

const BASE_REQUEST = {
  rID: 'uuid-request-5678',
  clientAddress: '127.0.0.1',
  method: 'POST',
  headers: [{ name: 'Content-Type', value: 'application/json' }],
  url: new URL('http://localhost/uuid-session-1234'),
  capturedAt: new Date('2025-01-01T12:00:00Z'),
  payload: null,
}

const renderPanel = (session: Session = BASE_SESSION, request = BASE_REQUEST) =>
  render(
    <MantineProvider>
      <MemoryRouter>
        <ReplayPanel session={session} request={request} />
      </MemoryRouter>
    </MantineProvider>
  )

describe('ReplayPanel', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('target URL input defaults to session forwardUrl', () => {
    renderPanel()

    const input = screen.getByRole('textbox', { name: /target url/i }) as HTMLInputElement
    expect(input.value).toBe('https://example.com/hook')
  })

  test('target URL input is empty when session has no forwardUrl', () => {
    renderPanel({ ...BASE_SESSION, forwardUrl: null })

    const input = screen.getByRole('textbox', { name: /target url/i }) as HTMLInputElement
    expect(input.value).toBe('')
  })

  test('replay button is disabled when no target URL and no session forwardUrl', () => {
    renderPanel({ ...BASE_SESSION, forwardUrl: null })

    expect(screen.getByRole('button', { name: /replay/i })).toBeDisabled()
  })

  test('replay button is enabled when session has forwardUrl', () => {
    renderPanel()

    expect(screen.getByRole('button', { name: /replay/i })).not.toBeDisabled()
  })

  test('replay button is enabled when user types a target URL even without session forwardUrl', () => {
    renderPanel({ ...BASE_SESSION, forwardUrl: null })

    fireEvent.change(screen.getByRole('textbox', { name: /target url/i }), {
      target: { value: 'https://typed.example.com/hook' },
    })

    expect(screen.getByRole('button', { name: /replay/i })).not.toBeDisabled()
  })

  test('clicking Replay calls replayRequest with sID, rID, and the target URL', async () => {
    const result: ReplayResult = {
      statusCode: 200,
      headers: [],
      body: ENCODER.encode('{"ok":true}'),
    }
    mockReplayRequest.mockResolvedValue(result)

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(mockReplayRequest).toHaveBeenCalledWith(
        'uuid-session-1234',
        'uuid-request-5678',
        'https://example.com/hook'
      )
    })
  })

  test('clicking Replay calls replayRequest with typed target URL overriding session forwardUrl', async () => {
    const result: ReplayResult = {
      statusCode: 201,
      headers: [],
      body: ENCODER.encode('created'),
    }
    mockReplayRequest.mockResolvedValue(result)

    renderPanel()

    fireEvent.change(screen.getByRole('textbox', { name: /target url/i }), {
      target: { value: 'https://custom.example.com/hook' },
    })

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(mockReplayRequest).toHaveBeenCalledWith(
        'uuid-session-1234',
        'uuid-request-5678',
        'https://custom.example.com/hook'
      )
    })
  })

  test('on success renders the downstream HTTP status badge', async () => {
    const result: ReplayResult = {
      statusCode: 200,
      headers: [],
      body: ENCODER.encode('{"ok":true}'),
    }
    mockReplayRequest.mockResolvedValue(result)

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(screen.getByText('200')).toBeInTheDocument()
    })
  })

  test('on success renders the response body', async () => {
    const result: ReplayResult = {
      statusCode: 200,
      headers: [],
      body: ENCODER.encode('hello from downstream'),
    }
    mockReplayRequest.mockResolvedValue(result)

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(screen.getByText(/hello from downstream/)).toBeInTheDocument()
    })
  })

  test('on success shows (empty body) for empty response body', async () => {
    const result: ReplayResult = {
      statusCode: 204,
      headers: [],
      body: new Uint8Array(),
    }
    mockReplayRequest.mockResolvedValue(result)

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(screen.getByText('204')).toBeInTheDocument()
      expect(screen.getByText(/empty body/i)).toBeInTheDocument()
    })
  })

  test('on error renders a friendly error message', async () => {
    mockReplayRequest.mockRejectedValue(new Error('network failure'))

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(screen.getByText(/network failure/i)).toBeInTheDocument()
    })
  })

  test('on unknown error renders a generic error message', async () => {
    mockReplayRequest.mockRejectedValue('something went wrong')

    renderPanel()

    fireEvent.click(screen.getByRole('button', { name: /replay/i }))

    await waitFor(() => {
      expect(screen.getByText(/replay failed/i)).toBeInTheDocument()
    })
  })

  test('shows hint about target URL when no forwardUrl and input is empty', () => {
    renderPanel({ ...BASE_SESSION, forwardUrl: null })

    expect(screen.getByText(/set a target url/i)).toBeInTheDocument()
  })
})
