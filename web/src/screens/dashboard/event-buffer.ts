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

/**
 * Merge a batch of newly-arrived items (arrival order, oldest-first) onto an existing newest-first list,
 * DE-DUPLICATING by `keyOf` (an item already present is dropped) and capping the result to `cap` (keeping
 * the newest). Pure — used by the unified dashboard stream to prepend live firehose events without
 * duplicating rows already shown from the recent backfill (a request can arrive on both paths).
 */
export const mergeNewestDeduped = <T>(
  prev: ReadonlyArray<T>,
  batchOldestFirst: ReadonlyArray<T>,
  keyOf: (item: T) => string,
  cap: number
): ReadonlyArray<T> => {
  if (batchOldestFirst.length === 0) {
    return prev
  }

  const seen = new Set(prev.map(keyOf))
  const fresh: Array<T> = []

  // walk newest-first so the most recent arrival ends up at the head of `fresh`
  for (let i = batchOldestFirst.length - 1; i >= 0; i--) {
    const k = keyOf(batchOldestFirst[i])

    if (!seen.has(k)) {
      seen.add(k)
      fresh.push(batchOldestFirst[i])
    }
  }

  if (fresh.length === 0) {
    return prev
  }

  const merged = fresh.concat(prev)

  return merged.length > cap ? merged.slice(0, cap) : merged
}

/**
 * Append an older page (already newest-first) onto the END of an existing newest-first list, dropping any
 * items already present (de-duplicated by `keyOf`). Pure — backs the dashboard's infinite-scroll into
 * older history. No cap: paging older is user-driven.
 */
export const appendOlderDeduped = <T>(
  prev: ReadonlyArray<T>,
  olderNewestFirst: ReadonlyArray<T>,
  keyOf: (item: T) => string
): ReadonlyArray<T> => {
  if (olderNewestFirst.length === 0) {
    return prev
  }

  const seen = new Set(prev.map(keyOf))
  const fresh = olderNewestFirst.filter((item) => !seen.has(keyOf(item)))

  return fresh.length === 0 ? prev : prev.concat(fresh)
}
