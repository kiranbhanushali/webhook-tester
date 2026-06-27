import { describe, expect, test } from 'vitest'
import { queryJson } from './json-query'

describe('queryJson', () => {
  // ── Root selectors ──────────────────────────────────────────────────────
  test.each([
    { name: 'bare $', path: '$', data: { a: 1 }, wantValue: { a: 1 } },
    { name: 'bare .', path: '.', data: { a: 1 }, wantValue: { a: 1 } },
    { name: 'empty string', path: '', data: { a: 1 }, wantValue: { a: 1 } },
  ])('root selector ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Dotted key traversal ────────────────────────────────────────────────
  test.each([
    { name: 'top-level key', path: 'a', data: { a: 42 }, wantValue: 42 },
    { name: 'leading dot', path: '.a', data: { a: 42 }, wantValue: 42 },
    { name: 'nested dotted', path: 'a.b.c', data: { a: { b: { c: 'hi' } } }, wantValue: 'hi' },
    { name: 'leading dot nested', path: '.a.b', data: { a: { b: 99 } }, wantValue: 99 },
  ])('dotted key ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Array index ─────────────────────────────────────────────────────────
  test.each([
    { name: 'first element', path: 'items[0]', data: { items: ['x', 'y'] }, wantValue: 'x' },
    { name: 'nested after index', path: 'items[1].id', data: { items: [{ id: 'a' }, { id: 'b' }] }, wantValue: 'b' },
    { name: 'index zero', path: '[0]', data: ['alpha', 'beta'], wantValue: 'alpha' },
  ])('array index ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data as unknown, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Bracket-quoted keys ─────────────────────────────────────────────────
  test.each([
    { name: 'double-quoted weird key', path: 'data["weird key"]', data: { data: { 'weird key': 99 } }, wantValue: 99 },
    { name: 'single-quoted key', path: "data['odd key']", data: { data: { 'odd key': 7 } }, wantValue: 7 },
    { name: 'normal key in brackets double-quoted', path: '["name"]', data: { name: 'alice' }, wantValue: 'alice' },
  ])('bracket-quoted key ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data as unknown, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Wildcard over arrays ────────────────────────────────────────────────
  test.each([
    {
      name: 'pluck id from array',
      path: 'items[*].id',
      data: { items: [{ id: 'a', v: 1 }, { id: 'b', v: 2 }] },
      wantValue: ['a', 'b'],
    },
    {
      name: 'wildcard index at root array',
      path: '[*].name',
      data: [{ name: 'alice' }, { name: 'bob' }],
      wantValue: ['alice', 'bob'],
    },
    {
      name: 'nested wildcard produces sub-array',
      path: 'rows[*].cell[0]',
      data: { rows: [{ cell: [10, 20] }, { cell: [30, 40] }] },
      wantValue: [10, 30],
    },
  ])('wildcard array ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data as unknown, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Wildcard over objects (dot-star) ────────────────────────────────────
  test.each([
    {
      name: 'object values',
      path: 'data.*',
      data: { data: { x: 1, y: 2, z: 3 } },
      wantValue: [1, 2, 3],
    },
  ])('wildcard object ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data as unknown, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Value types ─────────────────────────────────────────────────────────
  test.each([
    { name: 'null value', path: 'n', data: { n: null }, wantValue: null },
    { name: 'boolean true', path: 'b', data: { b: true }, wantValue: true },
    { name: 'boolean false', path: 'f', data: { f: false }, wantValue: false },
    { name: 'float', path: 'num', data: { num: 3.14 }, wantValue: 3.14 },
    { name: 'object value', path: 'obj', data: { obj: { k: 'v' } }, wantValue: { k: 'v' } },
    { name: 'array value', path: 'arr', data: { arr: [1, 2, 3] }, wantValue: [1, 2, 3] },
  ])('value types ($name)', ({ path, data, wantValue }) => {
    const result = queryJson(data as unknown, path)
    expect(result).toEqual({ ok: true, value: wantValue })
  })

  // ── Missing path ────────────────────────────────────────────────────────
  test.each([
    { name: 'missing top-level key', path: 'missing', data: { a: 1 } },
    { name: 'missing nested key', path: 'a.missing', data: { a: {} } },
    { name: 'index out of bounds', path: 'arr[5]', data: { arr: [1, 2] } },
    { name: 'key on non-object', path: 'a.b', data: { a: 42 } },
  ])('missing path → ok:false ($name)', ({ path, data }) => {
    const result = queryJson(data, path)
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.error).toBeTruthy()
    }
  })

  // ── Malformed path ──────────────────────────────────────────────────────
  test.each([
    { name: 'unclosed bracket', path: 'items[0', data: { items: [] } },
    { name: 'empty bracket', path: 'items[]', data: { items: [] } },
    { name: 'non-integer bracket', path: 'items[abc]', data: { items: [] } },
    { name: 'double dot', path: 'a..b', data: { a: { b: 1 } } },
  ])('malformed path → ok:false ($name)', ({ path, data }) => {
    const result = queryJson(data, path)
    expect(result.ok).toBe(false)
    if (!result.ok) {
      expect(result.error).toBeTruthy()
    }
  })
})
