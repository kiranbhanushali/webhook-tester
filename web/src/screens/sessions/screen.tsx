import { Badge, Button, Group, Select, Skeleton, Table, Text, TextInput, Title } from '@mantine/core'
import { notifications as notify } from '@mantine/notifications'
import { IconList, IconSearch, IconTrash } from '@tabler/icons-react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import React, { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import type { SessionSummary } from '~/api'
import { pathTo, RouteIDs } from '~/routing'
import { buildWebhookUrl } from '~/shared/utils/webhook-url'
import { useData } from '~/shared'
import styles from './screen.module.css'

// Extend dayjs with relative time support (idempotent; main.tsx does this too at runtime)
dayjs.extend(relativeTime)

const UNGROUPED_LABEL = 'Ungrouped'

/** Map a HTTP status code to a Mantine color. */
const statusCodeToColor = (code: number): string => {
  if (code >= 500) return 'red'
  if (code >= 400) return 'orange'
  if (code >= 300) return 'blue'
  if (code >= 200) return 'green'
  return 'gray'
}

/** Group an array of sessions by their `group` field, placing null-group sessions under `Ungrouped`. */
const groupSessions = (sessions: ReadonlyArray<SessionSummary>): Map<string, SessionSummary[]> => {
  const result = new Map<string, SessionSummary[]>()

  for (const session of sessions) {
    const key = session.group ?? UNGROUPED_LABEL

    if (!result.has(key)) {
      result.set(key, [])
    }

    result.get(key)!.push(session)
  }

  return result
}

