<script setup lang="ts">
import { onMounted, onUnmounted, ref } from 'vue'

defineProps<{ chapterTitle: string }>()
const emit = defineEmits<{ (event: 'wording'): void; (event: 'semantic'): void; (event: 'cancel'): void }>()
const dialog = ref<HTMLDialogElement | null>(null)
let returnFocus: HTMLElement | null = null
onMounted(() => {
  returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  dialog.value?.showModal()
})
onUnmounted(() => {
  dialog.value?.close()
  if (returnFocus?.isConnected) returnFocus.focus()
})
</script>

<template>
  <dialog ref="dialog" class="chapter-commit-review" aria-labelledby="chapter-commit-title" aria-describedby="chapter-commit-help" @cancel.prevent="emit('cancel')">
    <header><h2 id="chapter-commit-title">这次修改是否改变研究内容？</h2><button type="button" aria-label="返回编辑" @click="emit('cancel')">×</button></header>
    <p id="chapter-commit-help">正在提交“{{ chapterTitle }}”。系统不会替你判断研究含义，请选择本次修改的性质。工作副本会保留。</p>
    <div class="commit-choices">
      <button type="button" @click="emit('wording')"><strong>仅调整表述，提交正文</strong><span>修改措辞、顺序或格式，未改变已确认的研究条件、结论或资料范围。将创建章节版本，之后仍需确认本章。</span></button>
      <button type="button" @click="emit('semantic')"><strong>研究内容有变化，先起草变更</strong><span>研究对象、方法、已确认结论、正式资料范围或规则发生变化。打开现有正式变更入口，评估后由 Owner 决定；此刻不提交正文。</span></button>
      <button type="button" autofocus @click="emit('cancel')"><strong>还不确定，继续编辑</strong><span>暂不提交，也不把本次修改自动归为普通表述。</span></button>
    </div>
  </dialog>
</template>

<style scoped>
.chapter-commit-review { width:min(640px,calc(100vw - 32px));max-height:calc(100vh - 48px);overflow-y:auto;box-sizing:border-box;border:1px solid #d9dfda;border-radius:16px;padding:24px;background:#fff;color:#24352c;box-shadow:0 24px 72px #12231c30; }
.chapter-commit-review::backdrop { background:#17241d66; }
header { display:flex;align-items:flex-start;gap:16px;justify-content:space-between; }
h2 { margin:0;font-size:20px;line-height:1.5; }
header button { border:0;background:transparent;font-size:24px;cursor:pointer; }
p { line-height:1.7;color:#59685f; }
.commit-choices { display:grid;gap:12px;margin-top:20px; }
.commit-choices button { width:100%;text-align:left;padding:16px;border:1px solid #cdd9d0;border-radius:10px;background:#f8faf8;color:inherit;cursor:pointer;white-space:normal; }
.commit-choices button:hover { background:#edf5ee; }
.commit-choices button:focus-visible { outline:3px solid #347d58;outline-offset:3px; }
strong,span { display:block; }
span { margin-top:6px;font-size:13px;line-height:1.7;color:#59685f; }
</style>
