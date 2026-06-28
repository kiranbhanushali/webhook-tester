/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import type { SearchResultItem } from '~/api'

// Hoist mock so the vi.mock factory can reference it.
const { mockSearchIdentifiers } = vi.hoisted(() => ({
  mockSearchIdentifiers: vi.fn<
    (params: {
      value: string
      key?: string
      match?: string
      session?: string
      group?: string
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

// Import after mocks are defined (vi.mock is hoisted, import order does not matter at runtime).
import { DashboardSearch } from './dashboard-search'

const makeItem = (rID: string, value = 'abc'): SearchResultItem => ({
  sessionUUID: 'sess-uuid-1',
  sessionSlug: 'my-session',
  requestUUID: rID,
  key: 'trackingId',
  value,
  capturedAt: new Date('2024-01-01T12:00:00Z'),
})

const renderSearch = (overrides: Partial<React.ComponentProps<typeof DashboardSearch>> = {}) =>
  render(
    <MantineProvider>
      <DashboardSearch
        session={null}
        group={null}
        onResultClick={vi.fn()}
        selectedUUID={null}
        onActiveChange={vi.fn()}
        {...overrides}
      />
    </MantineProvider>
  )

describe('DashboardSearch', () => {
  beforeEach(() => {
    mockSearchIdentifiers.mockResolvedValue([])
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('renders search key (default trackingId) and value inputs plus Search button', () => {
    renderSearch()
    expect(screen.getByRole('textbox', { name: /search key/i })).toHaveValue('trackingId')
    expect(screen.getByRole('textbox', { name: /search value/i })).toHaveValue('')
    expect(screen.getByRole('button', { name: /search identifiers/i })).toBeDisabled()
  })

  test('Search button is enabled once value is non-empty', () => {
    renderSearch()
    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'abc-123' },
    })
    expect(screen.getByRole('button', { name: /search identifiers/i })).not.toBeDisabled()
  })

  // ── Exact mode ────────────────────────────────────────────────────────────────

  test('exact mode calls searchIdentifiers once with the single value and match=exact', async () => {
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'abc-123' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(mockSearchIdentifiers).toHaveBeenCalledTimes(1))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(
      expect.objectContaining({ value: 'abc-123', match: 'exact', key: 'trackingId' })
    )
  })

  test('exact mode passes the active session and group filters', async () => {
    renderSearch({ session: 'uuid-alpha', group: 'team-a' })

    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'val' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(mockSearchIdentifiers).toHaveBeenCalledTimes(1))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(
      expect.objectContaining({ session: 'uuid-alpha', group: 'team-a' })
    )
  })

  // ── Multi (OR) mode ───────────────────────────────────────────────────────────

  test('OR mode splits on whitespace and calls searchIdentifiers once per keyword', async () => {
    renderSearch()

    fireEvent.click(screen.getByText('OR'))
    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'kw-1 kw-2 kw-3' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(mockSearchIdentifiers).toHaveBeenCalledTimes(3))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(expect.objectContaining({ value: 'kw-1', match: 'exact' }))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(expect.objectContaining({ value: 'kw-2', match: 'exact' }))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(expect.objectContaining({ value: 'kw-3', match: 'exact' }))
  })

  test('OR mode splits on commas as well as whitespace', async () => {
    renderSearch()

    fireEvent.click(screen.getByText('OR'))
    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'kw-a,kw-b' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(mockSearchIdentifiers).toHaveBeenCalledTimes(2))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(expect.objectContaining({ value: 'kw-a' }))
    expect(mockSearchIdentifiers).toHaveBeenCalledWith(expect.objectContaining({ value: 'kw-b' }))
  })

  test('OR mode deduplicates results by requestUUID across keyword pages', async () => {
    const sharedItem = makeItem('req-dupe', 'both')
    // Both keyword pages return the same item (same requestUUID).
    mockSearchIdentifiers.mockResolvedValue([sharedItem])

    const onResultClick = vi.fn()
    renderSearch({ onResultClick })

    fireEvent.click(screen.getByText('OR'))
    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'kw-a kw-b' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(mockSearchIdentifiers).toHaveBeenCalledTimes(2))

    // Despite 2 pages each returning the same item, only 1 row is rendered.
    const rows = screen.getAllByRole('button', { name: /open result for/i })
    expect(rows).toHaveLength(1)
  })

  // ── Results list ──────────────────────────────────────────────────────────────

  test('shows "no results" message when search returns an empty array', async () => {
    mockSearchIdentifiers.mockResolvedValue([])
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'nope' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(screen.getByText(/no matching requests/i)).toBeInTheDocument())
  })

  test('clicking a result row calls onResultClick with session UUID and request UUID', async () => {
    mockSearchIdentifiers.mockResolvedValue([makeItem('req-1', 'found-val')])
    const onResultClick = vi.fn()
    renderSearch({ onResultClick })

    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'found-val' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    const row = await screen.findByRole('button', { name: /open result for trackingId=found-val/i })
    fireEvent.click(row)

    expect(onResultClick).toHaveBeenCalledOnce()
    expect(onResultClick).toHaveBeenCalledWith('sess-uuid-1', 'req-1')
  })

  // ── Clear ─────────────────────────────────────────────────────────────────────

  test('Clear button calls onActiveChange(false) and hides results', async () => {
    mockSearchIdentifiers.mockResolvedValue([makeItem('req-x')])
    const onActiveChange = vi.fn()
    renderSearch({ onActiveChange })

    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'abc' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    // Wait for results to appear
    await screen.findByRole('button', { name: /open result for/i })
    expect(onActiveChange).toHaveBeenLastCalledWith(true)

    fireEvent.click(screen.getByRole('button', { name: /clear search/i }))

    expect(onActiveChange).toHaveBeenLastCalledWith(false)
    expect(screen.queryByRole('button', { name: /open result for/i })).not.toBeInTheDocument()
  })

  // ── Error state ───────────────────────────────────────────────────────────────

  test('shows an error alert when searchIdentifiers rejects', async () => {
    mockSearchIdentifiers.mockRejectedValue(new Error('network failure'))
    renderSearch()

    fireEvent.change(screen.getByRole('textbox', { name: /search value/i }), {
      target: { value: 'abc' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search identifiers/i }))

    await waitFor(() => expect(screen.getByText(/network failure/i)).toBeInTheDocument())
    expect(screen.getByText(/search failed/i)).toBeInTheDocument()
  })
})
