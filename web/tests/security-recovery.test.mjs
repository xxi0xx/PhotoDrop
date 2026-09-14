import test from 'node:test';
import assert from 'node:assert/strict';
import { runDirect, recoverUploadGrant } from '../src/lib/direct-upload.ts';

test('expired grant can finalize an existing upload without prepare, refresh, or PUT', async () => {
  const calls = [];
  await runDirect({requestID:'existing',assetID:'original'}, {
    prepare:async()=>{throw new Error('new reservation');}, authorize:async()=>{throw new Error('expired refresh');},
    put:async()=>{throw new Error('retransmission');}, complete:async id=>calls.push(id),
    stage:()=>{}, canceled:()=>false,
  });
  assert.deepEqual(calls,['original']);
});

for (const code of ['rate_limited','event_quota','session_expired','session_quota']) {
  test(`${code} preserves successful/uncertain direct-upload identity and does not retransmit`, async () => {
    const attempt={requestID:'original-request',assetID:'original-asset'};let writes=0;
    const error=Object.assign(new Error('safe security message'),{code});
    await assert.rejects(runDirect(attempt,{
      prepare:async()=>{writes++;}, authorize:async()=>{writes++;},put:async()=>{writes++;},
      complete:async()=>{throw error;},stage:()=>{},canceled:()=>false,
    }),/safe security message/);
    assert.equal(writes,0);assert.equal(attempt.assetID,'original-asset');
  });
}

test('missing object plus expired refresh never sends another body', async () => {
  let puts=0;let refreshes=0;
  await assert.rejects(runDirect({requestID:'original',assetID:'original'}, {
    prepare:async()=>{throw new Error('unexpected preparation');},
    complete:async()=>{throw Object.assign(new Error('missing'),{code:'object_missing'});},
    authorize:async()=>{refreshes++;throw Object.assign(new Error('session expired'),{code:'session_expired'});},
    put:async()=>{puts++;},stage:()=>{},canceled:()=>false,
  }),/session expired/);
  assert.equal(refreshes,1);assert.equal(puts,0);
});

for (const scenario of ['cleaned empty grant', 'cleaned pending attempt', 'expired authorization']) {
  test(`${scenario} retries with a new grant and only one new reservation`, async () => {
    const item = {grant:{id:'old-grant'},attempt:{requestID:'request',...(scenario === 'cleaned empty grant' ? {} : {assetID:'old-asset'})}};
    const completedSibling = {grant:item.grant,attempt:{requestID:'sibling',assetID:'ready-sibling'}};
    let stage = '', puts = 0, reservations = 0, grants = 0;
    const calls = [];
    const failure = (code,status) => Object.assign(new Error(code),{code,status});
    try {
      await runDirect(item.attempt, {
        prepare:async()=>{throw failure('upload_session_not_found',404);},
        complete:async id=>{calls.push(`complete:${id}`);throw failure(scenario === 'expired authorization' ? 'object_missing' : 'asset_not_found',scenario === 'expired authorization' ? 409 : 404);},
        authorize:async()=>{throw failure('session_expired',409);},
        put:async()=>{puts++;},stage:value=>stage=value,canceled:()=>false,
      });
      assert.fail('first attempt should fail');
    } catch (error) { assert.equal(recoverUploadGrant(item,error,stage),true); }
    assert.equal(item.grant,undefined);assert.equal(item.attempt.assetID,undefined);assert.equal(puts,0);
    // Mirrors the UI's next explicit retry: a missing grant invokes verification/session creation.
    item.grant ??= await (async()=>{grants++;return {id:'new-verified-grant'};})();
    await runDirect(item.attempt, {
      prepare:async()=>{reservations++;return {asset:{id:'new-asset',status:'pending'},upload:{}};},
      authorize:async()=>{assert.fail('unexpected refresh');},
      put:async()=>{puts++;},complete:async id=>calls.push(`complete:${id}`),stage:()=>{},canceled:()=>false,
    });
    assert.equal(grants,1);assert.equal(reservations,1);assert.equal(puts,1);
    assert.deepEqual(completedSibling,{grant:{id:'old-grant'},attempt:{requestID:'sibling',assetID:'ready-sibling'}});
    if(scenario !== 'cleaned empty grant') assert.equal(calls[0],'complete:old-asset');
  });
}

for (const [code,status] of [['storage_unavailable',503],['internal_error',500],['rate_limited',429],['event_quota',409],['session_expired',409],['session_quota',409],['not_found',404],['asset_not_found',503],['',0]]) {
  test(`uncertain completion ${code || 'network error'} keeps both IDs and retries finalize-first`, async () => {
    const grant={id:'original-grant'}, attempt={requestID:'original-request',assetID:'original-asset'};
    const item={grant,attempt};let stage='',writes=0,completions=0;
    const ops={prepare:async()=>{writes++;},authorize:async()=>{writes++;},put:async()=>{writes++;},stage:value=>stage=value,canceled:()=>false};
    try { await runDirect(attempt,{...ops,complete:async()=>{throw Object.assign(new Error('uncertain'),{code,status});}}); }
    catch(error){assert.equal(recoverUploadGrant(item,error,stage),false);}
    assert.equal(item.grant,grant);assert.equal(item.attempt,attempt);
    await runDirect(item.attempt,{...ops,complete:async id=>{assert.equal(id,'original-asset');completions++;}});
    assert.equal(completions,1);assert.equal(writes,0);
  });
}
