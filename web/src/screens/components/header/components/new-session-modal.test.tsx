/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { MemoryRouter } from 'react-router-dom'
import type { Session } from '~/shared'

// Hoist mock functions so they are available in vi.mock factory
const { mockNewSession, mockDestroySession } = vi.hoisted(() => ({
  mockNewSession: vi.fn<
    (opts: {
      statusCode?: number
      headers?: Record<string, string>
      delay?: number
      responseBody?: Uint8Array
      slug?: string
      group?: string
      responseScript?: string
      securityHeaders?: Array<{ name: string; value: string }>
      forwardUrl?: string
      longLived?: boolean
    }) => Promise<Session>
  >(),
  mockDestroySession: vi.fn<() => Promise<() => Promise<void>>>(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      session: null,
      newSession: mockNewSession,
      destroySession: mockDestroySession,
    }),
    useSettings: () => ({
      maxRequestBodySize: 0,
    }),
  }
})

// Import after mock is defined (vi.mock is hoisted, so this is fine)
import { NewSessionModal } from './new-session-modal'

const MOCK_SESSION: Session = {
  sID: 'test-session-uuid',
  responseCode: 200,
  responseHeaders: [],
  responseDelay: 0,
  responseBody: new Uint8Array(),
  slug: 'test-slug',
  group: null,
  responseScript: null,
  securityHeaders: [],
  forwardUrl: null,
  longLived: false,
}

const renderModal = () =>
  render(
    <MantineProvider>
      <Notifications />
      <MemoryRouter>
        <NewSessionModal opened={true} onClose={() => {}} />
      </MemoryRouter>
    </MantineProvider>
  )

describe('NewSessionModal — new fields', () => {
  beforeEach(() => {
    mockNewSession.mockResolvedValue(MOCK_SESSION)
    mockDestroySession.mockResolvedValue(() => Promise.resolve())
    sessionStorage.clear()
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('renders slug, group, forward-url, long-lived, security headers and response script fields', () => {
    renderModal()

    expect(screen.getByRole('textbox', { name: /slug/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: /group/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: /forward/i })).toBeInTheDocument()
    expect(screen.getByRole('switch', { name: /long.?lived/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: /security headers/i })).toBeInTheDocument()
    expect(screen.getByRole('textbox', { name: /response script/i })).toBeInTheDocument()
  })

  test('submits slug, group, longLived, securityHeaders and responseScript to newSession', async () => {
    renderModal()

    // Fill in slug
    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), {
      target: { value: 'my-slug' },
    })

    // Fill in group
    fireEvent.change(screen.getByRole('textbox', { name: /group/i }), {
      target: { value: 'team-a' },
    })

    // Fill in security headers
    fireEvent.change(screen.getByRole('textbox', { name: /security headers/i }), {
      target: { value: 'X-Frame-Options: DENY' },
    })

    // Fill in response script
    fireEvent.change(screen.getByRole('textbox', { name: /response script/i }), {
      target: { value: '{{ .Body }}' },
    })

    // Toggle long-lived switch (Mantine v8 Switch renders role="switch")
    fireEvent.click(screen.getByRole('switch', { name: /long.?lived/i }))

    // Click Create
    fireEvent.click(screen.getByRole('button', { name: /create/i }))

    await waitFor(() => {
      expect(mockNewSession).toHaveBeenCalledWith(
        expect.objectContaining({
          slug: 'my-slug',
          group: 'team-a',
          securityHeaders: [{ name: 'X-Frame-Options', value: 'DENY' }],
          responseScript: '{{ .Body }}',
          longLived: true,
        })
      )
    })
  })

  test('rejects invalid slug "BadSlug!" — Create button disabled', () => {
    renderModal()

    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), {
      target: { value: 'BadSlug!' },
    })

    expect(screen.getByRole('button', { name: /create/i })).toBeDisabled()
  })

  test('accepts valid slug "valid-slug" — Create button enabled', () => {
    renderModal()

    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), {
      target: { value: 'valid-slug' },
    })

    expect(screen.getByRole('button', { name: /create/i })).not.toBeDisabled()
  })

  test('blank slug is valid (server auto-generates) — Create button enabled', () => {
    renderModal()

    // Default slug is empty — don't touch it
    expect((screen.getByRole('textbox', { name: /slug/i }) as HTMLInputElement).value).toBe('')
    expect(screen.getByRole('button', { name: /create/i })).not.toBeDisabled()
  })

  test('invalid forward URL disables Create button', () => {
    renderModal()

    fireEvent.change(screen.getByRole('textbox', { name: /forward/i }), {
      target: { value: 'not-a-url' },
    })

    expect(screen.getByRole('button', { name: /create/i })).toBeDisabled()
  })

  test('valid forward URL does not disable Create button', () => {
    renderModal()

    fireEvent.change(screen.getByRole('textbox', { name: /forward/i }), {
      target: { value: 'https://example.com/hook' },
    })

    expect(screen.getByRole('button', { name: /create/i })).not.toBeDisabled()
  })

  test('blank forward URL is valid', () => {
    renderModal()

    // Leave forward URL blank
    expect(screen.getByRole('button', { name: /create/i })).not.toBeDisabled()
  })
})
