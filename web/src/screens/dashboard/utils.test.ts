import { describe, test, expect } from 'vitest'
import type { FirehoseEvent, SessionSummary } from '~/api'
import { requestPeek, returnedStatus, slugColor, statusCodeToColor } from './utils'

const makeSession = (over: Partial<SessionSummary> & { uuid: string; slug: string }): SessionSummary => ({
  group: null,
  statusCode: 200,
  requestsCount: 0,
  lastRequestAt: null,
  createdAt: new Date(0),
  expiresAt: new Date(0),
  longLived: false,
  ...over,
})

const makeEvent = (over: {
  sessionUUID: string
  url?: string
  authorized?: boolean
  hasRequest?: boolean
}): FirehoseEvent => ({
  sessionUUID: over.sessionUUID,
  sessionSlug: 'a-slug',
  action: 'create' as FirehoseEvent['action'],
  request:
    over.hasRequest === false
      ? null
      : {
          uuid: 'req-1',
          clientAddress: '203.0.113.7',
          method: 'POST',
          url: new URL(over.url ?? 'http://localhost/w/a-slug/x'),
          capturedAt: new Date(0),
          authorized: over.authorized ?? true,
        },
})

describe('slugColor', () => {
  test('is deterministic for the same slug', () => {
    expect(slugColor('my-app')).toBe(slugColor('my-app'))
  })

  test('returns a non-empty palette color', () => {
    expect(typeof slugColor('anything')).toBe('string')
    expect(slugColor('anything').length).toBeGreaterThan(0)
  })

  test('handles the empty slug without throwing', () => {
    expect(slugColor('')).toBe(slugColor(''))
  })
})

describe('statusCodeToColor', () => {
  test('maps ranges to colors', () => {
    expect(statusCodeToColor(500)).toBe('red')
    expect(statusCodeToColor(503)).toBe('red')
    expect(statusCodeToColor(404)).toBe('orange')
    expect(statusCodeToColor(301)).toBe('blue')
    expect(statusCodeToColor(200)).toBe('green')
    expect(statusCodeToColor(100)).toBe('gray')
    expect(statusCodeToColor(0)).toBe('gray')
  })
})

describe('returnedStatus', () => {
  const sessions = new Map<string, SessionSummary>([
    ['uuid-known', makeSession({ uuid: 'uuid-known', slug: 'known', statusCode: 418 })],
  ])

  test('a rejected (inbound-auth-failed) capture always returns 401', () => {
    expect(returnedStatus(makeEvent({ sessionUUID: 'uuid-known', authorized: false }), sessions)).toBe(401)
    // 401 even when the session is unknown
    expect(returnedStatus(makeEvent({ sessionUUID: 'uuid-missing', authorized: false }), sessions)).toBe(401)
  })

  test('an authorized capture falls back to the session default status code', () => {
    expect(returnedStatus(makeEvent({ sessionUUID: 'uuid-known', authorized: true }), sessions)).toBe(418)
  })

  test('returns null when the session is unknown', () => {
    expect(returnedStatus(makeEvent({ sessionUUID: 'uuid-missing', authorized: true }), sessions)).toBeNull()
  })
})

describe('requestPeek', () => {
  test('returns the captured path + query', () => {
    expect(requestPeek(makeEvent({ sessionUUID: 's', url: 'http://localhost/w/a-slug/path?q=1' }))).toBe(
      '/w/a-slug/path?q=1'
    )
  })

  test('returns "/" for a root path', () => {
    expect(requestPeek(makeEvent({ sessionUUID: 's', url: 'http://localhost' }))).toBe('/')
  })

  test('returns an empty string when there is no request', () => {
    expect(requestPeek(makeEvent({ sessionUUID: 's', hasRequest: false }))).toBe('')
  })
})
