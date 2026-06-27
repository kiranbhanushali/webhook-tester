import { describe, expect, test } from 'vitest'
import type { Middleware } from 'openapi-fetch'
import { createAuthMiddleware } from './auth'

type OnRequestParams = Parameters<NonNullable<Middleware['onRequest']>>[0]

/** Invokes the middleware's onRequest with a minimal, correctly-typed params object. */
const callOnRequest = async (mw: Middleware, request: Request): Promise<Request> => {
  const result = await mw.onRequest?.({ request } as unknown as OnRequestParams)

  if (!(result instanceof Request)) {
    throw new Error('onRequest did not return a Request')
  }

  return result
}

const ORIGIN = 'http://localhost'
const newRequest = (url = `${ORIGIN}/api/sessions`): Request => new Request(url)

describe('createAuthMiddleware', () => {
  test('attaches the Bearer header for a same-origin request when a token is present', async () => {
    const req = await callOnRequest(createAuthMiddleware(() => 'secret-token', ORIGIN), newRequest())

    expect(req.headers.get('Authorization')).toBe('Bearer secret-token')
  })

  test('does NOT attach the header for a cross-origin request (the token must never leak off-origin)', async () => {
    const req = await callOnRequest(
      createAuthMiddleware(() => 'secret-token', ORIGIN),
      newRequest('https://evil.example.com/api/sessions')
    )

    expect(req.headers.get('Authorization')).toBeNull()
  })

  test('does not attach the header when the token is null', async () => {
    const req = await callOnRequest(createAuthMiddleware(() => null, ORIGIN), newRequest())

    expect(req.headers.get('Authorization')).toBeNull()
  })

  test('does not attach the header when the token is an empty string', async () => {
    const req = await callOnRequest(createAuthMiddleware(() => '', ORIGIN), newRequest())

    expect(req.headers.get('Authorization')).toBeNull()
  })

  test('reads the token lazily on every request (so login mid-session takes effect)', async () => {
    let token: string | null = null
    const mw = createAuthMiddleware(() => token, ORIGIN)

    expect((await callOnRequest(mw, newRequest())).headers.get('Authorization')).toBeNull()

    token = 'late-token'

    expect((await callOnRequest(mw, newRequest())).headers.get('Authorization')).toBe('Bearer late-token')
  })
})
