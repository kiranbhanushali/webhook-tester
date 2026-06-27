/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import type { SearchResultItem } from '~/api'

const SESSION_UUID = 'uuid-sess-aaaa-bbbb-cccccccccccc'

// Hoist mock so it's available in vi.mock factory
const { mockSearchIdentifiers } = vi.hoisted(() => ({
  mockSearchIdentifiers: vi.fn<
    (params: {
      value: string
      key?: string
      match?: 'exact' | 'prefix'
      session?: string
    }) => Promise<ReadonlyArray<SearchResultItem>>
  >(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      searchIdentifiers: mockSearchIdentifiers,
    }),
  }
})

import { SessionSearch } from './session-search'

const RESULTS: ReadonlyArray<SearchResultItem> = [
  {
    sessionSlug: 'my-session',
    sessionUUID: SESSION_UUID,
    requestUUID: 'uuid-req-1111-1111-111111111111',
    key: 'trackingId',
    value: 'order-99',
    capturedAt: Object.freeze(new Date('2025-06-01T10:00:00Z')),
  },
]

const renderSearch = (children: React.ReactNode = <span data-testid="normal-list">normal list</span>) =>
  render(
    <MantineProvider>
      <MemoryRouter>
        <SessionSearch sessionUUID={SESSION_UUID}>{children}</SessionSearch>
      </MemoryRouter>
    </MantineProvider>
  )

describe('SessionSearch', () => {
  beforeEach(() => {
    mockSearchIdentifiers.mockResolvedValue([])
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('shows the normal list (children) when idle', () => {
    renderSearch()
    expect(screen.getByTestId('normal-list')).toBeInTheDocument()
  })

  test('submit button is disabled when value is empty', () => {
    renderSearch()
    expect(screen.getByRole('button', { name: /search/i })).toBeDisabled()
  })

  test('submitting calls searchIdentifiers scoped to the given session uuid', async () => {
    mockSearchIdentifiers.mockResolvedValue(RESULTS)
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'order-99' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(mockSearchIdentifiers).toHaveBeenCalledOnce()
    })
    expect(mockSearchIdentifiers).toHaveBeenCalledWith({
      key: 'trackingId',
      value: 'order-99',
      match: 'exact',
      session: SESSION_UUID,
    })
  })

  test('results render with link to the correct session/request path', async () => {
    mockSearchIdentifiers.mockResolvedValue(RESULTS)
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'order-99' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.getByText(/order-99/)).toBeInTheDocument()
    })

    const link = screen.getByRole('link')
    expect(link.getAttribute('href')).toBe(`/s/${SESSION_UUID}/uuid-req-1111-1111-111111111111`)
  })

  test('clearing search returns to the normal list', async () => {
    mockSearchIdentifiers.mockResolvedValue(RESULTS)
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'order-99' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.queryByTestId('normal-list')).not.toBeInTheDocument()
    })

    fireEvent.click(screen.getByRole('button', { name: /clear/i }))

    expect(screen.getByTestId('normal-list')).toBeInTheDocument()
  })

  test('shows "no matches in this session" when search returns empty', async () => {
    mockSearchIdentifiers.mockResolvedValue([])
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'missing' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.getByText(/no matches in this session/i)).toBeInTheDocument()
    })
  })

  test('shows loading indicator while search is in flight', async () => {
    mockSearchIdentifiers.mockReturnValue(new Promise(() => {}))
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'pending' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.getByText(/searching/i)).toBeInTheDocument()
    })
  })

  test('shows error message when searchIdentifiers rejects', async () => {
    mockSearchIdentifiers.mockRejectedValue(new Error('network failure'))
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'anything' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.getByText(/network failure/i)).toBeInTheDocument()
    })
  })
})
