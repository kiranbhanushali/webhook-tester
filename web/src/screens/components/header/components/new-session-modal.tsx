import React, { useState, useMemo } from 'react'
import { Button, Checkbox, Group, Modal, NumberInput, Space, Switch, Text, Textarea, TextInput } from '@mantine/core'
import {
  IconCodeAsterisk,
  IconHeading,
  IconHourglassHigh,
  IconLink,
  IconTag,
  IconVersions,
} from '@tabler/icons-react'
import { notifications as notify } from '@mantine/notifications'
import { useNavigate } from 'react-router-dom'
import { useStorage, UsedStorageKeys, type StorageArea, useSettings, useData } from '~/shared'
import { pathTo, RouteIDs } from '~/routing'
import { ScriptHelpButton } from '~/screens/session/components/script-help'

/** Regex for a valid session slug: starts with [a-z0-9], followed by 1-48 chars of [a-z0-9-] */
const SLUG_REGEX = /^[a-z0-9][a-z0-9-]{1,48}$/

/** Controls for the new session modal */
const controls = {
  // status code
  code: {
    def: 200,
    limits: { min: 200, max: 530 },
    key: UsedStorageKeys.NewSessionStatusCode,
    area: 'session' satisfies StorageArea as StorageArea,
  },
  // response headers
  head: {
    def: 'Content-Type: application/json\nServer: WebhookTester',
    limits: { maxCount: 10, minNameLen: 1, maxNameLen: 40, maxValueLen: 2048 },
    key: UsedStorageKeys.NewSessionHeadersList,
    area: 'session' satisfies StorageArea as StorageArea,
  },
  // response delay
  delay: {
    def: 0,
    limits: { min: 0, max: 30 },
    key: UsedStorageKeys.NewSessionSessionDelay,
    area: 'session' satisfies StorageArea as StorageArea,
  },
  // response body
  body: {
    def: '{"captured": true}',
    key: UsedStorageKeys.NewSessionResponseBody,
    area: 'session' satisfies StorageArea as StorageArea,
  },
  // destroy current session
  destroy: {
    def: true,
    key: UsedStorageKeys.NewSessionDestroyCurrentSession,
    area: 'session' satisfies StorageArea as StorageArea,
  },
}

/** Shared header validation limits (reused for security headers). */
const HEADER_LIMITS = controls.head.limits

