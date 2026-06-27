import React, { useState, useMemo } from 'react'
import {
  Button,
  Group,
  Modal,
  NumberInput,
  Switch,
  Text,
  Textarea,
  TextInput,
  Alert,
} from '@mantine/core'
import {
  IconCodeAsterisk,
  IconHeading,
  IconHourglassHigh,
  IconLink,
  IconTag,
  IconVersions,
  IconAlertCircle,
} from '@tabler/icons-react'
import { notifications as notify } from '@mantine/notifications'
import { useData, type Session } from '~/shared'
import { ScriptHelpButton } from './script-help'

/** Regex for a valid session slug: starts with [a-z0-9], followed by 1-48 chars of [a-z0-9-] */
const SLUG_REGEX = /^[a-z0-9][a-z0-9-]{1,48}$/

/** Validate a slug: empty is OK; non-empty must match the regex. */
const validateSlug = (v: string): boolean => v === '' || SLUG_REGEX.test(v)

/** Validate a forward URL: empty is OK; non-empty must be a valid http/https URL. */
const validateForwardUrl = (v: string): boolean => {
  if (!v) return true
  try {
    const u = new URL(v)
    return u.protocol === 'http:' || u.protocol === 'https:'
  } catch {
    return false
  }
}

/** Header validation limits (mirrors the create modal). */
const HEADER_LIMITS = {
  maxCount: 10,
  minNameLen: 1,
  maxNameLen: 40,
  maxValueLen: 2048,
}

/** Validate headers in the "Name: Value" per-line format. */
const validateHeaders = (text: string): boolean => {
  if (!text.trim()) return true

  const rows = headersTextToHeaders(text)

  return (
    rows.length <= HEADER_LIMITS.maxCount &&
    rows.every(
      (h) =>
        h.name.length >= HEADER_LIMITS.minNameLen &&
        h.name.length <= HEADER_LIMITS.maxNameLen &&
        h.value.length <= HEADER_LIMITS.maxValueLen &&
        /^[a-zA-Z0-9-]+$/i.test(h.name) &&
        /^[^\r\n]*$/i.test(h.value) &&
        h.name.trim().length > 0 &&
        h.value.trim().length > 0
    )
  )
}

/** Convert "Name: Value" text to an array of {name, value} objects. */
const headersTextToHeaders = (text: string): Array<{ name: string; value: string }> =>
  text
    .split('\n')
    .map((line) => line.trim())
    .filter((line) => line.length > 0)
    .map((line) => {
      const [name, ...valueParts] = line.split(': ')
      return { name: name.trim(), value: valueParts.join(': ').trim() }
    })

/** Convert an array of {name, value} objects to "Name: Value" text. */
const headersToText = (headers: ReadonlyArray<{ name: string; value: string }>): string =>
  headers.map((h) => `${h.name}: ${h.value}`).join('\n')

