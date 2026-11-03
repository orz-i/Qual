import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import type { components } from '@qual/sdk-ts'
import { api, apiErrorMessage, requestKey } from '@/lib/qual-api'
import { useSession } from '@/features/auth/session'

export type TestCase = components['schemas']['TestCase']
export type TestSuite = components['schemas']['TestSuite']
export type TestCaseVersion = components['schemas']['TestCaseVersion']
export type TestSpecification = components['schemas']['TestSpecification']
export type TestRun = components['schemas']['TestRun']
export type TestRunItem = components['schemas']['TestRunItem']
export type TestAttempt = components['schemas']['TestAttempt']
export type ResultStatus = components['schemas']['TestResultStatus']
export type JUnitSummary = components['schemas']['JUnitImportSummary']
export type DefectLink = components['schemas']['DefectLink']
export type AttemptDefect = components['schemas']['AttemptDefect']

const testKey = (projectID: string) => ['testing',projectID] as const

export function useTestCases(projectID: string) {
  return useQuery({
    queryKey: [...testKey(projectID),'cases'],
    queryFn: async () => {
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-cases',{params:{path:{projectID}}})
      if(error||!data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
export function useSuites(projectID: string){
  return useQuery({
    queryKey: [...testKey(projectID),'suites'],
    queryFn: async () => {
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-suites',{params:{path:{projectID}}})
      if(error||!data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
export function useTestCase(projectID:string,caseID:string){
  return useQuery({
    queryKey: [...testKey(projectID),'case',caseID],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-cases/{caseID}',
        {params:{path:{projectID,caseID}}})
      if(error||!data) throw new Error(apiErrorMessage(error))
      return data
    },
  })
}
export function useVersions(projectID:string,caseID:string){
  return useQuery({
    queryKey: [...testKey(projectID),'versions',caseID],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-cases/{caseID}/versions',
        {params:{path:{projectID,caseID}}})
      if(error||!data) throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
export async function getLatestVersion(projectID:string,caseID:string) {
  const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-cases/{caseID}/versions',
    {params:{path:{projectID,caseID}}})
  if(error||!data)throw new Error(apiErrorMessage(error))
  const version=data.items[0]
  if(!version)throw new Error('此用例尚未发布，不可添加到 TestRun。')
  return version
}
export function useCaseCoverage(projectID:string,caseID:string){
  return useQuery({
    queryKey:[...testKey(projectID),'coverage',caseID],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-cases/{caseID}/coverage',
        {params:{path:{projectID,caseID}}})
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
export function useWorkTestCoverage(projectID:string,itemID:string){
  return useQuery({
    queryKey:[...testKey(projectID),'work-coverage',itemID],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/work-items/{itemID}/test-coverage',
        {params:{path:{projectID,itemID}}})
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
export function useTestRuns(projectID: string){
  return useQuery({
    queryKey: [...testKey(projectID),'runs'],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-runs',{params:{path:{projectID}}})
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}
export function useTestRun(projectID:string,runID:string){
  return useQuery({
    queryKey: [...testKey(projectID),'run',runID],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-runs/{runID}',
        {params:{path:{projectID,runID}}})
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },
  })
}
export function useTestAttempts(projectID:string,itemID:string,enabled=true){
  return useQuery({
    enabled,
    queryKey: [...testKey(projectID),'attempts',itemID],
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-run-items/{itemID}/attempts',
        {params:{path:{projectID,itemID}}})
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}

export function useTestAttemptDefects(projectID:string,attemptID:string,enabled:boolean){
  return useQuery({
    enabled,
    queryKey:[...testKey(projectID),'attempt-defects',attemptID],
    retry:false,
    queryFn:async()=>{
      const {data,error}=await api.GET('/api/v1/projects/{projectID}/test-attempts/{attemptID}/defects',{
        params:{path:{projectID,attemptID}},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data.items
    },
  })
}

export function useTestActions(projectID:string){
  const queryClient=useQueryClient()
  const {data:session}=useSession()
  const csrf=()=>{
    const value=session?.csrf_token
    if(!value)throw new Error('浏览器会话失效，请重新登录。')
    return value
  }
  const invalidate=async()=>{
    await queryClient.invalidateQueries({queryKey:testKey(projectID)})
    await queryClient.invalidateQueries({queryKey:['work',projectID]})
  }
  const createSuite=useMutation({
    mutationFn:async(name:string)=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-suites',{
        params:{path:{projectID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},
        body:{name},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const createCase=useMutation({
    mutationFn:async(input:{specification:TestSpecification;suite_id?:string|null})=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-cases',{
        params:{path:{projectID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:input,
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const updateCase=useMutation({
    mutationFn:async({caseID,revision,specification,suiteID}:{caseID:string;revision:number;specification:TestSpecification;suiteID:string|null})=>{
      const {data,error}=await api.PATCH('/api/v1/projects/{projectID}/test-cases/{caseID}',{
        params:{path:{projectID,caseID},header:{'If-Match':`"${revision}"`,'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:{specification,suite_id:suiteID},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const publishCase=useMutation({
    mutationFn:async({caseID,revision}:{caseID:string;revision:number})=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-cases/{caseID}/versions',{
        params:{path:{projectID,caseID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:{expected_revision:revision},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const linkCoverage=useMutation({
    mutationFn:async({caseID,workID}:{caseID:string;workID:string})=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-cases/{caseID}/coverage',{
        params:{path:{projectID,caseID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:{work_item_id:workID},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const createRun=useMutation({
    mutationFn:async(input:components['schemas']['TestRunInput'])=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-runs',{
        params:{path:{projectID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:input,
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const recordAttempt=useMutation({
    mutationFn:async({itemID,status,note}:{itemID:string;status:ResultStatus;note:string})=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-run-items/{itemID}/attempts',{
        params:{path:{projectID,itemID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:{status,note,source_kind:'manual'},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const linkDefect=useMutation({
    mutationFn:async({attemptID,bugID,title,severity}:{attemptID:string;bugID?:string;title?:string;severity?:'critical'|'major'|'minor'})=>{
      const {data,error}=await api.POST('/api/v1/projects/{projectID}/test-attempts/{attemptID}/defects',{
        params:{path:{projectID,attemptID},header:{'Idempotency-Key':requestKey()}},
        headers:{'X-CSRF-Token':csrf()},body:bugID?{bug_id:bugID}:{title,severity},
      })
      if(error||!data)throw new Error(apiErrorMessage(error))
      return data
    },onSuccess:invalidate,
  })
  const importJUnit=useMutation({
    mutationFn:async({runID,file}:{runID:string;file:File}):Promise<JUnitSummary>=>{
      if(file.size>1048576)throw new Error('JUnit XML 文件必须小于或等于 1 MiB。')
      if(!/^[\w.-]+\.xml$/i.test(file.name))throw new Error('请使用不包含路径的 .xml 文件名。')
      const content=await file.text()
      const response=await fetch(`/api/v1/projects/${projectID}/test-runs/${runID}/junit-imports`,{
        method:'POST',
        credentials:'same-origin',
        headers:{
          'Content-Type':'application/xml',
          'Idempotency-Key':requestKey(),
          'X-Report-Filename':file.name,
          'X-CSRF-Token':csrf(),
        },
        body:content,
      })
      const result:unknown=await response.json()
      if(!response.ok)throw new Error(apiErrorMessage(result))
      return result as JUnitSummary
    },onSuccess:invalidate,
  })
  return {createSuite,createCase,updateCase,publishCase,linkCoverage,createRun,recordAttempt,linkDefect,importJUnit}
}
