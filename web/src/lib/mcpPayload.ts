export interface McpPayloadTool {
  name: string
  description: string
}

export function mcpToolsOrEmpty<T extends McpPayloadTool>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}
