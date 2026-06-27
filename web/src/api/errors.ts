interface APIError extends Error {
  readonly response?: Response
  readonly description: string
}

abstract class BaseAPIError extends Error implements APIError {
  public readonly response?: Response
  public abstract readonly description: string

  constructor({ message, response }: { message?: string; response?: Response } = {}) {
    super(message)

    this.response = response
  }
}

class APIErrorNotFound extends BaseAPIError {
  public readonly description = 'Not found'
}

class APIErrorUnauthorized extends BaseAPIError {
  public readonly description = 'Authentication is required (or the provided token is invalid)'
}

class APIErrorCommon extends BaseAPIError {
  public readonly description = "Something went wrong on the server side, but we can't identify it as a specific error"
}

class APIErrorUnknown extends BaseAPIError {
  public readonly description =
    "Something went wrong, and we don't know what (usually on the client or JS libraries side)"
}

class APIErrorConflict extends BaseAPIError {
  public readonly description = 'Conflict (e.g. the requested slug is already taken)'
}

/**
 * Narrow an unknown caught error to its HTTP status code, if any.
 *
 * Reuses the unknown+narrow pattern: an {@link APIError} carries the originating `Response`,
 * whose `status` is the HTTP status. Returns `null` for anything that is not a recognisable
 * HTTP error (network failures, thrown strings, etc.).
 */
const httpStatusFromError = (err: unknown): number | null => {
  if (
    typeof err === 'object' &&
    err !== null &&
    'response' in err &&
    typeof (err as { response: unknown }).response === 'object' &&
    (err as { response: unknown }).response !== null
  ) {
    const status = (err as { response: { status?: unknown } }).response.status

    return typeof status === 'number' ? status : null
  }

  return null
}

export {
  type APIError,
  APIErrorNotFound,
  APIErrorUnauthorized,
  APIErrorCommon,
  APIErrorUnknown,
  APIErrorConflict,
  httpStatusFromError,
}
