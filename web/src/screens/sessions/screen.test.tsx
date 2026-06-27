/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { MemoryRouter } from 'react-router-dom'
import type { SessionSummary } from '~/api'

// Hoist mock functions so they are available in vi.mock factory
const { mockListAllSessions, mockDestroySession } = vi.hoisted(() => ({
  mockListAllSessions: vi.fn<() => Promise<ReadonlyArray<SessionSummary>>>(),
  mockDestroySession: vi.fn<(sID: string) => Promise<() => Promise<void>>>(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      listAllSessions: mockListAllSessions,
      destroySession: mockDestroySession,
    }),
  }
})

// Import after mock is defined (vi.mock is hoisted, so this is fine)
import { SessionsListScreen } from './screen'

const NOW = new Date('2025-01-01T12:00:00Z')
const LATER = new Date(NOW.getTime() + 86_400_000 * 7) // 7 days from now

const SESSIONS: ReadonlyArray<SessionSummary> = [
  {
    uuid: 'uuid-1111-1111-1111-111111111111',
    slug: 'session-alpha',
    group: 'team-a',
    statusCode: 200,
    requestsCount: 5,
    lastRequestAt: NOW,
    createdAt: NOW,
    expiresAt: LATER,
    longLived: false,
  },
  {
    uuid: 'uuid-2222-2222-2222-222222222222',
    slug: 'session-beta',
    group: 'team-a',
    statusCode: 404,
    requestsCount: 0,
    lastRequestAt: null,
    createdAt: NOW,
    expiresAt: LATER,
    longLived: false,
  },
  {
    uuid: 'uuid-3333-3333-3333-333333333333',
    slug: 'session-gamma',
    group: null,
    statusCode: 200,
    requestsCount: 2,
    lastRequestAt: NOW,
    createdAt: NOW,
    expiresAt: LATER,
    longLived: true,
  },
]

const renderScreen = () =>
  render(
    <MantineProvider>
      <Notifications />
      <MemoryRouter>
        <SessionsListScreen />
      </MemoryRouter>
    </MantineProvider>
  )

describe('SessionsListScreen', () => {
  beforeEach(() => {
    mockListAllSessions.mockResolvedValue(SESSIONS)
    mockDestroySession.mockResolvedValue(() => Promise.resolve())
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('renders group headers and sessions grouped correctly', async () => {
    renderScreen()

    // Sessions should appear with group headers after loading
    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })

    // Group header for 'team-a' (appears multiple times: header + Group column cells + dropdown options)
    expect(screen.getAllByText('team-a').length).toBeGreaterThan(0)
    // Group header for sessions with no group
    expect(screen.getAllByText('Ungrouped').length).toBeGreaterThan(0)

    // All three sessions are present
    expect(screen.getByText('session-alpha')).toBeInTheDocument()
    expect(screen.getByText('session-beta')).toBeInTheDocument()
    expect(screen.getByText('session-gamma')).toBeInTheDocument()
  })

  test('Open link for each row navigates to /s/{uuid}', async () => {
    renderScreen()

    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })

    // There should be an Open link pointing to the uuid-based session route for each row
    const openLinks = screen.getAllByRole('link', { name: /^open$/i })
    const hrefs = openLinks.map((el) => el.getAttribute('href'))

    expect(hrefs).toContain('/s/uuid-1111-1111-1111-111111111111')
    expect(hrefs).toContain('/s/uuid-2222-2222-2222-222222222222')
    expect(hrefs).toContain('/s/uuid-3333-3333-3333-333333333333')
  })

  test('webhook URL column shows /w/{slug} for each session', async () => {
    renderScreen()

    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })

    // Each row shows a URL containing /w/{slug}
    expect(screen.getByText(/\/w\/session-alpha/)).toBeInTheDocument()
    expect(screen.getByText(/\/w\/session-beta/)).toBeInTheDocument()
    expect(screen.getByText(/\/w\/session-gamma/)).toBeInTheDocument()
  })

  test('text filter narrows displayed rows by slug', async () => {
    renderScreen()

    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })

    // Type 'alpha' in the search box — only session-alpha should remain visible
    const searchInput = screen.getByRole('textbox', { name: /filter/i })
    fireEvent.change(searchInput, { target: { value: 'alpha' } })

    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
      expect(screen.queryByText('session-beta')).not.toBeInTheDocument()
      expect(screen.queryByText('session-gamma')).not.toBeInTheDocument()
    })
  })

  test('shows empty state message when no sessions are returned', async () => {
    mockListAllSessions.mockResolvedValue([])
    renderScreen()

    await waitFor(() => {
      expect(screen.getByText(/no sessions/i)).toBeInTheDocument()
    })
  })

  test('Delete: confirms, calls destroySession with the row uuid, awaits the two-phase flow, removes the row', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(true)
    const slow = vi.fn<() => Promise<void>>(() => Promise.resolve())
    mockDestroySession.mockResolvedValue(slow)

    renderScreen()

    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })

    // The first Delete button corresponds to the first row (session-alpha → uuid-1111).
    const deleteButtons = screen.getAllByRole('button', { name: /delete/i })
    fireEvent.click(deleteButtons[0])

    // Phase 1: the local-removal call is made with the row's uuid.
    await waitFor(() => {
      expect(mockDestroySession).toHaveBeenCalledWith('uuid-1111-1111-1111-111111111111')
    })

    // Phase 2: the slow (server) function returned by destroySession is awaited.
    await waitFor(() => {
      expect(slow).toHaveBeenCalledTimes(1)
    })

    // The deleted row is removed from the rendered list.
    await waitFor(() => {
      expect(screen.queryByText('session-alpha')).not.toBeInTheDocument()
    })

    confirmSpy.mockRestore()
  })

  test('Delete: does NOT call destroySession when the confirm is cancelled', async () => {
    const confirmSpy = vi.spyOn(window, 'confirm').mockReturnValue(false)

    renderScreen()

    await waitFor(() => {
      expect(screen.getByText('session-alpha')).toBeInTheDocument()
    })

    const deleteButtons = screen.getAllByRole('button', { name: /delete/i })
    fireEvent.click(deleteButtons[0])

    expect(mockDestroySession).not.toHaveBeenCalled()
    expect(screen.getByText('session-alpha')).toBeInTheDocument()

    confirmSpy.mockRestore()
  })
})
