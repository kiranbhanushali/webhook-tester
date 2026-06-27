import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, test, vi } from 'vitest'
import fetchMock from '@fetch-mock/vitest'
import { base64ToUint8Array, uint8ArrayToBase64 } from '~/shared'
import { APIErrorCommon } from './errors'
import { Client, type FirehoseEvent } from './client'

beforeAll(() => fetchMock.mockGlobal())
afterAll(() => fetchMock.mockRestore())

const baseUrl = 'http://localhost'

/** A quick way to test if an error is thrown is to check it. */
const expectError = async (fn: () => Promise<unknown> | unknown, checkErrorFn: (e: TypeError) => void) => {
  try {
    await fn()

    expect(true).toBe(false) // fail the test if the error is not thrown
  } catch (e: TypeError | unknown) {
    expect(e).toBeInstanceOf(Error)

    checkErrorFn(e as Error)
  }
}

describe('currentVersion', () => {
  const mockUrlMatcher = /\/api\/version$/

  test('pass', async () => {
    fetchMock.getOnce(mockUrlMatcher, { status: 200, body: { version: 'v1.2.3' } })

    const client = new Client({ baseUrl })

    expect((await client.currentVersion()).toString()).equals('1.2.3')
    expect((await client.currentVersion()).toString()).equals('1.2.3') // the second call should use the cache

    fetchMock.getOnce(mockUrlMatcher, { status: 200, body: { version: 'V3.2.1' } })

    expect((await client.currentVersion(true)).toString()).equals('3.2.1') // the cache should be updated
    expect((await client.currentVersion()).toString()).equals('3.2.1') // the second call should use the cache
  })

  test('throws', async () => {
    fetchMock.getOnce(mockUrlMatcher, { status: 501, body: '"error"' })

    await expectError(
      async () => await new Client({ baseUrl }).currentVersion(),
      (e) => {
        expect(e).toBeInstanceOf(APIErrorCommon)
        expect(e.message).toBe('Not Implemented')
      }
    )
  })
})

describe('newSession (new fields)', () => {
  test('decodes slug, group, response_script, security_headers, forward_url, long_lived + base64 body', async () => {
    const body = new Uint8Array([1, 2, 3, 4])

    fetchMock.postOnce(/\/api\/session$/, {
      status: 200,
      body: {
        uuid: '9b6bbab9-c197-4dd3-bc3f-3cb6253820c7',
        created_at_unix_milli: 1000,
        expires_at_unix_milli: 2000,
        response: {
          status_code: 201,
          headers: [{ name: 'X-Foo', value: 'bar' }],
          delay: 3,
          response_body_base64: uint8ArrayToBase64(body),
          slug: 'my-session',
          group: 'team-a',
          response_script: 'return 1',
          security_headers: [{ name: 'X-Frame-Options', value: 'DENY' }],
          forward_url: 'https://example.com/webhook',
          long_lived: true,
        },
      },
    })

    const s = await new Client({ baseUrl }).newSession({ statusCode: 201 })

    expect(s.uuid).toBe('9b6bbab9-c197-4dd3-bc3f-3cb6253820c7')
    expect(s.response.slug).toBe('my-session')
    expect(s.response.group).toBe('team-a')
    expect(s.response.responseScript).toBe('return 1')
    expect(s.response.securityHeaders).toEqual([{ name: 'X-Frame-Options', value: 'DENY' }])
    expect(s.response.forwardUrl).toBe('https://example.com/webhook')
    expect(s.response.longLived).toBe(true)
    expect(Array.from(s.response.body)).toEqual([1, 2, 3, 4])
    expect(s.expiresAt?.getTime()).toBe(2000)
  })

  test('falls back to safe defaults when the optional fields are absent', async () => {
    fetchMock.postOnce(/\/api\/session$/, {
      status: 200,
      body: {
        uuid: 'u-1',
        created_at_unix_milli: 0,
        response: { status_code: 200, headers: [], delay: 0, response_body_base64: '' },
      },
    })

    const s = await new Client({ baseUrl }).newSession({})

    expect(s.response.slug).toBe('')
    expect(s.response.group).toBeNull()
    expect(s.response.responseScript).toBeNull()
    expect(s.response.securityHeaders).toEqual([])
    expect(s.response.forwardUrl).toBeNull()
    expect(s.response.longLived).toBe(false)
  })
})

