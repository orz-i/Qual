import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { api, apiErrorMessage, requestKey } from '@/lib/qual-api'
import { useSession } from '@/features/auth/session'

export type PermissionName = components['schemas']['PermissionName']
export type PermissionEffect = components['schemas']['PermissionEffect']
export type PermissionSetting = components['schemas']['PermissionSetting']
export type AccessMember = components['schemas']['AccessMember']
export type SelfAccess = components['schemas']['SelfAccess']

const accessKey=(projectID:string)=>['project-access',projectID] as const

export function useMyAccess(projectID:string){
  return useQuery({
    queryKey:[...accessKey(projectID),'self'],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/access/me',{
        params:{path:{projectID}},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },
    retry:false,
  })
}
export function useAccessMembers(projectID:string,enabled:boolean){
  return useQuery({
    queryKey:[...accessKey(projectID),'members'],
    enabled,
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/access',{
        params:{path:{projectID}},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data.items
    },
    retry:false,
  })
}
export function useChangeAccess(projectID:string){
  const {data:session}=useSession()
  const queries=useQueryClient()
  return useMutation({
    mutationFn:async(input:{
      principalID:string;permission:PermissionName;effect:PermissionEffect;expectedVersion:number;reason:string
    })=>{
      if(!session?.csrf_token)throw new Error('请重新登录并获取当前授权会话。')
      const {data,error}=await api.PUT(
        '/api/v1/projects/{projectID}/access/{principalID}/permissions/{permission}',{
          params:{
            path:{projectID,principalID:input.principalID,permission:input.permission},
            header:{'Idempotency-Key':requestKey()},
          },
          headers:{'X-CSRF-Token':session.csrf_token},
          body:{effect:input.effect,expected_version:input.expectedVersion,reason:input.reason.trim()},
        })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },
    onSuccess:async()=>{await queries.invalidateQueries({queryKey:accessKey(projectID)})},
  })
}
export const allPermissions: PermissionName[]=[
  'work:read','work:write','work:claim','work:review','audit:read',
  'test:read','test:write','artifact:read','artifact:write','run:report',
  'archive:export','webhook:manage','access:manage',
]
export const permissionLabels:Record<PermissionName,string>={
  'work:read':'读取任务/缺陷',
  'work:write':'创建/修改任务',
  'work:claim':'领取任务',
  'work:review':'审核并完成任务',
  'audit:read':'读取项目审计',
  'test:read':'读取测试',
  'test:write':'记录/维护测试',
  'artifact:read':'读取证据附件',
  'artifact:write':'上传证据附件',
  'run:report':'登记外部 AgentRun（需显式授权）',
  'archive:export':'生成项目归档',
  'webhook:manage':'管理事件订阅',
  'access:manage':'管理项目权限',
}
