import { describe, expect, it } from 'vitest'

import {
  normalizeAdminPoolChannel,
  normalizeAdminPoolChannelModel,
  normalizeAdminPoolModel,
} from './api'

describe('admin pool normalization', () => {
  it('normalizes a standard model, including JSON-string lists', () => {
    expect(
      normalizeAdminPoolModel({
        id: 1,
        name: 'deepseek-flash',
        origin_vendor: 'deepseek',
        context_length: 128000,
        capabilities: '["reasoning","tools"]',
        aliases: ['deepseek-chat'],
        verify_prompt: 'ping',
        status: 'active',
      }),
    ).toEqual({
      id: 1,
      name: 'deepseek-flash',
      origin_vendor: 'deepseek',
      context_length: 128000,
      capabilities: ['reasoning', 'tools'],
      aliases: ['deepseek-chat'],
      verify_prompt: 'ping',
      status: 'active',
    })
  })

  it('normalizes a pooled channel with protocols, lists and quota window', () => {
    const channel = normalizeAdminPoolChannel({
      id: 2,
      slug: 'deepseek',
      name: 'DeepSeek',
      kind: 'official',
      protocols: { openai: 'https://api.deepseek.com', anthropic: '' },
      allow_host_pattern: '^api\\.deepseek\\.com$',
      key_pattern: '^sk-',
      deny_paths: '["/admin"]',
      deny_key_prefixes: ['sk-test'],
      adaptor: '',
      strip_v1_prefix: false,
      passthrough_body: true,
      quota_window: '{"period":"month"}',
      exhaustion_signature: '',
      terms_url: '',
      terms_checked_at: null,
      status: 'active',
    })
    expect(channel.protocols.openai).toBe('https://api.deepseek.com')
    expect(channel.deny_paths).toEqual(['/admin'])
    expect(channel.deny_key_prefixes).toEqual(['sk-test'])
    expect(channel.quota_window).toEqual({ period: 'month' })
    expect(channel.passthrough_body).toBe(true)
  })

  it('normalizes a channel-model qualification and its differences', () => {
    const row = normalizeAdminPoolChannelModel({
      id: 3,
      channel_slug: 'volcengine-ark-coding-plan',
      upstream_model: 'deepseek-v3.1',
      model: 'deepseek-flash',
      coefficient_milli: 800,
      differences: '{"context_length":128000,"vision":false}',
      verified: true,
      verified_at: '2030-01-01T00:00:00+08:00',
      fingerprint_ratio: 1,
      status: 'active',
    })
    expect(row.channel_slug).toBe('volcengine-ark-coding-plan')
    expect(row.coefficient_milli).toBe(800)
    expect(row.differences).toEqual({ context_length: 128000, vision: false })
    expect(row.verified).toBe(true)
  })
})