describe('getSession (new fields)', () => {
  test('decodes the new fields', async () => {
    fetchMock.getOnce(/\/api\/session\/[^/]+$/, {
      status: 200,
      body: {
        uuid: 'u-2',
        created_at_unix_milli: 10,
        response: {
          status_code: 200,
          headers: [],
          delay: 0,
          response_body_base64: '',
          slug: 'slug-2',
          group: 'g',
          long_lived: false,
        },
      },
    })

    const s = await new Client({ baseUrl }).getSession('slug-2')

    expect(s.response.slug).toBe('slug-2')
    expect(s.response.group).toBe('g')
  })
})

describe('listSessions', () => {
  beforeEach(() => fetchMock.removeRoutes())

  test('maps the session summaries', async () => {
    fetchMock.getOnce(/\/api\/sessions/, {
      status: 200,
      body: [
        {
          slug: 's1',
          group: 'g1',
          status_code: 200,
          requests_count: 3,
          last_request_unix_milli: 500,
          created_at_unix_milli: 100,
          expires_at_unix_milli: 900,
          long_lived: false,
        },
        {
          slug: 's2',
          status_code: 404,
          requests_count: 0,
          created_at_unix_milli: 50,
          expires_at_unix_milli: 800,
          long_lived: true,
        },
      ],
    })

    const list = await new Client({ baseUrl }).listSessions()

    expect(list).toHaveLength(2)
    expect(list[0].slug).toBe('s1')
    expect(list[0].group).toBe('g1')
    expect(list[0].requestsCount).toBe(3)
    expect(list[0].statusCode).toBe(200)
    expect(list[0].lastRequestAt?.getTime()).toBe(500)
    expect(list[0].createdAt.getTime()).toBe(100)
    expect(list[0].expiresAt.getTime()).toBe(900)
    expect(list[0].longLived).toBe(false)
    expect(list[1].group).toBeNull()
    expect(list[1].lastRequestAt).toBeNull()
    expect(list[1].longLived).toBe(true)
  })

  test('forwards the group and q query parameters', async () => {
    fetchMock.getOnce(/\/api\/sessions/, { status: 200, body: [] })

    await new Client({ baseUrl }).listSessions({ group: 'team-a', q: 'foo' })

    const url = new URL(String(fetchMock.callHistory.lastCall()?.url))

    expect(url.pathname).toBe('/api/sessions')
    expect(url.searchParams.get('group')).toBe('team-a')
    expect(url.searchParams.get('q')).toBe('foo')
  })
})

describe('searchIdentifiers', () => {
  beforeEach(() => fetchMock.removeRoutes())

  test('maps the search results', async () => {
    fetchMock.getOnce(/\/api\/search/, {
      status: 200,
      body: [
        {
          session_slug: 'my-session',
          request_uuid: '11111111-1111-1111-1111-111111111111',
          key: 'txn_id',
          value: 'abc-123',
          captured_at_unix_milli: 1234,
        },
      ],
    })

    const res = await new Client({ baseUrl }).searchIdentifiers({ value: 'abc-123' })

    expect(res).toHaveLength(1)
    expect(res[0].sessionSlug).toBe('my-session')
    expect(res[0].requestUUID).toBe('11111111-1111-1111-1111-111111111111')
    expect(res[0].key).toBe('txn_id')
    expect(res[0].value).toBe('abc-123')
    expect(res[0].capturedAt.getTime()).toBe(1234)
  })

  test('builds the full query string from the structured params', async () => {
    fetchMock.getOnce(/\/api\/search/, { status: 200, body: [] })

    await new Client({ baseUrl }).searchIdentifiers({
      key: 'txn',
      value: 'abc',
      match: 'prefix',
      group: 'g',
      session: 'sess',
      from: 10,
      to: 20,
      limit: 5,
    })

    const url = new URL(String(fetchMock.callHistory.lastCall()?.url))

    expect(url.pathname).toBe('/api/search')
    expect(url.searchParams.get('value')).toBe('abc')
    expect(url.searchParams.get('key')).toBe('txn')
    expect(url.searchParams.get('match')).toBe('prefix')
    expect(url.searchParams.get('group')).toBe('g')
    expect(url.searchParams.get('session')).toBe('sess')
    expect(url.searchParams.get('from')).toBe('10')
    expect(url.searchParams.get('to')).toBe('20')
    expect(url.searchParams.get('limit')).toBe('5')
  })

  test('omits optional params that are not provided', async () => {
    fetchMock.getOnce(/\/api\/search/, { status: 200, body: [] })

    await new Client({ baseUrl }).searchIdentifiers({ value: 'only-value' })

    const url = new URL(String(fetchMock.callHistory.lastCall()?.url))

    expect(url.searchParams.get('value')).toBe('only-value')
    expect(url.searchParams.has('key')).toBe(false)
    expect(url.searchParams.has('match')).toBe(false)
    expect(url.searchParams.has('limit')).toBe(false)
  })
})

