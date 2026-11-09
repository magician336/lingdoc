import {test} from 'node:test';
import assert from 'node:assert/strict';
import {embedDrawing,readDrawing} from './drawing-png.mjs';
const png='iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR4nGP4z8DwHwAFAAH/iZk9HQAAAABJRU5ErkJggg==';
const record={id:'diagram-1',revision:1,title:'图',xml:'<mxfile><diagram/></mxfile>',messages:[{role:'user',content:'hello'}]};
test('portable drawing round trip and replacement',()=>{ assert.equal(readDrawing(png),null); const out=embedDrawing(png,record); assert.deepEqual(readDrawing(out),record); const next={...record,revision:2}; assert.deepEqual(readDrawing(embedDrawing(out,next)),next); });
test('reject corrupt or truncated chunks',()=>{ const out=Buffer.from(embedDrawing(png,record),'base64'); out[20]^=1; assert.throws(()=>readDrawing(out.toString('base64')),/CRC/); assert.throws(()=>readDrawing(Buffer.from(png,'base64').subarray(0,30).toString('base64'))); });
test('reject unsafe XML',()=>assert.throws(()=>embedDrawing(png,{...record,xml:'<!DOCTYPE x [<!ENTITY x SYSTEM "file:///x">]><x>&x;</x>'})));

test('accept drawing attributes containing greater-than and reject non-drawing roots',()=>{const xml='<mxfile name="a > b"><diagram/></mxfile>';assert.equal(readDrawing(embedDrawing(png,{...record,xml})).xml,xml);assert.throws(()=>embedDrawing(png,{...record,xml:'<html/>'}),{code:'VALIDATION'});});
