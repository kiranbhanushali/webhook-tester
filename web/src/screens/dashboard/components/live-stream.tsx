import { Alert, Badge, Box, Flex, Group, Stack, Text, Tooltip, UnstyledButton } from '@mantine/core'
import { useInterval } from '@mantine/hooks'
import { IconAlertTriangle, IconBolt } from '@tabler/icons-react'
import dayjs from 'dayjs'
import React, { useEffect, useState } from 'react'
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
  /** True when a session filter is applied and it hid every event. */
  filtered: boolean
  onRowClick: (sID: string, rID: string) => void
}> = ({ events, sessionByUUID, live, error, filtered, onRowClick }) => {
  // re-render periodically so the relative "fromNow" times stay fresh without per-row timers
  const [, setTick] = useState(0)
  const interval = useInterval(() => setTick((t) => t + 1), 5000)

  useEffect(() => {
    interval.start()

    return interval.stop
  }, [interval.start, interval.stop])

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
          <Text c="dimmed">{filtered ? 'No requests for this endpoint yet.' : 'Waiting for incoming webhooks…'}</Text>
          <Text c="dimmed" size="xs">
            Requests captured across all endpoints appear here instantly.
          </Text>
        </Flex>
      ) : (
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
      )}
    </Stack>
  )
}
