import { get, post } from '@/utils/request'
import type { Result, ChapterVersion } from './workspace'
export interface ChapterReviewState {chapter_id:string;chapter_version_id:string;context_revision:number;spec_revision:number;version:number;status:'DRAFT'|'IN_REVIEW'|'STALE'|'CONFIRMED'|'ARCHIVED';actor_id:string;reason?:string;updated_at?:string}
export interface CommentSelection {start_utf16:number;end_utf16:number;quote_markdown:string}
export interface ChapterComment {id:string;chapter_id:string;chapter_version_id:string;context_revision:number;version:number;actor_id:string;body:string;selection?:CommentSelection;resolved:boolean;created_at:string;updated_at:string}
const path=(p:string,c:string)=>`/api/v1/lingdoc/projects/${encodeURIComponent(p)}/chapters/${encodeURIComponent(c)}`
export const getChapterReview=(p:string,c:string)=>get<Result<ChapterReviewState>>(`${path(p,c)}/review`)
export const listChapterComments=(p:string,c:string,v?:string)=>get<Result<ChapterComment[]>>(`${path(p,c)}/comments`,{params:{version_id:v}})
export const setChapterReview=(p:string,c:string,inBody:{expected_project_version:number;expected_context_revision:number;expected_review_version:number;chapter_version_id:string;action:'submit'|'return';reason:string},key:string)=>post<Result<ChapterReviewState>>(`${path(p,c)}/review`,inBody,{headers:{'Idempotency-Key':key}})
export const saveChapterComment=(p:string,c:string,inBody:{expected_project_version:number;expected_context_revision:number;chapter_version_id:string;expected_version:number;body:string;id?:string;selection?:CommentSelection;resolve:boolean},key:string)=>post<Result<ChapterComment>>(`${path(p,c)}/comments`,inBody,{headers:{'Idempotency-Key':key}})

export const listReviewChapterVersions=(p:string,c:string)=>get<Result<{items:ChapterVersion[]}>>(`${path(p,c)}/versions`)
