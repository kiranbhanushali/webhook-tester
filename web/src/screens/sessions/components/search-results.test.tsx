/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import type { SearchResultItem } from '~/api'

// Hoist mock functions so they are available in vi.mock factory
const { mockSearchIdentifiers } = vi.hoisted(() => ({
  mockSearchIdentifiers: vi.fn<
    (params: {
      value: string
      key?: string
      match?: 'exact' | 'prefix'
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

import { IdentifierSearch } from './search-results'

const RESULTS: ReadonlyArray<SearchResultItem> = [
  {
    sessionSlug: 'session-alpha',
    sessionUUID: 'uuid-sess-1111-1111-111111111111',
    requestUUID: 'uuid-req-aaaa-aaaa-aaaaaaaaaaaa',
    key: 'trackingId',
    value: 'abc-123',
    capturedAt: Object.freeze(new Date('2025-01-01T10:00:00Z')),
  },
  {
    sessionSlug: 'session-beta',
    sessionUUID: 'uuid-sess-2222-2222-222222222222',
    requestUUID: 'uuid-req-bbbb-bbbb-bbbbbbbbbbbb',
    key: 'trackingId',
    value: 'abc-123',
    capturedAt: Object.freeze(new Date('2025-01-02T11:30:00Z')),
  },
]

const renderSearch = () =>
  render(
    <MantineProvider>
      <MemoryRouter>
        <IdentifierSearch />
      </MemoryRouter>
    </MantineProvider>
  )

describe('IdentifierSearch', () => {
  beforeEach(() => {
    mockSearchIdentifiers.mockResolvedValue([])
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('does not call searchIdentifiers on initial render', () => {
    renderSearch()
    expect(mockSearchIdentifiers).not.toHaveBeenCalled()
  })

  test('submitting key+value calls searchIdentifiers with correct params including match=exact', async () => {
    mockSearchIdentifiers.mockResolvedValue(RESULTS)
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'abc-123' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(mockSearchIdentifiers).toHaveBeenCalledOnce()
    })
    expect(mockSearchIdentifiers).toHaveBeenCalledWith({
      key: 'trackingId',
      value: 'abc-123',
      match: 'exact',
    })
  })

  test('results table renders with matched key, value, session slug, and link to request', async () => {
    mockSearchIdentifiers.mockResolvedValue(RESULTS)
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'abc-123' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.getAllByText('abc-123').length).toBeGreaterThan(0)
    })

    // Both session slugs are shown
    expect(screen.getByText('session-alpha')).toBeInTheDocument()
    expect(screen.getByText('session-beta')).toBeInTheDocument()

    // Both "Open" links point to the correct session/request paths
    const links = screen.getAllByRole('link', { name: /open/i })
    const hrefs = links.map((el) => el.getAttribute('href'))

    expect(hrefs).toContain(
      '/s/uuid-sess-1111-1111-111111111111/uuid-req-aaaa-aaaa-aaaaaaaaaaaa'
    )
    expect(hrefs).toContain(
      '/s/uuid-sess-2222-2222-222222222222/uuid-req-bbbb-bbbb-bbbbbbbbbbbb'
    )
  })

  test('shows "no results" state after search returns empty', async () => {
    mockSearchIdentifiers.mockResolvedValue([])
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'missing' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(screen.getByText(/no results/i)).toBeInTheDocument()
    })
  })

  test('shows error state when searchIdentifiers rejects', async () => {
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

  test('prefix match: submits with match=prefix when prefix mode is selected', async () => {
    mockSearchIdentifiers.mockResolvedValue([])
    renderSearch()

    // Switch to prefix mode
    fireEvent.click(screen.getByText(/prefix/i))

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'order-' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    await waitFor(() => {
      expect(mockSearchIdentifiers).toHaveBeenCalledWith({
        key: 'trackingId',
        value: 'order-',
        match: 'prefix',
      })
    })
  })
})
