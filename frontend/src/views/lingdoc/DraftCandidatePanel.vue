<template>
  <details class="draft-candidate-pool" @toggle="onToggle">
    <summary>课题草稿候选</summary>
    <p>在立项前保存备选思路。候选不会自动成为研究条件或正式证据。</p>
    <p v-if="loading" role="status">正在读取候选。</p>
    <p v-if="error" role="alert">{{ error }}</p>
    <button type="button" :disabled="loading || saving" @click="load">重新读取候选</button>
    <form v-if="!readonly" @submit.prevent="save">
      <label>候选标题<input v-model="title" required maxlength="120" :disabled="saving"></label>
      <label>人工备选内容<textarea v-model="content" required maxlength="10000" rows="4" :disabled="saving"></textarea></label>
      <button :disabled="saving || !title.trim() || !content.trim()">{{ saving ? '正在保存' : '保存人工备选' }}</button>
    </form>
    <p v-if="loaded && !candidates.length && !error">尚未保存草稿候选。</p>
    <article v-for="candidate in candidates" :key="candidate.id">
      <h4>{{ candidate.title }}</h4>
      <p class="muted">{{ candidate.kind === 'manual_note' ? '人工备选' : '待核候选' }} · 保存者 {{ candidate.created_by }} · 基线 {{ candidate.based_on_context_revision }}</p>
      <pre>{{ candidate.content }}</pre>
      <p v-if="candidate.provenance">来源 {{ candidate.provenance.source_type }} · {{candidate.provenance.locator || candidate.provenance.source_id}} · 仍需核对</p>
      <div v-if="!readonly && candidate.kind === 'manual_note' && !candidate.provenance">
        <label>填入的条件字段<select v-model="targetField"><option value="">选择字段</option><option v-for="field in fields" :key="field.field_id" :value="field.field_id">{{field.label}}</option></select></label>
        <button type="button" :disabled="!targetField || saving" @click="emit('use-field',targetField,candidate.content)">放入可编辑条件草稿</button>
      </div>
      <p v-else-if="candidate.kind !== 'manual_note' || candidate.provenance" class="muted">带来源候选需保持其来源与人工核对记录；此处不将它伪装成人工填写。</p>
    </article>
  </details>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { Project } from '@/api/lingdoc/workspace'
import { createDraftCandidate, listDraftCandidates, type DraftCandidate } from '@/api/lingdoc/g1Initialization'
const props=withDefaults(defineProps<{project:Project;readonly?:boolean;currentUserId?:string}>(),{readonly:true,currentUserId:''})
const emit=defineEmits<{dirty:[value:boolean];'use-field':[fieldId:string,content:string]}>()
const candidates=ref<DraftCandidate[]>([]),title=ref(''),content=ref(''),targetField=ref(''),loading=ref(false),saving=ref(false),loaded=ref(false),error=ref('')
const fields=computed(()=>props.project.template_copy?.definition.fields || [])
let epoch=0,readSequence=0,lastKey='',lastPayload=''
async function load(){const token=epoch,sequence=++readSequence,id=props.project.id;loading.value=true;loaded.value=false;error.value='';candidates.value=[];try{const result=await listDraftCandidates(id);if(token===epoch&&sequence===readSequence&&id===props.project.id){candidates.value=result.data;loaded.value=true}}catch{if(token===epoch&&sequence===readSequence)error.value='候选暂不可读，请重试。'}finally{if(token===epoch&&sequence===readSequence)loading.value=false}}
function onToggle(event:Event){if((event.target as HTMLDetailsElement).open&&!loaded.value&&!loading.value)void load()}
async function save(){if(props.readonly||saving.value||!title.value.trim()||!content.value.trim())return;const token=epoch,id=props.project.id,input={kind:'manual_note',title:title.value.trim(),content:content.value,level:'background' as const},payload=JSON.stringify(input);if(payload!==lastPayload){lastPayload=payload;lastKey=crypto.randomUUID()}saving.value=true;error.value='';try{const result=await createDraftCandidate(id,input,lastKey);if(!result.data?.id||result.data.project_id!==id||result.data.kind!==input.kind||result.data.title!==input.title||result.data.content!==input.content)throw new Error('unknown acknowledgement');if(token!==epoch||id!==props.project.id)return;title.value='';content.value='';lastKey='';lastPayload='';await load()}catch{if(token===epoch)error.value='候选未确认保存成功，当前输入和幂等键保留，请重试。'}finally{if(token===epoch)saving.value=false}}
watch([()=>props.project.id,()=>props.currentUserId],()=>{epoch++;readSequence++;candidates.value=[];title.value='';content.value='';targetField.value='';loading.value=false;saving.value=false;loaded.value=false;error.value='';lastKey='';lastPayload='';emit('dirty',false)})
watch([title,content],()=>emit('dirty',!!title.value||!!content.value))
const inputsSnapshot=computed(()=>JSON.stringify([title.value,content.value,targetField.value]))
defineExpose({saving,inputsSnapshot})
</script>

<style scoped>
.draft-candidate-pool{margin-block:1rem;border:1px solid #dce5df;border-radius:12px;padding:16px}.draft-candidate-pool summary{cursor:pointer;font-weight:600}.draft-candidate-pool form,.draft-candidate-pool label{display:grid;gap:8px}.draft-candidate-pool form{gap:12px;margin-block:16px}.draft-candidate-pool article{border-top:1px solid #dce5df;margin-top:16px;padding-top:8px}.draft-candidate-pool input,.draft-candidate-pool textarea,.draft-candidate-pool select{max-width:100%;min-width:0}.draft-candidate-pool pre{white-space:pre-wrap;overflow-wrap:anywhere}.muted{color:#63756b}
</style>
