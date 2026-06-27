import { Alert, Button, Loader, Stack, Text } from '@mantine/core'
import { IconAlertCircle, IconSearch, IconX } from '@tabler/icons-react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import React, { useState } from 'react'
import { Link } from 'react-router-dom'
import type { SearchResultItem } from '~/api'
import { pathTo, RouteIDs } from '~/routing'
import { SearchBar, type SearchQuery } from '~/screens/sessions/components/search-bar'
import { useData } from '~/shared'

dayjs.extend(relativeTime)

type SearchState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'done'; results: ReadonlyArray<SearchResultItem> }
  | { status: 'error'; message: string }

type Props = {
  sessionUUID: string
  children: React.ReactNode
}

/**
 * SessionSearch wraps a per-session request list with an identifier-search box.
 *
 * When idle (no search submitted) it renders `children` (the normal paginated list).
 * When a search is active it replaces `children` with session-scoped results.
 * A "Clear search" button returns to the idle/normal-list view.
 */
export function SessionSearch({ sessionUUID, children }: Props): React.JSX.Element {
  const { searchIdentifiers } = useData()
  const [state, setState] = useState<SearchState>({ status: 'idle' })

  const handleSearch = async (query: SearchQuery): Promise<void> => {
    setState({ status: 'loading' })
    try {
      const results = await searchIdentifiers({
        key: query.key,
        value: query.value,
        match: query.match,
        session: sessionUUID,
      })
      setState({ status: 'done', results })
    } catch (err: unknown) {
      setState({ status: 'error', message: err instanceof Error ? err.message : String(err) })
    }
  }

  const clearSearch = (): void => setState({ status: 'idle' })

  return (
    <Stack gap="xs">
      <SearchBar onSearch={(q) => void handleSearch(q)} />

      {state.status !== 'idle' && (
        <Button
          size="compact-xs"
          variant="subtle"
          color="gray"
          leftSection={<IconX size="0.75em" />}
          onClick={clearSearch}
          aria-label="Clear search"
        >
          Clear search
        </Button>
      )}

      {state.status === 'idle' && children}

      {state.status === 'loading' && (
        <Stack align="center" py="md" gap="xs">
          <Loader size="sm" color="dimmed" />
          <Text c="dimmed" size="sm">
            Searching…
          </Text>
        </Stack>
      )}

      {state.status === 'error' && (
        <Alert icon={<IconAlertCircle size="1rem" />} color="red" title="Search failed">
          {state.message}
        </Alert>
      )}

      {state.status === 'done' && state.results.length === 0 && (
        <Text c="dimmed" ta="center" size="sm" py="md">
          No matches in this session.
        </Text>
      )}

      {state.status === 'done' && state.results.length > 0 && (
        <Stack gap={4}>
          {Array.from(state.results).map((item) => (
            <Button
              key={item.requestUUID}
              component={Link}
              to={pathTo(RouteIDs.SessionAndRequest, item.sessionUUID, item.requestUUID)}
              variant="light"
              size="xs"
              leftSection={<IconSearch size="0.9em" />}
              fullWidth
              styles={{
                root: { height: 'auto', padding: '6px 10px' },
                inner: { justifyContent: 'flex-start' },
              }}
            >
              <Stack gap={2} align="flex-start">
                <Text size="xs" fw={500} style={{ fontFamily: 'monospace' }}>
                  {item.key}: {item.value}
                </Text>
                <Text size="xs" c="dimmed">
                  {dayjs(item.capturedAt).fromNow()}
                </Text>
              </Stack>
            </Button>
          ))}
        </Stack>
      )}
    </Stack>
  )
}
