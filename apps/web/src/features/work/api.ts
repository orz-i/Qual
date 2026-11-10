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

export function useWorkCapabilities(projectID: string, itemID: string) {
  return useQuery({
    queryKey: [...workKey(projectID), 'capabilities', itemID],
    enabled: Boolean(projectID && itemID),
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/capabilities', {
        params: { path: { projectID, itemID } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
  })
}

export function useTaskContextFacts(
  projectID: string, itemID: string, version: number,
  section: 'overview' | 'attempts' | 'historical_attributions' | 'artifacts', authorized: boolean,
) {
  return useQuery({
    queryKey: [...workKey(projectID), 'context', itemID, version, section],
    enabled: authorized && Boolean(projectID && itemID && version > 0),
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/context', {
        params: { path: { projectID, itemID }, query: { section, limit: 8 } },
      })
      if (error || !data) throw new QualAPIError(error)
      if (data.context_revision !== version) {
        throw new Error('CONTEXT_CHANGED：任务版本已变化，请刷新后核对实际来源')
      }
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
    // Both a 403 revocation and a 409 race invalidate the *task* capability
    // snapshot. Never keep a previously visible mutating button as a grant.
    if (error.code === 'PERMISSION_DENIED' || error.code === 'VERSION_CONFLICT' ||
        error.code === 'CONFLICT' || error.code === 'ALREADY_CLAIMED') {
      void queryClient.invalidateQueries({ queryKey: workKey(projectID) })
      if (error.code === 'PERMISSION_DENIED') {
        void queryClient.invalidateQueries({ queryKey: ['project-access', projectID] })
      }
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

  const lifecycleRequest = async (path: 'release' | 'review-claim' | 'review-release', itemID: string, version: number) => {
    const body = { expected_version: version }
    const params = { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } }
    const headers = { 'X-CSRF-Token': requireCSRF() }
    const result = path === 'release'
      ? await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/release', { params, headers, body })
      : path === 'review-claim'
        ? await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/review-claim', { params, headers, body })
        : await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/review-release', { params, headers, body })
    if (result.error || !result.data) throw new QualAPIError(result.error)
    return result.data
  }
  type VersionInput = { itemID: string; version: number }
  const release = useMutation({
    mutationFn: ({ itemID, version }: VersionInput) => lifecycleRequest('release', itemID, version),
    onSuccess: invalidate,
    onError: handleError,
  })
  const reviewClaim = useMutation({
    mutationFn: ({ itemID, version }: VersionInput) => lifecycleRequest('review-claim', itemID, version),
    onSuccess: invalidate,
    onError: handleError,
  })
  const reviewRelease = useMutation({
    mutationFn: ({ itemID, version }: VersionInput) => lifecycleRequest('review-release', itemID, version),
    onSuccess: invalidate,
    onError: handleError,
  })

  const reassign = useMutation({
    mutationFn: async ({ itemID, version, newAssigneeID }: { itemID: string; version: number; newAssigneeID: string }) => {
      const { data, error } = await api.POST('/api/v1/projects/{projectID}/work-items/{itemID}/reassign', {
        params: { path: { projectID, itemID }, header: { 'Idempotency-Key': requestKey() } },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: { expected_version: version, new_assignee_id: newAssigneeID },
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
    mutationFn: async ({ itemID, version, title, description, acceptanceCriteria }: { itemID: string; version: number; title: string; description: string; acceptanceCriteria: string[] }) => {
      const { data, error } = await api.PATCH('/api/v1/projects/{projectID}/work-items/{itemID}', {
        params: {
          path: { projectID, itemID },
          header: { 'Idempotency-Key': requestKey(), 'If-Match': `"${version}"` },
        },
        headers: { 'X-CSRF-Token': requireCSRF() },
        body: { title, description, acceptance_criteria: acceptanceCriteria },
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

  return { create, claim, release, reassign, reviewClaim, reviewRelease, transition, update, comment }
}
