import { cleanup, fireEvent, render } from '@testing-library/react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { VersionStrip } from '@/components/canvas/version-strip'
import { VersionsProvider, type VersionsSource } from '@/components/canvas/versions-context'
import { DictionaryProvider } from '@/hooks/use-dictionary'
import { EMPTY_SUMMARY } from '@/lib/diagram-diff'
import type { Dictionary } from '@/lib/i18n/dictionaries'
import en from '@/lib/i18n/dictionaries/en.json'
import { useVersionsStore } from '@/stores/versions-store'
import { useUiStore } from '@/stores/ui-store'

Element.prototype.scrollTo = vi.fn()
afterEach(() => { cleanup(); vi.restoreAllMocks(); useVersionsStore.getState().clear() })
const item = (id: string, number: number) => ({ id, number, xml:`<mxfile>${id}</mxfile>`, svg:'', turnIndex:number, summary:EMPTY_SUMMARY })

describe('individual diagram history deletion', () => {
    it('removes just one id without changing other labels or the canvas identity', () => {
        const store = useVersionsStore.getState()
        store.setVersions([item('a',1),item('b',2),item('c',3)])
        store.setOnCanvas('c')
        useVersionsStore.getState().removeVersion('b')
        const state = useVersionsStore.getState()
        expect(state.versions.map(v => [v.id,v.number])).toEqual([['a',1],['c',3]])
        expect(state.onCanvasVersionId).toBe('c')
        state.removeVersion('c')
        expect(useVersionsStore.getState().onCanvasVersionId).toBeNull()
        expect(useVersionsStore.getState().versions.map(v=>v.id)).toEqual(['a'])
    })
    it('offers separate delete control even for the last entry without opening compare', () => {
        const remove=vi.fn(), restore=vi.fn()
        const source: VersionsSource = {versions:[item('a',1)],onCanvasId:'a',undoneId:null,isBusy:false,canUndo:false,canRedo:false,restore,undo:vi.fn(),redo:vi.fn(),remove}
        vi.spyOn(window,'confirm').mockReturnValue(true)
        useUiStore.getState().closeCompare()
        const ui=render(<DictionaryProvider dictionary={en as unknown as Dictionary}><VersionsProvider value={source}><VersionStrip /></VersionsProvider></DictionaryProvider>)
        fireEvent.click(ui.getByRole('button',{name:/delete.*v1/i}))
        expect(remove).toHaveBeenCalledWith('a')
        expect(restore).not.toHaveBeenCalled()
        expect(useUiStore.getState().compareVersionId).toBeNull()
    })
    it('deleting the current or undone item clears only its flags and leaves remaining XML intact', () => {
        const store=useVersionsStore.getState()
        store.setVersions([item('a',1),item('b',2),item('c',3)])
        store.setUndone('c','b')
        useVersionsStore.getState().removeVersion('b')
        expect(useVersionsStore.getState().onCanvasVersionId).toBeNull()
        expect(useVersionsStore.getState().undoneVersionId).toBe('c')
        useVersionsStore.getState().removeVersion('c')
        expect(useVersionsStore.getState().undoneVersionId).toBeNull()
        expect(useVersionsStore.getState().versions[0].xml).toBe('<mxfile>a</mxfile>')
        useVersionsStore.getState().removeVersion('a')
        expect(useVersionsStore.getState().versions).toEqual([])
    })
    it('cancellation and busy state do not remove versions', () => {
        const remove=vi.fn()
        const value: VersionsSource = {versions:[item('a',1)],onCanvasId:'a',undoneId:null,isBusy:false,canUndo:false,canRedo:false,restore:vi.fn(),undo:vi.fn(),redo:vi.fn(),remove}
        vi.spyOn(window,'confirm').mockReturnValue(false)
        const ui=render(<DictionaryProvider dictionary={en as unknown as Dictionary}><VersionsProvider value={value}><VersionStrip /></VersionsProvider></DictionaryProvider>)
        fireEvent.click(ui.getByRole('button',{name:/delete.*v1/i}))
        expect(remove).not.toHaveBeenCalled()
        ui.rerender(<DictionaryProvider dictionary={en as unknown as Dictionary}><VersionsProvider value={{...value,isBusy:true}}><VersionStrip /></VersionsProvider></DictionaryProvider>)
        expect((ui.getByRole('button',{name:/delete.*v1/i}) as HTMLButtonElement).disabled).toBe(true)
    })

})
