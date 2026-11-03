import { Button } from '@/components/ui/button'
import { qualityCounts, qualityLabels, type QualityFilter, type QualityStatus } from './quality'

export type QualityCounts = ReturnType<typeof qualityCounts>
const keys: QualityStatus[] = ['passed','failed','blocked','skipped','unreported']
const tones:Record<QualityStatus,string>={
 passed:'bg-emerald-500',
 failed:'bg-rose-500',
 blocked:'bg-amber-500',
 skipped:'bg-indigo-400',
 unreported:'bg-slate-300 dark:bg-slate-600',
}

export function QualityBar({counts}:{counts:QualityCounts}){
 return (
  <div
    role="img"
    aria-label={counts.total
      ? `最新结果：通过 ${counts.passed}、失败 ${counts.failed}、阻塞 ${counts.blocked}、跳过 ${counts.skipped}、未上报 ${counts.unreported}，共 ${counts.total} 项`
      : '当前运行没有用例'}
    className="flex h-2 w-full overflow-hidden rounded-full bg-muted"
  >
    {keys.map(status=>counts[status]>0&&(
      <span key={status} className={tones[status]}
        style={{width:`${counts[status]/counts.total*100}%`}} />
    ))}
  </div>
 )
}

export function QualityCountsSummary({counts}:{counts:QualityCounts}) {
 return (
  <div role="group" className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-5" aria-label="最新结果分布">
   {keys.map(status=>(
    <div key={status} className="rounded-xl border border-border bg-card px-3 py-3 sm:px-4">
     <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span aria-hidden="true" className={`size-2 shrink-0 rounded-full ${tones[status]}`} />
      {qualityLabels[status]}
     </div>
     <p className="mt-1.5 text-xl font-semibold tabular-nums">{counts[status]}</p>
    </div>
   ))}
  </div>
 )
}

export function QualityCompactCounts({counts}:{counts:QualityCounts}){
 return (
  <div className="flex flex-wrap gap-x-3 gap-y-1 text-xs text-muted-foreground">
    {keys.map(status=>(
      <span key={status} className="inline-flex items-center gap-1.5">
        <span aria-hidden="true" className={`size-1.5 rounded-full ${tones[status]}`} />
        {qualityLabels[status]} {counts[status]}
      </span>
    ))}
  </div>
 )
}

const filters: QualityFilter[] = ['all','failed','blocked','unreported','passed','skipped']
export function QualityFilters({value,onChange,counts}:{value:QualityFilter;onChange:(value:QualityFilter)=>void;counts:QualityCounts}){
 return (
  <div role="group" aria-label="按最新测试结果筛选" className="flex flex-wrap gap-1.5">
   {filters.map(status=>(
    <Button key={status} size="sm" type="button"
      variant={value===status?'secondary':'ghost'}
      aria-pressed={value===status} onClick={()=>onChange(status)}
      className={value===status?'bg-secondary font-semibold text-secondary-foreground':'text-muted-foreground'}>
      {status==='all'?'全部':qualityLabels[status]}
      <span className="ml-1 tabular-nums">{status==='all'?counts.total:counts[status]}</span>
    </Button>
   ))}
  </div>
 )
}
