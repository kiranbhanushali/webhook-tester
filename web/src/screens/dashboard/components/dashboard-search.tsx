import {
  Alert,
  Badge,
  Button,
  Center,
  Divider,
  Group,
  Loader,
  ScrollArea,
  SegmentedControl,
  Stack,
  Text,
  TextInput,
  UnstyledButton,
} from '@mantine/core'
import { IconAlertCircle, IconSearch, IconX } from '@tabler/icons-react'
import dayjs from 'dayjs'
import relativeTime from 'dayjs/plugin/relativeTime'
import React, { useState } from 'react'
import type { SearchResultItem } from '~/api'
import { useData } from '~/shared'
import styles from './dashboard-search.module.css'

dayjs.extend(relativeTime)

type SearchMode = 'exact' | 'multi'

type SearchState =
  | { status: 'idle' }
  | { status: 'loading' }
  | { status: 'done'; results: ReadonlyArray<SearchResultItem> }
  | { status: 'error'; message: string }

export type DashboardSearchProps = {
  /** Session UUID/slug to restrict the search; null = all sessions */
  session: string | null
  /** Group name to restrict the search; null = no group filter */
  group: string | null
  /** Called when a result row is clicked — use the same path as clicking a live stream row */
  onResultClick: (sID: string, rID: string) => void
  /** UUID of the currently-selected request (for row highlight) */
  selectedUUID: string | null
  /** Called with true when a search runs (results replace the live stream) and false when cleared */
  onActiveChange: (active: boolean) => void
}

/**
 * DashboardSearch — identifier search panel embedded in the live-stream column.
 *
 * Supports two modes:
 *   exact  — single exact-match value (standard searchIdentifiers call)
 *   multi  — space/comma-separated keywords; fires one searchIdentifiers call per keyword,
 *            merges and deduplicates results by requestUUID (OR semantics).
 *
 * Respects the dashboard's active session/group filter when set.
 */
export function DashboardSearch({
  session,
  group,
  onResultClick,
  selectedUUID,
  onActiveChange,
}: DashboardSearchProps): React.JSX.Element {
  const { searchIdentifiers } = useData()
  const [key, setKey] = useState<string>('trackingId')
  const [value, setValue] = useState<string>('')
  const [mode, setMode] = useState<SearchMode>('exact')
  const [state, setState] = useState<SearchState>({ status: 'idle' })

  const handleSearch = async (): Promise<void> => {
    const trimmed = value.trim()
    if (!trimmed) {
      return
    }

    setState({ status: 'loading' })
    onActiveChange(true)

    try {
      const keywords: string[] =
        mode === 'multi' ? trimmed.split(/[\s,]+/).filter(Boolean) : [trimmed]

      const pages = await Promise.all(
        keywords.map((kw) =>
          searchIdentifiers({
            key,
            value: kw,
            match: 'exact',
            session: session ?? undefined,
            group: group ?? undefined,
          })
        )
      )

      // Merge and deduplicate by requestUUID; first keyword wins when the same request matches multiple keywords.
      const seen = new Set<string>()
      const merged: SearchResultItem[] = []
      for (const page of pages) {
        for (const item of page) {
          if (!seen.has(item.requestUUID)) {
            seen.add(item.requestUUID)
            merged.push(item)
          }
        }
      }

      setState({ status: 'done', results: merged })
    } catch (err: unknown) {
      setState({
        status: 'error',
        message: err instanceof Error ? err.message : String(err),
      })
    }
  }

  const handleClear = (): void => {
    setState({ status: 'idle' })
    onActiveChange(false)
  }

  const isActive = state.status !== 'idle'

  return (
    <Stack gap="xs" mb="xs">
      <Group gap="xs" align="flex-end" wrap="nowrap">
        <TextInput
          label="Key"
          aria-label="Search key"
          value={key}
          onChange={(e) => setKey(e.currentTarget.value)}
          w="6.5rem"
          size="xs"
        />
        <TextInput
          label={mode === 'multi' ? 'Values (space/comma)' : 'Value'}
          aria-label="Search value"
          placeholder={mode === 'multi' ? 'val-1 val-2' : 'val-123'}
          value={value}
          onChange={(e) => setValue(e.currentTarget.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') {
              void handleSearch()
            }
          }}
          style={{ flex: 1, minWidth: 0 }}
          size="xs"
        />
        <SegmentedControl
          value={mode}
          onChange={(v) => {
            if (v === 'exact' || v === 'multi') {
              setMode(v)
            }
          }}
          data={[
            { value: 'exact', label: 'Exact' },
            { value: 'multi', label: 'OR' },
          ]}
          size="xs"
        />
        <Button
          leftSection={<IconSearch size="0.85em" />}
          onClick={() => void handleSearch()}
          disabled={!value.trim()}
          loading={state.status === 'loading'}
          size="xs"
          aria-label="Search identifiers"
        >
          Search
        </Button>
        {isActive && (
          <Button
            variant="subtle"
            color="gray"
            leftSection={<IconX size="0.85em" />}
            onClick={handleClear}
            size="xs"
            aria-label="Clear search"
          >
            Clear
          </Button>
        )}
      </Group>

      {state.status === 'loading' && (
        <Center py="xs">
          <Loader size="xs" />
          <Text c="dimmed" size="xs" ml="xs">
            Searching…
          </Text>
        </Center>
      )}

      {state.status === 'error' && (
        <Alert icon={<IconAlertCircle size="0.9em" />} color="red" title="Search failed" p="xs">
          <Text size="xs">{state.message}</Text>
        </Alert>
      )}

      {state.status === 'done' && state.results.length === 0 && (
        <Text c="dimmed" size="sm" ta="center" py="xs">
          No matching requests found.
        </Text>
      )}

      {state.status === 'done' && state.results.length > 0 && (
        <>
          <Text size="xs" c="dimmed">
            {state.results.length} result{state.results.length !== 1 ? 's' : ''}
          </Text>
          <Divider />
          <ScrollArea className={styles.scroll} scrollbarSize={6} type="hover">
            <Stack gap={0}>
              {state.results.map((item) => (
                <UnstyledButton
                  key={`${item.sessionUUID}:${item.requestUUID}`}
                  className={
                    item.requestUUID === selectedUUID
                      ? `${styles.row} ${styles.rowSelected}`
                      : styles.row
                  }
                  onClick={() => onResultClick(item.sessionUUID, item.requestUUID)}
                  aria-label={`Open result for ${item.key}=${item.value}`}
                >
                  <Group justify="space-between" wrap="nowrap" gap="xs">
                    <Group gap="xs" wrap="nowrap" style={{ minWidth: 0 }}>
                      <Text size="xs" c="dimmed" style={{ flex: '0 0 auto', width: '5.5em' }}>
                        {dayjs(item.capturedAt).fromNow(true)}
                      </Text>
                      <Badge variant="light" size="sm" style={{ flex: '0 0 auto' }}>
                        {item.sessionSlug}
                      </Badge>
                      <Text
                        size="xs"
                        style={{
                          fontFamily: 'monospace',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'nowrap',
                        }}
                      >
                        {item.key}={item.value}
                      </Text>
                    </Group>
                  </Group>
                </UnstyledButton>
              ))}
            </Stack>
          </ScrollArea>
        </>
      )}
    </Stack>
  )
}
