import React, { useCallback, useEffect, useRef } from 'react'
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

  // Re-entrancy guard. Set synchronously BEFORE the fetch so two observer callbacks fired in the same
  // tick (e.g. the sentinel staying visible) cannot start two overlapping loads. A state flag alone
  // races: setState is async, so both callbacks would still read the stale `false`.
  const loadingRef = useRef<boolean>(false)

  // Keep the latest loader + flag in refs so the observer can be created ONCE (see the effect below)
  // and read fresh values without re-subscribing on every appended page.
  const loadMoreRef = useRef(loadMoreRequests)
  loadMoreRef.current = loadMoreRequests
  const hasMoreRef = useRef(hasMoreRequests)
  hasMoreRef.current = hasMoreRequests

  // fetch the next (older) page when the user scrolls the sentinel into view (infinite scroll)
  const onSentinelVisible = useCallback(() => {
    if (loadingRef.current || !hasMoreRef.current) {
      return
    }

    loadingRef.current = true
    loadMoreRef.current().finally(() => {
      loadingRef.current = false
    })
  }, [])

  useEffect(() => {
    const el = sentinelRef.current

    if (!el || typeof IntersectionObserver === 'undefined') {
      return
    }

    // Observe the sentinel ONCE. We deliberately do NOT depend on `requests.length`: recreating the
    // observer per appended page makes `observe()` re-fire for the still-visible sentinel → load →
    // append → recreate → fire → infinite pagination + "Maximum update depth exceeded" (#185). The
    // single observer fires naturally only when the user actually scrolls the sentinel into view.
    // `hasMoreRequests` is in the deps because the sentinel element only mounts while it is true.
    // rootMargin pre-loads the next page slightly before the sentinel is fully on screen.
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
  }, [hasMoreRequests, onSentinelVisible])

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
