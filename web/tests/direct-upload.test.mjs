import test from 'node:test';
import assert from 'node:assert/strict';
import { runDirect } from '../src/lib/direct-upload.ts';

const missing = () => Object.assign(new Error('not received'), {code:'object_missing'});
const unavailable = () => Object.assign(new Error('temporarily unavailable'), {code:'storage_unavailable'});
function fixture() {
  const attempt = {requestID:'one-request'};
  const calls = []; const stages = [];
  const plan = {strategy:'direct',method:'PUT',url:'https://objects.test/private',headers:{'Content-Type':'image/png','If-None-Match':'*'},expires_at:'later'};
  const prepared = {asset:{id:'one-asset',status:'pending'},upload:plan};
  const ops = {
    prepare: async () => { calls.push('prepare'); return prepared; },
    authorize: async id => { assert.equal(id,'one-asset'); calls.push('authorize'); return prepared; },
    put: async received => {assert.equal(received,plan);calls.push('put');},
    complete: async id => {assert.equal(id,'one-asset');calls.push('complete');},
    stage: state => stages.push(state), canceled:()=>false,
  };
  return {attempt,calls,stages,ops};
}
test('prepare, direct PUT, then server verification', async () => {
  const f=fixture(); await runDirect(f.attempt,f.ops);
  assert.deepEqual(f.calls,['prepare','put','complete']);assert.deepEqual(f.stages,['preparing','uploading','verifying']);assert.equal(f.attempt.assetID,'one-asset');
});
test('lost PUT response finalizes the same asset without reupload', async () => {
  const f=fixture();f.ops.put=async()=>{f.calls.push('put');throw new Error('network');};
  await runDirect(f.attempt,f.ops);assert.deepEqual(f.calls,['prepare','put','complete']);
});
test('lost completion response recovers on retry without another PUT', async () => {
  const f=fixture();let count=0;f.ops.complete=async()=>{f.calls.push('complete');if(count++===0)throw new Error('response lost');};
  await assert.rejects(runDirect(f.attempt,f.ops));await runDirect(f.attempt,f.ops);
  assert.deepEqual(f.calls,['prepare','put','complete','complete']);
});
test('expired/CORS-opaque PUT refreshes the same pending asset once', async () => {
  const f=fixture();let puts=0,checks=0;
  f.ops.put=async()=>{f.calls.push('put');if(puts++===0)throw new Error('opaque failure');};
  f.ops.complete=async()=>{f.calls.push('complete');if(checks++===0)throw missing();};
  await runDirect(f.attempt,f.ops);assert.deepEqual(f.calls,['prepare','put','complete','authorize','put','complete']);
});
test('automatic retry is bounded and manual retry preserves asset identity', async () => {
  const f=fixture();f.ops.put=async()=>{f.calls.push('put');throw new Error('network');};f.ops.complete=async()=>{f.calls.push('complete');throw missing();};
  await assert.rejects(runDirect(f.attempt,f.ops));assert.equal(f.calls.filter(x=>x==='put').length,2);
  await assert.rejects(runDirect(f.attempt,f.ops));assert.equal(f.calls.filter(x=>x==='prepare').length,1);assert.equal(f.calls.filter(x=>x==='put').length,4);assert.equal(f.attempt.assetID,'one-asset');
});
test('storage outage preserves object and does not authorize another PUT', async () => {
  const f=fixture();f.ops.put=async()=>{f.calls.push('put');throw new Error('network');};f.ops.complete=async()=>{f.calls.push('complete');throw unavailable();};
  await assert.rejects(runDirect(f.attempt,f.ops));assert.deepEqual(f.calls,['prepare','put','complete']);
});
test('invalid image is shown as failed, never treated as complete', async () => {
  const f=fixture();f.ops.complete=async()=>{f.calls.push('complete');throw Object.assign(new Error('unsupported'),{code:'unsupported_image'});};
  await assert.rejects(runDirect(f.attempt,f.ops));assert.deepEqual(f.calls,['prepare','put','complete']);
});
test('cancellation retains prepared asset for later recovery', async () => {
  const f=fixture();let canceled=false;const prepare=f.ops.prepare;
  f.ops.prepare=async()=>{const p=await prepare();canceled=true;return p;};f.ops.canceled=()=>canceled;
  await assert.rejects(runDirect(f.attempt,f.ops),/canceled/);assert.equal(f.attempt.assetID,'one-asset');assert.deepEqual(f.calls,['prepare']);
  canceled=false;await runDirect(f.attempt,f.ops);assert.deepEqual(f.calls,['prepare','complete']);
});
