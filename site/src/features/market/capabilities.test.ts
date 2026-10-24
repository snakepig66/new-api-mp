import { describe, expect, it } from 'vitest'

import { capabilitiesOf, matchesCapabilities } from './capabilities'

describe('capabilitiesOf', () => {
  it('detects reasoning model families', () => {
    expect(capabilitiesOf('deepseek-r1')).toContain('reasoning')
    expect(capabilitiesOf('o3-mini')).toContain('reasoning')
    expect(capabilitiesOf('qwen3-235b-a22b')).toContain('reasoning')
    expect(capabilitiesOf('deepseek-chat')).not.toContain('reasoning')
  })

  it('detects vision and search families by name', () => {
    expect(capabilitiesOf('qwen2.5-vl-72b')).toContain('vision')
    expect(capabilitiesOf('gpt-4o')).toContain('vision')
    expect(capabilitiesOf('sonar-pro')).toContain('search')
  })

  it('tags chat families as tool-capable', () => {
    expect(capabilitiesOf('deepseek-chat')).toContain('tools')
    expect(capabilitiesOf('claude-sonnet-4')).toContain('tools')
  })

  it('returns no tags for an unknown name', () => {
    expect(capabilitiesOf('zzz-unknown')).toEqual([])
  })
})

describe('matchesCapabilities', () => {
  it('keeps every model when nothing is selected', () => {
    expect(matchesCapabilities('deepseek-chat', [])).toBe(true)
  })

  it('matches any selected capability (OR semantics)', () => {
    expect(matchesCapabilities('deepseek-r1', ['reasoning'])).toBe(true)
    expect(matchesCapabilities('deepseek-r1', ['vision'])).toBe(false)
    expect(matchesCapabilities('qwen2.5-vl-72b', ['reasoning', 'vision'])).toBe(true)
  })
})
