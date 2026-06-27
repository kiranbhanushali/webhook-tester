/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi } from 'vitest'
import { render, screen } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { MemoryRouter } from 'react-router-dom'

vi.mock('~/shared', async (importOriginal) => {
  const mod = await importOriginal<typeof import('~/shared')>()
  return {
    ...mod,
    useData: () => ({ webHookUrl: null, allSessionIDs: [] }),
    useSettings: () => ({ tunnelEnabled: false, tunnelUrl: null }),
  }
})

import { Header } from './header'

const renderHeader = () =>
  render(
    <MantineProvider>
      <MemoryRouter>
        <Header currentVersion={null} latestVersion={null} isBurgerOpened={false} onBurgerClick={() => {}} />
      </MemoryRouter>
    </MantineProvider>
  )

describe('Header', () => {
  test('does NOT render a GitHub button or link', () => {
    renderHeader()
    expect(screen.queryByRole('link', { name: /github/i })).toBeNull()
    expect(screen.queryByText(/github/i)).toBeNull()
  })

  test('renders the Help button', () => {
    renderHeader()
    expect(screen.getByRole('button', { name: /help/i })).toBeInTheDocument()
  })

  test('renders the Sessions link', () => {
    renderHeader()
    expect(screen.getByRole('link', { name: /sessions/i })).toBeInTheDocument()
  })
})
