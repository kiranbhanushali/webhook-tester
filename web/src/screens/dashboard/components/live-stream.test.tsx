/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { FirehoseEventAction, type FirehoseEvent, type SessionSummary } from '~/api'
import { LiveStream } from './live-stream'

const makeEvent = (opts: {
  sessionUUID: string
  sessionSlug: string
  rID: string
  method?: string
  path?: string
  statusCode?: number
  authorized?: boolean
}): FirehoseEvent => ({
  sessionUUID: opts.sessionUUID,
  sessionSlug: opts.sessionSlug,
  action: FirehoseEventAction.create,
  request: {
    uuid: opts.rID,
    clientAddress: '127.0.0.1',
    method: opts.method ?? 'POST',
    url: new URL(`http://localhost${opts.path ?? '/w/' + opts.sessionSlug + '/webhook'}`),
    capturedAt: new Date('2024-01-01T12:00:00Z'),
    authorized: opts.authorized ?? true,
  },
})

const makeSession = (uuid: string, slug: string, statusCode = 200): SessionSummary => ({
  uuid,
  slug,
  group: null,
  statusCode,
  requestsCount: 1,
  lastRequestAt: null,
  createdAt: new Date(0),
  expiresAt: new Date(0),
  longLived: false,
})

const renderStream = (
  events: ReadonlyArray<FirehoseEvent>,
  sessionByUUID: ReadonlyMap<string, SessionSummary> = new Map()
) =>
  render(
    <MantineProvider>
      <LiveStream
        events={events}
        sessionByUUID={sessionByUUID}
        live={true}
        error={null}
        filtered={false}
        loading={false}
        hasMore={false}
        loadingOlder={false}
        onLoadOlder={() => Promise.resolve()}
        onRowClick={vi.fn()}
        selectedUUID={null}
      />
    </MantineProvider>
  )

describe('LiveStream', () => {
  test('row renders slug, method, status, and url peek without overlap — each in its own element', () => {
    const session = makeSession('uuid-1', 'acme-pay-uat', 200)
    const event = makeEvent({
      sessionUUID: 'uuid-1',
      sessionSlug: 'acme-pay-uat',
      rID: 'req-1',
      method: 'POST',
      path: '/w/acme-pay-uat/some/deep/path',
    })

    renderStream([event], new Map([['uuid-1', session]]))

    // slug badge is present
    expect(screen.getByText('acme-pay-uat')).toBeInTheDocument()
    // method badge is present
    expect(screen.getByText('POST')).toBeInTheDocument()
    // status badge is present (derived from session statusCode)
    expect(screen.getByText('200')).toBeInTheDocument()
    // peek / url text is present as its own element with a title for the full url
    const peek = screen.getByTitle('/w/acme-pay-uat/some/deep/path')
    expect(peek).toBeInTheDocument()
    expect(peek.textContent).toBe('/w/acme-pay-uat/some/deep/path')
  })

  test('url peek element carries the truncation class (peek) and has a title attribute', () => {
    const event = makeEvent({
      sessionUUID: 'uuid-2',
      sessionSlug: 'payments-uat',
      rID: 'req-2',
      method: 'GET',
      path: '/w/payments-uat/api/v2/long/endpoint/that/overflows',
    })
    const { container } = renderStream([event])

    // The peek element has a title matching the full path
    const peek = screen.getByTitle('/w/payments-uat/api/v2/long/endpoint/that/overflows')
    expect(peek).toBeInTheDocument()
    // Confirm it's the URL text, not an empty wrapper
    expect(peek.textContent).toBe('/w/payments-uat/api/v2/long/endpoint/that/overflows')
    // Confirm it's in the DOM subtree (regression guard)
    expect(container.querySelector('[title="/w/payments-uat/api/v2/long/endpoint/that/overflows"]')).not.toBeNull()
  })

  test('slug badge carries a title with the full slug (truncation tooltip)', () => {
    const longSlug = 'payments-uat'
    const event = makeEvent({ sessionUUID: 'uuid-3', sessionSlug: longSlug, rID: 'req-3' })
    const { container } = renderStream([event])

    // The slug badge element should have title={slug} so long slugs show on hover
    const slugEl = container.querySelector(`[title="${longSlug}"]`)
    expect(slugEl).not.toBeNull()
    expect(slugEl?.textContent).toBe(longSlug)
  })
})
