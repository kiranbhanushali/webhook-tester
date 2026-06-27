import React, { createContext, useCallback, useContext, useEffect, useRef, useState } from 'react'
import {
  APIErrorNotFound,
  type Client,
  type FirehoseEvent,
  RequestEventAction,
  type ReplayResult,
  type SearchResultItem,
  type SessionPatch,
  type SessionSummary,
} from '~/api'
import { Database, type Session as DbSession } from '~/db'
import { UsedStorageKeys, useSettings, useStorage } from '~/shared'
import { buildWebhookUrl } from '~/shared/utils/webhook-url'

export type Session = {
  /** Internal, stable identifier — the session UUID. Used for all API/WebSocket calls and DB keying. */
  sID: string
  responseCode: number
  responseHeaders: Array<{ name: string; value: string }>
  responseDelay: number
  responseBody: Uint8Array
  /** Human-readable, user-facing slug (null if the server did not assign one). */
  slug: string | null
  group: string | null
  responseScript: string | null
  securityHeaders: Array<{ name: string; value: string }>
  forwardUrl: string | null
  longLived: boolean
  /** Header name callers must include for auth; null = public endpoint. */
  inboundAuthHeader: string | null
  /** Expected secret value for the auth header; null = not set. */
  inboundAuthValue: string | null
}

export type Request = {
  rID: string
  clientAddress: string // IPv4 or IPv6
  method: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE' | 'HEAD' | 'OPTIONS' | 'CONNECT' | 'TRACE' | string
  headers: Array<{ name: string; value: string }>
  url: URL
  get payload(): Promise<Uint8Array | null> | null // the payload is lazy-loaded to avoid memory overuse
  capturedAt: Date
  /** False when the request was rejected by inbound auth. Undefined for WS-received events (schema omits it). */
  authorized?: boolean
}

export type SessionEvents = {
  onNewRequest: (r: Omit<Request, 'payload'>) => void // server does not send the payload
  onRequestDelete: (r: Omit<Request, 'payload'>) => void // server does not send the payload
  onRequestsClear: () => void
  onError: (err: Error | unknown) => void
}

type DataContext = {
  /** The last used session ID (updates every time a session is switched) */
  readonly lastUsedSID: string | null

  /** Create a new session */
  newSession({
    statusCode,
    headers,
    delay,
    responseBody,
    slug,
    group,
    responseScript,
    securityHeaders,
    forwardUrl,
    longLived,
    inboundAuthHeader,
    inboundAuthValue,
  }: {
    statusCode?: number
    headers?: Record<string, string>
    delay?: number
    responseBody?: Uint8Array
    slug?: string
    group?: string
    responseScript?: string
    securityHeaders?: Array<{ name: string; value: string }>
    forwardUrl?: string
    longLived?: boolean
    inboundAuthHeader?: string
    inboundAuthValue?: string
  }): Promise<Readonly<Session>>

  /**
   * Switch to a session with the given ID.
   *
   * NOTE: The first promise resolves when the session and requests are loaded from the database (FAST), and the
   * second one resolves when the session and requests are loaded from the server (SLOW).
   */
  switchToSession(sID: string, listeners?: Partial<SessionEvents>): Promise<() => Promise<void>>

  /** Current active session */
  readonly session: Readonly<Session> | null

  /** The list of all session IDs, available to the user */
  readonly allSessionIDs: ReadonlyArray<string>

  /**
   * Destroy a session with the given ID.
   *
   * NOTE: The first promise resolves when the session is removed from the database (FAST), and the second one
   * resolves when the session is removed from the server (SLOW).
   */
  destroySession(sID: string): Promise<() => Promise<void>>

  /** Current active request */
  readonly request: Readonly<Request> | null

  /** The list of requests for the current session, ordered by the captured time (from newest to oldest) */
  readonly requests: ReadonlyArray<Request>

  /**
   * Switch to a request with the given session and request ID.
   *
   * NOTE: The first promise resolves when the request is loaded from the database (FAST), and the second one
   * resolves when the request is loaded from the server (SLOW).
   */
  switchToRequest(sID: string, rID: string | null): Promise<() => Promise<void>>

  /**
   * Remove a request with the given session and request ID.
   *
   * NOTE: The first promise resolves when the request is removed from the database (FAST), and the second one
   * resolves when the request is removed from the server (SLOW).
   */
  removeRequest(sID: string, rID: string, andFromServer?: boolean): Promise<() => Promise<void>>

  /**
   * Remove all requests for the session with the given ID.
   *
   * NOTE: The first promise resolves when the requests are removed from the database (FAST), and the second one
   * resolves when the requests are removed from the server (SLOW).
   */
  removeAllRequests(sID: string, andFromServer?: boolean): Promise<() => Promise<void>>

  /** Limit the number of requests by removing the oldest ones, if the count exceeds the limit */
  setRequestsCount(limit: number): void

  /** List all sessions available on the server (optionally filtered by group and/or a free-text query). */
  listAllSessions(params?: { group?: string; q?: string }): Promise<ReadonlyArray<SessionSummary>>

  /** Search captured-request identifiers by key/value across sessions. */
  searchIdentifiers(params: {
    value: string
    key?: string
    match?: 'exact' | 'prefix'
    group?: string
    session?: string // session reference (uuid or slug)
    from?: number // unix milliseconds
    to?: number // unix milliseconds
    limit?: number
  }): Promise<ReadonlyArray<SearchResultItem>>

  /**
   * Update session options by reference (uuid or slug). On success the change is reflected in the local database and,
   * if it is the current session, in the live session state.
   */
  updateSession(ref: string, patch: SessionPatch): Promise<Readonly<Session>>

  /** Replay a captured request to a target URL (defaults to the session's forward URL when omitted). */
  replayRequest(ref: string, rID: string, targetUrl?: string): Promise<ReplayResult>

  /**
   * Subscribe to the GLOBAL cross-session firehose: a single live stream of every captured webhook across
   * ALL sessions (for the dashboard). Resolves with a closer function to terminate the WebSocket.
   */
  subscribeFirehose(listeners: {
    onConnected?: () => void
    onEvent: (event: FirehoseEvent) => void
    onError?: (err: Error) => void
  }): Promise<() => void>

  /** The URL for the webhook (if session is active) */
  readonly webHookUrl: Readonly<URL> | null
}

