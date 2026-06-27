import { useCallback, useEffect, useRef, useState } from 'react'
import { type FirehoseEvent, FirehoseEventAction } from '~/api'
import { useData } from '~/shared'
import { appendOlderDeduped, createBatcher, mergeNewestDeduped } from './event-buffer'

/** Maximum number of events kept in memory (newest first). Live arrivals beyond this drop the oldest. */
export const MAX_EVENTS = 1_000

/**
 * How often (ms) buffered firehose arrivals are flushed into React state. Batching collapses a high-rate
 * feed into ~4 renders/s instead of one render per message, preventing the render storm (#185) under load.
 */
export const FIREHOSE_FLUSH_INTERVAL_MS = 250

/** Page size for the recent backfill and each older (infinite-scroll) page. */
export const BACKFILL_LIMIT = 50

/** De-dupe key for an event: a request is unique within its session. */
const keyOf = (e: FirehoseEvent): string => `${e.sessionUUID}:${e.request?.uuid ?? ''}`

export type EventStreamFilter = Readonly<{
  /** Restrict to a single session (UUID); null = all sessions. */
  session: string | null
  /** Restrict to a session group (exact); null = all groups. */
  group: string | null
}>

export type EventStreamState = Readonly<{
  /** The captured webhook events, newest first (recent backfill + live arrivals, de-duplicated). */
  events: ReadonlyArray<FirehoseEvent>
  /** True while the live firehose WebSocket is connected. */
  live: boolean
  /** The last connection/backfill error, if any. */
  error: Error | null
  /** True while older events remain to be loaded for the current filter. */
  hasMore: boolean
  /** True while an older page is being fetched. */
  loadingOlder: boolean
  /** True while the initial/refresh backfill for the current filter is loading. */
  loading: boolean
  /** Fetch the next (older) page; safe to call repeatedly (guarded against overlap). */
  loadOlder: () => Promise<void>
}>

/**
 * Drives the unified dashboard event stream for a given filter.
 *
 * On mount and whenever the filter (session/group) changes, it BACKFILLS the most-recent captured
 * requests via `GET /api/events` (newest-first) so the viewer is populated immediately with recent
 * history — not just events captured after the page opened. It then keeps a SINGLE firehose WebSocket
 * open for the component's lifetime and PREPENDS matching live arrivals (de-duplicated by request uuid,
 * batched to one render per flush window). `loadOlder` pages backwards through history via the `before`
 * cursor for infinite scroll.
 *
 * Loop-safety: the firehose subscribe effect runs ONCE (its only dep, `subscribeFirehose`, is stable);
 * the current filter and live predicate are read through refs so it never re-subscribes. A monotonically
 * increasing token ignores backfill/older results that arrive after the filter has changed.
 *
 * @param filter      the active session/group filter (object identity may change every render; only the
 *                    primitive fields drive the backfill effect).
 * @param matchesLive predicate deciding whether a LIVE firehose event belongs to the current filter
 *                    (group membership needs the sessions map, which lives in the caller). Read via a ref,
 *                    so its identity may change freely without re-subscribing.
 */
