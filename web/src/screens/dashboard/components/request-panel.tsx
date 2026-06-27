import { Center, Text } from '@mantine/core'
import { notifications as notify } from '@mantine/notifications'
import React, { useEffect, useState } from 'react'
import { useData } from '~/shared'
import { RequestDetails } from '~/screens/session/components'
import type { SelectedRequest } from './request-drawer'

/**
 * Persistent right-hand detail panel for the dashboard 3-pane layout.
 * Loads the clicked request via the shared data context (same mechanism as RequestDrawer)
 * and renders RequestDetails inline — no overlay, no modal.
 */
export const RequestPanel: React.FC<{ selected: SelectedRequest | null }> = ({ selected }) => {
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

  if (!selected) {
    return (
      <Center py="xl">
        <Text c="dimmed" size="sm">
          Select a request to view its body
        </Text>
      </Center>
    )
  }

  return <RequestDetails loading={loading} />
}
