import { useEffect, useState } from 'react'
import { type FirehoseEvent, FirehoseEventAction } from '~/api'
import { useData } from '~/shared'
import { createBatcher, prependCapped } from './event-buffer'

/** Maximum number of live firehose events kept in memory (newest first); older ones are dropped. */
export const MAX_FIREHOSE_EVENTS = 200

/**
 * How often (ms) buffered firehose arrivals are flushed into React state. A busy server emits ~100
 * messages/s; flushing on this cadence collapses that into ~4 renders/s instead of ~100 (one per
 * message), which prevents the render storm / "Maximum update depth exceeded" (#185) under load.
 */
export const FIREHOSE_FLUSH_INTERVAL_MS = 250

export type FirehoseState = Readonly<{
  /** The captured webhook events, newest first, capped at {@link MAX_FIREHOSE_EVENTS}. */
  events: ReadonlyArray<FirehoseEvent>
  /** True while the firehose WebSocket is connected. */
  live: boolean
  /** The last connection error, if any (v1 uses a single connection without auto-reconnect). */
  error: Error | null
}>

/**
 * Subscribes to the global cross-session firehose for the lifetime of the calling component and exposes
 * a capped, newest-first list of captured webhook events plus the connection's live/error state.
 *
 * Incoming events are BATCHED: each message is buffered (no per-message render) and flushed into state
 * on a fixed interval (see {@link FIREHOSE_FLUSH_INTERVAL_MS}), so a high-rate feed produces one render
 * per flush window instead of one per message. The subscribe effect runs ONCE (its only dependency,
 * `subscribeFirehose`, is stable) — there is no re-subscribe loop and no setState during render.
 *
 * v1 uses a SINGLE connection: if the socket drops, `live` flips to false and `error` is set (no
 * automatic reconnect). This keeps the daily-use dashboard simple and predictable.
 */
export const useFirehose = (): FirehoseState => {
  const { subscribeFirehose } = useData()
  const [events, setEvents] = useState<ReadonlyArray<FirehoseEvent>>([])
  const [live, setLive] = useState<boolean>(false)
  const [error, setError] = useState<Error | null>(null)

  useEffect(() => {
    let cancelled = false
    let closer: (() => void) | null = null

    // buffer arrivals and flush them in batches: one render per window, not one per message
    const batcher = createBatcher<FirehoseEvent>((batch) => {
      setEvents((prev) => prependCapped(prev, batch, MAX_FIREHOSE_EVENTS))
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
          close() // the component unmounted before the socket finished opening
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

  return { events, live, error }
}
