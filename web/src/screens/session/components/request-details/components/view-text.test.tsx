/// <reference types="@testing-library/jest-dom/vitest" />
import { describe, test, expect } from 'vitest'
import { render, screen, fireEvent } from '@testing-library/react'
import { MantineProvider } from '@mantine/core'
import { ViewText } from './view-text'

const enc = (s: string): Uint8Array => new TextEncoder().encode(s)

const renderViewText = (input: Uint8Array | null, contentType: string | null = null) =>
  render(
    <MantineProvider>
      <ViewText input={input} contentType={contentType} />
    </MantineProvider>
  )

describe('ViewText — JSON query box', () => {
  test('query input is shown when body is valid JSON', () => {
    renderViewText(enc('{"a":1}'), 'application/json')
    expect(screen.getByPlaceholderText(/trackingId/i)).toBeInTheDocument()
  })

  test('query input is NOT shown when body is plain text (non-JSON)', () => {
    renderViewText(enc('hello world'), 'text/plain')
    expect(screen.queryByPlaceholderText(/trackingId/i)).toBeNull()
  })

  test('query input is NOT shown when input is null', () => {
    renderViewText(null, null)
    expect(screen.queryByPlaceholderText(/trackingId/i)).toBeNull()
  })

  test('empty query shows full payload (no result label)', () => {
    renderViewText(enc('{"key":"val"}'), 'application/json')
    // query input is empty by default — "Query result" label should not be visible
    expect(screen.queryByText(/query result/i)).toBeNull()
  })

  test('typing a valid path shows the extracted value', () => {
    renderViewText(enc('{"data":{"txn":{"trackingId":"abc123"}}}'), 'application/json')
    const input = screen.getByPlaceholderText(/trackingId/i)
    fireEvent.change(input, { target: { value: 'data.txn.trackingId' } })
    expect(screen.getByText(/"abc123"/)).toBeInTheDocument()
  })

  test('typing a bad path shows an inline error', () => {
    renderViewText(enc('{"a":1}'), 'application/json')
    const input = screen.getByPlaceholderText(/trackingId/i)
    fireEvent.change(input, { target: { value: 'a.missing.path' } })
    expect(screen.getByText(/no match/i)).toBeInTheDocument()
  })

  test('clearing the query hides the result', () => {
    renderViewText(enc('{"x":42}'), 'application/json')
    const input = screen.getByPlaceholderText(/trackingId/i)
    fireEvent.change(input, { target: { value: 'x' } })
    // result visible — "42" should appear
    expect(screen.getByText(/42/)).toBeInTheDocument()
    // clear
    fireEvent.change(input, { target: { value: '' } })
    // "Query result" label gone
    expect(screen.queryByText(/query result/i)).toBeNull()
  })
})
