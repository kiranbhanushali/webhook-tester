import type { Middleware } from 'openapi-fetch'

/**
 * The localStorage key under which the API auth token is persisted. It mirrors the `useStorage` convention
 * (`webhook-tester-v2-` prefix + JSON value) so the token survives reloads and is the single source of truth shared
 * between the openapi-fetch middleware (non-React) and the {@link import('~/shared').AuthProvider} (React state mirror).
 */
const STORAGE_KEY = 'webhook-tester-v2-auth-token'

/** A function that returns the current auth token (or `null`/empty when auth is not configured / not logged in). */
export type TokenProvider = () => string | null

/** Reads the persisted auth token, or `null` when absent / malformed. */
export const getStoredToken = (): string | null => {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)

    if (raw === null) {
      return null
    }

    const parsed: unknown = JSON.parse(raw)

    return typeof parsed === 'string' && parsed.length > 0 ? parsed : null
  } catch {
    return null // malformed value — treat as "no token"
  }
}

/** Persists the auth token. */
export const setStoredToken = (token: string): void => {
  localStorage.setItem(STORAGE_KEY, JSON.stringify(token))
}

/** Removes the persisted auth token. */
export const clearStoredToken = (): void => {
  localStorage.removeItem(STORAGE_KEY)
}

/**
 * Creates an openapi-fetch middleware that attaches `Authorization: Bearer <token>` when a token is present.
 *
 * The token is read lazily on every request, so logging in mid-session takes effect immediately.
 *
 * SAME-ORIGIN CONSTRAINT: the header is only attached when the request's origin matches `allowedOrigin` (the client's
 * configured base origin). The API is same-origin in practice, but this guard guarantees the secret can never leak to a
 * third party even if a cross-origin base URL is ever configured or a request is redirected off-origin.
 */
export const createAuthMiddleware = (getToken: TokenProvider, allowedOrigin: string): Middleware => ({
  async onRequest({ request }): Promise<Request> {
    const token = getToken()

    if (token && new URL(request.url).origin === allowedOrigin) {
      request.headers.set('Authorization', `Bearer ${token}`)
    }

    return request
  },
})

// --- unauthorized (HTTP 401) event bus ------------------------------------------------------------
// A tiny pub/sub so the response middleware (which has no React/Client reference) can notify the UI that auth is
// required, independent of middleware ordering. The AuthProvider subscribes and surfaces a token prompt.

type UnauthorizedListener = () => void

const unauthorizedListeners = new Set<UnauthorizedListener>()

/** Subscribe to "the server rejected us with 401" events. Returns an unsubscribe function. */
export const onUnauthorized = (listener: UnauthorizedListener): (() => void) => {
  unauthorizedListeners.add(listener)

  return (): void => {
    unauthorizedListeners.delete(listener)
  }
}

/** Notify all subscribers that the server responded with 401 Unauthorized. */
export const notifyUnauthorized = (): void => {
  for (const listener of unauthorizedListeners) {
    listener()
  }
}
