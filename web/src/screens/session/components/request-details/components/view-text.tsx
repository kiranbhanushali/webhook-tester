import { CodeHighlight } from '@mantine/code-highlight'
import { Alert, Code, Group, Text, TextInput, Tooltip } from '@mantine/core'
import { IconHelp, IconInfoCircle } from '@tabler/icons-react'
import React, { useEffect, useState } from 'react'
import { queryJson } from '~/shared/utils/json-query'

const decoder = new TextDecoder('utf-8')
const cutMessage = '\n\n[...content truncated (to view the full content, please download the binary file)...]\n\n'

const QUERY_HELP_TEXT =
  'Supported syntax:\n' +
  '  $  or  .          → whole document\n' +
  '  data.txn.id       → dotted key path\n' +
  '  items[0].id       → array index\n' +
  '  data["weird key"] → bracket-quoted key\n' +
  '  items[*].id       → wildcard (returns array)\n' +
  '  data.*            → all object values'

export const ViewText: React.FC<{
  input: Uint8Array | null
  contentType: string | null
  lengthLimit?: number
}> = ({
  input,
  contentType = null,
  lengthLimit = 1024 * 128, // 128KB
}) => {
  const [content, setContent] = useState<string | null>(null)
  const [language, setLanguage] = useState<'json' | 'xml' | null>(null)
  const [trimmed, setTrimmed] = useState<boolean>(false)
  const [parsedJson, setParsedJson] = useState<unknown>(null)
  const [query, setQuery] = useState<string>('')

  useEffect(() => {
    setQuery('') // reset query whenever the payload changes
    setParsedJson(null)

    if (!input || input.length === 0) {
      setContent('// empty request body')
      setTrimmed(false)
      setLanguage('json')

      return
    }

    if (input.length > lengthLimit + cutMessage.length) {
      const [start, end] = [input.slice(0, lengthLimit / 2), input.slice(-lengthLimit / 2)]

      setContent(decoder.decode(start) + cutMessage + decoder.decode(end))
      setTrimmed(true)
      setLanguage(null)

      return
    }

    const [maybePretty, lang, parsed] = tryToFormat(decoder.decode(input), contentType)

    setTrimmed(false)
    setContent(maybePretty)
    setLanguage(lang)
    setParsedJson(parsed)
  }, [input, lengthLimit, contentType])

  // Only show the query box when the body successfully parsed as JSON
  const isJson = language === 'json' && parsedJson !== null

  // Evaluate the query on every keystroke — the payload is already in memory, so this is instant
  const queryResult = isJson && query.trim() !== '' ? queryJson(parsedJson, query.trim()) : null

  return (
    <>
      {trimmed && (
        <Alert color="yellow" my="sm" title="Data trimmed" icon={<IconInfoCircle />}>
          The request body is large and has been trimmed to {lengthLimit} bytes for performance reasons.
        </Alert>
      )}

      {isJson && (
        <Group my="xs" gap="xs" align="center" wrap="nowrap">
          <TextInput
            placeholder="data.txn.trackingId  ·  items[*].id"
            value={query}
            onChange={(e) => setQuery(e.currentTarget.value)}
            size="xs"
            style={{ flex: 1 }}
            aria-label="JSON query path"
          />
          <Tooltip
            label={<Text size="xs" style={{ whiteSpace: 'pre' }}>{QUERY_HELP_TEXT}</Text>}
            multiline
            w={340}
            withArrow
            position="top-end"
          >
            <IconHelp size="1.1em" style={{ cursor: 'help', flexShrink: 0 }} />
          </Tooltip>
        </Group>
      )}

      {queryResult !== null ? (
        queryResult.ok ? (
          <CodeHighlight
            code={JSON.stringify(queryResult.value, undefined, 2)}
            language="json"
          />
        ) : (
          <Text c="dimmed" size="sm" mt="xs">
            {queryResult.error}
          </Text>
        )
      ) : (
        !!content && (language ? <CodeHighlight code={content} language={language} /> : <Code block>{content}</Code>)
      )}
    </>
  )
}

const tryToFormat = (
  str: string,
  contentType: string | null
): [string /* content, probably well-formatted */, 'json' | 'xml' | null /* language */, unknown /* parsedJson or null */] => {
  let looksLikeJson = false
  let looksLikeXml = false

  // try to determine format by content type
  if (contentType) {
    const clear = contentType.toLowerCase()

    looksLikeJson = clear.includes('json')
    looksLikeXml = clear.includes('xml')
  }

  // otherwise, try to determine format by content
  if (!looksLikeJson && !looksLikeXml) {
    const clear = str.trimStart()

    looksLikeJson = clear.length > 0 && (clear[0] === '{' || clear[0] === '[' || clear[0] === '"')
    looksLikeXml = clear.length > 0 && clear[0] === '<'
  }

  if (looksLikeJson) {
    try {
      const parsed = JSON.parse(str)

      return [JSON.stringify(parsed, undefined, 2), 'json', parsed]
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
    } catch (_) {
      // wrong json
    }
  } else if (looksLikeXml) {
    try {
      new DOMParser().parseFromString(str, 'text/xml')

      return [str, 'xml', null]
      // eslint-disable-next-line @typescript-eslint/no-unused-vars
    } catch (_) {
      // wrong xml
    }
  }

  return [str, null, null] // return as is
}
