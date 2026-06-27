import createClient, { type Client as OpenapiClient, type ClientOptions } from 'openapi-fetch'
import { coerce as semverCoerce, parse as semverParse, type SemVer } from 'semver'
import { base64ToUint8Array, uint8ArrayToBase64 } from '~/shared'
import { createAuthMiddleware, getStoredToken, type TokenProvider } from './auth'
import { APIErrorUnknown } from './errors'
import { throwIfNotJSON, throwIfNotValidResponse } from './middleware'
import { components, paths, PathsApiSearchGetParametersQueryMatch, type RequestEventAction } from './schema.gen'

/** A single name/value header pair. */
type Header = Readonly<{ name: string; value: string }>

/**
 * The shared shape returned by the create/get/update session endpoints.
 *
 * Header lists are typed as `ArrayLike<Header>` (rather than `readonly Header[]`) because openapi-fetch wraps response
 * bodies in its deep-readonly `Readable<>` transform, which turns arrays into array-like objects; `ArrayLike` matches
 * that and is still accepted by `Array.from(...)`.
 */
type SessionOptionsData = {
  uuid: string
  response: {
    status_code: number
    headers: ArrayLike<Header>
    delay: number
    response_body_base64: string
    slug?: string
    group?: string
    response_script?: string
    security_headers?: ArrayLike<Header>
    forward_url?: string
    long_lived?: boolean
  }
  created_at_unix_milli: number
  expires_at_unix_milli?: number
}

/** The value-match mode for identifier search. */
export type SearchMatch = 'exact' | 'prefix'

type AppSettings = Readonly<{
  limits: Readonly<{
    maxRequests: number
    maxRequestBodySize: number // In bytes
    sessionTTL: number // In seconds
  }>
  tunnel: Readonly<{
    enabled: boolean
    url: URL | null
  }>
  publicUrlRoot: URL | null
}>

type SessionOptions = Readonly<{
  uuid: string
  response: Readonly<{
    statusCode: number
    headers: ReadonlyArray<Header>
    delay: number
    body: Readonly<Uint8Array>
    /** Human-readable session slug (empty string if the server did not assign one) */
    slug: string
    group: string | null
    responseScript: string | null
    securityHeaders: ReadonlyArray<Header>
    forwardUrl: string | null
    longLived: boolean
  }>
  createdAt: Readonly<Date>
  expiresAt: Readonly<Date> | null
}>

/** Summary information about a session, as returned by the sessions-list endpoint. */
export type SessionSummary = Readonly<{
  uuid: string
  slug: string
  group: string | null
  statusCode: number
  requestsCount: number
  lastRequestAt: Readonly<Date> | null
  createdAt: Readonly<Date>
  expiresAt: Readonly<Date>
  longLived: boolean
}>

/** A single identifier-search result, linking a matched key/value to its captured request. */
export type SearchResultItem = Readonly<{
  sessionUUID: string
  sessionSlug: string
  requestUUID: string
  key: string
  value: string
  capturedAt: Readonly<Date>
}>

/** The response received when replaying a captured request to a target URL. */
export type ReplayResult = Readonly<{
  statusCode: number
  headers: ReadonlyArray<Header>
  body: Readonly<Uint8Array>
}>

/** Fields that can be patched on an existing session (all optional; omitted fields are left unchanged). */
export type SessionPatch = Readonly<{
  statusCode?: number
  headers?: ReadonlyArray<Header>
  delay?: number
  responseBody?: Uint8Array
  slug?: string
  group?: string
  responseScript?: string
  securityHeaders?: ReadonlyArray<Header>
  forwardUrl?: string
  longLived?: boolean
}>

type HttpMethod = 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS' | 'CONNECT' | 'TRACE' | string

type CapturedRequest = Readonly<{
  uuid: string
  clientAddress: string
  method: HttpMethod
  requestPayload: Uint8Array
  headers: ReadonlyArray<{ name: string; value: string }>
  url: Readonly<URL>
  capturedAt: Readonly<Date>
}>

type RequestEvent = Readonly<{
  action: RequestEventAction
  request: {
    uuid: string
    clientAddress: string
    method: HttpMethod
    headers: ReadonlyArray<{ name: string; value: string }>
    url: Readonly<URL>
    capturedAt: Readonly<Date>
  } | null
}>

export class Client {
  private readonly baseUrl: URL
  private readonly api: OpenapiClient<paths>
  private cache: Partial<{
    currentVersion: Readonly<SemVer>
    latestVersion: Readonly<SemVer>
    settings: AppSettings
  }> = {}

