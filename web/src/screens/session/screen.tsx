import { Anchor, Blockquote, Button, Group } from '@mantine/core'
import { notifications as notify } from '@mantine/notifications'
import { IconEye, IconInfoCircle, IconPencil } from '@tabler/icons-react'
import React, { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { pathTo, RouteIDs } from '~/routing'
import { useData } from '~/shared'
import { SessionDetails, SessionEditor } from './components'

/**
 * The per-session page is CONFIGURATION ONLY: it shows the endpoint's webhook URL, test snippets and
 * current options, plus the settings editor. Viewing captured requests now happens on the dashboard,
 * filtered to this endpoint — so there is no request list/viewer or per-request websocket here. A deep
 * link that still carries a request id (/s/:sID/:rID) is redirected to the dashboard for that endpoint.
 */
export function SessionAndRequestScreen(): React.JSX.Element {
  const navigate = useNavigate()
  const { sID } = useParams<{ sID: string }>() as Readonly<{ sID: string }>
  const { rID } = useParams<Readonly<{ rID?: string }>>()
  const [sessionLoading, setSessionLoading] = useState<boolean>(false)
  const [editorOpened, setEditorOpened] = useState<boolean>(false)
  const { session, switchToSession } = useData()

  const stateSID = useRef<string | null>(session?.sID || null)
  useEffect(() => {
    stateSID.current = session?.sID || null
  }, [session])

  // Redirect legacy per-request deep links to the dashboard filtered to this endpoint.
  useEffect(() => {
    if (rID) {
      navigate(`${pathTo(RouteIDs.Dashboard)}?session=${encodeURIComponent(sID)}`, { replace: true })
    }
  }, [rID, sID, navigate])

  // Load this session's configuration into the shared state (fast from the local DB, then the server).
  useEffect(() => {
    if (rID || stateSID.current === sID) {
      return
    }

    void (async () => {
      try {
        const slow = await switchToSession(sID)

        setSessionLoading(true)

        await slow()
      } catch (err) {
        notify.show({ title: 'Switching to the session failed', message: String(err), color: 'red' })
        navigate(pathTo(RouteIDs.Home))
      } finally {
        setSessionLoading(false)
      }
    })()
  }, [sID, rID, switchToSession, navigate])

  return (
    <>
      <SessionDetails loading={sessionLoading} />

      {!!session && (
        <>
          <Group mb="sm" gap="xs">
            <Button variant="light" leftSection={<IconPencil size="1em" />} onClick={() => setEditorOpened(true)}>
              Edit session settings
            </Button>
            <Button
              component={Link}
              to={`${pathTo(RouteIDs.Dashboard)}?session=${encodeURIComponent(session.sID)}`}
              variant="subtle"
              color="teal"
              leftSection={<IconEye size="1em" />}
            >
              View captured events
            </Button>
          </Group>

          <SessionEditor
            key={session.sID}
            session={session}
            opened={editorOpened}
            onClose={() => setEditorOpened(false)}
          />
        </>
      )}

      <Blockquote my="lg" color="blue" icon={<IconInfoCircle />}>
        This page configures the endpoint (status code, response body, slug, group, scripting, inbound auth,
        forwarding, and more). Captured requests are viewed on the{' '}
        <Anchor component={Link} to={`${pathTo(RouteIDs.Dashboard)}?session=${encodeURIComponent(sID)}`}>
          dashboard
        </Anchor>
        , filtered to this endpoint.
      </Blockquote>
    </>
  )
}
