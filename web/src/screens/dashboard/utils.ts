import type { MantineColor } from '@mantine/core'
import type { FirehoseEvent, SessionSummary } from '~/api'

/** Stable palette used to color-code session slugs across the rail and the live stream. */
const SLUG_COLORS: ReadonlyArray<MantineColor> = [
  'blue',
  'grape',
  'teal',
  'orange',
  'cyan',
  'pink',
  'indigo',
  'lime',
  'violet',
  'green',
  'red',
  'yellow',
]

/**
 * Maps a session slug to a stable color from {@link SLUG_COLORS} (deterministic FNV-ish hash), so the
 * same endpoint always renders in the same color in the rail and the stream.
 */
export const slugColor = (slug: string): MantineColor => {
  let hash = 0

  for (let i = 0; i < slug.length; i++) {
    hash = (hash * 31 + slug.charCodeAt(i)) >>> 0
  }

  return SLUG_COLORS[hash % SLUG_COLORS.length]
}

/** Maps an HTTP status code to a Mantine color (5xx red, 4xx orange, 3xx blue, 2xx green, else gray). */
export const statusCodeToColor = (code: number): MantineColor => {
  if (code >= 500) return 'red'
  if (code >= 400) return 'orange'
  if (code >= 300) return 'blue'
  if (code >= 200) return 'green'

  return 'gray'
}

/**
 * Derives the HTTP status the server returned for a captured webhook. The firehose event does not carry
 * the response code, so: a rejected (inbound-auth-failed) capture always returns 401; otherwise we fall
 * back to the originating session's configured default status code. Returns null when unknown.
 */
export const returnedStatus = (
  event: FirehoseEvent,
  sessionByUUID: ReadonlyMap<string, SessionSummary>
): number | null => {
  if (event.request?.authorized === false) {
    return 401
  }

  const session = sessionByUUID.get(event.sessionUUID)

  return session ? session.statusCode : null
}

/**
 * A short, human-readable "peek" for a stream row. The firehose intentionally omits the request body and
 * headers (for performance and to avoid shipping inbound-auth secrets), so the peek is the captured path
 * + query — the most informative signal available without re-fetching the full request.
 */
export const requestPeek = (event: FirehoseEvent): string => {
  if (!event.request) {
    return ''
  }

  const { pathname, search } = event.request.url

  return `${pathname}${search}` || '/'
}