describe('updateSession', () => {
  beforeEach(() => fetchMock.removeRoutes())

  test('encodes the patch (incl. base64 body + new fields) and decodes the response', async () => {
    const newBody = new Uint8Array([9, 8, 7])

    fetchMock.patchOnce(/\/api\/session\/[^/]+$/, {
      status: 200,
      body: {
        uuid: 'u-3',
        created_at_unix_milli: 0,
        response: {
          status_code: 418,
          headers: [],
          delay: 1,
          response_body_base64: uint8ArrayToBase64(newBody),
          slug: 'renamed',
          group: 'g2',
        },
      },
    })

    const s = await new Client({ baseUrl }).updateSession('old-slug', {
      statusCode: 418,
      slug: 'renamed',
      group: 'g2',
      responseBody: newBody,
      longLived: true,
    })

    expect(s.response.statusCode).toBe(418)
    expect(s.response.slug).toBe('renamed')
    expect(Array.from(s.response.body)).toEqual([9, 8, 7])

    const sentBody = JSON.parse(String(fetchMock.callHistory.lastCall()?.options?.body))

    expect(sentBody.status_code).toBe(418)
    expect(sentBody.slug).toBe('renamed')
    expect(sentBody.group).toBe('g2')
    expect(sentBody.long_lived).toBe(true)
    expect(Array.from(base64ToUint8Array(sentBody.response_body_base64))).toEqual([9, 8, 7])
  })

  test('only sends the provided fields', async () => {
    fetchMock.patchOnce(/\/api\/session\/[^/]+$/, {
      status: 200,
      body: { uuid: 'u-4', created_at_unix_milli: 0, response: { status_code: 200, headers: [], delay: 0, response_body_base64: '' } },
    })

    await new Client({ baseUrl }).updateSession('ref', { slug: 'just-slug' })

    const sentBody = JSON.parse(String(fetchMock.callHistory.lastCall()?.options?.body))

    expect(sentBody).toEqual({ slug: 'just-slug' })
  })
})

describe('replayRequest', () => {
  beforeEach(() => fetchMock.removeRoutes())

  test('sends the target_url and decodes the base64 response body', async () => {
    const respBody = new Uint8Array([5, 6])

    fetchMock.postOnce(/\/api\/session\/[^/]+\/requests\/[^/]+\/replay$/, {
      status: 200,
      body: {
        status_code: 200,
        headers: [{ name: 'X-Replayed', value: 'yes' }],
        body_base64: uint8ArrayToBase64(respBody),
      },
    })

    const res = await new Client({ baseUrl }).replayRequest('sess', 'req-1', 'https://example.com/hook')

    expect(res.statusCode).toBe(200)
    expect(res.headers).toEqual([{ name: 'X-Replayed', value: 'yes' }])
    expect(Array.from(res.body)).toEqual([5, 6])

    const sentBody = JSON.parse(String(fetchMock.callHistory.lastCall()?.options?.body))

    expect(sentBody).toEqual({ target_url: 'https://example.com/hook' })
  })

  test('omits the body when no target URL is given (server uses the session forward URL)', async () => {
    fetchMock.postOnce(/\/api\/session\/[^/]+\/requests\/[^/]+\/replay$/, {
      status: 200,
      body: { status_code: 200, headers: [], body_base64: '' },
    })

    await new Client({ baseUrl }).replayRequest('sess', 'req-1')

    // no target_url must be sent (the server then falls back to the session forward URL)
    expect(String(fetchMock.callHistory.lastCall()?.options?.body ?? '')).not.toContain('target_url')
  })
})

