import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'
import { fileURLToPath } from 'node:url'
import { runInNewContext } from 'node:vm'
import ts from 'typescript'

test('template audit client encodes the profile and forwards the pagination cursor', async () => {
 const file = fileURLToPath(new URL('./templateGovernance.ts', import.meta.url))
 const compiled = ts.transpileModule(readFileSync(file, 'utf8'), {
  compilerOptions: { module: ts.ModuleKind.CommonJS, target: ts.ScriptTarget.ES2022 },
 }).outputText
 const calls: Array<{ url: string; config: unknown }> = []
 const exports: any = {}
 runInNewContext(compiled, {
  exports,
  require(name: string) {
   assert.equal(name, '@/utils/request')
   return { get: (url: string, config: unknown) => { calls.push({ url, config }); return Promise.resolve({ data: { events: [] } }) }, post: () => undefined }
  },
 })
 await exports.listTemplateAuditHistory('template/a', 42)
 assert.equal(calls[0].url, '/api/v1/lingdoc/template-catalog/profiles/template%2Fa/audit-events')
 assert.equal(JSON.stringify(calls[0].config), JSON.stringify({ params: { after_id: 42 } }))
})
