import { describe, expect, test } from 'bun:test'
import { mcpToolsOrEmpty } from './mcpPayload.ts'

describe('MCP payload normalization', () => {
  test('turns null or missing tools into an empty array', () => {
    expect(mcpToolsOrEmpty(null)).toEqual([])
    expect(mcpToolsOrEmpty(undefined)).toEqual([])
    expect(mcpToolsOrEmpty(null).length).toBe(0)
  })

  test('keeps valid tool arrays', () => {
    const tools = [{ name: 'server_health', description: 'Read server health' }]
    expect(mcpToolsOrEmpty(tools)).toBe(tools)
  })
})