describe('newSession — inbound-auth fields', () => {
  test('decodes inbound_auth_header and inbound_auth_value from response', async () => {
    fetchMock.postOnce(/\/api\/session$/, {
      status: 200,
      body: {
        uuid: 'uuid-inbound',
        created_at_unix_milli: 0,
        response: {
          status_code: 200,
          headers: [],
          delay: 0,
          response_body_base64: '',
          inbound_auth_header: 'X-Webhook-Token',
          inbound_auth_value: 'super-secret',
        },
      },
    })
    const s = await new Client({ baseUrl }).newSession({
      inboundAuthHeader: 'X-Webhook-Token',
      inboundAuthValue: 'super-secret',
    })
    expect(s.response.inboundAuthHeader).toBe('X-Webhook-Token')
    expect(s.response.inboundAuthValue).toBe('super-secret')
  })

  test('maps null/absent inbound_auth fields to null', async () => {
    fetchMock.postOnce(/\/api\/session$/, {
      status: 200,
      body: {
        uuid: 'uuid-no-auth',
        created_at_unix_milli: 0,
        response: { status_code: 200, headers: [], delay: 0, response_body_base64: '' },
      },
    })
    const s = await new Client({ baseUrl }).newSession({})
    expect(s.response.inboundAuthHeader).toBeNull()
    expect(s.response.inboundAuthValue).toBeNull()
  })
})

describe('getSessionRequests — paginated (newest first)', () => {
  const page = (items: unknown[], next_before: number, has_more: boolean) => ({ items, next_before, has_more })
  const item = (uuid: string, authorized: boolean, seq: number) => ({
    seq,
    uuid,
    client_address: '1.2.3.4',
    method: 'POST',
    request_payload_base64: '',
    headers: [],
    url: 'http://localhost/' + uuid,
    captured_at_unix_milli: seq, // monotonic so newest-first sort is deterministic
    authorized,
  })

  test('first page: defaults to before=0&limit=50 and parses items/cursor/has_more', async () => {
    fetchMock.getOnce(/\/api\/session\/s1\/requests/, {
      status: 200,
      body: page([item('r2', true, 2), item('r1', true, 1)], 1, true),
    })

    const res = await new Client({ baseUrl }).getSessionRequests('s1')

    // default cursor params are sent
    const url = new URL(String(fetchMock.callHistory.lastCall()?.url))
    expect(url.searchParams.get('before')).toBe('0')
    expect(url.searchParams.get('limit')).toBe('50')

    // page shape parsed, newest first
    expect(res.items).toHaveLength(2)
    expect(res.items[0].uuid).toBe('r2')
    expect(res.items[0].seq).toBe(2)
    expect(res.items[0].authorized).toBe(true)
    expect(res.nextBefore).toBe(1)
    expect(res.hasMore).toBe(true)
  })

  test('next page: forwards the before cursor and limit, parses has_more=false', async () => {
    fetchMock.getOnce(/\/api\/session\/s2\/requests/, {
      status: 200,
      body: page([item('r0', false, 0)], 0, false),
    })

    const res = await new Client({ baseUrl }).getSessionRequests('s2', { before: 1, limit: 50 })

    const url = new URL(String(fetchMock.callHistory.lastCall()?.url))
    expect(url.searchParams.get('before')).toBe('1')
    expect(url.searchParams.get('limit')).toBe('50')
    expect(res.items).toHaveLength(1)
    expect(res.items[0].authorized).toBe(false)
    expect(res.hasMore).toBe(false)
    expect(res.nextBefore).toBe(0)
  })
})

describe('getSessionRequest (single) — authorized field', () => {
  test('maps authorized field on single request fetch', async () => {
    fetchMock.getOnce(/\/api\/session\/s3\/requests\/r3$/, {
      status: 200,
      body: {
        uuid: 'r3',
        client_address: '1.2.3.4',
        method: 'GET',
        request_payload_base64: '',
        headers: [],
        url: 'http://localhost/s3',
        captured_at_unix_milli: 0,
        authorized: false,
      },
    })
    const req = await new Client({ baseUrl }).getSessionRequest('s3', 'r3')
    expect(req.authorized).toBe(false)
  })
})

