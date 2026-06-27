/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi, beforeEach } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'
import type { Request as RequestData } from '~/shared'

// Hoist mock so it is available in vi.mock factory
const { mockRemoveRequest } = vi.hoisted(() => ({
  mockRemoveRequest: vi.fn<() => Promise<() => Promise<void>>>(),
}))

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({
      requests: [],
      removeRequest: mockRemoveRequest,
    }),
  }
})

import { Request } from './request'

type TinyRequest = Omit<RequestData, 'payload'>

const BASE_REQUEST: TinyRequest = {
  rID: 'req-1',
  clientAddress: '127.0.0.1',
  method: 'POST',
  capturedAt: new Date(),
  url: new URL('https://example.com/hook'),
  headers: [],
  authorized: undefined,
}

const renderRequest = (request: TinyRequest) =>
  render(
    <MantineProvider>
      <MemoryRouter>
        <Request sID="session-1" request={request} />
      </MemoryRouter>
    </MantineProvider>
  )

describe('Request sidebar row — unauthorized badge', () => {
  beforeEach(() => {
    mockRemoveRequest.mockResolvedValue(() => Promise.resolve())
  })

  test('does NOT show unauthorized badge when authorized is undefined (no auth configured)', () => {
    renderRequest(BASE_REQUEST)
    expect(screen.queryByRole('status', { name: /unauthorized/i })).toBeNull()
    expect(screen.queryByText(/unauthorized/i)).toBeNull()
  })

  test('does NOT show unauthorized badge when authorized is true', () => {
    renderRequest({ ...BASE_REQUEST, authorized: true })
    expect(screen.queryByText(/unauthorized/i)).toBeNull()
  })

  test('shows red unauthorized badge when authorized is false', () => {
    renderRequest({ ...BASE_REQUEST, authorized: false })
    expect(screen.getByText(/unauthorized/i)).toBeInTheDocument()
  })
})
