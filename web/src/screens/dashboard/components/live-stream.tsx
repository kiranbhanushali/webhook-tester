import { Alert, Badge, Box, Center, Flex, Group, Loader, ScrollArea, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { useInterval } from '@mantine/hooks'
import { IconAlertTriangle, IconBolt } from '@tabler/icons-react'
import dayjs from 'dayjs'
import React, { useCallback, useEffect, useRef, useState } from 'react'
import type { FirehoseEvent, SessionSummary } from '~/api'
import { methodToColor } from '~/theme'
import { requestPeek, returnedStatus, slugColor, statusCodeToColor } from '../utils'
import styles from './live-stream.module.css'

const StreamRow: React.FC<{
  event: FirehoseEvent
  sessionByUUID: ReadonlyMap<string, SessionSummary>
  onClick: () => void
}> = ({ event, sessionByUUID, onClick }) => {
  const req = event.request

  if (!req) {
    return null
  }

  const status = returnedStatus(event, sessionByUUID)
  const unauthorized = req.authorized === false

  return (
    <UnstyledButton
      className={styles.row}
      onClick={onClick}
      aria-label={`Open ${req.method} request to ${event.sessionSlug}`}
    >
      <Group justify="space-between" wrap="nowrap" gap="xs">
        <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
          <Tooltip label={dayjs(req.capturedAt).format('YYYY-MM-DD HH:mm:ss.SSS')} withArrow openDelay={300}>
            <Text size="xs" c="dimmed" style={{ flex: '0 0 auto', width: '5.5em' }}>
              {dayjs(req.capturedAt).fromNow(true)}
            </Text>
          </Tooltip>

          <Badge variant="light" color={slugColor(event.sessionSlug)} style={{ flex: '0 0 auto' }}>
            {event.sessionSlug}
          </Badge>

          <Badge variant="dot" color={methodToColor(req.method)} style={{ flex: '0 0 auto' }}>
            {req.method}
          </Badge>

          {status !== null && (
            <Badge variant="light" color={statusCodeToColor(status)} style={{ flex: '0 0 auto' }}>
              {status}
            </Badge>
          )}

          {unauthorized && (
            <Tooltip label="Inbound auth failed — 401 returned" withArrow>
              <Badge color="red" variant="filled" size="sm" style={{ flex: '0 0 auto' }}>
                Unauthorized
              </Badge>
            </Tooltip>
          )}
        </Group>

        <Text className={styles.peek} c="dimmed" style={{ minWidth: 0 }}>
          {requestPeek(event)}
        </Text>
      </Group>
    </UnstyledButton>
  )
}

export const LiveStream: React.FC<{
  events: ReadonlyArray<FirehoseEvent>
  sessionByUUID: ReadonlyMap<string, SessionSummary>
  live: boolean
  error: Error | null
  /** True when a filter is applied and it hid every event. */
  filtered: boolean
  /** True while the initial/refresh backfill for the current filter is loading. */
  loading: boolean
  /** True while older events remain to be loaded (the infinite-scroll sentinel is shown). */
  hasMore: boolean
  /** True while an older page is being fetched. */
  loadingOlder: boolean
  /** Load the next (older) page; returns a promise so the sentinel can release its guard on completion. */
  onLoadOlder: () => Promise<void>
  onRowClick: (sID: string, rID: string) => void
}> = ({ events, sessionByUUID, live, error, filtered, loading, hasMore, loadingOlder, onLoadOlder, onRowClick }) => {
  // re-render periodically so the relative "fromNow" times stay fresh without per-row timers
  const [, setTick] = useState(0)
  const interval = useInterval(() => setTick((t) => t + 1), 5000)

  useEffect(() => {
    interval.start()

    return interval.stop
  }, [interval.start, interval.stop])

  // Infinite scroll into older history. Mirrors the sidebar's #185-safe pattern: a bounded scroll
  // container (so the sentinel only intersects at the real bottom), a single observer created ONCE
  // (deps are [hasMore, onSentinelVisible] — NOT events.length, which would re-fire for the still-
  // visible sentinel → runaway pagination), and a synchronous re-entrancy guard.
  const viewportRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const loadingRef = useRef<boolean>(false)
  const onLoadOlderRef = useRef(onLoadOlder)
  onLoadOlderRef.current = onLoadOlder
  const hasMoreRef = useRef<boolean>(hasMore)
  hasMoreRef.current = hasMore

  const onSentinelVisible = useCallback(() => {
    if (loadingRef.current || !hasMoreRef.current) {
      return
    }

    loadingRef.current = true
    onLoadOlderRef.current().finally(() => {
      loadingRef.current = false
    })
  }, [])

  useEffect(() => {
    const el = sentinelRef.current

    if (!el || typeof IntersectionObserver === 'undefined') {
      return
    }

    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          onSentinelVisible()
        }
      },
      { root: viewportRef.current ?? null, rootMargin: '200px' }
    )

    observer.observe(el)

    return () => observer.disconnect()
  }, [hasMore, onSentinelVisible])

  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center">
        <Group gap="xs" align="center">
          <IconBolt size="1.2em" />
          <Text fw={600}>Live stream</Text>
          <Text size="xs" c="dimmed">
            ({events.length})
          </Text>
        </Group>
        <Group gap={6} align="center">
          <Box className={live ? styles.liveDot : styles.offlineDot} />
          <Text size="xs" c={live ? 'green' : 'dimmed'} fw={500}>
            {live ? 'Live' : 'Offline'}
          </Text>
        </Group>
      </Group>

      {error && (
        <Alert color="orange" icon={<IconAlertTriangle size="1.1em" />} variant="light" title="Live stream disconnected">
          {error.message || 'The connection was lost. Reload the page to reconnect.'}
        </Alert>
      )}

      {events.length === 0 ? (
        <Flex direction="column" align="center" justify="center" py="xl" gap={4}>
          {loading ? (
            <>
              <Loader size="sm" color="dimmed" />
              <Text c="dimmed" size="xs">
                Loading recent requests…
              </Text>
            </>
          ) : (
            <>
              <Text c="dimmed">{filtered ? 'No requests for this filter yet.' : 'Waiting for incoming webhooks…'}</Text>
              <Text c="dimmed" size="xs">
                Recent requests appear here on load; new ones stream in live.
              </Text>
            </>
          )}
        </Flex>
      ) : (
        <ScrollArea className={styles.scroll} viewportRef={viewportRef} scrollbarSize={6} type="hover">
          <Stack gap={0}>
            {events.map((event) =>
              event.request ? (
                <StreamRow
                  key={`${event.sessionUUID}:${event.request.uuid}`}
                  event={event}
                  sessionByUUID={sessionByUUID}
                  onClick={() => event.request && onRowClick(event.sessionUUID, event.request.uuid)}
                />
              ) : null
            )}
          </Stack>

          {hasMore && (
            <Center ref={sentinelRef} py="xs" data-testid="events-load-more">
              <Loader color="dimmed" size="xs" mr={8} />
              <Text c="dimmed" size="xs">
                {loadingOlder ? 'Loading older requests…' : 'Scroll for older requests'}
              </Text>
            </Center>
          )}
        </ScrollArea>
      )}
    </Stack>
  )
}
