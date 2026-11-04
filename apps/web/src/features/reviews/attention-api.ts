import { useInfiniteQuery } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { api, apiErrorMessage } from '@/lib/qual-api'
import { workKey } from '@/features/work/api'

export type AttentionItem = components['schemas']['AttentionItem']
export type AttentionReason = AttentionItem['reason']
export type AttentionPage = components['schemas']['AttentionPage']

export function useAttentionInbox(projectID: string, canRead: boolean) {
  return useInfiniteQuery({
    queryKey: [...workKey(projectID), 'attention'],
    enabled: Boolean(projectID && canRead),
    retry: false,
    initialPageParam: 0,
    queryFn: async ({ pageParam }) => {
      const { data, error } = await api.GET('/api/v1/projects/{projectID}/attention', {
        params: { path: { projectID }, query: { limit: 30, offset: pageParam } },
      })
      if (error || !data) throw new Error(apiErrorMessage(error))
      return data
    },
    getNextPageParam: (last) => last.has_more && !last.paging_limited ? last.next_offset : undefined,
  })
}
