/**
 * Coalescing buffer for the high-rate firehose feed.
 *
 * A busy server can push ~100 firehose messages/second. Calling `setState` per message storms React
 * (one render each → "message handler took 200ms" jank and, under load, "Maximum update depth exceeded"
 * / error #185). The batcher buffers arrivals and delivers them to `onFlush` at most once per
 * `intervalMs`, so the dashboard does ONE render per flush window instead of one per message.
 */
export type Batcher<T> = Readonly<{
  /** Buffer an item; it is delivered (in arrival order) on the next flush tick. Never flushes synchronously. */
  push: (item: T) => void
  /** Start the periodic flush. Idempotent — calling it again while running is a no-op. */
  start: () => void
  /** Stop the periodic flush and drop any still-buffered items (used on unmount). */
  stop: () => void
}>

/**
 * Create a {@link Batcher} that calls `onFlush` with the items buffered since the previous tick, at most
 * once per `intervalMs`. Items are delivered oldest-first (arrival order); an empty window does NOT call
 * `onFlush`, so idle periods cost zero renders.
 */
export const createBatcher = <T>(onFlush: (batch: ReadonlyArray<T>) => void, intervalMs: number): Batcher<T> => {
  let buffer: Array<T> = []
  let timer: ReturnType<typeof setInterval> | null = null

  const flush = (): void => {
    if (buffer.length === 0) {
      return // nothing arrived this window — skip the render entirely
    }

    const batch = buffer
    buffer = []
    onFlush(batch)
  }

  return Object.freeze({
    push: (item: T): void => {
      buffer.push(item)
    },
    start: (): void => {
      if (timer === null) {
        timer = setInterval(flush, intervalMs)
      }
    },
    stop: (): void => {
      if (timer !== null) {
        clearInterval(timer)
        timer = null
      }

      buffer = []
    },
  })
}

/**
 * Prepend a batch of newly-arrived items (in arrival order, oldest-first) onto an existing newest-first
 * list and cap it to `cap` items, keeping the newest. Pure — used by the firehose flush to merge a
 * buffered batch into the capped, newest-first event list in a single state update.
 */
export const prependCapped = <T>(prev: ReadonlyArray<T>, batchOldestFirst: ReadonlyArray<T>, cap: number): ReadonlyArray<T> => {
  if (batchOldestFirst.length === 0) {
    return prev
  }

  // reverse the batch so its newest arrival lands first, then keep only the `cap` newest items
  const merged = [...batchOldestFirst].reverse().concat(prev)

  return merged.length > cap ? merged.slice(0, cap) : merged
}
