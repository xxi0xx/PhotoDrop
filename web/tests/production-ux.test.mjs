import test from 'node:test';
import assert from 'node:assert/strict';
import jsQR from 'jsqr';
import { PNG } from 'pngjs';
import { eventQR } from '../src/lib/qr.ts';
import { selectedForUpload, photoStatus, photoProblem, retryAfterAt, guestError } from '../src/lib/upload-ux.ts';
import { recoverUploadGrant, runDirect } from '../src/lib/direct-upload.ts';

test('downloadable QR decodes to exact stable event link without network calls', async () => {
  const originalFetch = globalThis.fetch;
  globalThis.fetch = () => { throw new Error('QR must not contact a service'); };
  try {
    const url='https://photos.example.test/e/Immutable_Event-ID';
    const first=await eventQR(url);
    const second=await eventQR(url); // Event enable/expire state is not QR input.
    assert.equal(first,second);assert.match(first,/^data:image\/png;base64,/);
    const png=PNG.sync.read(Buffer.from(first.split(',')[1],'base64'));
    assert.equal(png.width,1024);
    assert.equal(jsQR(new Uint8ClampedArray(png.data),png.width,png.height)?.data,url);
    assert.deepEqual([...png.data.subarray(0,4)],[255,255,255,255]);
    await assert.rejects(eventQR('javascript:alert(1)'));
    await assert.rejects(eventQR('https://secret@photos.example.test/e/id'));
  } finally {globalThis.fetch=originalFetch;}
});

test('selection → partial failure → failed-only retry → completion → another batch', async () => {
  const first={status:'waiting',attempt:{requestID:'first'},batch:{name:'Maria'}};
  const second={status:'waiting',attempt:{requestID:'second',assetID:'accepted'},grant:{id:'old'},batch:{name:'Maria'}};
  const items=[first,second];assert.deepEqual(selectedForUpload(items,false),items);
  first.status='ready';second.status='failed';
  assert.deepEqual(selectedForUpload(items,true),[second]);
  let complete=0,writes=0;
  await runDirect(second.attempt,{prepare:async()=>{writes++;},authorize:async()=>{writes++;},put:async()=>{writes++;},complete:async()=>{complete++;},stage:()=>{},canceled:()=>false});
  second.status='ready';assert.equal(complete,1);assert.equal(writes,0);
  const later={status:'waiting',attempt:{requestID:'later'},batch:{name:'Maria & David'}};
  items.push(later);assert.deepEqual(selectedForUpload(items,false),[later]);
  assert.equal(first.batch.name,'Maria');assert.equal(second.batch.name,'Maria');
});

test('cleaned grant recovery retains the batch name and successful siblings', () => {
  const batch={name:'José 王小明'};
  const ready={status:'ready',batch,attempt:{requestID:'done',assetID:'done-id'}};
  const failed={status:'failed',batch,grant:{id:'old'},attempt:{requestID:'retry',assetID:'stale'}};
  assert.equal(recoverUploadGrant(failed,{status:404,code:'asset_not_found'},'verifying'),true);
  assert.equal(failed.batch.name,'José 王小明');assert.equal(failed.grant,undefined);
  assert.deepEqual(selectedForUpload([ready,failed],true),[failed]);assert.equal(ready.attempt.assetID,'done-id');
});

test('validation, accessible labels, quota and rate-limit messages stay guest friendly', () => {
  assert.match(photoProblem({name:'a.svg',type:'image/svg+xml',size:10},100),/JPEG/);
  assert.match(photoProblem({name:'a.jpg',type:'image/jpeg',size:101},100),/too large/);
  assert.equal(photoProblem({name:'a.heic',type:'',size:10},100),'');
  assert.equal(selectedForUpload([{status:'failed',validationError:'unsupported'}],true).length,0);
  assert.equal(photoStatus('verifying',10,10),'Processing…');assert.equal(photoStatus('ready',10,10),'✓ Uploaded');
  assert.match(guestError({code:'event_quota'}),/contact the host/);
  assert.match(guestError({status:429}),/wait a moment/);
  assert.equal(retryAfterAt('5',1000),6000);assert.equal(retryAfterAt('bad',1000),0);
  assert.equal(retryAfterAt('Thu, 01 Jan 1970 00:00:10 GMT',1000),10000);
});