export const useEventStream = (
  filter: EventStreamFilter,
  matchesLive: (e: FirehoseEvent) => boolean
): EventStreamState => {
  const { subscribeFirehose, getRecentEvents } = useData()

  const [events, setEvents] = useState<ReadonlyArray<FirehoseEvent>>([])
  const [live, setLive] = useState<boolean>(false)
  const [error, setError] = useState<Error | null>(null)
  const [hasMore, setHasMore] = useState<boolean>(false)
  const [loadingOlder, setLoadingOlder] = useState<boolean>(false)
  const [loading, setLoading] = useState<boolean>(true)

  // Keep the live predicate and current filter in refs so the firehose effect can subscribe ONCE and
  // still read fresh values (no re-subscribe loop, no setState during render).
  const matchesLiveRef = useRef(matchesLive)
  matchesLiveRef.current = matchesLive
  const filterRef = useRef<EventStreamFilter>(filter)
  filterRef.current = filter

  // Pagination cursor (seq of the oldest loaded event; 0 = newest page) + overlap guards.
  const cursorRef = useRef<number>(0)
  const loadingOlderRef = useRef<boolean>(false)
  const hasMoreRef = useRef<boolean>(false)
  hasMoreRef.current = hasMore
  // Bumped on every filter change; async results carrying a stale token are dropped.
  const tokenRef = useRef<number>(0)

  // ---- live firehose: subscribe ONCE for the component lifetime ----
  useEffect(() => {
    let cancelled = false
    let closer: (() => void) | null = null

    const batcher = createBatcher<FirehoseEvent>((batch) => {
      const matched = batch.filter((e) => matchesLiveRef.current(e))

      if (matched.length === 0) {
        return
      }

      setEvents((prev) => mergeNewestDeduped(prev, matched, keyOf, MAX_EVENTS))
    }, FIREHOSE_FLUSH_INTERVAL_MS)
    batcher.start()

    subscribeFirehose({
      onConnected: () => {
        if (!cancelled) {
          setLive(true)
          setError(null)
        }
      },
      onEvent: (event) => {
        // the cross-session feed currently emits only `create`; ignore anything else or bodiless events
        if (cancelled || event.action !== FirehoseEventAction.create || !event.request) {
          return
        }

        batcher.push(event) // buffered, NOT a state update — flushed on the next interval tick
      },
      onError: (err) => {
        if (!cancelled) {
          setLive(false)
          setError(err)
        }
      },
    })
      .then((close) => {
        if (cancelled) {
          close()
        } else {
          closer = close
        }
      })
      .catch((err: unknown) => {
        if (!cancelled) {
          setLive(false)
          setError(err instanceof Error ? err : new Error(String(err)))
        }
      })

    return () => {
      cancelled = true
      setLive(false)
      batcher.stop()
      closer?.()
    }
  }, [subscribeFirehose])

  // ---- backfill: (re)load the newest page whenever the filter changes ----
  useEffect(() => {
    const token = ++tokenRef.current
    let cancelled = false

    // reset for the new filter
    cursorRef.current = 0
    setLoading(true)
    setHasMore(false)
    setEvents([])

    void (async () => {
      try {
        const page = await getRecentEvents({
          session: filter.session ?? undefined,
          group: filter.group ?? undefined,
          limit: BACKFILL_LIMIT,
        })

        if (cancelled || tokenRef.current !== token) {
          return // a newer filter superseded this request
        }

        setEvents(page.items)
        cursorRef.current = page.nextBefore
        setHasMore(page.hasMore)
        setError(null)
      } catch (err) {
        if (!cancelled && tokenRef.current === token) {
          setError(err instanceof Error ? err : new Error(String(err)))
        }
      } finally {
        if (!cancelled && tokenRef.current === token) {
          setLoading(false)
        }
      }
    })()

    return () => {
      cancelled = true
    }
  }, [filter.session, filter.group, getRecentEvents])

  // ---- infinite scroll: append the next (older) page via the before cursor ----
  const loadOlder = useCallback(async (): Promise<void> => {
    if (loadingOlderRef.current || !hasMoreRef.current) {
      return
    }

    const token = tokenRef.current
    loadingOlderRef.current = true
    setLoadingOlder(true)

    try {
      const page = await getRecentEvents({
        session: filterRef.current.session ?? undefined,
        group: filterRef.current.group ?? undefined,
        before: cursorRef.current,
        limit: BACKFILL_LIMIT,
      })

      if (tokenRef.current !== token) {
        return // the filter changed mid-flight; drop this stale page
      }

      setEvents((prev) => appendOlderDeduped(prev, page.items, keyOf))
      cursorRef.current = page.nextBefore
      setHasMore(page.hasMore)
    } catch {
      // a failed older-page fetch leaves the list intact; the sentinel retries on the next scroll
    } finally {
      loadingOlderRef.current = false
      setLoadingOlder(false)
    }
  }, [getRecentEvents])

  return { events, live, error, hasMore, loadingOlder, loading, loadOlder }
}
