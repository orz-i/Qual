import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { useSession } from '@/features/auth/session'
import { api, apiErrorCode, apiErrorMessage, QualAPIError, requestKey } from '@/lib/qual-api'
import { workKey } from '@/features/work/api'

export type ReviewPolicy = components['schemas']['ProjectReviewPolicy']
export type WorkAcceptanceHistory = components['schemas']['WorkAcceptanceHistory']
export type WorkReviewPolicy = components['schemas']['WorkReviewPolicy']
export type DecisionInput = components['schemas']['DoneDecisionInput']

// Null is specifically Goose12's ACCEPTANCE_REQUIRED: not a network error.
export function useProjectReviewPolicy(projectID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'review-policy'],
    retry: false,
    queryFn: async (): Promise<ReviewPolicy | null> => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/review-policy', {
        params: { path: { projectID } },
      })
      if (apiErrorCode(error) === 'ACCEPTANCE_REQUIRED') return null
      if (error || !data) throw new QualAPIError(error)
      return data
    },
  })
}

export function useWorkReviewPolicy(projectID: string, itemID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'effective-review-policy', itemID],
    retry: false,
    queryFn: async (): Promise<WorkReviewPolicy | null> => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/review-policy', {
        params: { path: { projectID, itemID } },
      })
      if (apiErrorCode(error) === 'ACCEPTANCE_REQUIRED') return null
      if (error || !data) throw new QualAPIError(error)
      return data
    },
  })
}

export function useChangeWorkReviewPolicy(projectID: string, itemID: string) {
  const qc = useQueryClient()
  const { data: session } = useSession()
  return useMutation({
    mutationFn: async (body: components['schemas']['WorkReviewPolicyChange']) => {
      if (!session?.csrf_token) throw new Error('缺少浏览器安全会话，请重新登录。')
      const { data, error } = await api.PUT('/api/v1/projects/{projectID}/work-items/{itemID}/review-policy', {
        params: { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': session.csrf_token },
        body,
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: async () => { await qc.invalidateQueries({ queryKey: workKey(projectID) }) },
  })
}

export function useWorkAcceptanceHistory(projectID: string, itemID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'acceptance', itemID],
    retry: false,
    queryFn: async (): Promise<WorkAcceptanceHistory> => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/acceptances', {
        params: { path: { projectID, itemID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
  })
}

export function useCompleteWorkItem(projectID: string, itemID: string) {
  const qc = useQueryClient()
  const { data: session } = useSession()
  return useMutation({
    mutationFn: async (body: DecisionInput) => {
      if (!session?.csrf_token) throw new Error('缺少浏览器安全会话，请重新登录。')
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/complete', {
        params: { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': session.csrf_token },
        body,
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: async () => {
      await qc.invalidateQueries({ queryKey: workKey(projectID) })
    },
  })
}