const notInitialized = (): never => {
  throw new Error('DataProvider is not initialized')
}

const dataContext = createContext<DataContext>({
  lastUsedSID: null,
  newSession: () => notInitialized(),
  switchToSession: () => notInitialized(),
  session: null,
  allSessionIDs: [],
  destroySession: () => notInitialized(),
  request: null,
  requests: [],
  switchToRequest: () => notInitialized(),
  removeRequest: () => notInitialized(),
  removeAllRequests: () => notInitialized(),
  setRequestsCount: () => notInitialized(),
  listAllSessions: () => notInitialized(),
  searchIdentifiers: () => notInitialized(),
  updateSession: () => notInitialized(),
  replayRequest: () => notInitialized(),
  subscribeFirehose: () => notInitialized(),
  webHookUrl: null,
})

/** Sort requests by the captured time (from newest to oldest) */
const requestsSorter = <T extends { capturedAt: Date }>(a: T, b: T) => b.capturedAt.getTime() - a.capturedAt.getTime()

/** Build the immutable in-memory session state from a stored (database) session, coalescing the optional new fields. */
const dbSessionToState = (s: DbSession): Readonly<Session> =>
  Object.freeze({
    sID: s.sID,
    responseCode: s.responseCode,
    responseDelay: s.responseDelay,
    responseHeaders: s.responseHeaders,
    responseBody: s.responseBody,
    slug: s.slug ?? null,
    group: s.group ?? null,
    responseScript: s.responseScript ?? null,
    securityHeaders: s.securityHeaders ?? [],
    forwardUrl: s.forwardUrl ?? null,
    longLived: s.longLived ?? false,
    inboundAuthHeader: s.inboundAuthHeader ?? null,
    inboundAuthValue: s.inboundAuthValue ?? null,
  })

/** Helper function to get the request payload from the database (lazy-loaded) */
const payloadGetter = (db: Database, rID: string): { payload: Request['payload'] } => {
  return {
    get payload() {
      return new Promise<Uint8Array | null>((resolve, reject) => {
        db.getRequest(rID)
          .then((r) => {
            if (r) {
              resolve(r.payload)
            } else {
              reject(new Error('Request not found in the database'))
            }
          })
          .catch(reject)
      })
    },
  }
}

/**
 * DataProvider is a context provider that manages application data.
 *
 * Think of it as the **core** of the business logic, handling all data and key methods related to sessions and requests.
 */
