import { Accordion, Code, List, Popover, Text, ActionIcon, ScrollArea } from '@mantine/core'
import { IconHelp } from '@tabler/icons-react'
import React, { useState } from 'react'

/** Template data fields available inside a response script. */
const DATA_FIELDS: ReadonlyArray<{ field: string; description: string }> = [
  { field: '.Method', description: 'HTTP method of the incoming request (e.g. POST)' },
  { field: '.Path', description: 'URL path of the incoming request' },
  { field: '.Slug', description: 'Session slug' },
  { field: '.Body', description: 'Raw request body as a string' },
  { field: '.JSON', description: 'Parsed JSON body (access fields with .JSON.key)' },
  { field: '.Query', description: 'Map of URL query parameters' },
  { field: '.Header', description: 'Map of request headers' },
  { field: '.Now', description: 'Current UTC time as a time.Time value' },
]

/** Helper functions available inside a response script. */
const HELPER_FUNCS: ReadonlyArray<{ func: string; description: string }> = [
  { func: 'json', description: 'Serialise a value to a JSON string' },
  { func: 'jsonPath', description: 'Extract a value from a JSON string using a dot-path' },
  { func: 'uuid', description: 'Generate a random UUID v4' },
  { func: 'now', description: 'Return the current UTC time' },
  { func: 'randInt', description: 'Crypto-random integer in [min, max): randInt min max' },
  { func: 'randHex', description: 'n random bytes as a hex string (2n chars): randHex n' },
  { func: 'base64', description: 'Base64-encode a string' },
  { func: 'sha256', description: 'SHA-256 hash of a string (hex)' },
  { func: 'hmacSHA256', description: 'HMAC-SHA-256 of a message with a key: hmacSHA256 key message (hex)' },
  { func: 'upper', description: 'Convert a string to uppercase' },
  { func: 'lower', description: 'Convert a string to lowercase' },
  { func: 'default', description: 'Return a fallback value when the primary is empty/zero' },
  { func: 'seq', description: 'Generate a sequence of integers (e.g. for ranges)' },
]

const EXAMPLE_SCRIPT = `{{ $id := jsonPath .JSON "trackingId" }}
@status 200
{"id":"{{ $id }}","sig":"{{ hmacSHA256 "my-secret" $id }}"}`

/** Popover button that shows the response-script template reference. */
export const ScriptHelpButton: React.FC = () => {
  const [opened, setOpened] = useState(false)

  return (
    <Popover
      opened={opened}
      onChange={setOpened}
      width={480}
      position="bottom-start"
      withArrow
      shadow="md"
    >
      <Popover.Target>
        <ActionIcon
          variant="subtle"
          size="sm"
          onClick={() => setOpened((o) => !o)}
          aria-label="Template help"
          title="Template help"
        >
          <IconHelp size="1em" />
        </ActionIcon>
      </Popover.Target>

      <Popover.Dropdown>
        <ScrollArea.Autosize mah={480} type="auto">
          <Text fw={600} size="sm" mb="xs">
            Response Script Reference
          </Text>
          <Text size="xs" c="dimmed" mb="sm">
            Scripts use Go{' '}
            <Code>text/template</Code> syntax. The first line may be{' '}
            <Code>@status NNN</Code> to set the response status code.
          </Text>

          <Accordion variant="contained" defaultValue="fields">
            <Accordion.Item value="fields">
              <Accordion.Control>Data fields</Accordion.Control>
              <Accordion.Panel>
                <List size="xs" spacing={4}>
                  {DATA_FIELDS.map(({ field, description }) => (
                    <List.Item key={field}>
                      <Code>{field}</Code>
                      {' — '}
                      {description}
                    </List.Item>
                  ))}
                </List>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="funcs">
              <Accordion.Control>Helper functions</Accordion.Control>
              <Accordion.Panel>
                <List size="xs" spacing={4}>
                  {HELPER_FUNCS.map(({ func, description }) => (
                    <List.Item key={func}>
                      <Code>{func}</Code>
                      {' — '}
                      {description}
                    </List.Item>
                  ))}
                </List>
              </Accordion.Panel>
            </Accordion.Item>

            <Accordion.Item value="example">
              <Accordion.Control>Example</Accordion.Control>
              <Accordion.Panel>
                <Code block style={{ whiteSpace: 'pre', fontSize: '0.8em' }}>
                  {EXAMPLE_SCRIPT}
                </Code>
              </Accordion.Panel>
            </Accordion.Item>
          </Accordion>
        </ScrollArea.Autosize>
      </Popover.Dropdown>
    </Popover>
  )
}
