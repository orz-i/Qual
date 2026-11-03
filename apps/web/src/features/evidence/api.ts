import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { api, apiErrorMessage, requestKey } from '@/lib/qual-api'
import { useSession } from '@/features/auth/session'

export type Artifact = components['schemas']['Artifact']
export type ArtifactTargetKind = components['schemas']['ArtifactTargetKind']
export type ExternalReference = components['schemas']['ExternalReference']
export type ArchiveExport = components['schemas']['ArchiveExport']
export type WebhookSubscription = components['schemas']['WebhookSubscription']
export type WebhookDelivery = components['schemas']['WebhookDelivery']
export type WebhookRegistration = components['schemas']['WebhookRegistration']

const evidenceKey = (projectID: string) => ['evidence', projectID] as const

function useCSRF() {
  const { data: session } = useSession()
  return () => {
    if (!session?.csrf_token) throw new Error('浏览器会话失效，请重新登录。')
    return session.csrf_token
  }
}

async function expectSuccess(response: Response) {
  const json: unknown = await response.json().catch(() => null)
  if (!response.ok) throw new Error(apiErrorMessage(json))
  return json
}

export function useArtifacts(projectID: string, targetKind: ArtifactTargetKind, targetID: string) {
  return useQuery({
    queryKey: [...evidenceKey(projectID), 'artifacts', targetKind, targetID],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/artifacts', {
        params: { path: { projectID }, query: { target_kind: targetKind, target_id: targetID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}

export function useReferences(projectID: string, targetKind: ArtifactTargetKind, targetID: string) {
  return useQuery({
    queryKey: [...evidenceKey(projectID), 'references', targetKind, targetID],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/external-references', {
        params: { path: { projectID }, query: { target_kind: targetKind, target_id: targetID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}

export function useEvidenceActions(projectID: string) {
  const queryClient = useQueryClient()
  const csrf = useCSRF()
  const refresh = async () => {
    await queryClient.invalidateQueries({ queryKey: evidenceKey(projectID) })
  }
  const upload = useMutation({
    mutationFn: async ({ file, targetKind, targetID }: {
      file: File; targetKind: ArtifactTargetKind; targetID: string
    }) => {
      if (file.size < 1 || file.size > 10485760) throw new Error('附件必须在 1 字节到 10 MiB 之间。')
      if (file.name.length > 160 || file.name.includes('/') || file.name.includes('\\')) {
        throw new Error('文件名无效或过长。')
      }
      const bytes = await file.arrayBuffer()
      const digest = Array.from(new Uint8Array(await crypto.subtle.digest('SHA-256', bytes)))
        .map(byte => byte.toString(16).padStart(2, '0')).join('')
      const contentType = (file.type || 'application/octet-stream').split(';')[0]
      const { data: pending, error } = await api.POST('/api/v1/projects/{projectID}/artifacts', {
        params: {
          path: { projectID },
          header: { 'Idempotency-Key': requestKey() },
        },
        headers: { 'X-CSRF-Token': csrf() },
        body: {
          target_kind: targetKind, target_id: targetID, filename: file.name,
          media_type: contentType, size_bytes: file.size, sha256: digest,
        },
      })
      if (error || !pending) throw new Error(apiErrorMessage(error))
      const base = `/api/v1/projects/${projectID}/artifacts/${pending.id}`
      const put = await fetch(`${base}/content`, {
        method: 'PUT', credentials: 'same-origin',
        headers: {
          'Content-Type': 'application/octet-stream',
          'Idempotency-Key': requestKey(),
          'X-CSRF-Token': csrf(),
        },
        body: bytes,
      })
      await expectSuccess(put)
      const { data: ready, error: completeError } = await api.POST(
        '/api/v1/projects/{projectID}/artifacts/{artifactID}/complete', {
          params: {
            path: { projectID, artifactID: pending.id },
            header: { 'Idempotency-Key': requestKey() },
          },
          headers: { 'X-CSRF-Token': csrf() },
        })
      if (completeError || !ready) throw new Error(apiErrorMessage(completeError))
      if (ready.state !== 'ready') throw new Error('附件尚未完成 S3 完整性校验。')
      return ready
    },
    onSuccess: refresh,
  })
  const addReference = useMutation({
    mutationFn: async (input: components['schemas']['ExternalReferenceInput']) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/external-references', {
        params: { path: { projectID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': csrf() },
        body: input,
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    onSuccess: refresh,
  })
  return { upload, addReference }
}

export function useProjectExports(projectID: string) {
  return useQuery({
    queryKey: [...evidenceKey(projectID), 'exports'],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/exports', {
        params: { path: { projectID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
    retry: false,
  })
}

export function useProjectExportActions(projectID: string) {
  const queryClient = useQueryClient()
  const csrf = useCSRF()
  return useMutation({
    mutationFn: async () => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/exports', {
        params: { path: { projectID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': csrf() },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    onSuccess: async () => { await queryClient.invalidateQueries({ queryKey: [...evidenceKey(projectID), 'exports'] }) },
  })
}

export function useWebhookSubscriptions(projectID: string) {
  return useQuery({
    queryKey: [...evidenceKey(projectID), 'webhooks'],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/webhooks', {
        params: { path: { projectID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
    retry: false,
  })
}

export function useWebhookDeliveries(projectID: string) {
  return useQuery({
    queryKey: [...evidenceKey(projectID), 'deliveries'],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/webhook-deliveries', {
        params: { path: { projectID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
    retry: false,
  })
}

export function useWebhookActions(projectID: string) {
  const queryClient = useQueryClient()
  const csrf = useCSRF()
  const invalidate = async () => { await queryClient.invalidateQueries({ queryKey: evidenceKey(projectID) }) }
  const register = useMutation({
    mutationFn: async ({ endpoint, event_types }: { endpoint: string; event_types: string[] }) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/webhooks', {
        params: { path: { projectID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': csrf() },
        body: { endpoint, event_types },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    onSuccess: invalidate,
  })
  const disable = useMutation({
    mutationFn: async (subscriptionID: string) => {
      const { error, response } = await api.POST(
        '/api/v1/projects/{projectID}/webhooks/{subscriptionID}/disable', {
          params: {
            path: { projectID, subscriptionID },
            header: { 'Idempotency-Key': requestKey() },
          },
          headers: { 'X-CSRF-Token': csrf() },
        },
      )
      if (error || response.status !== 204) throw new Error(apiErrorMessage(error))
    },
    onSuccess: invalidate,
  })
  return { register, disable }
}