  private readonly getToken: TokenProvider

  /**
   * @param opt      openapi-fetch client options (e.g. `baseUrl`).
   * @param getToken Provider for the API auth token; defaults to the persisted token. When it returns a non-empty
   *                 token, an `Authorization: Bearer <token>` header is attached to every request.
   */
  constructor(opt?: ClientOptions, getToken: TokenProvider = getStoredToken) {
    const baseUrl: string | null = opt?.baseUrl
      ? opt.baseUrl
      : typeof window !== 'undefined' // for non-browser environments, like tests
        ? window.location.protocol + '//' + window.location.host
        : null

    if (!baseUrl) {
      throw new Error('The base URL is not provided and cannot be determined')
    }

    this.baseUrl = new URL(baseUrl)
    this.getToken = getToken

    this.api = createClient<paths>({ ...opt, baseUrl: baseUrl.toString() })
    // the auth middleware is registered first so its onRequest runs first (attaching the Bearer header before the
    // request is sent); the response validators run afterwards and surface 401s as APIErrorUnauthorized. The token is
    // only attached to same-origin requests (see createAuthMiddleware) so it can never leak cross-origin.
    this.api.use(createAuthMiddleware(this.getToken, this.baseUrl.origin), throwIfNotJSON, throwIfNotValidResponse)
  }

  /** Maps the wire shape of the create/get/update session response into the immutable client model. */
  private static mapSessionOptions(data: SessionOptionsData): SessionOptions {
    return Object.freeze({
      uuid: data.uuid,
      response: Object.freeze({
        statusCode: data.response.status_code,
        headers: Array.from(data.response.headers).map(({ name, value }) => Object.freeze({ name, value })),
        delay: data.response.delay,
        body: base64ToUint8Array(data.response.response_body_base64),
        slug: data.response.slug ?? '',
        group: data.response.group ?? null,
        responseScript: data.response.response_script ?? null,
        securityHeaders: Array.from(data.response.security_headers ?? []).map(({ name, value }) =>
          Object.freeze({ name, value })
        ),
        forwardUrl: data.response.forward_url ?? null,
        longLived: data.response.long_lived ?? false,
      }),
      createdAt: Object.freeze(new Date(data.created_at_unix_milli)),
      expiresAt: data.expires_at_unix_milli ? Object.freeze(new Date(data.expires_at_unix_milli)) : null,
    })
  }

  /**
   * Logs in with the given shared token: POSTs `/api/auth/login` so the server sets the `wh_token` cookie (required by
   * the WebSocket, which cannot send an Authorization header). Persisting the token for the Bearer middleware is the
   * caller's responsibility (see the AuthProvider).
   *
   * Returns `true` on success (HTTP 200), `false` when the token is rejected (HTTP 401).
   *
   * NOTE: this endpoint is intentionally outside the generated OpenAPI schema, so it is called via a raw `fetch`.
   *
   * @throws {Error} on network failures or unexpected status codes.
   */
  async login(token: string): Promise<boolean> {
    const resp = await fetch(new URL('/api/auth/login', this.baseUrl).toString(), {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include', // so the Set-Cookie (wh_token) is stored for the WebSocket
      body: JSON.stringify({ token }),
    })

    if (resp.status === 401) {
      return false
    }

    if (!resp.ok) {
      throw new Error(`Login failed: ${resp.status} ${resp.statusText}`)
    }

    return true
  }

  /**
   * Returns the version of the app.
   *
   * @throws {APIError}
   */
  async currentVersion(force: boolean = false): Promise<Readonly<SemVer>> {
    if (this.cache.currentVersion && !force) {
      return this.cache.currentVersion
    }

    const { data, response } = await this.api.GET('/api/version', { priority: 'low' })

    if (data) {
      const version = semverParse(semverCoerce(data.version.replace('@', '-')))

      if (!version) {
        throw new APIErrorUnknown({ message: `Failed to parse the current version value: ${data.version}`, response })
      }

      this.cache.currentVersion = Object.freeze(version)

      return this.cache.currentVersion
    }

    throw new APIErrorUnknown({ message: response.statusText, response }) // will never happen due to the middleware
  }

