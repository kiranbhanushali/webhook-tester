import { Drawer } from '@mantine/core'
import { notifications as notify } from '@mantine/notifications'
import React, { useEffect, useState } from 'react'
import { useData } from '~/shared'
import { RequestDetails } from '~/screens/session/components'

/** A request selected from the live stream, identified by its session + request UUIDs. */
export type SelectedRequest = Readonly<{ sID: string; rID: string }>

/**
 * Slide-over panel that shows the full detail of a request picked from the live stream. It reuses the
 * existing {@link RequestDetails} (payload viewer with the jq query + replay) by populating the shared
 * data context via switchToSession/switchToRequest (the same machinery the session screen uses).
 */
export const RequestDrawer: React.FC<{
  selected: SelectedRequest | null
  onClose: () => void
}> = ({ selected, onClose }) => {
  const { switchToSession, switchToRequest } = useData()
  const [loading, setLoading] = useState<boolean>(false)

  useEffect(() => {
    if (!selected) {
      return
    }

    let cancelled = false

    setLoading(true)

    void (async () => {
      try {
        // fast (local) phase first, then resolve the slow (server) phase for both session and request
        const [sessionSlow, requestSlow] = await Promise.all([
          switchToSession(selected.sID),
          switchToRequest(selected.sID, selected.rID),
        ])

        await Promise.allSettled([sessionSlow(), requestSlow()])
      } catch (err) {
        notify.show({ title: 'Failed to load request', message: String(err), color: 'red' })
      } finally {
        if (!cancelled) {
          setLoading(false)
        }
      }
    })()

    return () => {
      cancelled = true
    }
  }, [selected, switchToSession, switchToRequest])

  return (
    <Drawer
      opened={!!selected}
      onClose={onClose}
      position="right"
      size="xl"
      title="Request detail"
      padding="md"
      keepMounted={false}
    >
      {!!selected && <RequestDetails loading={loading} />}
    </Drawer>
  )
}
