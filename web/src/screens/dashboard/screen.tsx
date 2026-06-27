import { Paper, Title } from '@mantine/core'
import { useDisclosure } from '@mantine/hooks'
import { notifications as notify } from '@mantine/notifications'
import React, { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { SessionSummary } from '~/api'
import { useData } from '~/shared'
import { NewSessionModal } from '~/screens/components/header/components'
import { ALL_SESSIONS, EndpointRail, LiveStream, RequestDrawer, type SelectedRequest } from './components'
import { useFirehose } from './use-firehose'
import styles from './screen.module.css'

/** A session is shown as "live" in the rail if it captured a request within this window (ms). */
const ACTIVE_WINDOW_MS = 6_000

export function DashboardScreen(): React.JSX.Element {
  const { listAllSessions } = useData()
  const { events, live, error } = useFirehose()

  const [sessions, setSessions] = useState<ReadonlyArray<SessionSummary>>([])
  const [loading, setLoading] = useState<boolean>(true)
  const [selected, setSelected] = useState<string | null>(ALL_SESSIONS)
  const [detail, setDetail] = useState<SelectedRequest | null>(null)
  const [newSessionOpened, newSessionHandlers] = useDisclosure(false)

  // bumped on an interval so the rail's live dots + the rail itself refresh as the activity window slides
  const [, setTick] = useState(0)

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
    // `setTick` drives the recompute as the window slides; events drives it as new ones arrive
  }, [events])

  const filteredEvents = useMemo(
    () => (selected === ALL_SESSIONS ? events : events.filter((e) => e.sessionUUID === selected)),
    [events, selected]
  )

  const handleRowClick = useCallback((sID: string, rID: string) => setDetail({ sID, rID }), [])

  const handleNewSessionClose = useCallback(() => {
    newSessionHandlers.close()
    void loadSessions() // a freshly created endpoint should appear in the rail
  }, [newSessionHandlers, loadSessions])

  return (
    <>
      <NewSessionModal opened={newSessionOpened} onClose={handleNewSessionClose} />
      <RequestDrawer selected={detail} onClose={() => setDetail(null)} />

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
            activeUUIDs={activeUUIDs}
            onNewSession={newSessionHandlers.open}
          />
        </Paper>

        <Paper className={styles.stream} withBorder p="sm" radius="md">
          <LiveStream
            events={filteredEvents}
            sessionByUUID={sessionByUUID}
            live={live}
            error={error}
            filtered={selected !== ALL_SESSIONS && filteredEvents.length === 0 && events.length > 0}
            onRowClick={handleRowClick}
          />
        </Paper>
      </div>
    </>
  )
}
