import { Button, Group, SegmentedControl, Stack, TextInput } from '@mantine/core'
import { IconSearch } from '@tabler/icons-react'
import React, { useState } from 'react'

export type SearchQuery = {
  key: string
  value: string
  match: 'exact' | 'prefix'
}

type Props = {
  onSearch: (query: SearchQuery) => void
}

export function SearchBar({ onSearch }: Props): React.JSX.Element {
  const [key, setKey] = useState<string>('trackingId')
  const [value, setValue] = useState<string>('')
  const [match, setMatch] = useState<'exact' | 'prefix'>('exact')

  const submit = (): void => {
    if (!value.trim()) {
      return
    }
    onSearch({ key, value, match })
  }

  const handleKeyDown = (e: React.KeyboardEvent<HTMLInputElement>): void => {
    if (e.key === 'Enter') {
      submit()
    }
  }

  return (
    <Stack gap="sm">
      <Group align="flex-end" grow>
        <TextInput
          label="Identifier key"
          aria-label="Identifier key"
          placeholder="trackingId"
          value={key}
          onChange={(e) => setKey(e.currentTarget.value)}
        />
        <TextInput
          label="Identifier value"
          aria-label="Identifier value"
          placeholder="e.g. abc-123"
          value={value}
          onChange={(e) => setValue(e.currentTarget.value)}
          onKeyDown={handleKeyDown}
        />
      </Group>
      <Group>
        <SegmentedControl
          value={match}
          onChange={(v) => setMatch(v as 'exact' | 'prefix')}
          data={[
            { value: 'exact', label: 'Exact' },
            { value: 'prefix', label: 'Prefix' },
          ]}
          size="xs"
        />
        <Button
          leftSection={<IconSearch size="1em" />}
          onClick={submit}
          disabled={!value.trim()}
          aria-label="Search"
        >
          Search
        </Button>
      </Group>
    </Stack>
  )
}
