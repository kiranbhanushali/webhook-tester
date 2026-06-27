/**
 * The reserved URL prefix under which incoming webhooks are captured by the server.
 *
 * The backend serves the capture endpoint at `**\/w/{slug}/...` (slug OR uuid is accepted), so every
 * copyable webhook URL shown to the user MUST live under this prefix — never the bare reference.
 */
export const WEBHOOK_URL_PREFIX = 'w'

/**
 * Builds the user-facing, copyable webhook URL: `{origin}/w/{ref}`.
 *
 * @param base The public URL root (e.g. from the server settings) or `null` to use the current window origin.
 * @param ref  The session reference to capture under — the slug (preferred) or, as a fallback, the uuid. Both are
 *             accepted by the `/w/` capture endpoint.
 */
export const buildWebhookUrl = (base: Readonly<URL> | null, ref: string): URL => {
  const root = base ? new URL(base.toString()) : new URL(window.location.origin)

  // ensure a trailing slash so the relative `w/{ref}` part is appended (not used to replace the last path segment)
  if (!root.pathname.endsWith('/')) {
    root.pathname = `${root.pathname}/`
  }

  return new URL(`${WEBHOOK_URL_PREFIX}/${encodeURIComponent(ref)}`, root)
}