export const SessionEditor: React.FC<{
  session: Readonly<Session>
  opened: boolean
  onClose: () => void
}> = ({ session, opened, onClose }) => {
  const { updateSession } = useData()
  const [loading, setLoading] = useState<boolean>(false)
  const [slugConflictError, setSlugConflictError] = useState<string | null>(null)

  // Pre-fill all fields from the session
  const [slug, setSlug] = useState<string>(session.slug ?? '')
  const [group, setGroup] = useState<string>(session.group ?? '')
  const [statusCode, setStatusCode] = useState<number>(session.responseCode)
  const [headers, setHeaders] = useState<string>(headersToText(session.responseHeaders))
  const [delay, setDelay] = useState<number>(session.responseDelay)
  const [responseBody, setResponseBody] = useState<string>(
    session.responseBody.length > 0 ? new TextDecoder().decode(session.responseBody) : ''
  )
  const [securityHeaders, setSecurityHeaders] = useState<string>(headersToText(session.securityHeaders))
  const [forwardUrl, setForwardUrl] = useState<string>(session.forwardUrl ?? '')
  const [responseScript, setResponseScript] = useState<string>(session.responseScript ?? '')
  const [longLived, setLongLived] = useState<boolean>(session.longLived)

  // Validation
  const wrongSlug = useMemo(() => !validateSlug(slug), [slug])
  const wrongStatusCode = useMemo(() => statusCode < 100 || statusCode > 530, [statusCode])
  const wrongDelay = useMemo(() => delay < 0 || delay > 30, [delay])
  const wrongHeaders = useMemo(() => !validateHeaders(headers), [headers])
  const wrongSecurityHeaders = useMemo(() => !validateHeaders(securityHeaders), [securityHeaders])
  const wrongForwardUrl = useMemo(() => !validateForwardUrl(forwardUrl), [forwardUrl])

  const saveDisabled = useMemo(
    () =>
      wrongSlug ||
      wrongStatusCode ||
      wrongDelay ||
      wrongHeaders ||
      wrongSecurityHeaders ||
      wrongForwardUrl,
    [wrongSlug, wrongStatusCode, wrongDelay, wrongHeaders, wrongSecurityHeaders, wrongForwardUrl]
  )

  const handleSave = async (): Promise<void> => {
    if (saveDisabled) return

    setLoading(true)
    setSlugConflictError(null)

    const parsedHeaders = headersTextToHeaders(headers)
    const parsedSecHeaders = headersTextToHeaders(securityHeaders)

    try {
      await updateSession(session.sID, {
        slug: slug.trim() || undefined,
        group: group.trim() || undefined,
        statusCode,
        headers: parsedHeaders.length > 0 ? parsedHeaders : undefined,
        delay,
        responseBody: responseBody.trim().length > 0 ? new TextEncoder().encode(responseBody) : undefined,
        securityHeaders: parsedSecHeaders.length > 0 ? parsedSecHeaders : undefined,
        forwardUrl: forwardUrl.trim() || undefined,
        responseScript: responseScript.trim() || undefined,
        longLived,
      })

      notify.show({
        title: 'Session updated',
        message: null,
        color: 'green',
        autoClose: 4000,
      })

      onClose()
    } catch (err: unknown) {
      // Surface 409 (slug already taken) as a field-level error
      const isConflict =
        (typeof err === 'object' &&
          err !== null &&
          'response' in err &&
          typeof (err as { response: unknown }).response === 'object' &&
          (err as { response: { status: number } }).response?.status === 409)

      if (isConflict) {
        setSlugConflictError('This slug is already taken — choose a different one')
      } else {
        notify.show({
          title: 'Failed to update session',
          message: String(err),
          color: 'red',
          autoClose: 6000,
        })
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="md"
      overlayProps={{ backgroundOpacity: 0.55, blur: 3 }}
      title={
        <Text size="lg" fw={700}>
          Edit Session
        </Text>
      }
      centered
    >
      {/* ── Slug conflict error ──────────────────────────────────── */}
      {slugConflictError && (
        <Alert icon={<IconAlertCircle size="1em" />} color="red" mb="sm">
          {slugConflictError}
        </Alert>
      )}

      {/* ── Slug ─────────────────────────────────────────────────── */}
      <TextInput
        my="sm"
        label="Slug"
        description="Custom URL slug (leave blank to keep auto-generated). Pattern: [a-z0-9][a-z0-9-]{1,48}"
        placeholder="my-webhook"
        leftSection={<IconTag size="1em" />}
        error={
          wrongSlug
            ? 'Slug must start with a lowercase letter or digit and contain only a-z, 0-9 and -'
            : undefined
        }
        disabled={loading}
        value={slug}
        onChange={(e) => {
          setSlug(e.currentTarget.value)
          setSlugConflictError(null)
        }}
      />

      {/* ── Group ────────────────────────────────────────────────── */}
      <TextInput
        my="sm"
        label="Group"
        description="Optional group name for organising sessions"
        placeholder="team-a"
        disabled={loading}
        value={group}
        onChange={(e) => setGroup(e.currentTarget.value)}
      />

      {/* ── Status code ──────────────────────────────────────────── */}
      <NumberInput
        my="sm"
        label="Default status code"
        description="The default status code for the URL"
        placeholder="200"
        allowDecimal={false}
        leftSection={<IconCodeAsterisk />}
        min={100}
        max={530}
        error={wrongStatusCode}
        disabled={loading}
        value={statusCode}
        onChange={(v: string | number): void => setStatusCode(typeof v === 'string' ? parseInt(v, 10) : v)}
      />

      {/* ── Response headers ─────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Response headers"
        description={`Headers to include in the response (one per line, max ${HEADER_LIMITS.maxCount})`}
        placeholder={'Content-Type: application/json\nServer: WebhookTester'}
        leftSection={<IconHeading />}
        styles={{ input: { fontFamily: 'monospace', fontSize: '0.9em' } }}
        minRows={2}
        maxRows={10}
        error={wrongHeaders}
        disabled={loading}
        value={headers}
        onChange={(e) => setHeaders(e.currentTarget.value)}
        autosize
      />

      {/* ── Response delay ───────────────────────────────────────── */}
      <NumberInput
        my="sm"
        label="Response delay"
        description="The delay in seconds before the response is sent"
        placeholder="0"
        allowDecimal={false}
        leftSection={<IconHourglassHigh />}
        min={0}
        max={30}
        error={wrongDelay}
        disabled={loading}
        value={delay}
        onChange={(v: string | number): void => setDelay(typeof v === 'string' ? parseInt(v, 10) : v)}
      />

      {/* ── Response body ─────────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Response body"
        description="The content of the response"
        placeholder={'{"message": "Hello, World!"}'}
        leftSection={<IconVersions />}
        styles={{ input: { fontFamily: 'monospace', fontSize: '0.9em' } }}
        minRows={1}
        maxRows={15}
        disabled={loading}
        value={responseBody}
        onChange={(e) => setResponseBody(e.currentTarget.value)}
        autosize
      />

      {/* ── Security headers ─────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Security headers"
        description={`Extra headers added to every response (one per line, max ${HEADER_LIMITS.maxCount})`}
        placeholder={'X-Frame-Options: DENY\nX-Content-Type-Options: nosniff'}
        styles={{ input: { fontFamily: 'monospace', fontSize: '0.9em' } }}
        minRows={2}
        maxRows={8}
        error={wrongSecurityHeaders}
        disabled={loading}
        value={securityHeaders}
        onChange={(e) => setSecurityHeaders(e.currentTarget.value)}
        autosize
      />

      {/* ── Forward URL ──────────────────────────────────────────── */}
      <TextInput
        my="sm"
        label="Forward URL"
        description="Forward incoming requests to this URL (optional)"
        placeholder="https://example.com/webhook"
        leftSection={<IconLink size="1em" />}
        error={wrongForwardUrl ? 'Must be a valid http:// or https:// URL' : undefined}
        disabled={loading}
        value={forwardUrl}
        onChange={(e) => setForwardUrl(e.currentTarget.value)}
      />

      {/* ── Response script ──────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Response script"
        description={
          <Group gap={4} align="center">
            <Text size="xs" component="span">
              Go text/template script for dynamic responses (optional)
            </Text>
            <ScriptHelpButton />
          </Group>
        }
        placeholder={'@status 200\n{{ .Body }}'}
        styles={{ input: { fontFamily: 'monospace', fontSize: '0.9em' } }}
        minRows={2}
        maxRows={15}
        disabled={loading}
        value={responseScript}
        onChange={(e) => setResponseScript(e.currentTarget.value)}
        autosize
      />

      {/* ── Long-lived ───────────────────────────────────────────── */}
      <Switch
        my="sm"
        label="Long-lived session"
        description="If enabled, the session does not expire on the normal TTL"
        disabled={loading}
        checked={longLived}
        onChange={(e) => setLongLived(e.currentTarget.checked)}
      />

      <Group mt="xl" justify="flex-end">
        <Button variant="default" onClick={onClose} disabled={loading}>
          Cancel
        </Button>
        <Button
          variant="filled"
          color="blue"
          size="md"
          radius="xl"
          onClick={() => void handleSave()}
          disabled={saveDisabled}
          loading={loading}
        >
          Save
        </Button>
      </Group>
    </Modal>
  )
}
