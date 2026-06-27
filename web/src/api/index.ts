export {
  Client,
  type SessionSummary,
  type SearchResultItem,
  type ReplayResult,
  type SessionPatch,
  type SearchMatch,
} from './client'
export { type APIError, APIErrorNotFound, APIErrorUnauthorized, APIErrorCommon, APIErrorUnknown } from './errors'
export { getStoredToken, setStoredToken, clearStoredToken, onUnauthorized, type TokenProvider } from './auth'
export { RequestEventAction } from './schema.gen'
