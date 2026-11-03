import { useInfiniteQuery, useQuery } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { api, apiErrorMessage } from '@/lib/qual-api'
import { workKey } from '@/features/work/api'

export type ReviewQueueEntry = components['schemas']['ReviewQueueEntry']
export type ReviewQueueDecision = components['schemas']['ReviewQueueDecision']
export type ReviewQueueLane = 'all' | ReviewQueueEntry['review_lane']

export function useReviewQueue(projectID: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'review-queue'],
    enabled: Boolean(enabled && projectID),
    initialPageParam: 0,
    retry: false,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/review-queue', {
        params: { path: { projectID }, query: { limit: 50, offset: pageParam } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.has_more ? last.next_offset : undefined,
  })
}

export function useProjectAudit(projectID: string, enabled: boolean) {
  return useQuery({
    queryKey: [...workKey(projectID), 'audit'],
    enabled: Boolean(enabled && projectID),
    retry: false,
    queryFn: async () => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/audit', {
        params: { path: { projectID }, query: { limit: 30 } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
