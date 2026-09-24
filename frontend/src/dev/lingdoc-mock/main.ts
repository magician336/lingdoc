// Match the app entry: initialize Vue before request.ts loads TDesign utilities.
import 'vue'
import { get } from '@/utils/request'
import { createContractMockAdapter } from './adapter'

if (!import.meta.env.DEV) throw new Error('T06 Mock page is development-only')
const status = document.querySelector<HTMLParagraphElement>('#status')!
const payload = document.querySelector<HTMLPreElement>('#payload')!
const buttons = [...document.querySelectorAll<HTMLButtonElement>('button')]

async function run(code: 200 | 422, example: string) {
  buttons.forEach(button => { button.disabled = true })
  status.textContent = 'MOCK · 加载中'
  try {
    const result = await get<unknown>('/api/v1/lingdoc/templates/template-demo', {
      adapter: createContractMockAdapter({ enabled: import.meta.env.DEV, status: code, example }),
    })
    status.textContent = `MOCK · 成功 · HTTP ${result.$httpStatus}`
    payload.textContent = JSON.stringify(result, null, 2)
  } catch (error) {
    const failure = error as { $httpStatus?: number; message?: string }
    status.textContent = `MOCK · 失败 · ${failure.$httpStatus ?? '连接或校验异常'} · ${failure.message ?? ''}`
    payload.textContent = JSON.stringify(error, null, 2)
  } finally {
    buttons.forEach(button => { button.disabled = false })
  }
}

document.querySelector('#success')!.addEventListener('click', () => void run(200, 'success'))
document.querySelector('#failure')!.addEventListener('click', () => void run(422, 'invalid_state'))
document.documentElement.dataset.mockReady = 'true'
