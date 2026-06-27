import { describe, expect, test } from 'vitest'
import { buildWebhookUrl } from './webhook-url'

describe('buildWebhookUrl', () => {
  test.each([
    { base: 'https://example.com', ref: 'my-slug', want: 'https://example.com/w/my-slug' },
    { base: 'https://example.com/', ref: 'my-slug', want: 'https://example.com/w/my-slug' },
    { base: 'https://example.com/sub', ref: 'abc', want: 'https://example.com/sub/w/abc' },
    { base: 'https://example.com/sub/', ref: 'abc', want: 'https://example.com/sub/w/abc' },
    {
      base: 'http://localhost:8080',
      ref: '9b6bbab9-c197-4dd3-bc3f-3cb6253820c7',
      want: 'http://localhost:8080/w/9b6bbab9-c197-4dd3-bc3f-3cb6253820c7',
    },
  ])('builds /w/{ref} from $base (ref=$ref)', ({ base, ref, want }) => {
    expect(buildWebhookUrl(new URL(base), ref).toString()).toBe(want)
  })

  test('falls back to the window origin when the base is null', () => {
    expect(buildWebhookUrl(null, 'slugy').toString()).toBe(`${window.location.origin}/w/slugy`)
  })

  test('the reserved /w/ prefix is always present (never the bare ref)', () => {
    const url = buildWebhookUrl(new URL('https://example.com'), 'some-slug')

    expect(url.pathname).toBe('/w/some-slug')
    expect(url.pathname.startsWith('/w/')).toBe(true)
  })
})
