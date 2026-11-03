import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { api, apiErrorMessage, QualAPIError, requestKey, type WorkItemKind, type WorkItemStatus } from '@/lib/qual-api'
import { useSession } from '@/features/auth/session'

type CreateItem = {
  kind: WorkItemKind
  title: string
  description: string
  priority: 'urgent' | 'high' | 'medium' | 'low'
  bug_details?: { severity: 'blocker' | 'critical' | 'major' | 'minor' | 'trivial' }
}

export const workKey = (projectID: string) => ['work', projectID] as const

export function useWorkItems(projectID: string, status: string, kind: string) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'list', status, kind],
    initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items', {
        params: {
          path: { projectID },
          query: {
            limit: 100,
            status: (status || undefined) as WorkItemStatus | undefined,
            kind: (kind || undefined) as WorkItemKind | undefined,
            cursor: pageParam || undefined,
          },
        },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
}

export function useWorkItem(projectID: string, itemID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'item', itemID],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}', {
        params: { path: { projectID, itemID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
  })
}

export function useComments(projectID: string, itemID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'comments', itemID],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/comments', {
        params: { path: { projectID, itemID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}

export function useProjectActivity(projectID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'activity'],
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/activity', {
        params: { path: { projectID }, query: { limit: 50 } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}

export function useWorkActions(projectID: string) {
  const queryClient = useQueryClient()
  const { data: session } = useSession()
  const csrf = session?.csrf_token ?? ''
  const invalidate = async () => { await queryClient.invalidateQueries({ queryKey: workKey(projectID) }) }
  const handleError = (error: QualAPIError) => {
    if (error.code === 'PERMISSION_DENIED') {
      void queryClient.invalidateQueries({ queryKey: ['project-access', projectID] })
    }
  }
  const requireCSRF = () => {
    if (!csrf) throw new Error('缺少浏览器安全会话，请重新登录。')
    return csrf
  }

  const create = useMutation({
    mutationFn: async (input: CreateItem) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/work-items', {
        params: { path: { projectID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: input,
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: invalidate,
    onError: handleError,
  })

  const claim = useMutation({
    mutationFn: async ({ itemID, version }: { itemID: string; version: number }) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/claim', {
        params: { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: { expected_version: version },
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: invalidate,
    onError: handleError,
  })

  const transition = useMutation({
    mutationFn: async ({ itemID, version, status }: { itemID: string; version: number; status: WorkItemStatus }) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/transitions', {
        params: { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: { status, expected_version: version },
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: invalidate,
    onError: handleError,
  })

  const update = useMutation({
    mutationFn: async ({ itemID, version, title, description }: { itemID: string; version: number; title: string; description: string }) => {
      const { data, error } = await api.PATCH('/api/v1/projects/{projectID}/work-items/{itemID}', {
        params: {
          path: { projectID, itemID },
          header: { 'Idempotency-Key': requestKey(), 'If-Match': `"${version}"` },
        },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: { title, description },
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: invalidate,
    onError: handleError,
  })

  const comment = useMutation({
    mutationFn: async ({ itemID, version, body }: { itemID: string; version: number; body: string }) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/comments', {
        params: { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: { body, expected_version: version },
      })
      if (error || !data) throw new QualAPIError(error)
      return data
    },
    onSuccess: invalidate,
    onError: handleError,
  })

  return { create, claim, transition, update, comment }
}
