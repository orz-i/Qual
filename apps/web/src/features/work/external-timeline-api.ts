import { useInfiniteQuery } from '@tanstack/react-query'
import { api, apiErrorMessage } from '@/lib/qual-api'
import { workKey } from '@/features/work/api'

export function useWorkThreads(projectID: string, itemID: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'threads', itemID],
    enabled: Boolean(enabled && projectID && itemID),
    retry: false,
    initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/threads', {
        params: { path: { projectID, itemID }, query: { limit: 30, cursor: pageParam || undefined } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
}

export function useWorkExternalEvents(projectID: string, itemID: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'external-events', itemID],
    enabled: Boolean(enabled && projectID && itemID),
    retry: false,
    initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/external-events', {
        params: { path: { projectID, itemID }, query: { limit: 30, cursor: pageParam || undefined } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.has_more ? last.next_cursor : undefined,
  })
}

export function useThreadWorkItems(projectID: string, threadID: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'thread-work-items', threadID],
    enabled: Boolean(enabled && projectID && threadID),
    retry: false,
    initialPageParam: '',
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/threads/{threadID}/work-items', {
        params: { path: { projectID, threadID }, query: { limit: 30, cursor: pageParam || undefined } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.next_cursor || undefined,
  })
}
