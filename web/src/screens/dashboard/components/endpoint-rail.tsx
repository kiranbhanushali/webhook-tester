import { ActionIcon, Badge, Box, Button, Group, Select, Skeleton, Stack, Text, UnstyledButton } from '@mantine/core'
import { IconChevronLeft, IconChevronRight, IconCirclePlusFilled, IconStack2 } from '@tabler/icons-react'
import React from 'react'
import type { SessionSummary } from '~/api'
import { slugColor } from '../utils'
import styles from './endpoint-rail.module.css'

/** Sentinel passed to onSelect to mean "show every session" (the All option). */
export const ALL_SESSIONS = null

export const EndpointRail: React.FC<{
  sessions: ReadonlyArray<SessionSummary>
  loading: boolean
  /** Currently selected session UUID, or null for "All". */
  selected: string | null
  onSelect: (uuid: string | null) => void
  /** Distinct group names available across all sessions (for the group filter). */
  groups: ReadonlyArray<string>
  /** Currently selected group filter, or null for "All groups". */
  groupFilter: string | null
  onGroupFilter: (group: string | null) => void
  /** Session UUIDs that captured a webhook in the live stream recently (shows a pulsing dot). */
  activeUUIDs: ReadonlySet<string>
  onNewSession: () => void
  collapsed: boolean
  onToggleCollapse: () => void
}> = ({ sessions, loading, selected, onSelect, groups, groupFilter, onGroupFilter, activeUUIDs, onNewSession, collapsed, onToggleCollapse }) => {
  if (collapsed) {
    return (
      <div className={styles.collapsedStrip}>
        <ActionIcon variant="subtle" size="sm" aria-label="Expand endpoints panel" onClick={onToggleCollapse}>
          <IconChevronRight size="1em" />
        </ActionIcon>
      </div>
    )
  }

  return (
    <Stack gap="xs">
      <Group justify="space-between" align="center" wrap="nowrap">
        <Group gap="xs" align="center" wrap="nowrap">
          <IconStack2 size="1.2em" />
          <Text fw={600}>Endpoints</Text>
        </Group>
        <ActionIcon variant="subtle" size="sm" aria-label="Collapse endpoints panel" onClick={onToggleCollapse}>
          <IconChevronLeft size="1em" />
        </ActionIcon>
        <Button
          size="compact-xs"
          variant="light"
          color="teal"
          leftSection={<IconCirclePlusFilled size="1.1em" />}
          onClick={onNewSession}
        >
          New
        </Button>
      </Group>

      <Select
        size="xs"
        label="Group"
        placeholder="All groups"
        data={[{ value: '', label: 'All groups' }, ...groups.map((g) => ({ value: g, label: g }))]}
        value={groupFilter ?? ''}
        onChange={(v) => onGroupFilter(v ? v : null)}
        comboboxProps={{ withinPortal: true }}
        clearable
        aria-label="Filter stream by group"
      />

      <UnstyledButton
        className={`${styles.item} ${selected === ALL_SESSIONS ? styles.itemActive : ''}`}
        onClick={() => onSelect(ALL_SESSIONS)}
        aria-pressed={selected === ALL_SESSIONS}
      >
        <Group justify="space-between" wrap="nowrap">
          <Text fw={500} size="sm">
            All endpoints
          </Text>
          <Badge size="sm" variant="light" color="gray">
            {sessions.length}
          </Badge>
        </Group>
      </UnstyledButton>

      {loading && sessions.length === 0 ? (
        <>
          <Skeleton height={34} radius="sm" />
          <Skeleton height={34} radius="sm" />
          <Skeleton height={34} radius="sm" />
        </>
      ) : sessions.length === 0 ? (
        <Text c="dimmed" size="sm" py="xs">
          No endpoints yet. Click “New” to create one.
        </Text>
      ) : (
        sessions.map((session) => {
          const isActive = selected === session.uuid
          const isLive = activeUUIDs.has(session.uuid)

          return (
            <UnstyledButton
              key={session.uuid}
              className={`${styles.item} ${isActive ? styles.itemActive : ''}`}
              onClick={() => onSelect(session.uuid)}
              aria-pressed={isActive}
              aria-label={`Filter stream to ${session.slug}`}
            >
              <Group justify="space-between" wrap="nowrap" gap="xs">
                <Group gap={6} wrap="nowrap" style={{ minWidth: 0 }}>
                  {isLive ? (
                    <Box className={styles.dot} title="Live activity" />
                  ) : (
                    <Badge
                      size="xs"
                      circle
                      variant="filled"
                      color={slugColor(session.slug)}
                      style={{ flex: '0 0 auto' }}
                    >
                      {' '}
                    </Badge>
                  )}
                  <Box style={{ minWidth: 0 }}>
                    <Text size="sm" fw={500} truncate>
                      {session.slug}
                    </Text>
                    {session.group && (
                      <Text size="xs" c="dimmed" truncate>
                        {session.group}
                      </Text>
                    )}
                  </Box>
                </Group>
                <Badge size="sm" variant="light" color={slugColor(session.slug)} style={{ flex: '0 0 auto' }}>
                  {session.requestsCount}
                </Badge>
              </Group>
            </UnstyledButton>
          )
        })
      )}
    </Stack>
  )
}
