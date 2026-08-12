export interface ReasoningValue {
  value: string
  label: string
  kind?: 'disable'
}

export interface ReasoningCapability {
  values: ReasoningValue[]
  default?: string
  mandatory: boolean
  can_disable: boolean
  source: 'live' | 'static'
}

export interface ChatModelSelection {
  provider: string
  model: string
  name: string
  providerLabel: string
  reasoningCapability?: ReasoningCapability
}
