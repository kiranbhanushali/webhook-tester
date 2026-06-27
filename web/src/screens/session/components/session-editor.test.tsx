/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, screen, fireEvent, waitFor } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { Notifications } from '@mantine/notifications'
import { MemoryRouter } from 'react-router-dom'
import type { Session } from '~/shared'
import type { SessionPatch } from '~/api'
import { validateInboundAuth } from './session-validation'

describe('validateInboundAuth', () => {
  test.each([
    { header: '', value: '', expected: true },
    { header: '', value: 'anything', expected: true },
    { header: 'X-Token', value: 'secret', expected: true },
    { header: 'X-Token', value: '', expected: false },
    { header: 'X-Token', value: '   ', expected: false },
  ])('header=$header value=$value → $expected', ({ header, value, expected }) => {
    expect(validateInboundAuth(header, value)).toBe(expected)
  })
})

// Hoist mock functions so they are available in vi.mock factory
const { mockUpdateSession } = vi.hoisted(() => ({
  mockUpdateSession: vi.fn<(ref: string, patch: SessionPatch) => Promise<Session>>(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      updateSession: mockUpdateSession,
    }),
    useSettings: () => ({
      maxRequestBodySize: 0,
    }),
  }
})

// Import after mock is defined (vi.mock is hoisted, so this is fine)
import { SessionEditor } from './session-editor'

const BASE_SESSION: Session = {
  sID: 'uuid-test',
  responseCode: 200,
  responseHeaders: [{ name: 'Content-Type', value: 'application/json' }],
  responseDelay: 0,
  responseBody: new Uint8Array(),
  slug: 'existing-slug',
  group: 'team-a',
  responseScript: '',
  securityHeaders: [],
  forwardUrl: null,
  longLived: false,
  inboundAuthHeader: null,
  inboundAuthValue: null,
}

const renderEditor = (session: Session = BASE_SESSION) =>
  render(
    <MantineProvider>
      <Notifications />
      <MemoryRouter>
        <SessionEditor session={session} opened={true} onClose={() => {}} />
      </MemoryRouter>
    </MantineProvider>
  )

const SESSION_WITH_AUTH: Session = {
  ...BASE_SESSION,
  inboundAuthHeader: 'X-Webhook-Token',
  inboundAuthValue: 'existing-secret',
}

