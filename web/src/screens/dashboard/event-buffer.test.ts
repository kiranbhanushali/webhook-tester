import { describe, test, expect, vi, beforeEach, afterEach } from 'vitest'
import { createBatcher, prependCapped } from './event-buffer'

describe('prependCapped', () => {
  test('prepends an arrival-order batch newest-first onto an existing newest-first list', () => {
    // prev is newest-first: [b1 newest, a1]; batch arrived oldest-first: [c1, c2]
    expect(prependCapped(['b1', 'a1'], ['c1', 'c2'], 10)).toEqual(['c2', 'c1', 'b1', 'a1'])
  })

  test('returns the same reference and does no work for an empty batch', () => {
    const prev = ['x']
    expect(prependCapped(prev, [], 10)).toBe(prev)
  })

  test('caps to the newest `cap` items, dropping the oldest', () => {
    const prev = ['p1', 'p2', 'p3']
    // batch newest is "n2"; cap=3 keeps the 3 newest overall
    expect(prependCapped(prev, ['n1', 'n2'], 3)).toEqual(['n2', 'n1', 'p1'])
  })
})

describe('createBatcher', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  test('coalesces many pushes within one window into a SINGLE flush (bounded updates)', () => {
    const onFlush = vi.fn<(batch: ReadonlyArray<number>) => void>()
    const batcher = createBatcher(onFlush, 250)
    batcher.start()

    // 100 arrivals inside one flush window must NOT flush per push
    for (let i = 0; i < 100; i++) {
      batcher.push(i)
    }
    expect(onFlush).not.toHaveBeenCalled()

    // one tick → exactly one flush carrying all 100, in arrival order
    vi.advanceTimersByTime(250)
    expect(onFlush).toHaveBeenCalledTimes(1)
    expect(onFlush.mock.calls[0][0]).toHaveLength(100)
    expect(onFlush.mock.calls[0][0][0]).toBe(0)
    expect(onFlush.mock.calls[0][0][99]).toBe(99)

    batcher.stop()
  })

  test('an idle window does not flush (zero renders when nothing arrives)', () => {
    const onFlush = vi.fn()
    const batcher = createBatcher(onFlush, 250)
    batcher.start()

    vi.advanceTimersByTime(1000) // four idle windows
    expect(onFlush).not.toHaveBeenCalled()

    batcher.stop()
  })

  test('flushes once per window across multiple windows', () => {
    const onFlush = vi.fn()
    const batcher = createBatcher<number>(onFlush, 250)
    batcher.start()

    batcher.push(1)
    batcher.push(2)
    vi.advanceTimersByTime(250)
    batcher.push(3)
    vi.advanceTimersByTime(250)

    expect(onFlush).toHaveBeenCalledTimes(2)
    expect(onFlush.mock.calls[0][0]).toEqual([1, 2])
    expect(onFlush.mock.calls[1][0]).toEqual([3])

    batcher.stop()
  })

  test('stop() halts flushing and drops buffered items', () => {
    const onFlush = vi.fn()
    const batcher = createBatcher<number>(onFlush, 250)
    batcher.start()

    batcher.push(1)
    batcher.stop() // drops the buffered item and clears the interval

    vi.advanceTimersByTime(1000)
    expect(onFlush).not.toHaveBeenCalled()
  })

  test('start() is idempotent (no duplicate intervals)', () => {
    const onFlush = vi.fn()
    const batcher = createBatcher<number>(onFlush, 250)
    batcher.start()
    batcher.start() // must not create a second interval

    batcher.push(1)
    vi.advanceTimersByTime(250)
    expect(onFlush).toHaveBeenCalledTimes(1)

    batcher.stop()
  })
})
