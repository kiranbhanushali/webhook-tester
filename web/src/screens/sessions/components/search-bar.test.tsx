/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect, vi } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { SearchBar } from './search-bar'

const renderBar = (onSearch = vi.fn()) =>
  render(
    <MantineProvider>
      <SearchBar onSearch={onSearch} />
    </MantineProvider>
  )

describe('SearchBar', () => {
  test('renders key input defaulting to trackingId', () => {
    renderBar()
    const keyInput = screen.getByRole('textbox', { name: /identifier key/i })
    expect(keyInput).toHaveValue('trackingId')
  })

  test('renders value input that starts empty', () => {
    renderBar()
    const valueInput = screen.getByRole('textbox', { name: /identifier value/i })
    expect(valueInput).toHaveValue('')
  })

  test('submit button is disabled when value is empty', () => {
    renderBar()
    const submitButton = screen.getByRole('button', { name: /search/i })
    expect(submitButton).toBeDisabled()
  })

  test('submit button is enabled when value is non-empty', () => {
    renderBar()
    const valueInput = screen.getByRole('textbox', { name: /identifier value/i })
    fireEvent.change(valueInput, { target: { value: 'abc-123' } })
    const submitButton = screen.getByRole('button', { name: /search/i })
    expect(submitButton).not.toBeDisabled()
  })

  test('clicking search calls onSearch with key, value, and match=exact (default)', () => {
    const onSearch = vi.fn()
    renderBar(onSearch)

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'abc-123' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    expect(onSearch).toHaveBeenCalledOnce()
    expect(onSearch).toHaveBeenCalledWith({ key: 'trackingId', value: 'abc-123', match: 'exact' })
  })

  test('pressing Enter on the value input triggers search', () => {
    const onSearch = vi.fn()
    renderBar(onSearch)

    const valueInput = screen.getByRole('textbox', { name: /identifier value/i })
    fireEvent.change(valueInput, { target: { value: 'order-99' } })
    fireEvent.keyDown(valueInput, { key: 'Enter', code: 'Enter' })

    expect(onSearch).toHaveBeenCalledOnce()
    expect(onSearch).toHaveBeenCalledWith({ key: 'trackingId', value: 'order-99', match: 'exact' })
  })

  test('changing key input is reflected in the search call', () => {
    const onSearch = vi.fn()
    renderBar(onSearch)

    fireEvent.change(screen.getByRole('textbox', { name: /identifier key/i }), {
      target: { value: 'referenceId' },
    })
    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'xyz' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    expect(onSearch).toHaveBeenCalledWith({ key: 'referenceId', value: 'xyz', match: 'exact' })
  })

  test('toggling to prefix updates match param in onSearch call', () => {
    const onSearch = vi.fn()
    renderBar(onSearch)

    // Switch match mode to prefix
    const prefixControl = screen.getByText(/prefix/i)
    fireEvent.click(prefixControl)

    fireEvent.change(screen.getByRole('textbox', { name: /identifier value/i }), {
      target: { value: 'prefix-' },
    })
    fireEvent.click(screen.getByRole('button', { name: /search/i }))

    expect(onSearch).toHaveBeenCalledWith({ key: 'trackingId', value: 'prefix-', match: 'prefix' })
  })
})