export const DataProvider: React.FC<{
  api: Client
  db: Database
  errHandler: (err: Error | unknown) => void // error handler for non-critical errors
  children: React.JSX.Element
}> = ({ api, db, errHandler, children }) => {
  const { publicUrlRoot } = useSettings()
  const [lastUsedSID, setLastUsedSID] = useStorage<string | null>(null, UsedStorageKeys.SessionsLastUsed, 'local')
  const [session, setSession] = useState<Readonly<Session> | null>(null)
  const [allSessionIDs, setAllSessionIDs] = useState<ReadonlyArray<string>>([])
  const [request, setRequest] = useState<Readonly<Request> | null>(null)
  const [requests, setRequests] = useState<ReadonlyArray<Request>>([])
  const [webHookUrl, setWebHookUrl] = useState<URL | null>(null)

  // the subscription closer function (if not null, it means the subscription is active)
  const closeSubRef = useRef<(() => void) | null>(null)

  /** Unsubscribe from the session requests on the server */
  const unsubscribe = (): void => {
    if (closeSubRef.current) {
      closeSubRef.current()
    }

    closeSubRef.current = null
  }

  /** Subscribe to the session requests on the server */
  const subscribeToRequestEvents = useCallback(
    async (sID: string, listeners?: Partial<SessionEvents>) => {
      // terminate the previous subscription, if any
      unsubscribe()

      // subscribe to the session requests on the server
      closeSubRef.current = await api.subscribeToSessionRequests(sID, {
        onUpdate: async (requestEvent): Promise<void> => {
          try {
            switch (requestEvent.action) {
              // a new request was captured
              case RequestEventAction.create: {
                const req = requestEvent.request

                if (req) {
                  // save the request to the database (without payload)
                  await db.putRequest({
                    sID,
                    rID: req.uuid,
                    method: req.method,
                    clientAddress: req.clientAddress,
                    url: req.url.toString(),
                    payload: null, // server does not send the payload
                    capturedAt: req.capturedAt,
                    headers: [...req.headers],
                    authorized: req.authorized,
                  })

                  // append the new request in front of the list (update the state)
                  setRequests((prev) => [
                    Object.freeze({
                      ...payloadGetter(db, req.uuid),
                      rID: req.uuid,
                      clientAddress: req.clientAddress,
                      method: req.method,
                      headers: [...req.headers],
                      url: req.url,
                      capturedAt: req.capturedAt,
                      authorized: req.authorized,
                    }),
                    ...prev,
                  ])

                  // invoke the listener callback
                  listeners?.onNewRequest?.(
                    Object.freeze({
                      rID: req.uuid,
                      clientAddress: req.clientAddress,
                      method: req.method,
                      headers: [...req.headers],
                      url: req.url,
                      capturedAt: req.capturedAt,
                      authorized: req.authorized,
                    })
                  )
                }

                break
              }

              // a request was deleted
              case RequestEventAction.delete: {
                const req = requestEvent.request

                if (req) {
                  // remove the request from the list
                  setRequests((prev) => prev.filter((r) => r.rID !== req.uuid))

                  // invoke the listener callback
                  listeners?.onRequestDelete?.(
                    Object.freeze({
                      rID: req.uuid,
                      clientAddress: req.clientAddress,
                      method: req.method,
                      headers: [...req.headers],
                      url: req.url,
                      capturedAt: req.capturedAt,
                    })
                  )

                  // remove the request from the database
                  await db.deleteRequest(req.uuid)
                }

                break
              }

              // all requests were cleared
              case RequestEventAction.clear: {
                // clear the requests list
                setRequests(Object.freeze([]))

                // invoke the listener callback
                listeners?.onRequestsClear?.()

                // clear the requests from the database
                await db.deleteAllRequests(sID)

                break
              }
            }
          } catch (err) {
            if (listeners?.onError) {
              listeners.onError(err)
            } else {
              throw err
            }
          }
        },
        onError: (err) => {
          if (listeners?.onError) {
            listeners.onError(err)
          } else {
            throw err
          }
        },
      })
    },
    [api, db]
  )

  /** Create a new session */
  const newSession = useCallback(
    async ({
      statusCode = 200, // default session options
      headers = {},
      delay = 0,
      responseBody = new Uint8Array(),
      slug,
      group,
      responseScript,
      securityHeaders,
      forwardUrl,
      longLived,
      inboundAuthHeader,
      inboundAuthValue,
    }: {
      statusCode?: number
      headers?: Record<string, string>
      delay?: number
      responseBody?: Uint8Array
      slug?: string
      group?: string
      responseScript?: string
      securityHeaders?: Array<{ name: string; value: string }>
      forwardUrl?: string
      longLived?: boolean
      inboundAuthHeader?: string
      inboundAuthValue?: string
    }): Promise<Readonly<Session>> => {
      // save the session to the server
      const opts = await api.newSession({
        statusCode,
        headers,
        delay,
        responseBody,
        slug,
        group,
        responseScript,
        securityHeaders,
        forwardUrl,
        longLived,
        inboundAuthHeader,
        inboundAuthValue,
      })

      // add the session ID to the list of all session IDs (update the state)
      setAllSessionIDs((prev) => [...prev, opts.uuid])

      // build the database record, carrying the new slug-aware fields from the server response
      const dbRecord: DbSession = {
        sID: opts.uuid,
        responseCode: statusCode,
        responseDelay: delay,
        responseHeaders: Object.entries(headers).map(([name, value]) => ({ name, value })),
        responseBody,
        createdAt: opts.createdAt,
        slug: opts.response.slug || undefined,
        group: opts.response.group,
        responseScript: opts.response.responseScript,
        securityHeaders: [...opts.response.securityHeaders],
        forwardUrl: opts.response.forwardUrl,
        longLived: opts.response.longLived,
        inboundAuthHeader: opts.response.inboundAuthHeader ?? undefined,
        inboundAuthValue: opts.response.inboundAuthValue ?? undefined,
      }

      // save the session to the database
      await db.putSession(dbRecord)

      return dbSessionToState(dbRecord)
    },
    [api, db]
  )

  /**
   * Load the requests for the session with the given ID.
   *
   * This action will reset the requests list and update it with the new data.
   *
   * NOTE: The first promise resolves when the requests are loaded from the database (FAST), and the second one
   * resolves when the requests are loaded from the server (SLOW).
   */
  const loadRequests = useCallback(
    async (sID: string): Promise<() => Promise<void>> => {
      // load requests for the session from the database (fast)
      const dbList = await db.getSessionRequests(sID)

      // update the requests list (first state update, to show the data from the database)
      setRequests(
        dbList
          .map((r) =>
            Object.freeze({
              ...payloadGetter(db, r.rID),
              rID: r.rID,
              clientAddress: r.clientAddress,
              method: r.method,
              headers: [...r.headers],
              url: new URL(r.url),
              capturedAt: r.capturedAt,
              authorized: r.authorized,
            })
          )
          .sort(requestsSorter)
      )

      // return a function that loads requests from the server (slow)
      return async () => {
        const reqs = await api.getSessionRequests(sID)

        // update the requests list (second state update, to show the fresh data)
        setRequests(
          reqs
            .map((r) =>
              Object.freeze({
                ...payloadGetter(db, r.uuid),
                rID: r.uuid,
                clientAddress: r.clientAddress,
                method: r.method,
                headers: [...r.headers],
                url: r.url,
                capturedAt: r.capturedAt,
                authorized: r.authorized,
              })
            )
            .sort(requestsSorter)
        )

        // update the requests in the database (for future use)
        await db.putRequest(
          ...reqs.map((r) => ({
            sID: sID,
            rID: r.uuid,
            method: r.method,
            clientAddress: r.clientAddress,
            url: r.url.toString(),
            capturedAt: r.capturedAt,
            headers: [...r.headers],
            payload: r.requestPayload,
            authorized: r.authorized,
          }))
        )

        // find requests that are not present in the server response but are in the database
        const toRemove = dbList.filter((r) => !reqs.find((req) => req.uuid === r.rID)).map((r) => r.rID)

        if (toRemove.length) {
          await db.deleteRequest(...toRemove)
        }
      }
    },
    [db, api]
  )

  /**
   * Switch to a session with the given ID.
   *
   * NOTE: The first promise resolves when the session and requests are loaded from the database (FAST), and the
   * second one resolves when the session and requests are loaded from the server (SLOW).
   */
  const switchToSession = useCallback(
    async (sID: string, listeners?: Partial<SessionEvents>) => {
      try {
        // try to find out if the session exists in the database
        const dbSession = await db.getSession(sID)

        if (dbSession) {
          // if the session exists in the database
          setSession(dbSessionToState(dbSession))

          setLastUsedSID(dbSession.sID)

          // load requests for the session
          const requestsSlow = await loadRequests(dbSession.sID)

          // return a function that resolves the second (slow) promise
          return async () => {
            try {
              await requestsSlow()
              await subscribeToRequestEvents(dbSession.sID, listeners)
            } catch (err) {
              unsubscribe() // unsubscribe from the session requests if something went wrong
              setLastUsedSID(null) // unset the last used session ID
              setSession(null) // clear the session state

              throw err // reject the second (slow) promise
            }
          }
        } else {
          // otherwise, load the session from the server
          return async () => {
            try {
              const apiSession = await api.getSession(sID)

              // build the database record, carrying the new slug-aware fields from the server response
              const dbRecord: DbSession = {
                sID: apiSession.uuid,
                responseCode: apiSession.response.statusCode,
                responseDelay: apiSession.response.delay,
                responseHeaders: [...apiSession.response.headers],
                responseBody: apiSession.response.body,
                createdAt: apiSession.createdAt,
                slug: apiSession.response.slug || undefined,
                group: apiSession.response.group,
                responseScript: apiSession.response.responseScript,
                securityHeaders: [...apiSession.response.securityHeaders],
                forwardUrl: apiSession.response.forwardUrl,
                longLived: apiSession.response.longLived,
              }

              // save the session to the database
              await db.putSession(dbRecord)

              setAllSessionIDs((prev) => [...prev, apiSession.uuid])

              setSession(dbSessionToState(dbRecord))

              setLastUsedSID(apiSession.uuid)

              // load requests for the session
              const requestsSlow = await loadRequests(apiSession.uuid)

              // load requests from the server and subscribe to the session requests
              await requestsSlow()
              await subscribeToRequestEvents(apiSession.uuid, listeners)
            } catch (err) {
              unsubscribe() // unsubscribe from the session requests if something went wrong
              setLastUsedSID(null) // unset the last used session ID
              setSession(null) // clear the session state

              throw err // reject the second (slow) promise
            }
          }
        }
      } catch (err) {
        setLastUsedSID(null) // unset the last used session ID
        setSession(null) // clear the session state

        throw err // reject the first (fast) promise
      }
    },
    [api, db, loadRequests, subscribeToRequestEvents, setLastUsedSID]
  )

  /**
   * Destroy a session with the given ID.
   *
   * NOTE: The first promise resolves when the session is removed from the database (FAST), and the second one
   * resolves when the session is removed from the server (SLOW).
   */
  const destroySession = useCallback(
    async (sID: string): Promise<() => Promise<void>> => {
      // remove the session from the database first (fast)
      await db.deleteSession(sID)

      // update the session list state
      setAllSessionIDs((prev) => prev.filter((id) => id !== sID))

      // return a function to remove the session from the server (slow)
      return async () => {
        const ok = await api.deleteSession(sID)

        if (!ok) {
          throw new Error('Failed to delete the session on the server')
        }
      }
    },
    [api, db]
  )

  /**
   * Switch to a request with the given session and request ID.
   *
   * NOTE: The first promise resolves when the request is loaded from the database (FAST), and the second one
   * resolves when the request is loaded from the server (SLOW).
   */
  const switchToRequest = useCallback(
    async (sID: string, rID: string | null): Promise<() => Promise<void>> => {
      if (!rID) {
        setRequest(null)

        return async () => Promise.resolve()
      }

      // TODO: remove request from the database if API returns 404

      // try to get the request from the database (fast)
      const req = await db.getRequest(rID)

      if (req) {
        // if the request exists in the database
        setRequest(
          Object.freeze({
            rID: req.rID,
            clientAddress: req.clientAddress,
            method: req.method,
            headers: [...req.headers],
            url: new URL(req.url),
            capturedAt: req.capturedAt,
            authorized: req.authorized,
            get payload() {
              return Promise.resolve(req.payload)
            },
          })
        )

        // if the payload is already present
        if (req.payload !== null) {
          return async () => Promise.resolve()
        }

        // if the payload is not loaded, get it from the server (slow)
        return async () => {
          try {
            const serverReq = await api.getSessionRequest(sID, rID)

            // update the state with the actual data from the server including the payload
            setRequest((prev) =>
              prev
                ? Object.freeze({
                    rID: rID,
                    clientAddress: serverReq.clientAddress,
                    method: serverReq.method,
                    headers: [...serverReq.headers],
                    url: new URL(serverReq.url),
                    capturedAt: serverReq.capturedAt,
                    authorized: serverReq.authorized,
                    get payload() {
                      return Promise.resolve(serverReq.requestPayload)
                    },
                  })
                : prev
            )

            await db.putRequest({
              sID,
              rID: serverReq.uuid,
              method: serverReq.method,
              clientAddress: serverReq.clientAddress,
              url: serverReq.url.toString(),
              capturedAt: serverReq.capturedAt,
              headers: [...serverReq.headers],
              payload: serverReq.requestPayload,
              authorized: serverReq.authorized,
            })
          } catch (err) {
            // if the request is not found on the server
            if (err instanceof APIErrorNotFound) {
              // remove it from the database
              await db.deleteRequest(rID)

              // update the requests list (update the state)
              setRequests((prev) => prev.filter((r) => r.rID !== rID).sort(requestsSorter))

              // clear the request state
              setRequest(null)
            } else {
              throw err
            }
          }
        }
      } else {
        // if the request is not in the database, load it from the server (slow)
        return async () => {
          const serverReq = await api.getSessionRequest(sID, rID)

          setRequest(
            Object.freeze({
              rID: serverReq.uuid,
              clientAddress: serverReq.clientAddress,
              method: serverReq.method,
              headers: [...serverReq.headers],
              url: serverReq.url,
              capturedAt: serverReq.capturedAt,
              authorized: serverReq.authorized,
              get payload() {
                return Promise.resolve(serverReq.requestPayload)
              },
            })
          )

          await db.putRequest({
            sID,
            rID: serverReq.uuid,
            method: serverReq.method,
            clientAddress: serverReq.clientAddress,
            url: serverReq.url.toString(),
            capturedAt: serverReq.capturedAt,
            headers: [...serverReq.headers],
            payload: serverReq.requestPayload,
            authorized: serverReq.authorized,
          })
        }
      }
    },
    [api, db]
  )

  /**
   * Remove a request with the given session and request ID.
   *
   * NOTE: The first promise resolves when the request is removed from the database (FAST), and the second one
   * resolves when the request is removed from the server (SLOW).
   */
  const removeRequest = useCallback(
    async (sID: string, rID: string, andFromServer: boolean = true): Promise<() => Promise<void>> => {
      // remove the request from the database (fast)
      await db.deleteRequest(rID)

      // update the requests list (update the state)
      setRequests((prev) => prev.filter((r) => r.rID !== rID).sort(requestsSorter))

      // skip the slow operation if we don't need to remove the request from the server
      if (!andFromServer) {
        return async () => Promise.resolve()
      }

      // return a function to remove from the server (slow)
      return async () => {
        const ok = await api.deleteSessionRequest(sID, rID)

        if (!ok) {
          throw new Error('Failed to delete the request for the session on the server')
        }
      }
    },
    [api, db]
  )

  /**
   * Remove all requests for the session with the given ID.
   *
   * NOTE: The first operation resolves when the requests are removed from the database (FAST), and the second one
   * resolves when the requests are removed from the server (SLOW).
   */
  const removeAllRequests = useCallback(
    async (sID: string, andFromServer: boolean = true): Promise<() => Promise<void>> => {
      // remove all requests from the database
      await db.deleteAllRequests(sID)

      // clear the requests list (update the state)
      setRequests(Object.freeze([]))

      // skip the slow operation if we don't need to remove the request from the server
      if (!andFromServer) {
        return async () => Promise.resolve()
      }

      // return the function that removes requests from the server
      return async (): Promise<void> => {
        const ok = await api.deleteAllSessionRequests(sID)

        if (!ok) {
          throw new Error('Failed to delete all requests for the session on the server')
        }
      }
    },
    [api, db]
  )

  /** Limit the number of requests by removing the oldest ones, if the count exceeds the limit */
  const setRequestsCount = useCallback((limit: number) => {
    setRequests((prev) => prev.slice(0, limit))
  }, [])

  /** List all sessions available on the server. */
  const listAllSessions = useCallback<DataContext['listAllSessions']>(
    (params) => api.listSessions(params),
    [api]
  )

  /** Search captured-request identifiers by key/value across sessions. */
  const searchIdentifiers = useCallback<DataContext['searchIdentifiers']>(
    (params) => api.searchIdentifiers(params),
    [api]
  )

  /** Update session options by reference (uuid or slug); reflects the change in the DB and the live session state. */
  const updateSession = useCallback<DataContext['updateSession']>(
    async (ref, patch) => {
      const opts = await api.updateSession(ref, patch)

      // preserve the original creation time if we already know this session locally
      const existing = await db.getSession(opts.uuid)

      const dbRecord: DbSession = {
        sID: opts.uuid,
        responseCode: opts.response.statusCode,
        responseDelay: opts.response.delay,
        responseHeaders: [...opts.response.headers],
        responseBody: opts.response.body,
        createdAt: existing?.createdAt ?? opts.createdAt,
        slug: opts.response.slug || undefined,
        group: opts.response.group,
        responseScript: opts.response.responseScript,
        securityHeaders: [...opts.response.securityHeaders],
        forwardUrl: opts.response.forwardUrl,
        longLived: opts.response.longLived,
        inboundAuthHeader: opts.response.inboundAuthHeader ?? undefined,
        inboundAuthValue: opts.response.inboundAuthValue ?? undefined,
      }

      await db.putSession(dbRecord)

      const next = dbSessionToState(dbRecord)

      // if it is the current session, update the live state so the webhook URL and editors reflect the change
      setSession((prev) => (prev && prev.sID === opts.uuid ? next : prev))

      return next
    },
    [api, db]
  )

  /** Replay a captured request to a target URL (defaults to the session forward URL when omitted). */
  const replayRequest = useCallback<DataContext['replayRequest']>(
    (ref, rID, targetUrl) => api.replayRequest(ref, rID, targetUrl),
    [api]
  )

  /** Subscribe to the global cross-session firehose (all captured webhooks). */
  const subscribeFirehose = useCallback<DataContext['subscribeFirehose']>(
    (listeners) => api.subscribeFirehose(listeners),
    [api]
  )

  // on provider mount
  useEffect(() => {
    // load all session IDs from the database
    db.getSessionIDs()
      .then((dbSessionIDs) => {
        // set the initial list of session IDs (fast)
        setAllSessionIDs(dbSessionIDs)

        if (dbSessionIDs.length) {
          // if we have any session IDs, check the sessions existence on the server to invalidate the ones that do not
          api
            .checkSessionExists(...dbSessionIDs)
            .then((checkResult) => {
              // filter out the IDs that do not exist on the server
              const toRemove = dbSessionIDs.filter((id) => !checkResult[id])

              // if we have any IDs to remove
              if (toRemove.length) {
                // if all sessions from the database are to be removed (we have no any sessions left)
                if (dbSessionIDs.filter((id) => !toRemove.includes(id)).length === 0) {
                  // clear the state
                  setSession(null)
                  setRequest(null)
                  setLastUsedSID(null)
                }

                // cleanup the database
                db.deleteSession(...toRemove)
                  // update the list of session IDs (slow)
                  .then(() => setAllSessionIDs((prev) => prev.filter((id) => !toRemove.includes(id))))
                  .catch(errHandler)
              }
            })
            .catch(errHandler)
        }
      })
      .catch(errHandler)
  }, [api, db, errHandler, setLastUsedSID])

  // watch for the session changes and update the webhook URL
  useEffect(() => {
    if (session) {
      // the user-facing webhook URL lives under the reserved /w/ prefix and uses the slug; it falls back to the uuid
      // (which the /w/ capture endpoint also accepts) so sessions created before slugs existed keep working.
      setWebHookUrl(Object.freeze(buildWebhookUrl(publicUrlRoot, session.slug || session.sID)))
    }
  }, [session, publicUrlRoot])

  return (
    <dataContext.Provider
      value={{
        lastUsedSID,
        newSession,
        switchToSession,
        session,
        allSessionIDs,
        destroySession,
        request,
        requests,
        switchToRequest,
        removeRequest,
        removeAllRequests,
        listAllSessions,
        searchIdentifiers,
        updateSession,
        replayRequest,
        subscribeFirehose,
        webHookUrl,
        setRequestsCount,
      }}
    >
      {children}
    </dataContext.Provider>
  )
}

export function useData(): DataContext {
  const ctx = useContext(dataContext)

  if (!ctx) {
    throw new Error('useData must be used within a DataProvider')
  }

  return ctx
}
