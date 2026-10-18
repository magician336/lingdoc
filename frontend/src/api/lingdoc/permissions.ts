import {get} from '@/utils/request'
import type {Result} from './workspace'
export interface EffectivePermission {allowed:boolean;capability:string;target:string;reason_code:string;project_version:number;context_revision:number}
export const getEffectivePermission=(id:string,capability:string)=>get<Result<EffectivePermission>>(`/api/v1/lingdoc/projects/${encodeURIComponent(id)}/permissions`,{params:{capability}})
