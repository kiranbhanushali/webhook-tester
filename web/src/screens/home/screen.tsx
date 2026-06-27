import React, { useEffect } from 'react'
import { useLocation, useNavigate } from 'react-router-dom'
import { pathTo, RouteIDs } from '~/routing'

/**
 * The index route. The dashboard is the app's home, so this screen redirects to `/dashboard` — except
 * it first honors v1-style anchor deep links (`#/:sID/:rID`) by redirecting to the session/request route,
 * preserving backward compatibility with old bookmarked URLs.
 */
export function HomeScreen(): React.JSX.Element {
  const navigate = useNavigate()
  const { hash } = useLocation()

  useEffect(() => {
    if (hash) {
      // v1 stored the current state (sID and rID) in the url hash as `#/:sID/:rID`; redirect those.
      const [sID, rID]: Array<string | undefined> = hash
        .replace(/^#\/+/, '')
        .split('/')
        .map((v) => v || undefined)
        .filter((v) => v && v.length === 36) // 36 characters is the length of a UUID

      if (sID && rID) {
        navigate(pathTo(RouteIDs.SessionAndRequest, sID, rID), { replace: true })

        return
      } else if (sID) {
        navigate(pathTo(RouteIDs.SessionAndRequest, sID), { replace: true })

        return
      }
    }

    // the dashboard is the default landing
    navigate(pathTo(RouteIDs.Dashboard), { replace: true })
  }, [hash, navigate])

  return <></>
}
