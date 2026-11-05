// Pure excerpt helpers live beside the shared browser component because the
// candidate host uses an explicit static module allow-list.
export {
  makeSourceExcerpt, mapTextareaSelectionToSource, pendingSourceRequestFields,
  refreshSourceExcerpt, resolveHistoricalSource, sourceRequestFields, toSourceExcerptInput,
} from './source-document-page.js';
