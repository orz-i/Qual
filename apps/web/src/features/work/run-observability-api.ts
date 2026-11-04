import { useInfiniteQuery } from '@tanstack/react-query'
import { api, apiErrorMessage } from '@/lib/qual-api'
import { workKey } from '@/features/work/api'

export function useWorkAgentRuns(projectID: string, itemID: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'agent-runs', itemID],
    enabled: Boolean(enabled && projectID && itemID),
    retry: false,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/agent-runs', {
        params: { path: { projectID, itemID }, query: { limit: 30, offset: pageParam } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last, pages) => last.items.length === 30 && pages.length * 30 <= 10000 ? pages.length * 30 : undefined,
  })
}

export function useAgentRunReports(projectID: string, runID: string) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'agent-run-reports', runID],
    enabled: Boolean(projectID && runID),
    retry: false,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/agent-runs/{runID}/reports', {
        params: { path: { projectID, runID }, query: { limit: 30, before: pageParam || undefined } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.next_before || undefined,
  })
}
