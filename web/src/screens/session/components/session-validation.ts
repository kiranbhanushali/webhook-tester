/**
 * Shared validation helpers for the session create + edit forms. Single source of truth so the create modal and the
 * editor cannot diverge.
 */

/** Regex for a valid session slug: starts with [a-z0-9], followed by 1-48 chars of [a-z0-9-]. */
export const SLUG_REGEX = /^[a-z0-9][a-z0-9-]{1,48}$/

/** Limits applied to both response headers and security headers (one "Name: Value" per line). */
export const HEADER_LIMITS = {
  maxCount: 10,
  minNameLen: 1,
  maxNameLen: 40,
  maxValueLen: 2048,
} as const

/** Validate a slug: empty is OK (server auto-generates / keeps current); non-empty must match the regex. */
export const validateSlug = (v: string): boolean => v === '' || SLUG_REGEX.test(v)

/** Validate a forward URL: empty is OK; non-empty must be a valid http/https URL. */
export const validateForwardUrl = (v: string): boolean => {
  if (!v) {
    return true
  }

  try {
    const u = new URL(v)

    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/** Convert "Name: Value" text (one per line) to an array of {name, value} objects. */
export const headersTextToHeaders = (text: string): Array<{ name: string; value: string }> =>
  text
    .split('\n') // split by each line
    .map((line) => line.trim()) // trim each line
    .filter((line) => line.length > 0) // filter out empty lines
    .map((line) => {
      const [name, ...valueParts] = line.split(': ')
      const value = valueParts.join(': ') // join in case of additional colons in value

      return { name: name.trim(), value: value.trim() }
    })

/** Convert an array of {name, value} objects to "Name: Value" text (one per line). */
export const headersToText = (headers: ReadonlyArray<{ name: string; value: string }>): string =>
  headers.map((h) => `${h.name}: ${h.value}`).join('\n')

/**
 * Validate inbound-auth: if a header name is provided, the value must also be non-empty.
 * Both blank = public endpoint = valid.
 */
export const validateInboundAuth = (header: string, value: string): boolean => {
  if (!header.trim()) {
    return true // no auth header set → public endpoint → valid
  }

  return value.trim().length > 0 // header set, so value is required
}

/** Validate header text (one "Name: Value" per line). Empty text is valid. */
export const validateHeadersText = (text: string): boolean => {
  if (!text.trim()) {
    return true
  }

  const rows = headersTextToHeaders(text)

  return (
    rows.length <= HEADER_LIMITS.maxCount && // check the count of headers
    rows.every(
      (h) =>
        h.name.length >= HEADER_LIMITS.minNameLen && // check the name length (min)
        h.name.length <= HEADER_LIMITS.maxNameLen && // check the name length (max)
        h.value.length <= HEADER_LIMITS.maxValueLen && // check the value length (max)
        /^[a-zA-Z0-9-]+$/i.test(h.name) &&
        /^[^\r\n]*$/i.test(h.value) && // check the header name and value format
        h.name.trim().length > 0 &&
        h.value.trim().length > 0 // check the header name and value are not empty
    )
  )
}
