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

const newRequest = (): Request => new Request('http://localhost/api/sessions')

describe('createAuthMiddleware', () => {
  test('attaches the Bearer header when a token is present', async () => {
    const req = await callOnRequest(createAuthMiddleware(() => 'secret-token'), newRequest())

    expect(req.headers.get('Authorization')).toBe('Bearer secret-token')
  })

  test('does not attach the header when the token is null', async () => {
    const req = await callOnRequest(createAuthMiddleware(() => null), newRequest())

    expect(req.headers.get('Authorization')).toBeNull()
  })

  test('does not attach the header when the token is an empty string', async () => {
    const req = await callOnRequest(createAuthMiddleware(() => ''), newRequest())

    expect(req.headers.get('Authorization')).toBeNull()
  })

  test('reads the token lazily on every request (so login mid-session takes effect)', async () => {
    let token: string | null = null
    const mw = createAuthMiddleware(() => token)

    expect((await callOnRequest(mw, newRequest())).headers.get('Authorization')).toBeNull()

    token = 'late-token'

    expect((await callOnRequest(mw, newRequest())).headers.get('Authorization')).toBe('Bearer late-token')
  })
})