  /**
   * Returns the latest available version of the app.
   *
   * @throws {APIError}
   */
  async latestVersion(force: boolean = false): Promise<Readonly<SemVer>> {
    if (this.cache.latestVersion && !force) {
      return this.cache.latestVersion
    }

    const { data, response } = await this.api.GET('/api/version/latest', { priority: 'low' })

    if (data) {
      const version = semverParse(semverCoerce(data.version))

      if (!version) {
        throw new APIErrorUnknown({ message: `Failed to parse the latest version value: ${data.version}`, response })
      }

      this.cache.latestVersion = Object.freeze(version)

      return this.cache.latestVersion
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Returns the app settings.
   *
   * @throws {APIError}
   */
  async getSettings(force: boolean = false): Promise<AppSettings> {
    if (this.cache.settings && !force) {
      return this.cache.settings
    }

    const { data, response } = await this.api.GET('/api/settings')

    if (data) {
      this.cache.settings = Object.freeze({
        limits: Object.freeze({
          maxRequests: data.limits.max_requests,
          maxRequestBodySize: data.limits.max_request_body_size,
          sessionTTL: data.limits.session_ttl, // in seconds
        }),
        tunnel: Object.freeze({
          enabled: data.tunnel.enabled,
          url: data?.tunnel.url ? new URL(data.tunnel.url) : null,
        }),
        publicUrlRoot: data?.public_url_root ? new URL(data.public_url_root.replace(/\/+$/, '')) : null,
      })

      return this.cache.settings
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Creates a new session with the specified response settings.
   *
   * @throws {APIError}
   */
  async newSession({
    statusCode = 200,
    headers = {},
    delay = 0,
    responseBody = new Uint8Array(),
  }: {
    statusCode?: number
    headers?: Record<string, string>
    delay?: number
    responseBody?: Uint8Array
  }): Promise<SessionOptions> {
    const { data, response } = await this.api.POST('/api/session', {
      body: {
        status_code: Math.min(Math.max(100, statusCode), 530), // clamp to the valid range
        headers: Object.entries(headers)
          .map(([name, value]) => ({ name, value })) // convert to array of objects
          .filter((h) => h.value), // remove empty values
        delay: Math.min(Math.max(0, delay), 30), // clamp to the valid range
        response_body_base64: uint8ArrayToBase64(responseBody),
      },
    })

    if (data) {
      return Client.mapSessionOptions(data)
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Returns the session by its reference (UUID or slug).
   *
   * @throws {APIError}
   */
  async getSession(ref: string): Promise<SessionOptions> {
    const { data, response } = await this.api.GET(`/api/session/{session_uuid}`, {
      params: { path: { session_uuid: ref } },
    })

    if (data) {
      return Client.mapSessionOptions(data)
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Returns the list of all sessions available on the server, optionally filtered by group and/or a free-text query
   * (case-sensitive substring of the id, slug or group name).
   *
   * @throws {APIError}
   */
  async listSessions(params?: { group?: string; q?: string }): Promise<ReadonlyArray<SessionSummary>> {
    const query: { group?: string; q?: string } = {}

    if (params?.group) {
      query.group = params.group
    }

    if (params?.q) {
      query.q = params.q
    }

    const { data, response } = await this.api.GET('/api/sessions', { params: { query } })

    if (data) {
      return Object.freeze(
        Array.from(data).map((s) =>
          Object.freeze({
            uuid: s.uuid,
            slug: s.slug,
            group: s.group ?? null,
            statusCode: s.status_code,
            requestsCount: s.requests_count,
            lastRequestAt: s.last_request_unix_milli ? Object.freeze(new Date(s.last_request_unix_milli)) : null,
            createdAt: Object.freeze(new Date(s.created_at_unix_milli)),
            expiresAt: Object.freeze(new Date(s.expires_at_unix_milli)),
            longLived: s.long_lived,
          })
        )
      )
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Searches captured-request identifiers by key/value across sessions.
   *
   * @throws {APIError}
   */
  async searchIdentifiers(params: {
    value: string
    key?: string
    match?: SearchMatch
    group?: string
    session?: string // session reference (uuid or slug) to restrict the search to
    from?: number // lower bound on capture time (unix milliseconds)
    to?: number // upper bound on capture time (unix milliseconds)
    limit?: number
  }): Promise<ReadonlyArray<SearchResultItem>> {
    const query: {
      value: string
      key?: string
      match?: PathsApiSearchGetParametersQueryMatch
      group?: string
      session?: string
      from?: number
      to?: number
      limit?: number
    } = { value: params.value }

    if (params.key) {
      query.key = params.key
    }

    if (params.match) {
      query.match =
        params.match === 'prefix'
          ? PathsApiSearchGetParametersQueryMatch.prefix
          : PathsApiSearchGetParametersQueryMatch.exact
    }

    if (params.group) {
      query.group = params.group
    }

    if (params.session) {
      query.session = params.session
    }

    if (typeof params.from === 'number') {
      query.from = params.from
    }

    if (typeof params.to === 'number') {
      query.to = params.to
    }

    if (typeof params.limit === 'number') {
      query.limit = params.limit
    }

    const { data, response } = await this.api.GET('/api/search', { params: { query } })

    if (data) {
      return Object.freeze(
        Array.from(data).map((item) =>
          Object.freeze({
            sessionUUID: item.session_uuid,
            sessionSlug: item.session_slug,
            requestUUID: item.request_uuid,
            key: item.key,
            value: item.value,
            capturedAt: Object.freeze(new Date(item.captured_at_unix_milli)),
          })
        )
      )
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Updates session options by reference (UUID or slug). Only the provided fields are sent; omitted fields are left
   * unchanged on the server.
   *
   * @throws {APIError}
   */
  async updateSession(ref: string, patch: SessionPatch): Promise<SessionOptions> {
    const body: {
      status_code?: number
      headers?: Array<Header>
      delay?: number
      response_body_base64?: string
      slug?: string
      group?: string
      response_script?: string
      security_headers?: Array<Header>
      forward_url?: string
      long_lived?: boolean
    } = {}

    if (typeof patch.statusCode === 'number') {
      body.status_code = Math.min(Math.max(100, patch.statusCode), 530) // clamp to the valid range
    }

    if (patch.headers) {
      body.headers = patch.headers.map(({ name, value }) => ({ name, value }))
    }

    if (typeof patch.delay === 'number') {
      body.delay = Math.min(Math.max(0, patch.delay), 30) // clamp to the valid range
    }

    if (patch.responseBody) {
      body.response_body_base64 = uint8ArrayToBase64(patch.responseBody)
    }

    if (patch.slug !== undefined) {
      body.slug = patch.slug
    }

    if (patch.group !== undefined) {
      body.group = patch.group
    }

    if (patch.responseScript !== undefined) {
      body.response_script = patch.responseScript
    }

    if (patch.securityHeaders) {
      body.security_headers = patch.securityHeaders.map(({ name, value }) => ({ name, value }))
    }

    if (patch.forwardUrl !== undefined) {
      body.forward_url = patch.forwardUrl
    }

    if (patch.longLived !== undefined) {
      body.long_lived = patch.longLived
    }

    const { data, response } = await this.api.PATCH('/api/session/{session_uuid}', {
      params: { path: { session_uuid: ref } },
      body,
    })

    if (data) {
      return Client.mapSessionOptions(data)
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Replays a captured request to a target URL. When `targetUrl` is omitted, the server replays to the session's
   * configured forward URL (and errors if neither is set).
   *
   * @throws {APIError}
   */
  async replayRequest(ref: string, rID: string, targetUrl?: string): Promise<ReplayResult> {
    const { data, response } = await this.api.POST(
      '/api/session/{session_uuid}/requests/{request_uuid}/replay',
      targetUrl
        ? { params: { path: { session_uuid: ref, request_uuid: rID } }, body: { target_url: targetUrl } }
        : { params: { path: { session_uuid: ref, request_uuid: rID } } }
    )

    if (data) {
      return Object.freeze({
        statusCode: data.status_code,
        headers: Array.from(data.headers).map(({ name, value }) => Object.freeze({ name, value })),
        body: base64ToUint8Array(data.body_base64),
      })
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Batch checking the existence of the sessions by their IDs.
   *
   * @throws {APIError}
   */
  async checkSessionExists<T extends string>(...ids: Array<T>): Promise<{ [K in T]: boolean }> {
    const { data, response } = await this.api.POST('/api/session/check/exists', {
      body: ids,
    })

    if (data) {
      // first, create an object with keys from the input array and values as `false`
      const result = Object.fromEntries(ids.map((id) => [id, false])) as { [K in T]: boolean }

      // next, iterate over the response data and set the value to `true` if the ID exists and is `true`
      for (const id in data) {
        if (data[id] === true) {
          result[id as T] = true
        }
      }

      return Object.freeze(result)
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Deletes the session by its ID.
   *
   * @throws {APIError}
   */
  async deleteSession(sID: string): Promise<boolean> {
    const { data, response } = await this.api.DELETE('/api/session/{session_uuid}', {
      params: { path: { session_uuid: sID } },
    })

    if (data) {
      return data.success
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Returns the list of captured requests for the session by its ID.
   *
   * @throws {APIError}
   */
  async getSessionRequests(sID: string): Promise<ReadonlyArray<CapturedRequest>> {
    const { data, response } = await this.api.GET('/api/session/{session_uuid}/requests', {
      params: { path: { session_uuid: sID } },
    })

    if (data) {
      return Object.freeze(
        Array.from(data)
          // convert the list of requests to the immutable objects with the correct types
          .map((req) =>
            Object.freeze({
              uuid: req.uuid,
              clientAddress: req.client_address,
              method: req.method,
              requestPayload: base64ToUint8Array(req.request_payload_base64),
              headers: Object.freeze(Array.from(req.headers).map(({ name, value }) => Object.freeze({ name, value }))),
              url: Object.freeze(new URL(req.url)),
              capturedAt: Object.freeze(new Date(req.captured_at_unix_milli)),
            })
          )
          // sort the list by capturedAt date, to have the latest requests first
          .sort((a, b) => b.capturedAt.getTime() - a.capturedAt.getTime())
      )
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Deletes all captured requests for the session by its ID.
   *
   * @throws {APIError}
   */
  async deleteAllSessionRequests(sID: string): Promise<boolean> {
    const { data, response } = await this.api.DELETE('/api/session/{session_uuid}/requests', {
      params: { path: { session_uuid: sID } },
    })

    if (data) {
      return data.success
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Subscribes to the captured requests for the session by its ID.
   *
   * The promise resolves with a closer function that can be called to close the WebSocket connection.
   */
  async subscribeToSessionRequests(
    sID: string,
    {
      onConnected,
      onUpdate,
      onError,
    }: {
      onConnected?: () => void // called when the WebSocket connection is established
      onUpdate: (request: RequestEvent) => void // called when the update is received
      onError?: (err: Error) => void // called when an error occurs on alive connection
    }
  ): Promise</* closer */ () => void> {
    const protocol = this.baseUrl.protocol === 'https:' ? 'wss:' : 'ws:'
    const path: keyof paths = '/api/session/{session_uuid}/requests/subscribe'

    return new Promise((resolve: (closer: () => void) => void, reject: (err: Error) => void) => {
      let connected: boolean = false

      try {
        const ws = new WebSocket(`${protocol}//${this.baseUrl.host}${path.replace('{session_uuid}', sID)}`)

        ws.onopen = (): void => {
          connected = true
          onConnected?.()
          resolve((): void => ws.close())
        }

        ws.onerror = (event: Event): void => {
          // convert Event to Error
          const err = new Error(event instanceof ErrorEvent ? String(event.error) : 'WebSocket error')

          if (connected) {
            onError?.(err)
          }

          reject(err) // will be ignored if the promise is already resolved
        }

        ws.onmessage = (event): void => {
          if (event.data) {
            const req = JSON.parse(event.data) as components['schemas']['RequestEvent']
            const payload: RequestEvent = {
              action: req.action,
              request: req.request
                ? Object.freeze({
                    uuid: req.request.uuid,
                    clientAddress: req.request.client_address,
                    method: req.request.method,
                    headers: Object.freeze(req.request.headers),
                    url: Object.freeze(new URL(req.request.url)),
                    capturedAt: Object.freeze(new Date(req.request.captured_at_unix_milli)),
                  })
                : null,
            }

            onUpdate(Object.freeze(payload))
          }
        }
      } catch (e) {
        // convert any exception to Error
        const err = e instanceof Error ? e : new Error(String(e))

        if (connected) {
          onError?.(err)
        }

        reject(err)
      }
    })
  }

  /**
   * Returns the captured request by its ID.
   *
   * @throws {APIError}
   */
  async getSessionRequest(sID: string, rID: string): Promise<CapturedRequest> {
    const { data, response } = await this.api.GET('/api/session/{session_uuid}/requests/{request_uuid}', {
      params: { path: { session_uuid: sID, request_uuid: rID } },
    })

    if (data) {
      return Object.freeze({
        uuid: data.uuid,
        clientAddress: data.client_address,
        method: data.method,
        requestPayload: base64ToUint8Array(data.request_payload_base64),
        headers: Object.freeze(Array.from(data.headers)),
        url: Object.freeze(new URL(data.url)),
        capturedAt: Object.freeze(new Date(data.captured_at_unix_milli)),
      })
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }

  /**
   * Deletes the captured request by its ID.
   *
   * @throws {APIError}
   */
  async deleteSessionRequest(sID: string, rID: string): Promise<boolean> {
    const { data, response } = await this.api.DELETE('/api/session/{session_uuid}/requests/{request_uuid}', {
      params: { path: { session_uuid: sID, request_uuid: rID } },
    })

    if (data) {
      return data.success
    }

    throw new APIErrorUnknown({ message: response.statusText, response })
  }
}
