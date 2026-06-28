/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { MemoryRouter, Routes, Route } from 'react-router-dom'
import type { Session } from '~/shared'

const BASE_SESSION: Session = {
  sID: 'test-uuid',
  responseCode: 200,
  responseHeaders: [{ name: 'Content-Type', value: 'application/json' }],
  responseDelay: 3,
  responseBody: new TextEncoder().encode('{"ok":true}'),
  slug: 'my-endpoint',
  group: 'team-a',
  responseScript: '@status 201',
  securityHeaders: [{ name: 'X-Frame-Options', value: 'DENY' }],
  forwardUrl: 'https://example.com/hook',
  longLived: true,
  inboundAuthHeader: 'X-Token',
  inboundAuthValue: 'super-secret',
}

// Hoist mock so the vi.mock factory can reference it.
const { mockSwitchToSession } = vi.hoisted(() => ({
  mockSwitchToSession: vi.fn<(sID: string) => Promise<() => Promise<void>>>(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      session: BASE_SESSION,
      switchToSession: mockSwitchToSession,
    }),
    useSettings: () => ({
      publicUrlRoot: null,
      maxRequestBodySize: 0,
    }),
  }
})

// Stub heavy session sub-components so this test stays at the screen boundary.
// SessionEditor is stubbed to expose the slug it received as a data attribute,
// letting us verify prefill without re-running the full editor tests.
vi.mock('~/screens/session/components', () => ({
  SessionDetails: () => <div data-testid="session-details" />,
  SessionEditor: ({
    opened,
    session,
  }: {
    opened: boolean
    session: Session
  }) =>
    opened ? (
      <div
        data-testid="session-editor"
        data-slug={session?.slug ?? ''}
        data-group={session?.group ?? ''}
        data-status-code={String(session?.responseCode ?? '')}
        data-long-lived={String(session?.longLived ?? false)}
        data-forward-url={session?.forwardUrl ?? ''}
        data-inbound-auth-header={session?.inboundAuthHeader ?? ''}
      />
    ) : null,
  ScriptHelpButton: () => null,
}))

// Import after mocks (vi.mock is hoisted).
import { SessionAndRequestScreen } from './screen'

const renderScreen = (sID = 'test-uuid') =>
  render(
    <MantineProvider>
      <Notifications />
      <MemoryRouter initialEntries={[`/s/${sID}`]}>
        <Routes>
          <Route path="/s/:sID" element={<SessionAndRequestScreen />} />
        </Routes>
      </MemoryRouter>
    </MantineProvider>
  )

describe('SessionAndRequestScreen – Edit configuration', () => {
  beforeEach(() => {
    mockSwitchToSession.mockResolvedValue(() => Promise.resolve())
  })

  test('"Edit configuration" button is visible when the session is loaded', () => {
    renderScreen()
    expect(screen.getByRole('button', { name: /edit configuration/i })).toBeInTheDocument()
  })

  test('clicking "Edit configuration" opens the SessionEditor', () => {
    renderScreen()

    // Editor is initially closed.
    expect(screen.queryByTestId('session-editor')).not.toBeInTheDocument()

    fireEvent.click(screen.getByRole('button', { name: /edit configuration/i }))

    expect(screen.getByTestId('session-editor')).toBeInTheDocument()
  })

  test('editor receives all current session values (prefill check)', () => {
    renderScreen()
    fireEvent.click(screen.getByRole('button', { name: /edit configuration/i }))

    const editor = screen.getByTestId('session-editor')

    // Verify the session object (and all its fields) is passed through to the editor stub.
    expect(editor).toHaveAttribute('data-slug', 'my-endpoint')
    expect(editor).toHaveAttribute('data-group', 'team-a')
    expect(editor).toHaveAttribute('data-status-code', '200')
    expect(editor).toHaveAttribute('data-long-lived', 'true')
    expect(editor).toHaveAttribute('data-forward-url', 'https://example.com/hook')
    expect(editor).toHaveAttribute('data-inbound-auth-header', 'X-Token')
  })

  test('"View captured events" link is also rendered next to the Edit button', () => {
    renderScreen()
    // The link text comes from the session screen
    expect(screen.getByRole('link', { name: /view captured events/i })).toBeInTheDocument()
  })
})
