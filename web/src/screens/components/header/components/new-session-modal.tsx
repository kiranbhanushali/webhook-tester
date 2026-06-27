import React, { useState, useMemo } from 'react'
import {
  Accordion,
  Button,
  Checkbox,
  Group,
  Modal,
  NumberInput,
  PasswordInput,
  Space,
  Switch,
  Text,
  Textarea,
  TextInput,
} from '@mantine/core'
import {
  IconCodeAsterisk,
  IconHeading,
  IconHourglassHigh,
  IconLink,
  IconLock,
  IconTag,
  IconVersions,
} from '@tabler/icons-react'
import { notifications as notify } from '@mantine/notifications'
import { useNavigate } from 'react-router-dom'
import { useStorage, UsedStorageKeys, type StorageArea, useSettings, useData } from '~/shared'
import { httpStatusFromError } from '~/api'
import { pathTo, RouteIDs } from '~/routing'
import { ScriptHelpButton } from '~/screens/session/components/script-help'
import {
  HEADER_LIMITS,
  headersTextToHeaders,
  validateForwardUrl,
  validateHeadersText,
  validateInboundAuth,
  validateSlug,
} from '~/screens/session/components/session-validation'

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
    limits: HEADER_LIMITS,
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

/** Validation functions for the controls */
const validate: { [K in keyof typeof controls]: (v: unknown) => boolean } = {
  code: (v) => typeof v === 'number' && v >= controls.code.limits.min && v <= controls.code.limits.max,
  head: (v) => typeof v === 'string' && validateHeadersText(v),
  delay: (v) => typeof v === 'number' && v >= controls.delay.limits.min && v <= controls.delay.limits.max,
  body: (v) => typeof v === 'string',
  destroy: (v) => typeof v === 'boolean',
}