describe('SessionEditor', () => {
  beforeEach(() => {
    mockUpdateSession.mockResolvedValue(BASE_SESSION)
  })

  afterEach(() => {
    vi.clearAllMocks()
  })

  test('pre-fills slug and group from session', () => {
    renderEditor()

    const slugInput = screen.getByRole('textbox', { name: /slug/i }) as HTMLInputElement
    expect(slugInput.value).toBe('existing-slug')

    const groupInput = screen.getByRole('textbox', { name: /group/i }) as HTMLInputElement
    expect(groupInput.value).toBe('team-a')
  })

  test('pre-fills status code, delay and response headers from session', () => {
    renderEditor()

    // Response headers textarea should contain the existing header
    const headersArea = screen.getByRole('textbox', { name: /response headers/i }) as HTMLTextAreaElement
    expect(headersArea.value).toContain('Content-Type: application/json')
  })

  test('calls updateSession with the session sID and changed fields on save', async () => {
    renderEditor()

    // Change the slug
    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), {
      target: { value: 'new-slug' },
    })

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    await waitFor(() => {
      expect(mockUpdateSession).toHaveBeenCalledWith(
        'uuid-test',
        expect.objectContaining({ slug: 'new-slug' })
      )
    })
  })

  test('clearing the group then saving sends group: "" so the server clears it', async () => {
    renderEditor()

    // Blank out the pre-filled group
    fireEvent.change(screen.getByRole('textbox', { name: /group/i }), {
      target: { value: '' },
    })

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    await waitFor(() => {
      expect(mockUpdateSession).toHaveBeenCalledWith('uuid-test', expect.objectContaining({ group: '' }))
    })
  })

  test('clearing forward URL and response script sends empty strings (clear)', async () => {
    renderEditor({
      ...BASE_SESSION,
      forwardUrl: 'https://old.example.com/hook',
      responseScript: '@status 200',
    })

    // Forward URL is in the collapsed Advanced panel — use hidden:true
    fireEvent.change(screen.getByRole('textbox', { name: /forward/i, hidden: true }), { target: { value: '' } })
    // Response script is in the open Response panel
    fireEvent.change(screen.getByRole('textbox', { name: /response script/i }), { target: { value: '' } })

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    await waitFor(() => {
      expect(mockUpdateSession).toHaveBeenCalledWith(
        'uuid-test',
        expect.objectContaining({ forwardUrl: '', responseScript: '' })
      )
    })
  })

  test('blank slug is OMITTED from the patch (the current slug is kept, not wiped)', async () => {
    renderEditor()

    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), { target: { value: '' } })

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    await waitFor(() => {
      expect(mockUpdateSession).toHaveBeenCalled()
    })

    const patch = mockUpdateSession.mock.calls[0][1]
    expect(patch.slug).toBeUndefined()
  })

  test('shows a slug-taken error when updateSession rejects with a 409', async () => {
    const conflictError = Object.assign(new Error('slug already taken'), {
      response: { status: 409 },
    })
    mockUpdateSession.mockRejectedValue(conflictError)

    renderEditor()

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    await waitFor(() => {
      // A friendly slug error message should appear somewhere in the document
      expect(screen.getByText(/slug.*taken|already.*taken|409|conflict/i)).toBeInTheDocument()
    })
  })

  test('invalid slug in editor disables Save button', () => {
    renderEditor()

    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), {
      target: { value: 'INVALID_SLUG!' },
    })

    expect(screen.getByRole('button', { name: /save/i })).toBeDisabled()
  })

  test('valid slug in editor does not disable Save button', () => {
    renderEditor()

    fireEvent.change(screen.getByRole('textbox', { name: /slug/i }), {
      target: { value: 'good-slug' },
    })

    expect(screen.getByRole('button', { name: /save/i })).not.toBeDisabled()
  })

  test('pre-fills inbound-auth header and value from session', () => {
    renderEditor(SESSION_WITH_AUTH)
    // Security panel is collapsed — use hidden:true to access its fields
    const authHeaderInput = screen.getByRole('textbox', { name: /auth header/i, hidden: true }) as HTMLInputElement
    expect(authHeaderInput.value).toBe('X-Webhook-Token')
    const authValueInput = screen.getByLabelText(/auth secret/i) as HTMLInputElement
    expect(authValueInput.value).toBe('existing-secret')
  })

  test('header set without value disables Save button', () => {
    renderEditor()
    // Security panel is collapsed — use hidden:true
    fireEvent.change(screen.getByRole('textbox', { name: /auth header/i, hidden: true }), {
      target: { value: 'X-Token' },
    })
    expect(screen.getByRole('button', { name: /save/i })).toBeDisabled()
  })

  test('calls updateSession with inboundAuthHeader + inboundAuthValue on save', async () => {
    renderEditor()
    // Security panel fields — use hidden:true
    fireEvent.change(screen.getByRole('textbox', { name: /auth header/i, hidden: true }), {
      target: { value: 'X-My-Token' },
    })
    fireEvent.change(screen.getByLabelText(/auth secret/i), {
      target: { value: 'my-value' },
    })
    fireEvent.click(screen.getByRole('button', { name: /save/i }))
    await waitFor(() => {
      expect(mockUpdateSession).toHaveBeenCalledWith(
        'uuid-test',
        expect.objectContaining({ inboundAuthHeader: 'X-My-Token', inboundAuthValue: 'my-value' })
      )
    })
  })

  test('server 400 is surfaced as a field-level error on the inbound-auth header input', async () => {
    const badAuthError = Object.assign(new Error('bad inbound-auth config'), {
      response: { status: 400 },
    })
    mockUpdateSession.mockRejectedValue(badAuthError)

    renderEditor(SESSION_WITH_AUTH)

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    expect(await screen.findByText(/rejected the inbound-auth/i)).toBeInTheDocument()
  })

  test('clearing the auth header sends inboundAuthValue:"" too (no value-without-header)', async () => {
    renderEditor(SESSION_WITH_AUTH)

    // Clear the header name; the secret value is left as the pre-filled "existing-secret"
    fireEvent.change(screen.getByRole('textbox', { name: /auth header/i, hidden: true }), {
      target: { value: '' },
    })

    fireEvent.click(screen.getByRole('button', { name: /save/i }))

    await waitFor(() => {
      expect(mockUpdateSession).toHaveBeenCalled()
    })
    const patch = mockUpdateSession.mock.calls[0][1]
    expect(patch.inboundAuthHeader).toBe('')
    expect(patch.inboundAuthValue).toBe('')
  })
})
