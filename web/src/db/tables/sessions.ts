import { Table } from 'dexie'

export type Session = {
  /** Internal, stable primary key — the session UUID (used for all API/WebSocket calls). */
  sID: string
  responseCode: number
  responseHeaders: Array<{ name: string; value: string }>
  responseDelay: number
  responseBody: Uint8Array
  createdAt: Date
  /** Human-readable slug (the user-facing identifier; also used to build the `/w/{slug}` webhook URL). */
  slug?: string
  group?: string | null
  responseScript?: string | null
  securityHeaders?: Array<{ name: string; value: string }>
  forwardUrl?: string | null
  longLived?: boolean
  /** Header name callers must include for inbound auth; absent = public endpoint. */
  inboundAuthHeader?: string
  /** Expected secret value for the inbound auth header. */
  inboundAuthValue?: string
}

export type SessionsTable = Table<Session, string>

/**
 * Dexie schema for the sessions table.
 *
 * `sID` (uuid) stays the primary key so existing keying/relations (requests are keyed by `sID`) keep working untouched.
 * `slug` is added as a secondary index so sessions can also be looked up by their user-facing slug.
 */
export const sessionsSchema = {
  sessions: '&sID, slug',
}