/** Validation functions for the controls */
const validate: { [K in keyof typeof controls]: (v: unknown) => boolean } = {
  code: (v) => typeof v === 'number' && v >= controls.code.limits.min && v <= controls.code.limits.max,
  head: (v) => {
    if (typeof v !== 'string') {
      return false
    }

    const raw = headersTextToHeaders(v) // convert the text to headers

    return (
      raw.length <= HEADER_LIMITS.maxCount && // check the count of headers
      raw.every(
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
  },
  delay: (v) => typeof v === 'number' && v >= controls.delay.limits.min && v <= controls.delay.limits.max,
  body: (v) => typeof v === 'string',
  destroy: (v) => typeof v === 'boolean',
}

/** Validate a slug: empty is OK (server auto-generates); non-empty must match the regex. */
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

export const NewSessionModal: React.FC<{
  opened: boolean
  onClose: () => void
}> = ({ opened, onClose }) => {
  const navigate = useNavigate()
  const { maxRequestBodySize: maxBodySize } = useSettings()
  const { session, newSession, destroySession } = useData()
  const [loading, setLoading] = useState<boolean>(false)

  // --- persisted fields (existing) ---
  const [status, setStatus] = useStorage<number>(controls.code.def, controls.code.key, controls.code.area)
  const [headers, setHeaders] = useStorage<string>(controls.head.def, controls.head.key, controls.head.area)
  const [delay, setDelay] = useStorage<number>(controls.delay.def, controls.delay.key, controls.delay.area)
  const [body, setBody] = useStorage<string>(controls.body.def, controls.body.key, controls.body.area)
  const [destroy, setDestroy] = useStorage<boolean>(controls.destroy.def, controls.destroy.key, controls.destroy.area)

  // --- new fields (local state, not persisted) ---
  const [slug, setSlug] = useState<string>('')
  const [group, setGroup] = useState<string>('')
  const [forwardUrl, setForwardUrl] = useState<string>('')
  const [longLived, setLongLived] = useState<boolean>(false)
  const [securityHeaders, setSecurityHeaders] = useState<string>('')
  const [responseScript, setResponseScript] = useState<string>('')

  // --- validation ---
  const wrongStatusCode = useMemo(() => !validate.code(status), [status])
  const wrongHeaders = useMemo(() => !validate.head(headers), [headers])
  const wrongDelay = useMemo(() => !validate.delay(delay), [delay])
  const wrongResponseBody = useMemo(() => {
    let bodyIsValid = validate.body(body)

    // if max body size is set and the body is valid, check the body length
    if (!!maxBodySize && bodyIsValid) {
      bodyIsValid = body.length <= maxBodySize
    }

    return !bodyIsValid
  }, [body, maxBodySize])

  const wrongSlug = useMemo(() => !validateSlug(slug), [slug])
  const wrongForwardUrl = useMemo(() => !validateForwardUrl(forwardUrl), [forwardUrl])
  const wrongSecurityHeaders = useMemo(() => {
    if (!securityHeaders.trim()) return false // empty is fine
    return !validate.head(securityHeaders) // reuse response-header validation
  }, [securityHeaders])

  const createDisabled = useMemo(
    () =>
      wrongStatusCode ||
      wrongHeaders ||
      wrongDelay ||
      wrongResponseBody ||
      wrongSlug ||
      wrongForwardUrl ||
      wrongSecurityHeaders,
    [wrongStatusCode, wrongHeaders, wrongDelay, wrongResponseBody, wrongSlug, wrongForwardUrl, wrongSecurityHeaders]
  )

  /** Handle the creation of a new session */
  const handleCreate = () => {
    // if any of the fields are invalid, return (kinda fuse)
    if (createDisabled) {
      return
    }

    // cook the response headers (convert text to an array and then to object)
    const respHeaders: { [k: string]: string } = Object.fromEntries(
      headersTextToHeaders(headers).map((h) => [h.name, h.value])
    )

    // cook the security headers
    const parsedSecurityHeaders = securityHeaders.trim() ? headersTextToHeaders(securityHeaders) : []

    // set the loading state
    setLoading(true)

    const id = notify.show({ title: 'Creating new WebHook', message: null, autoClose: false, loading: true })

    // create the new session
    newSession({
      statusCode: status,
      headers: Object.keys(respHeaders).length > 0 ? respHeaders : undefined,
      delay: delay > 0 ? delay : undefined,
      responseBody: body.trim().length > 0 ? new TextEncoder().encode(body) : undefined,
      slug: slug.trim() || undefined,
      group: group.trim() || undefined,
      responseScript: responseScript.trim() || undefined,
      securityHeaders: parsedSecurityHeaders.length > 0 ? parsedSecurityHeaders : undefined,
      forwardUrl: forwardUrl.trim() || undefined,
      longLived: longLived || undefined,
    })
      .then((opts) => {
        notify.update({
          id,
          title: 'A new WebHook has been created!',
          message: null,
          color: 'green',
          autoClose: 7000,
          loading: false,
        })

        if (destroy && !!session) {
          destroySession(session.sID)
            .then((slow) => slow())
            .catch((err) => {
              notify.show({
                title: 'Failed to destroy current WebHook',
                message: String(err),
                color: 'red',
                autoClose: 5000,
              })
            })
        }

        onClose()

        navigate(pathTo(RouteIDs.SessionAndRequest, opts.sID))
      })
      .catch((err) => {
        notify.update({
          id,
          title: 'Failed to create new WebHook',
          message: String(err),
          color: 'red',
          loading: false,
        })
      })
      .finally(() => setLoading(false))
  }

  return (
    <Modal
      opened={opened}
      onClose={onClose}
      size="md"
      overlayProps={{ backgroundOpacity: 0.55, blur: 3 }}
      title={
        <Text size="lg" fw={700}>
          Configure URL
        </Text>
      }
      centered
    >
      <Text size="xs">
        You have the ability to customize how your URL will respond by changing the status code, headers, response delay
        and the content.
      </Text>
      <Space h="sm" />

      {/* ── Slug ─────────────────────────────────────────────────── */}
      <TextInput
        my="sm"
        label="Slug"
        description='Custom URL slug (leave blank to auto-generate). Pattern: [a-z0-9][a-z0-9-]{1,48}'
        placeholder="my-webhook"
        leftSection={<IconTag size="1em" />}
        error={wrongSlug ? 'Slug must start with a lowercase letter or digit and contain only a-z, 0-9 and -' : undefined}
        disabled={loading}
        value={slug}
        onChange={(e) => setSlug(e.currentTarget.value)}
      />

      {/* ── Group ────────────────────────────────────────────────── */}
      <TextInput
        my="sm"
        label="Group"
        description="Optional group name for organising sessions"
        placeholder="team-a"
        error={false}
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
        min={controls.code.limits.min}
        max={controls.code.limits.max}
        error={wrongStatusCode}
        disabled={loading}
        value={status}
        onChange={(v: string | number): void => setStatus(typeof v === 'string' ? parseInt(v, 10) : v)}
      />

      {/* ── Response headers ─────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Response headers"
        description={`The list of headers to include in the response (one per line, max ${controls.head.limits.maxCount})`}
        placeholder={'Content-Type: application/json\nServer: WebhookTester\nX-Request-Id: 3C27:3A7ABF:250756C'}
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
        min={controls.delay.limits.min}
        max={controls.delay.limits.max}
        error={wrongDelay}
        disabled={loading}
        value={delay}
        onChange={(v: string | number): void => setDelay(typeof v === 'string' ? parseInt(v, 10) : v)}
      />

      {/* ── Response body ─────────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Response body"
        description={`The content of the response${!!maxBodySize && maxBodySize > 0 && ` (max ${new Intl.NumberFormat().format(maxBodySize)} characters)`}`}
        placeholder={'{"message": "Hello, World!"}'}
        leftSection={<IconVersions />}
        styles={{ input: { fontFamily: 'monospace', fontSize: '0.9em' } }}
        minRows={1}
        maxRows={15}
        error={wrongResponseBody}
        disabled={loading}
        value={body}
        onChange={(e) => setBody(e.currentTarget.value)}
        autosize
      />

      {/* ── Security headers ─────────────────────────────────────── */}
      <Textarea
        my="sm"
        label="Security headers"
        description={`Extra headers added to every response for this session (one per line, max ${HEADER_LIMITS.maxCount})`}
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

      <Group mt="xl" justify="space-between">
        <Checkbox
          my="sm"
          label="Destroy current session"
          disabled={loading}
          checked={destroy}
          onChange={(e) => setDestroy(e.currentTarget.checked)}
        />
        <Button
          variant="filled"
          color="green"
          size="md"
          radius="xl"
          onClick={handleCreate}
          disabled={createDisabled}
          loading={loading}
          data-autofocus
        >
          Create
        </Button>
      </Group>
    </Modal>
  )
}

/** Convert text to headers */
const headersTextToHeaders = (text: string): Array<{ name: string; value: string }> =>
  text
    .split('\n') // split by each line
    .map((line) => line.trim()) // trim each line
    .filter((line) => line.length > 0) // filter out empty lines
    .map((line) => {
      const [name, ...valueParts] = line.split(': ')
      const value = valueParts.join(': ') // join in case of additional colons in value

      return { name: name.trim(), value: value.trim() }
    })
