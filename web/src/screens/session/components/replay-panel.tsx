import React, { useState } from 'react'
import { Alert, Badge, Button, Code, Group, Stack, TextInput } from '@mantine/core'
import { IconAlertCircle, IconSend } from '@tabler/icons-react'
import { useData } from '~/shared'
import type { Session, Request } from '~/shared'
import type { ReplayResult } from '~/api'

const BODY_TRUNCATE_LIMIT = 4_096 as const

const statusCodeColor = (code: number): string => {
  if (code >= 200 && code < 300) return 'green'
  if (code >= 300 && code < 400) return 'blue'
  if (code >= 400 && code < 500) return 'orange'
  return 'red'
}

export const ReplayPanel: React.FC<{
  session: Readonly<Session>
  request: Readonly<Request>
}> = ({ session, request }): React.JSX.Element => {
  const { replayRequest } = useData()
  const [targetUrl, setTargetUrl] = useState<string>(session.forwardUrl ?? '')
  const [loading, setLoading] = useState<boolean>(false)
  const [result, setResult] = useState<ReplayResult | null>(null)
  const [error, setError] = useState<string | null>(null)

  // The button is usable when either the typed input or the session forward URL provides a target.
  const canReplay = targetUrl.length > 0 || (session.forwardUrl !== null && session.forwardUrl.length > 0)

  const handleReplay = async (): Promise<void> => {
    setLoading(true)
    setResult(null)
    setError(null)

    try {
      const res = await replayRequest(session.sID, request.rID, targetUrl || undefined)
      setResult(res)
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'An unexpected error occurred')
    } finally {
      setLoading(false)
    }
  }

  const bodyText = result
    ? (() => {
        const decoded = new TextDecoder().decode(result.body)
        return decoded.length > BODY_TRUNCATE_LIMIT ? decoded.slice(0, BODY_TRUNCATE_LIMIT) + '…' : decoded
      })()
    : null

  return (
    <Stack gap="sm">
      <Group align="flex-end">
        <TextInput
          label="Target URL"
          placeholder={session.forwardUrl ?? 'https://example.com/webhook'}
          value={targetUrl}
          onChange={(e) => setTargetUrl(e.currentTarget.value)}
          style={{ flex: 1 }}
        />
        <Button
          leftSection={<IconSend size="1em" />}
          loading={loading}
          disabled={!canReplay || loading}
          onClick={() => {
            void handleReplay()
          }}
          color="indigo"
        >
          Replay
        </Button>
      </Group>

      {!canReplay && (
        <Alert icon={<IconAlertCircle />} color="yellow">
          Set a target URL above or configure a session forward URL to enable replay.
        </Alert>
      )}

      {!!error && (
        <Alert icon={<IconAlertCircle />} color="red" title="Replay failed">
          {error}
        </Alert>
      )}

      {!!result && (
        <Stack gap="xs">
          <Group>
            <Badge color={statusCodeColor(result.statusCode)} size="lg">
              {result.statusCode}
            </Badge>
          </Group>
          <Code
            block
            style={{ maxHeight: '12em', overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}
          >
            {bodyText || '(empty body)'}
          </Code>
        </Stack>
      )}
    </Stack>
  )
}
