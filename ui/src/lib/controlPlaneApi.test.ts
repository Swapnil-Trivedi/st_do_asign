import { describe, expect, it } from 'vitest'
import { defaultParamsForAlgorithm, formatMatch } from './controlPlaneApi'

describe('defaultParamsForAlgorithm', () => {
  it('returns token bucket defaults', () => {
    expect(defaultParamsForAlgorithm('fixed_token')).toEqual({
      sustainedRps: 10,
      burst: 20,
    })
  })

  it('returns window defaults', () => {
    expect(defaultParamsForAlgorithm('sliding_window')).toEqual({
      limit: 10,
      windowSeconds: 1,
    })
    expect(defaultParamsForAlgorithm('fixed_window')).toEqual({
      limit: 10,
      windowSeconds: 1,
    })
  })

  it('returns empty for unknown', () => {
    expect(defaultParamsForAlgorithm('nope')).toEqual({})
  })
})

describe('formatMatch', () => {
  it('formats header matches', () => {
    expect(
      formatMatch({
        kind: 'header',
        name: 'US-API-Key',
        op: 'eq',
        value: 'ak_x',
      }),
    ).toBe('header US-API-Key eq ak_x')
  })

  it('formats path and method', () => {
    expect(formatMatch({ kind: 'path', op: 'eq', value: '/a' })).toBe('path eq /a')
    expect(formatMatch({ kind: 'method', op: 'eq', value: 'GET' })).toBe('method eq GET')
  })
})
