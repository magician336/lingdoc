import { uploadKnowledgeFile, getKnowledgeDetails, getKnowledgeSpans } from '@/api/knowledge-base'
import { bindAsset, listAssets, retrieveSources } from '@/api/lingdoc/workspace'
import { newIdempotencyKey } from '@/api/lingdoc/candidateAdoption'
import type { KnowledgeProcessOverrides } from '@/types/knowledgeProcess'
import type { MaterialKnowledge, MaterialPorts } from './projectMaterialIntake'

/** Keep platform envelopes and duplicate errors out of the project UI.
 * Metadata records upload intent, NOT authoritative project ownership. */
export const projectMaterialPorts: MaterialPorts = {
  newKey: newIdempotencyKey,
  async upload(kb, file, project, options) {
    let result: any
    let reused = false
    try {
      result = await uploadKnowledgeFile(kb, {
        file, process_config: options as KnowledgeProcessOverrides,
        metadata: JSON.stringify({ lingdoc_upload_project_id: project, lingdoc_upload_origin: 'workspace' }),
      })
      if (result.success !== true) throw result
    } catch (error: any) {
      if (error?.code !== 'duplicate_file' || !error.data?.id) throw error
      result = error
      reused = true
    }
    return { knowledge: result.data as MaterialKnowledge, reused }
  },
  async observe(project, reference) {
    const [knowledge, trace, assets] = await Promise.all([
      getKnowledgeDetails(reference.id), getKnowledgeSpans(reference.id), listAssets(project),
    ])
    return { knowledge: knowledge.data, trace: trace.data, assets: assets.data }
  },
  async bind(project, knowledge, key) { return (await bindAsset(project, knowledge, key)).data },
  async search(project, query, asset) { return (await retrieveSources(project, query, [asset])).data },
}

