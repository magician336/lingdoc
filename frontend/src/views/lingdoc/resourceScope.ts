const assetScopePrefix = 'asset:'

export function assetResourceScope(assetId: string): string {
  const id = assetId.trim()
  if (!id || id !== assetId || id.includes(':')) throw new Error('资料范围标识无效。')
  return `${assetScopePrefix}${id}`
}

export function isAssetResourceScope(scope: string): boolean {
  return scope.startsWith(assetScopePrefix) && scope.length > assetScopePrefix.length
}

export function assetIdFromResourceScope(scope: string): string | undefined {
  return isAssetResourceScope(scope) ? scope.slice(assetScopePrefix.length) : undefined
}
