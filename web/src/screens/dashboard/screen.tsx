import { Divider, Paper, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications as notify } from '@mantine/notifications'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import type { FirehoseEvent, SessionSummary } from '~/api'
import { useData } from '~/shared'
import { NewSessionModal } from '~/screens/components/header/components'
import { ALL_SESSIONS, DashboardSearch, EndpointRail, LiveStream, RequestPanel, type SelectedRequest } from './components'
import { useEventStream } from './use-event-stream'
import styles from './screen.module.css'

/** A session is shown as "live" in the rail if it captured a request within this window (ms). */
const ACTIVE_WINDOW_MS = 6_000

export function DashboardScreen(): React.JSX.Element {
  const { listAllSessions } = useData()
  const [searchParams] = useSearchParams()

  const [sessions, setSessions] = useState<ReadonlyArray<SessionSummary>>([])
  const [loading, setLoading] = useState<boolean>(true)
  // Filters can be seeded from the URL (?session=<uuid|slug>&group=<name>) so links from the sessions
  // list / per-session "Events" action open the dashboard pre-filtered to that endpoint.
  const [selected, setSelected] = useState<string | null>(searchParams.get('session') ?? ALL_SESSIONS)
  const [groupFilter, setGroupFilter] = useState<string | null>(searchParams.get('group'))
  const [detail, setDetail] = useState<SelectedRequest | null>(null)
  const [searchActive, setSearchActive] = useState<boolean>(false)
  const [newSessionOpened, newSessionHandlers] = useDisclosure(false)

  // bumped on an interval so the rail's live dots fade as the activity window slides (see activeUUIDs)
  const [tick, setTick] = useState(0)

  const loadSessions = useCallback(async (): Promise<void> => {
    try {
      setSessions(await listAllSessions())
    } catch (err) {
      notify.show({ title: 'Failed to load endpoints', message: String(err), color: 'red' })
    } finally {
      setLoading(false)
    }
  }, [listAllSessions])

  useEffect(() => {
    void loadSessions()
  }, [loadSessions])

  // keep the live-activity dots fresh (the active window slides even when no new events arrive)
  useEffect(() => {
    const id = window.setInterval(() => setTick((t) => t + 1), 2_000)

    return () => window.clearInterval(id)
  }, [])

  const sessionByUUID = useMemo(
    () => new Map<string, SessionSummary>(sessions.map((s) => [s.uuid, s])),
    [sessions]
  )

  // distinct, sorted group names for the rail's group filter
  const groups = useMemo<ReadonlyArray<string>>(() => {
    const set = new Set<string>()

    for (const s of sessions) {
      if (s.group) {
        set.add(s.group)
      }
    }

    return Array.from(set).sort()
  }, [sessions])

  // A ?session= value may be a slug; once the sessions load, normalize it to the canonical UUID so the
  // rail highlights it and the live filter (which compares UUIDs) matches.
  useEffect(() => {
    if (!selected || sessionByUUID.has(selected)) {
      return
    }

    const bySlug = sessions.find((s) => s.slug === selected)

    if (bySlug) {
      setSelected(bySlug.uuid)
    }
  }, [sessions, sessionByUUID, selected])

  // Live-event predicate for the current filter. Group membership needs the sessions map, so it lives
  // here (the stream hook reads it through a ref, so its changing identity never re-subscribes).
  const matchesLive = useCallback(
    (e: FirehoseEvent): boolean => {
      if (selected !== ALL_SESSIONS && e.sessionUUID !== selected) {
        return false
      }

      if (groupFilter) {
        const s = sessionByUUID.get(e.sessionUUID)

        if (!s || (s.group ?? null) !== groupFilter) {
          return false
        }
      }

      return true
    },
    [selected, groupFilter, sessionByUUID]
  )

  const streamFilter = useMemo(() => ({ session: selected, group: groupFilter }), [selected, groupFilter])
  const { events, live, error, hasMore, loadingOlder, loading: streamLoading, loadOlder } = useEventStream(
    streamFilter,
    matchesLive
  )

  // when an event arrives for a session we don't know yet (created elsewhere), reload the rail once
  const reloadingRef = useRef<boolean>(false)

  useEffect(() => {
    if (loading || reloadingRef.current) {
      return
    }

    if (events.some((e) => !sessionByUUID.has(e.sessionUUID))) {
      reloadingRef.current = true

      void loadSessions().finally(() => {
        reloadingRef.current = false
      })
    }
  }, [events, sessionByUUID, loading, loadSessions])

  const activeUUIDs = useMemo<ReadonlySet<string>>(() => {
    const cutoff = Date.now() - ACTIVE_WINDOW_MS
    const set = new Set<string>()

    for (const event of events) {
      if (event.request && event.request.capturedAt.getTime() >= cutoff) {
        set.add(event.sessionUUID)
      }
    }

    return set
    // `tick` (a 2s timer) drives the recompute as the window slides; `events` drives it as new ones arrive
  }, [events, tick])

  const handleRowClick = useCallback((sID: string, rID: string) => setDetail({ sID, rID }), [])

  // Refs so the keydown handler always reads the latest events/detail without being re-registered
  // every render (the same pattern used by useEventStream for its live predicate).
  const eventsRef = useRef<ReadonlyArray<FirehoseEvent>>(events)
  eventsRef.current = events
  const detailRef = useRef<SelectedRequest | null>(detail)
  detailRef.current = detail

  // Keyboard navigation: ArrowDown/Up moves the selection through the live stream.
  // A window listener is used so the dashboard doesn't need to be "focused" as a DOM element;
  // keys originating from an input/textarea/select/contenteditable are ignored.
  useEffect(() => {
    const handler = (e: KeyboardEvent): void => {
      if (e.key !== 'ArrowDown' && e.key !== 'ArrowUp') {
        return
      }

      const target = e.target
      if (
        target instanceof HTMLInputElement ||
        target instanceof HTMLTextAreaElement ||
        target instanceof HTMLSelectElement ||
        (target instanceof HTMLElement && target.isContentEditable)
      ) {
        return
      }

      const eventsWithRequests = eventsRef.current.filter((ev) => ev.request !== undefined)
      if (eventsWithRequests.length === 0) {
        return
      }

      const currentDetail = detailRef.current
      const currentIndex = currentDetail
        ? eventsWithRequests.findIndex((ev) => ev.request?.uuid === currentDetail.rID)
        : -1

      let nextIndex: number
      if (e.key === 'ArrowDown') {
        // Nothing selected yet → pick the first (newest); otherwise clamp at the last.
        nextIndex = currentIndex === -1 ? 0 : Math.min(currentIndex + 1, eventsWithRequests.length - 1)
      } else {
        // ArrowUp: nothing selected or already at the top → no-op (no preventDefault).
        if (currentIndex <= 0) {
          return
        }
        nextIndex = currentIndex - 1
      }

      // No actual movement (clamped at boundary) → let the browser scroll the page normally.
      if (nextIndex === currentIndex) {
        return
      }

      e.preventDefault()
      const nextEvent = eventsWithRequests[nextIndex]
      if (nextEvent?.request) {
        handleRowClick(nextEvent.sessionUUID, nextEvent.request.uuid)
      }
    }

    window.addEventListener('keydown', handler)
    return () => window.removeEventListener('keydown', handler)
  }, [handleRowClick])

  const handleNewSessionClose = useCallback(() => {
    newSessionHandlers.close()
    void loadSessions() // a freshly created endpoint should appear in the rail
  }, [newSessionHandlers, loadSessions])

  const filterApplied = selected !== ALL_SESSIONS || groupFilter !== null

  return (
    <>
      <NewSessionModal opened={newSessionOpened} onClose={handleNewSessionClose} />

      <Title order={3} mb="md" style={{ fontWeight: 400 }}>
        Dashboard
      </Title>

      <div className={styles.layout}>
        <Paper className={styles.rail} withBorder p="sm" radius="md">
          <EndpointRail
            sessions={sessions}
            loading={loading}
            selected={selected}
            onSelect={setSelected}
            groups={groups}
            groupFilter={groupFilter}
            onGroupFilter={setGroupFilter}
            activeUUIDs={activeUUIDs}
            onNewSession={newSessionHandlers.open}
          />
        </Paper>

        <Paper className={styles.stream} withBorder p="sm" radius="md">
          <DashboardSearch
            session={selected === ALL_SESSIONS ? null : selected}
            group={groupFilter}
            onResultClick={handleRowClick}
            selectedUUID={detail?.rID ?? null}
            onActiveChange={setSearchActive}
          />
          {!searchActive && (
            <>
              <Divider mb="xs" />
              <LiveStream
                events={events}
                sessionByUUID={sessionByUUID}
                live={live}
                error={error}
                filtered={filterApplied}
                loading={streamLoading}
                hasMore={hasMore}
                loadingOlder={loadingOlder}
                onLoadOlder={loadOlder}
                onRowClick={handleRowClick}
                selectedUUID={detail?.rID ?? null}
              />
            </>
          )}
        </Paper>

        <Paper className={styles.detail} withBorder p="sm" radius="md">
          <RequestPanel selected={detail} />
        </Paper>
      </div>
    </>
  )
}
