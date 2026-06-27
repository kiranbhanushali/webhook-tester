import { Table } from 'dexie'

export type Request = {
  sID: string
  rID: string
  clientAddress: string
  method: string
  headers: Array<{ name: string; value: string }>
  url: string
  payload: Uint8Array | null
  capturedAt: Date
  /** False when the request was rejected by inbound auth. Absent for WS-pushed requests until re-fetched from API. */
  authorized?: boolean
}

export type RequestsTable = Table<Request, string>

export const requestsSchema = {
  requests: '&rID, sID',
}
