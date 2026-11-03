import { createQualClient } from '@qual/sdk-ts'
import type { components } from '@qual/sdk-ts'

export const api = createQualClient()
export type WorkItem = components['schemas']['WorkItem']
export type Project = components['schemas']['Project']
export type BrowserSession = components['schemas']['BrowserSessionResponse']
export type WorkItemStatus = components['schemas']['Status']
export type WorkItemKind = components['schemas']['Kind']

export function apiErrorMessage(error: unknown): string {
  if (error && typeof error === 'object' && 'error' in error) {
    const envelope = error.error
    if (envelope && typeof envelope === 'object' && 'code' in envelope) {
      const code = String(envelope.code)
      if (code === 'VERSION_CONFLICT') return '数据已被其他人或 Agent 修改，请刷新后重试。'
      if (code === 'ALREADY_CLAIMED') return '此任务已被其他协作者领取。'
      if (code === 'PERMISSION_DENIED') return '当前身份没有权限执行此操作。'
      if (code === 'CSRF_DENIED') return '安全校验失效，请刷新会话。'
      if (code === 'UNAUTHENTICATED') return '会话已失效，请重新登录。'
      if (code === 'INVALID_TRANSITION') return '当前状态不允许此转换。'
      if (code === 'IDEMPOTENCY_CONFLICT') return '请求已被使用，请刷新页面重试。'
      if (code === 'INTEGRITY_MISMATCH') return '文件字节、大小或 SHA-256 校验失败。'
      if (code === 'EXPORT_LIMIT_EXCEEDED') return '项目快照超出本机归档上限，请分批整理数据。'
      if (code === 'CONFLICT') return '资源状态冲突或操作已经完成，请刷新后重试。'
      if ('message' in envelope && typeof envelope.message === 'string') return envelope.message
    }
  }
  return '请求失败，请检查服务连接或稍后重试。'
}

export function apiErrorCode(error: unknown): string | undefined {
  if (error && typeof error === 'object' && 'error' in error) {
    const detail = error.error
    if (detail && typeof detail === 'object' && 'code' in detail && typeof detail.code === 'string') {
      return detail.code
    }
  }
  return undefined
}

export class QualAPIError extends Error {
  readonly code?: string
  constructor(error: unknown) {
    super(apiErrorMessage(error))
    this.name = 'QualAPIError'
    this.code = apiErrorCode(error)
  }
}

export function requestKey() {
  return 'web-' + crypto.randomUUID()
}