describe('subscribeFirehose', () => {
  // a minimal fake WebSocket so the firehose parse logic can be exercised WITHOUT a real socket
  class FakeWebSocket {
    public onopen: ((ev: Event) => void) | null = null
    public onmessage: ((ev: { data: string }) => void) | null = null
    public onerror: ((ev: Event) => void) | null = null
    public onclose: ((ev: { wasClean: boolean; code: number }) => void) | null = null
    public readonly url: string
    public readonly close = vi.fn()
    public static instances: FakeWebSocket[] = []

    constructor(url: string) {
      this.url = url
      FakeWebSocket.instances.push(this)
    }
  }

  beforeEach(() => {
    FakeWebSocket.instances = []
    vi.stubGlobal('WebSocket', FakeWebSocket as unknown as typeof WebSocket)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  const lastWS = (): FakeWebSocket => {
    const ws = FakeWebSocket.instances[FakeWebSocket.instances.length - 1]
    if (!ws) {
      throw new Error('no WebSocket was constructed')
    }
    return ws
  }

  test('connects to ws://<host>/api/firehose/subscribe and resolves a closer on open', async () => {
    const onConnected = vi.fn()
    const onEvent = vi.fn<(e: FirehoseEvent) => void>()

    const closerPromise = new Client({ baseUrl }).subscribeFirehose({ onConnected, onEvent })

    const ws = lastWS()
    expect(ws.url).toBe('ws://localhost/api/firehose/subscribe')

    ws.onopen?.(new Event('open'))

    const closer = await closerPromise
    expect(onConnected).toHaveBeenCalledTimes(1)

    closer()
    expect(ws.close).toHaveBeenCalledTimes(1)
  })

  test('parses a FirehoseEvent message into the typed client model and invokes onEvent', async () => {
    const onEvent = vi.fn<(e: FirehoseEvent) => void>()

    const p = new Client({ baseUrl }).subscribeFirehose({ onEvent })
    const ws = lastWS()
    ws.onopen?.(new Event('open'))
    await p

    ws.onmessage?.({
      data: JSON.stringify({
        session_uuid: 'sess-uuid-1',
        session_slug: 'my-app',
        action: 'create',
        request: {
          uuid: 'req-uuid-1',
          client_address: '203.0.113.7',
          method: 'POST',
          url: 'https://example.com/w/my-app/path?q=1',
          captured_at_unix_milli: 1234,
          authorized: false,
        },
      }),
    })

    expect(onEvent).toHaveBeenCalledTimes(1)

    const ev = onEvent.mock.calls[0][0]
    expect(ev.sessionUUID).toBe('sess-uuid-1')
    expect(ev.sessionSlug).toBe('my-app')
    expect(ev.action).toBe('create')
    expect(ev.request?.method).toBe('POST')
    expect(ev.request?.clientAddress).toBe('203.0.113.7')
    expect(ev.request?.authorized).toBe(false)
    expect(ev.request?.url.toString()).toBe('https://example.com/w/my-app/path?q=1')
    expect(ev.request?.capturedAt.getTime()).toBe(1234)
  })

  test('surfaces a clean close as onError once connected (so the UI stops showing "Live")', async () => {
    const onEvent = vi.fn<(e: FirehoseEvent) => void>()
    const onError = vi.fn<(err: Error) => void>()

    const p = new Client({ baseUrl }).subscribeFirehose({ onEvent, onError })
    const ws = lastWS()
    ws.onopen?.(new Event('open'))
    await p

    ws.onclose?.({ wasClean: true, code: 1000 })

    expect(onError).toHaveBeenCalledTimes(1)
    expect(onError.mock.calls[0][0]).toBeInstanceOf(Error)
  })

  test('does NOT surface a close that happens before the connection opened (handled via onerror/reject)', () => {
    const onEvent = vi.fn<(e: FirehoseEvent) => void>()
    const onError = vi.fn<(err: Error) => void>()

    void new Client({ baseUrl }).subscribeFirehose({ onEvent, onError })
    const ws = lastWS()

    // close arrives before onopen → connected is still false
    ws.onclose?.({ wasClean: false, code: 1006 })

    expect(onError).not.toHaveBeenCalled()
  })
})

describe('updateSession — inbound-auth fields', () => {
  test('sends inbound_auth_header and inbound_auth_value in patch', async () => {
    fetchMock.patchOnce(/\/api\/session\/sid1$/, {
      status: 200,
      body: {
        uuid: 'sid1',
        created_at_unix_milli: 0,
        response: {
          status_code: 200,
          headers: [],
          delay: 0,
          response_body_base64: '',
          inbound_auth_header: 'X-Token',
          inbound_auth_value: 'abc',
        },
      },
    })
    const s = await new Client({ baseUrl }).updateSession('sid1', {
      inboundAuthHeader: 'X-Token',
      inboundAuthValue: 'abc',
    })
    expect(s.response.inboundAuthHeader).toBe('X-Token')
    expect(s.response.inboundAuthValue).toBe('abc')
  })
})
