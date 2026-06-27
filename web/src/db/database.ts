import { Dexie } from 'dexie'
import { SessionsTable, Session, sessionsSchema, RequestsTable, Request, requestsSchema } from './tables'

export class Database {
  public dexie: Dexie
  private readonly sessions: SessionsTable
  private readonly requests: RequestsTable

  constructor() {
    // create database
    this.dexie = new Dexie('webhook-tester-v2-db') // https://dexie.org/docs/Typescript

    // v1: the original schema (sessions keyed by `&sID`, requests by `&rID, sID`)
    this.dexie.version(1).stores({ sessions: '&sID', ...requestsSchema })

    // v2: adds the `slug` secondary index to the sessions table for the new slug-aware fields. The non-indexed fields
    // (slug, group, responseScript, securityHeaders, forwardUrl, longLived) need no schema change (Dexie is schemaless
    // for non-indexed props); the upgrade backfills sane defaults so older rows have a consistent shape.
    this.dexie
      .version(2)
      .stores({ ...sessionsSchema })
      .upgrade((tx) =>
        tx
          .table<Session, string>('sessions')
          .toCollection()
          .modify((session) => {
            session.slug ??= undefined
            session.group ??= null
            session.responseScript ??= null
            session.securityHeaders ??= []
            session.forwardUrl ??= null
            session.longLived ??= false
          })
      )

    // assign tables
    this.sessions = this.dexie.table('sessions')
    this.requests = this.dexie.table('requests')
  }

  /**
   * Insert a new session (the existing session with the same sID will be replaced).
   */
  async putSession(...data: Array<Session>): Promise<void> {
    if (data.length === 0) {
      return
    }

    await this.dexie.transaction('rw', this.sessions, async () => {
      await this.sessions.bulkPut(data)
    })
  }

  /**
   * Get all available session IDs, ordered by creation date from the newest to the oldest.
   */
  async getSessionIDs(): Promise<Array<string>> {
    return this.dexie.transaction('r', this.sessions, async () => {
      return (await this.sessions.toCollection().sortBy('createdAt')).reverse().map((session) => session.sID)
    })
  }

  /**
   * Get the session by sID.
   */
  async getSession(sID: string): Promise<Session | null> {
    return this.dexie.transaction('r', this.sessions, async () => {
      return (await this.sessions.get(sID)) || null
    })
  }

  /**
   * Get a session by its slug (the user-facing identifier), or null if no locally-known session has that slug.
   */
  async getSessionBySlug(slug: string): Promise<Session | null> {
    if (!slug) {
      return null
    }

    return this.dexie.transaction('r', this.sessions, async () => {
      return (await this.sessions.where('slug').equals(slug).first()) || null
    })
  }

  /**
   * Get all session requests, ordered by creation date from the newest to the oldest.
   */
  async getSessionRequests(sID: string): Promise<Array<Request>> {
    return this.dexie.transaction('r', this.requests, async () => {
      return (await this.requests.where('sID').equals(sID).sortBy('capturedAt')).reverse()
    })
  }

  /**
   * Delete session (and all requests associated with it).
   */
  async deleteSession(...sID: Array<string>): Promise<void> {
    if (sID.length === 0) {
      return
    }

    await this.dexie.transaction('rw', this.sessions, this.requests, async () => {
      await this.sessions.bulkDelete(sID)
      await this.requests.where('sID').anyOf(sID).delete()
    })
  }

  /**
   * Insert a new request (the existing request with the same rID will be replaced).
   */
  async putRequest(...data: Array<Request>): Promise<void> {
    if (data.length === 0) {
      return
    }

    await this.dexie.transaction('rw', this.requests, async () => {
      await this.requests.bulkPut(data)
    })
  }

  /**
   * Get a request by rID.
   */
  async getRequest(rID: string): Promise<Request | null> {
    return this.dexie.transaction('r', this.requests, async () => {
      return (await this.requests.get(rID)) || null
    })
  }

  /**
   * Delete requests by rID.
   */
  async deleteRequest(...rID: Array<string>): Promise<void> {
    if (rID.length === 0) {
      return
    }

    await this.dexie.transaction('rw', this.requests, async () => {
      await this.requests.bulkDelete(rID)
    })
  }

  /**
   * Delete all requests associated with a session.
   */
  async deleteAllRequests(sID: string): Promise<void> {
    await this.dexie.transaction('rw', this.requests, async () => {
      await this.requests.where('sID').equals(sID).delete()
    })
  }
}
