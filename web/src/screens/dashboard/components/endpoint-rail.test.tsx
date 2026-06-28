/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import type { SessionSummary } from '~/api'
import { EndpointRail } from './endpoint-rail'

const makeSession = (uuid: string, slug: string, group: string | null = null): SessionSummary => ({
  uuid,
  slug,
  group,
  statusCode: 200,
  requestsCount: 0,
  lastRequestAt: null,
  createdAt: new Date(0),
  expiresAt: new Date(0),
  longLived: false,
})

const SESSIONS: ReadonlyArray<SessionSummary> = [
  makeSession('uuid-1', 'acme-pay-uat', 'acme'),
  makeSession('uuid-2', 'orders-uat', 'team-a'),
  makeSession('uuid-3', 'payments-uat', 'team-b'),
  makeSession('uuid-4', 'reports', null),
]

const renderRail = (overrides: Partial<React.ComponentProps<typeof EndpointRail>> = {}) =>
  render(
    <MantineProvider>
      <EndpointRail
        sessions={SESSIONS}
        loading={false}
        selected={null}
        onSelect={vi.fn()}
        groups={['acme', 'team-a', 'team-b']}
        groupFilter={null}
        onGroupFilter={vi.fn()}
        activeUUIDs={new Set()}
        onNewSession={vi.fn()}
        collapsed={false}
        onToggleCollapse={vi.fn()}
        {...overrides}
      />
    </MantineProvider>
  )

describe('EndpointRail search', () => {
  test('renders a search input', () => {
    renderRail()
    expect(screen.getByRole('textbox', { name: /search endpoints/i })).toBeInTheDocument()
  })

  test('shows all sessions when search is empty', () => {
    renderRail()
    expect(screen.getByText('acme-pay-uat')).toBeInTheDocument()
    expect(screen.getByText('orders-uat')).toBeInTheDocument()
    expect(screen.getByText('payments-uat')).toBeInTheDocument()
    expect(screen.getByText('reports')).toBeInTheDocument()
  })

  test('filters by slug substring (case-insensitive)', () => {
    renderRail()
    const searchInput = screen.getByRole('textbox', { name: /search endpoints/i })

    fireEvent.change(searchInput, { target: { value: 'pay' } })

    // Both pay-containing slugs appear
    expect(screen.getByText('acme-pay-uat')).toBeInTheDocument()
    expect(screen.getByText('payments-uat')).toBeInTheDocument()
    // Non-pay slugs are hidden
    expect(screen.queryByText('orders-uat')).not.toBeInTheDocument()
    expect(screen.queryByText('reports')).not.toBeInTheDocument()
  })

  test('filters by group substring', () => {
    renderRail()
    const searchInput = screen.getByRole('textbox', { name: /search endpoints/i })

    fireEvent.change(searchInput, { target: { value: 'team-b' } })

    // bob group session shown
    expect(screen.getByText('payments-uat')).toBeInTheDocument()
    // Others hidden
    expect(screen.queryByText('acme-pay-uat')).not.toBeInTheDocument()
    expect(screen.queryByText('orders-uat')).not.toBeInTheDocument()
    expect(screen.queryByText('reports')).not.toBeInTheDocument()
  })

  test('shows no-match message when filter yields nothing', () => {
    renderRail()
    const searchInput = screen.getByRole('textbox', { name: /search endpoints/i })

    fireEvent.change(searchInput, { target: { value: 'zzz-no-match' } })

    expect(screen.getByText(/no endpoints match your search/i)).toBeInTheDocument()
  })

  test('clearing the search restores all sessions', () => {
    renderRail()
    const searchInput = screen.getByRole('textbox', { name: /search endpoints/i })

    fireEvent.change(searchInput, { target: { value: 'team-b' } })
    expect(screen.queryByText('reports')).not.toBeInTheDocument()

    fireEvent.change(searchInput, { target: { value: '' } })
    expect(screen.getByText('reports')).toBeInTheDocument()
    expect(screen.getByText('acme-pay-uat')).toBeInTheDocument()
  })
})
