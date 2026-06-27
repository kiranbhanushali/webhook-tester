import { useEffect, useState } from 'react'
import { type FirehoseEvent, FirehoseEventAction } from '~/api'
import { useData } from '~/shared'

/** Maximum number of live firehose events kept in memory (newest first); older ones are dropped. */
export const MAX_FIREHOSE_EVENTS = 200

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

        setEvents((prev) => [event, ...prev].slice(0, MAX_FIREHOSE_EVENTS))
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
      closer?.()
    }
  }, [subscribeFirehose])

  return { events, live, error }
}