export function SessionsListScreen(): React.JSX.Element {
  const { listAllSessions, destroySession } = useData()

  const [sessions, setSessions] = useState<ReadonlyArray<SessionSummary>>([])
  const [loading, setLoading] = useState<boolean>(true)
  const [searchText, setSearchText] = useState<string>('')
  const [groupFilter, setGroupFilter] = useState<string | null>(null)

  /** Load (or reload) the full sessions list from the server. */
  const loadSessions = useCallback(async (): Promise<void> => {
    setLoading(true)
    try {
      const result = await listAllSessions()
      setSessions(result)
    } catch (err) {
      notify.show({ title: 'Failed to load sessions', message: String(err), color: 'red' })
    } finally {
      setLoading(false)
    }
  }, [listAllSessions])

  useEffect(() => {
    loadSessions()
  }, [loadSessions])

  /** Distinct group names present in the full session list (for the filter dropdown). */
  const distinctGroups: string[] = Array.from(new Set(sessions.map((s) => s.group ?? UNGROUPED_LABEL))).sort()

  const groupSelectData = [
    { value: '', label: 'All groups' },
    ...distinctGroups.map((g) => ({ value: g, label: g })),
  ]

  /** Sessions filtered by the text search and group dropdown. */
  const filtered: ReadonlyArray<SessionSummary> = sessions.filter((s) => {
    const q = searchText.toLowerCase()
    const matchesText = !q || s.slug.toLowerCase().includes(q) || (s.group ?? '').toLowerCase().includes(q)

    const matchesGroup =
      !groupFilter ||
      (groupFilter === UNGROUPED_LABEL ? s.group === null : s.group === groupFilter)

    return matchesText && matchesGroup
  })

  const grouped = groupSessions(filtered)

  /** Delete a session: confirm, call destroySession, remove it from local state. */
  const handleDelete = useCallback(
    async (uuid: string, slug: string): Promise<void> => {
      if (!window.confirm(`Delete session "${slug}"? This cannot be undone.`)) {
        return
      }

      try {
        const slow = await destroySession(uuid)
        await slow()
        setSessions((prev) => prev.filter((s) => s.uuid !== uuid))
      } catch (err) {
        notify.show({ title: 'Failed to delete session', message: String(err), color: 'red' })
      }
    },
    [destroySession]
  )

  if (loading) {
    return (
      <div className={styles.container}>
        <Skeleton height={40} mb="sm" />
        <Skeleton height={60} mb="xs" />
        <Skeleton height={300} />
      </div>
    )
  }

  return (
    <div className={styles.container}>
      <Group mb="md" align="center">
        <IconList size="1.4em" />
        <Title order={3} style={{ fontWeight: 400 }}>
          All Sessions
        </Title>
      </Group>

      <Group mb="md" grow>
        <TextInput
          label="Filter sessions"
          placeholder="Filter by slug or group…"
          leftSection={<IconSearch size="1em" />}
          value={searchText}
          onChange={(e) => setSearchText(e.currentTarget.value)}
          aria-label="Filter sessions"
        />
        <Select
          label="Group"
          data={groupSelectData}
          value={groupFilter ?? ''}
          onChange={(v) => setGroupFilter(v || null)}
          placeholder="All groups"
          clearable
        />
      </Group>

      {filtered.length === 0 ? (
        <Text c="dimmed" ta="center" py="xl">
          No sessions found.
        </Text>
      ) : (
        <Table striped highlightOnHover withTableBorder withColumnBorders>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Slug / Webhook URL</Table.Th>
              <Table.Th>Group</Table.Th>
              <Table.Th>Requests</Table.Th>
              <Table.Th>Last Activity</Table.Th>
              <Table.Th>Expiry</Table.Th>
              <Table.Th>Default Status</Table.Th>
              <Table.Th>Actions</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {Array.from(grouped.entries()).map(([groupName, groupSessions]) => (
              <React.Fragment key={groupName}>
                {/* Group header row */}
                <Table.Tr>
                  <Table.Td colSpan={7} className={styles.groupHeaderCell}>
                    <Text fw={600} size="sm" c="dimmed">
                      {groupName}
                    </Text>
                  </Table.Td>
                </Table.Tr>

                {/* Session rows in this group */}
                {groupSessions.map((session) => (
                  <Table.Tr key={session.uuid}>
                    <Table.Td>
                      <Text fw={500} size="sm">
                        {session.slug}
                      </Text>
                      <Text size="xs" c="dimmed" style={{ fontFamily: 'monospace' }}>
                        {buildWebhookUrl(null, session.slug).toString()}
                      </Text>
                    </Table.Td>

                    <Table.Td>
                      <Text size="sm">{session.group ?? <Text span c="dimmed">—</Text>}</Text>
                    </Table.Td>

                    <Table.Td>
                      <Text size="sm">{session.requestsCount}</Text>
                    </Table.Td>

                    <Table.Td>
                      <Text size="sm">
                        {session.lastRequestAt ? dayjs(session.lastRequestAt).fromNow() : '—'}
                      </Text>
                    </Table.Td>

                    <Table.Td>
                      {session.longLived ? (
                        <Badge color="teal" variant="light">
                          long-lived
                        </Badge>
                      ) : (
                        <Text size="sm">{dayjs(session.expiresAt).fromNow()}</Text>
                      )}
                    </Table.Td>

                    <Table.Td>
                      <Badge color={statusCodeToColor(session.statusCode)} variant="light">
                        {session.statusCode}
                      </Badge>
                    </Table.Td>

                    <Table.Td>
                      <Group gap="xs" wrap="nowrap">
                        <Button
                          component={Link}
                          to={pathTo(RouteIDs.SessionAndRequest, session.uuid)}
                          size="xs"
                          variant="light"
                        >
                          Open
                        </Button>
                        <Button
                          size="xs"
                          variant="light"
                          color="red"
                          leftSection={<IconTrash size="0.9em" />}
                          onClick={() => handleDelete(session.uuid, session.slug)}
                        >
                          Delete
                        </Button>
                      </Group>
                    </Table.Td>
                  </Table.Tr>
                ))}
              </React.Fragment>
            ))}
          </Table.Tbody>
        </Table>
      )}
    </div>
  )
}
