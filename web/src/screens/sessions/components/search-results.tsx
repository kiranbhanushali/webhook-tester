import { Alert, Button, Divider, Stack, Table, Text, Title } from '@mantine/core'
import { IconAlertCircle, IconSearch } from '@tabler/icons-react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import React, { useState } from 'react'
import { Link } from 'react-router-dom'
import type { SearchResultItem } from '~/api'
import { pathTo, RouteIDs } from '~/routing'
import { useData } from '~/shared'
import { SearchBar, type SearchQuery } from './search-bar'

dayjs.extend(relativeTime)

type SearchState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'done'; results: ReadonlyArray<SearchResultItem> }
  | { status: 'error'; message: string }

/**
 * IdentifierSearch is a self-contained panel that lets the user search captured
 * requests by identifier key/value and jump to the matching request.
 */
export function IdentifierSearch(): React.JSX.Element {
  const { searchIdentifiers } = useData()
  const [state, setState] = useState<SearchState>({ status: 'idle' })

  const handleSearch = async (query: SearchQuery): Promise<void> => {
    setState({ status: 'loading' })
    try {
      const results = await searchIdentifiers({
        key: query.key,
        value: query.value,
        match: query.match,
      })
      setState({ status: 'done', results })
    } catch (err: unknown) {
      setState({ status: 'error', message: err instanceof Error ? err.message : String(err) })
    }
  }

  return (
    <Stack gap="sm">
      <Title order={4} style={{ fontWeight: 400 }}>
        Search by Identifier
      </Title>
      <SearchBar onSearch={(q) => void handleSearch(q)} />

      {state.status === 'loading' && (
        <Text c="dimmed" ta="center" py="md">
          Searching…
        </Text>
      )}

      {state.status === 'error' && (
        <Alert icon={<IconAlertCircle size="1rem" />} color="red" title="Search failed">
          {state.message}
        </Alert>
      )}

      {state.status === 'done' && state.results.length === 0 && (
        <Text c="dimmed" ta="center" py="md">
          No results found.
        </Text>
      )}

      {state.status === 'done' && state.results.length > 0 && (
        <Table striped highlightOnHover withTableBorder withColumnBorders>
          <Table.Thead>
            <Table.Tr>
              <Table.Th>Key</Table.Th>
              <Table.Th>Value</Table.Th>
              <Table.Th>Session</Table.Th>
              <Table.Th>Captured</Table.Th>
              <Table.Th>Action</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {Array.from(state.results).map((item) => (
              <Table.Tr key={`${item.sessionUUID}-${item.requestUUID}`}>
                <Table.Td>
                  <Text size="sm" style={{ fontFamily: 'monospace' }}>
                    {item.key}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm">{item.value}</Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm">{item.sessionSlug}</Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm">{dayjs(item.capturedAt).fromNow()}</Text>
                </Table.Td>
                <Table.Td>
                  <Button
                    component={Link}
                    to={pathTo(RouteIDs.SessionAndRequest, item.sessionUUID, item.requestUUID)}
                    size="xs"
                    variant="light"
                    leftSection={<IconSearch size="0.9em" />}
                  >
                    Open
                  </Button>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      )}

      <Divider />
    </Stack>
  )
}
