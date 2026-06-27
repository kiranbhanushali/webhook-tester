import React, { useCallback, useEffect, useRef, useState } from 'react'
import { Button, Center, Image, Loader, Stack, Text } from '@mantine/core'
import { IconTrash } from '@tabler/icons-react'
import { useNavigate } from 'react-router-dom'
import { pathTo, RouteIDs } from '~/routing'
import { Request, Navigator, SessionSearch } from './components'
import PandaSvg from '~/assets/panda.svg'
import { useData } from '~/shared'

export const SideBar = (): React.JSX.Element => {
  const navigate = useNavigate()
  const { session, request, requests, removeAllRequests, loadMoreRequests, hasMoreRequests } = useData()
  const activeRequestRef = useRef<HTMLDivElement>(null)
  const sentinelRef = useRef<HTMLDivElement>(null)
  const [loadingMore, setLoadingMore] = useState<boolean>(false)

  // fetch the next (older) page when the user scrolls the sentinel into view (infinite scroll)
  const onSentinelVisible = useCallback(() => {
    if (loadingMore) {
      return
    }

    setLoadingMore(true)
    loadMoreRequests().finally(() => setLoadingMore(false))
  }, [loadingMore, loadMoreRequests])

  useEffect(() => {
    const el = sentinelRef.current

    if (!el || !hasMoreRequests || typeof IntersectionObserver === 'undefined') {
      return
    }

    // rootMargin pre-loads the next page slightly before the sentinel is fully on screen
    const observer = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) {
          onSentinelVisible()
        }
      },
      { rootMargin: '200px' }
    )

    observer.observe(el)

    return () => observer.disconnect()
  }, [hasMoreRequests, onSentinelVisible, requests.length])

  return (
    <Stack align="stretch" justify="flex-start" gap="xs">
      {session ? (
        <SessionSearch sessionUUID={session.sID}>
          {requests.length > 0 ? (
            <>
              <Navigator />

              {requests.map((rq) => {
                const isActive = request?.rID === rq.rID

                return (
                  <Request
                    sID={session.sID}
                    request={rq}
                    key={rq.rID}
                    isActive={isActive}
                    componentRef={isActive ? activeRequestRef : null}
                  />
                )
              })}

              {hasMoreRequests && (
                <Center ref={sentinelRef} py="xs" data-testid="requests-load-more">
                  <Loader color="dimmed" size="xs" mr={8} />
                  <Text c="dimmed" size="xs">
                    Loading older requests…
                  </Text>
                </Center>
              )}

              {requests.length > 1 && (
                <Center>
                  <Button
                    leftSection={<IconTrash size="1em" />}
                    size="compact-xs"
                    variant="outline"
                    color="red"
                    px="xs"
                    mb="sm"
                    radius="xl"
                    opacity={0.7}
                    onClick={() => {
                      removeAllRequests(session.sID)
                        .then((slow) => slow())
                        .then(() =>
                          // navigate to the session screen
                          navigate(pathTo(RouteIDs.SessionAndRequest, session.sID))
                        )
                    }}
                  >
                    Delete all requests
                  </Button>
                </Center>
              )}
            </>
          ) : (
            <NoRequests />
          )}
        </SessionSearch>
      ) : (
        <NoSession />
      )}
    </Stack>
  )
}

const NoRequests = (): React.JSX.Element => (
  <Stack gap="xs" h="100%" justify="space-between">
    <Center pt="2em">
      <Image src={PandaSvg} w="50%" />
    </Center>
    <Center>
      <Loader color="dimmed" size="1em" mr={8} mb={3} />
      <Text c="dimmed">Waiting for first request</Text>
    </Center>
  </Stack>
)

const NoSession = (): React.JSX.Element => (
  <Center pt="2em">
    <Loader color="dimmed" size="1em" mr={8} mb={3} />
    <Text c="dimmed">No session selected</Text>
  </Center>
)
