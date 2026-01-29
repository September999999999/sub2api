import { apiClient } from './client'

export type PlatformType = 'bark' | 'custom' | 'dingtalk' | 'discord' | 'telegram' | 'smtp'

export interface PlatformConfig {
  id: string
  type: PlatformType
  name: string
  enabled: boolean
  // Common fields
  url?: string
  secret?: string
  token?: string
  channelId?: string
  // Dingtalk specific
  enableSign?: boolean
  // Telegram specific
  botToken?: string
  chatId?: string
  apiBaseUrl?: string
  proxyUrl?: string
  // SMTP specific
  smtpHost?: string
  smtpPort?: number
  smtpUser?: string
  smtpPass?: string
  smtpFrom?: string
  smtpTo?: string
  smtpSecure?: boolean
  smtpIgnoreTLS?: boolean
}

export interface WebhookConfig {
  enabled: boolean
  platforms: PlatformConfig[]
  notificationTypes?: Record<string, boolean>
  retrySettings: {
    maxRetries: number
    retryDelay: number
    timeout: number
  }
}

export interface WebhookConfigIssue {
  level: 'warning' | 'error'
  code: string
  message: string
  remediation?: string
}

export interface WebhookState {
  status: 'ok' | 'defaulted' | 'degraded'
  source: 'redis' | 'settings' | 'default'
  issues: WebhookConfigIssue[]
}

export interface WebhookConfigResponse {
  config: WebhookConfig
  state: WebhookState
}

export interface WebhookPlatformsResponse {
  items: PlatformConfig[]
  state: WebhookState
}

export type UpdateWebhookConfigRequest = Pick<WebhookConfig, 'enabled' | 'retrySettings'>

// API 函数
export const getWebhookConfig = async (): Promise<WebhookConfigResponse> => {
  const { data } = await apiClient.get<WebhookConfigResponse>('/admin/webhook/config')
  return data
}

export const updateWebhookConfig = async (config: UpdateWebhookConfigRequest): Promise<WebhookConfig> => {
  const { enabled, retrySettings } = config
  const { data } = await apiClient.put<WebhookConfig>('/admin/webhook/config', { enabled, retrySettings })
  return data
}

export const getPlatforms = async (): Promise<WebhookPlatformsResponse> => {
  const { data } = await apiClient.get<WebhookPlatformsResponse>('/admin/webhook/platforms')
  return data
}

export const addPlatform = async (platform: Omit<PlatformConfig, 'id'>): Promise<PlatformConfig[]> => {
  const { data } = await apiClient.post<PlatformConfig[]>('/admin/webhook/platforms', platform)
  return data
}

export const updatePlatform = async (id: string, platform: Partial<PlatformConfig>): Promise<PlatformConfig[]> => {
  const { data } = await apiClient.put<PlatformConfig[]>(`/admin/webhook/platforms/${id}`, platform)
  return data
}

export const deletePlatform = async (id: string): Promise<PlatformConfig[]> => {
  const { data } = await apiClient.delete<PlatformConfig[]>(`/admin/webhook/platforms/${id}`)
  return data
}

export const togglePlatform = async (id: string): Promise<PlatformConfig[]> => {
  const { data } = await apiClient.post<PlatformConfig[]>(`/admin/webhook/platforms/${id}/toggle`)
  return data
}

export const testPlatform = async (id: string): Promise<void> => {
  await apiClient.post(`/admin/webhook/platforms/${id}/test`)
}

export interface TestNotificationRequest {
  type: string
  title: string
  content: string
}

export const testNotification = async (msg: TestNotificationRequest): Promise<void> => {
  await apiClient.post('/admin/webhook/test-notification', msg)
}
