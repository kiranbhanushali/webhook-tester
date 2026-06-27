/**
 * Lean JSONPath-style field selector for already-parsed JSON values.
 *
 * Supported grammar:
 *   root         ::= "$" | "." | ""         → returns the whole document
 *   path         ::= root? segment+
 *   segment      ::= "." key                → dotted key        e.g. data.txn.id
 *                  | "." "*"               → wildcard over all object values
 *                  | "[" integer "]"       → array index        e.g. items[0]
 *                  | "[" "*" "]"           → wildcard over all array/object values
 *                  | "[" quote key quote "]" → bracket-quoted key e.g. ["weird key"]
 *   key          ::= [\w$-]+
 *   quote        ::= '"' | "'"
 *   integer      ::= [0-9]+
 *
 * Wildcard segments collect matches from every array element (or object value)
 * and return an array — even if there is only one element.
 *
 * Missing paths  → { ok: false, error: "no match at <segment>" }.
 * Malformed paths → { ok: false, error: "<description>" }.
 */

export type QueryResult = { ok: true; value: unknown } | { ok: false; error: string }

// ─── Segment types ─────────────────────────────────────────────────────────────

type SegKey = { type: 'key'; name: string }
type SegIndex = { type: 'index'; n: number }
type SegWildcard = { type: 'wildcard' }

type Segment = SegKey | SegIndex | SegWildcard

// ─── Path parser ───────────────────────────────────────────────────────────────

function parsePath(raw: string): Segment[] | { error: string } {
  let pos = 0
  const segments: Segment[] = []

  const peek = (): string => raw[pos] ?? ''

  function consume(): string {
    return raw[pos++] ?? ''
  }

  function readKey(): string {
    let s = ''
    while (pos < raw.length) {
      const c = raw[pos]!
      if (/[\w$-]/.test(c)) {
        s += c
        pos++
      } else {
        break
      }
    }
    return s
  }

  // strip leading root anchors — bare "$", bare ".", or empty → whole document
  if (raw === '' || raw === '$' || raw === '.') {
    return []
  }
  if (raw[0] === '$') {
    pos++ // skip $
  }

  while (pos < raw.length) {
    const ch = peek()

    if (ch === '.') {
      consume() // eat the dot
      if (peek() === '') {
        return { error: 'trailing dot in path' }
      }
      if (peek() === '[') {
        return { error: `unexpected "[" after "." at position ${pos}` }
      }
      if (peek() === '.') {
        return { error: `double dot at position ${pos}` }
      }
      // wildcard: .*
      if (peek() === '*') {
        consume()
        segments.push({ type: 'wildcard' })
        continue
      }
      // read a key
      const key = readKey()
      if (key === '') {
        return { error: `expected key after "." at position ${pos}` }
      }
      segments.push({ type: 'key', name: key })
    } else if (ch === '[') {
      consume() // eat [
      const inner = peek()

      if (inner === '*') {
        consume() // eat *
        if (peek() !== ']') {
          return { error: `expected "]" after "[*" at position ${pos}` }
        }
        consume() // eat ]
        segments.push({ type: 'wildcard' })
      } else if (inner === '"' || inner === "'") {
        const quote = consume() // eat opening quote
        let key = ''
        while (pos < raw.length && peek() !== quote) {
          key += consume()
        }
        if (peek() !== quote) {
          return { error: `unterminated quoted key starting at position ${pos}` }
        }
        consume() // eat closing quote
        if (peek() !== ']') {
          return { error: `expected "]" after quoted key at position ${pos}` }
        }
        consume() // eat ]
        if (key === '') {
          return { error: `empty quoted key at position ${pos}` }
        }
        segments.push({ type: 'key', name: key })
      } else if (inner >= '0' && inner <= '9') {
        let numStr = ''
        while (pos < raw.length && peek() >= '0' && peek() <= '9') {
          numStr += consume()
        }
        if (peek() !== ']') {
          return { error: `expected "]" after index at position ${pos}` }
        }
        consume() // eat ]
        segments.push({ type: 'index', n: parseInt(numStr, 10) })
      } else if (inner === ']') {
        return { error: `empty bracket "[]" at position ${pos}` }
      } else {
        return { error: `unexpected character "${inner}" inside "[...]" at position ${pos}` }
      }
    } else {
      // bare key at the start (no leading dot, e.g. "items[0].id" or "data.txn")
      const key = readKey()
      if (key === '') {
        return { error: `unexpected character "${ch}" at position ${pos}` }
      }
      segments.push({ type: 'key', name: key })
    }
  }

  return segments
}

// ─── Traversal ─────────────────────────────────────────────────────────────────

function traverse(value: unknown, segments: ReadonlyArray<Segment>): QueryResult {
  if (segments.length === 0) {
    return { ok: true, value }
  }

  const [head, ...tail] = segments as [Segment, ...Segment[]]

  if (head.type === 'wildcard') {
    if (Array.isArray(value)) {
      const results: unknown[] = []
      for (let i = 0; i < value.length; i++) {
        const r = traverse(value[i], tail)
        if (!r.ok) {
          return r
        }
        results.push(r.value)
      }
      return { ok: true, value: results }
    }
    if (value !== null && typeof value === 'object') {
      const entries = Object.values(value as Record<string, unknown>)
      const results: unknown[] = []
      for (const v of entries) {
        const r = traverse(v, tail)
        if (!r.ok) {
          return r
        }
        results.push(r.value)
      }
      return { ok: true, value: results }
    }
    return { ok: false, error: 'wildcard applied to non-array/object value' }
  }

  if (head.type === 'index') {
    if (!Array.isArray(value)) {
      return { ok: false, error: `index [${head.n}] applied to non-array` }
    }
    if (head.n < 0 || head.n >= value.length) {
      return { ok: false, error: `no match at index [${head.n}] (array length: ${value.length})` }
    }
    return traverse(value[head.n], tail)
  }

  // head.type === 'key'
  if (value === null || typeof value !== 'object' || Array.isArray(value)) {
    return { ok: false, error: `no match at "${head.name}" — parent is not an object` }
  }
  const rec = value as Record<string, unknown>
  if (!(head.name in rec)) {
    return { ok: false, error: `no match at "${head.name}"` }
  }
  return traverse(rec[head.name], tail)
}

// ─── Public API ────────────────────────────────────────────────────────────────

/**
 * Evaluate a lean path expression against an already-parsed JSON value.
 *
 * @param root  The parsed JSON value (any type).
 * @param path  The path expression (see grammar in the file doc-comment above).
 * @returns     `{ ok: true, value }` on success or `{ ok: false, error }` on failure.
 */
export const queryJson = (root: unknown, path: string): QueryResult => {
  const trimmed = path.trim()
  const parsed = parsePath(trimmed)

  if ('error' in parsed) {
    return { ok: false, error: parsed.error }
  }

  return traverse(root, parsed)
}