export const NewSessionModal: React.FC<{
  opened: boolean
  onClose: () => void
}> = ({ opened, onClose }) => {
  const navigate = useNavigate()
  const { maxRequestBodySize: maxBodySize } = useSettings()
  const { session, newSession, destroySession } = useData()
  const [loading, setLoading] = useState<boolean>(false)
  // Server-side field errors surfaced on submit (parallel to the editor's slugConflictError).
  const [slugConflictError, setSlugConflictError] = useState<string | null>(null)
  const [inboundAuthError, setInboundAuthError] = useState<string | null>(null)

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
  const [inboundAuthHeader, setInboundAuthHeader] = useState<string>('')
  const [inboundAuthValue, setInboundAuthValue] = useState<string>('')

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
  const wrongSecurityHeaders = useMemo(() => !validateHeadersText(securityHeaders), [securityHeaders])
  const wrongInboundAuth = useMemo(
    () => !validateInboundAuth(inboundAuthHeader, inboundAuthValue),
    [inboundAuthHeader, inboundAuthValue]
  )

  const createDisabled = useMemo(
    () =>
      wrongStatusCode ||
      wrongHeaders ||
      wrongDelay ||
      wrongResponseBody ||
      wrongSlug ||
      wrongForwardUrl ||
      wrongSecurityHeaders ||
      wrongInboundAuth,
    [
      wrongStatusCode,
      wrongHeaders,
      wrongDelay,
      wrongResponseBody,
      wrongSlug,
      wrongForwardUrl,
      wrongSecurityHeaders,
      wrongInboundAuth,
    ]
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

    // Symmetric inbound-auth: a secret value is meaningless without a header name, so when the
    // header is blank we never send a value-without-header config.
    const authHeader = inboundAuthHeader.trim()
    const authValue = authHeader ? inboundAuthValue.trim() : ''

    // set the loading state
    setLoading(true)
    setSlugConflictError(null)
    setInboundAuthError(null)

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
      inboundAuthHeader: authHeader || undefined,
      inboundAuthValue: authHeader && authValue ? authValue : undefined,
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
      .catch((err: unknown) => {
        const status = httpStatusFromError(err)

        // Surface 409 (slug taken) and 400 (bad inbound-auth config) as field-level errors.
        if (status === 409) {
          setSlugConflictError('This slug is already taken — choose a different one')
          notify.hide(id)
        } else if (status === 400) {
          setInboundAuthError('The server rejected the inbound-auth configuration')
          notify.hide(id)
        } else {
          notify.update({
            id,
            title: 'Failed to create new WebHook',
            message: String(err),
            color: 'red',
            loading: false,
          })
        }
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
      <Text size="xs">Customise how your URL responds — status code, headers, body, and more.</Text>
      <Space h="sm" />

      <Accordion multiple defaultValue={['identity', 'response']} variant="separated">
        {/* ── Identity ─────────────────────────────────── */}
        <Accordion.Item value="identity">
          <Accordion.Control>Identity</Accordion.Control>
          <Accordion.Panel>
            <TextInput
              my="sm"
              label="Slug"
              description="Custom URL slug (leave blank to auto-generate). Pattern: [a-z0-9][a-z0-9-]{1,48}"
              placeholder="my-webhook"
              leftSection={<IconTag size="1em" />}
              error={
                slugConflictError ??
                (wrongSlug
                  ? 'Slug must start with a lowercase letter or digit and contain only a-z, 0-9 and -'
                  : undefined)
              }
              disabled={loading}
              value={slug}
              onChange={(e) => {
                setSlug(e.currentTarget.value)
                setSlugConflictError(null)
              }}
            />
            <TextInput
              my="sm"
              label="Group"
              description="Optional group name for organising sessions"
              placeholder="team-a"
              disabled={loading}
              value={group}
              onChange={(e) => setGroup(e.currentTarget.value)}
            />
          </Accordion.Panel>
        </Accordion.Item>

        {/* ── Response ─────────────────────────────────── */}
        <Accordion.Item value="response">
          <Accordion.Control>Response</Accordion.Control>
          <Accordion.Panel>
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
            <Textarea
              my="sm"
              label="Response body"
              description={`The content of the response${!!maxBodySize && maxBodySize > 0 ? ` (max ${new Intl.NumberFormat().format(maxBodySize)} characters)` : ''}`}
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
          </Accordion.Panel>
        </Accordion.Item>

        {/* ── Security ─────────────────────────────────── */}
        <Accordion.Item value="security">
          <Accordion.Control>Security</Accordion.Control>
          <Accordion.Panel>
            <TextInput
              my="sm"
              label="Auth header name"
              description="Require callers to send this header; leave blank for a public endpoint."
              placeholder="X-Webhook-Token"
              leftSection={<IconLock size="1em" />}
              error={
                inboundAuthError ?? (wrongInboundAuth ? 'Header is set — secret value is required' : undefined)
              }
              disabled={loading}
              value={inboundAuthHeader}
              onChange={(e) => {
                setInboundAuthHeader(e.currentTarget.value)
                setInboundAuthError(null)
              }}
            />
            <PasswordInput
              my="sm"
              label="Auth secret value"
              description="The expected secret value for the auth header."
              placeholder="super-secret"
              disabled={loading}
              value={inboundAuthValue}
              onChange={(e) => {
                setInboundAuthValue(e.currentTarget.value)
                setInboundAuthError(null)
              }}
            />
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
          </Accordion.Panel>
        </Accordion.Item>

        {/* ── Advanced ─────────────────────────────────── */}
        <Accordion.Item value="advanced">
          <Accordion.Control>Advanced</Accordion.Control>
          <Accordion.Panel>
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
            <Switch
              my="sm"
              label="Long-lived session"
              description="If enabled, the session does not expire on the normal TTL"
              disabled={loading}
              checked={longLived}
              onChange={(e) => setLongLived(e.currentTarget.checked)}
            />
          </Accordion.Panel>
        </Accordion.Item>
      </Accordion>

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
