// Disposable native OIDC flow. No owner identity provider, .env or real data.
import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';
import {randomBytes} from 'node:crypto';
import {photo} from './photo-fixture.mjs';

const id=`photodrop-oidc-${randomBytes(5).toString('hex')}`, app=`${id}-app`, provider=`${id}-provider`, data=`${id}-data`;
const image=`${id}:app`, tools=`${id}:tools`, base='http://localhost:8085', issuer='http://localhost:18995/application/o/drop/';
const password=randomBytes(24).toString('hex');
const docker=(args,options={})=>execFileSync('docker',args,{encoding:'utf8',stdio:['pipe','pipe','pipe'],maxBuffer:20*1024*1024,...options});
let running=false, jar=new Map(), csrf='';
async function wait(url){for(let i=0;i<120;i++){try{if((await fetch(url)).ok)return}catch{}await new Promise(r=>setTimeout(r,500))}throw Error('Disposable test service did not become ready')}
async function request(path,{method='GET',body,expected=200,cookies=true}={}){
  const headers={Origin:base,'Content-Type':'application/json','X-CSRF-Token':csrf};
  if(cookies)headers.Cookie=[...jar].map(([k,v])=>`${k}=${v}`).join('; ');
  const res=await fetch(base+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body),redirect:'manual'});
  assert.equal(res.status,expected,'Unexpected PhotoDrop status');
  if(cookies)for(const line of res.headers.getSetCookie()){const [name,value]=line.split(';')[0].split('=');if(line.includes('Max-Age=0'))jar.delete(name);else jar.set(name,value)}
  return res;
}
async function faults(body){const r=await fetch('http://localhost:18995/__test/faults',{method:body?'POST':'GET',headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});assert.ok(r.ok);return r.json()}
function stop(){if(running){docker(['stop','--time','15',app]);docker(['rm','-v',app]);running=false}}
async function start(mode){
  const settings={PHOTODROP_LISTEN_ADDR:':8085',PHOTODROP_BASE_URL:base,PHOTODROP_DATA_DIR:'/data',PHOTODROP_ADMIN_AUTH:mode,PHOTODROP_OIDC_ISSUER:issuer,PHOTODROP_OIDC_CLIENT_ID:'photodrop-test',PHOTODROP_OIDC_CLIENT_SECRET:'disposable-oidc-client-secret',PHOTODROP_OIDC_ALLOWED_GROUPS:'photodrop-admins',PHOTODROP_RATE_LIMIT_DISABLED:'true'};
  if(mode!=='oidc')settings.PHOTODROP_ADMIN_PASSWORD=password;
  docker(['run','-d','--name',app,'--network',`container:${provider}`,'-v',`${data}:/data`,...Object.entries(settings).flatMap(([k,v])=>['-e',`${k}=${v}`]),image]);running=true;await wait(base+'/healthz');
}
async function login(){
  const begin=await request('/api/admin/oidc/login',{expected:303});
  const authorization=new URL(begin.headers.get('location'));
  assert.equal(authorization.searchParams.get('redirect_uri'),base+'/api/admin/oidc/callback');assert.equal(authorization.searchParams.get('code_challenge_method'),'S256');
  assert.equal(authorization.searchParams.get('response_type'),'code');
  assert.match(begin.headers.getSetCookie()[0],/HttpOnly/);assert.match(begin.headers.getSetCookie()[0],/SameSite=Lax/);
  const authorized=await fetch(authorization,{redirect:'manual'});assert.equal(authorized.status,303);
  const callback=new URL(authorized.headers.get('location'));const path=callback.pathname+callback.search;
  const unbound=await request(path,{expected:303,cookies:false});assert.equal(unbound.headers.get('location'),'/admin/login?error=oidc');
  const complete=await request(path,{expected:303});assert.equal(complete.headers.get('location'),'/admin');
  assert.ok(jar.get('photodrop_session'));assert.ok(!jar.has('photodrop_oidc'));
  csrf=(await (await request('/api/admin/session')).json()).csrf_token;
  const replay=await request(path,{expected:303});assert.equal(replay.headers.get('location'),'/admin/login?error=oidc');
}
try{
  docker(['build','-t',image,'.'],{stdio:'inherit'});docker(['build','--target','backend','-t',tools,'.'],{stdio:'inherit'});
  docker(['run','--name',`${id}-compile`,tools,'go','build','-o','/tmp/oidctest','./scripts/oidctest']);docker(['commit',`${id}-compile`,tools]);docker(['rm',`${id}-compile`]);
  docker(['run','-d','--name',provider,'-p','127.0.0.1:8085:8085','-p','127.0.0.1:18995:18995',tools,'/tmp/oidctest']);await wait('http://localhost:18995/__test/faults');
  docker(['volume','create',data]);await faults({Unavailable:true});await start('oidc');
  assert.equal((await faults()).requests,0,'startup contacted provider');
  assert.deepEqual(await (await request('/api/admin/auth/methods')).json(),{password:false,oidc:true});
  await request('/api/admin/login',{method:'POST',body:{password},expected:403});
  const failed=await request('/api/admin/oidc/login',{expected:303});assert.equal(failed.headers.get('location'),'/admin/login?error=oidc');
  await faults({});await login();
  const event=(await (await request('/api/admin/events',{method:'POST',body:{name:'Disposable OIDC event',enabled:true},expected:201})).json()).event;
  await faults({Unavailable:true});const counts=await faults();stop();await start('oidc');
  await request('/api/admin/session');await request('/admin');await request('/healthz');await request(`/e/${event.public_id}`);
  assert.equal((await faults()).requests,counts.requests,'restart/session lookup contacted provider');
  const grant=(await (await request(`/api/public/events/${event.public_id}/upload-sessions`,{method:'POST',body:{contributor_name:'OIDC outage guest'},expected:201})).json()).upload_session;
  const uploaded=await fetch(base+`/api/public/events/${event.public_id}/upload-sessions/${grant.id}/assets`,{method:'POST',headers:{Origin:base,'Content-Type':'image/png','Content-Disposition':'attachment; filename=guest.png'},body:photo()});assert.equal(uploaded.status,201);
  stop();await start('password+oidc');
  await request('/api/admin/login',{method:'POST',body:{password}});csrf=(await (await request('/api/admin/session')).json()).csrf_token;
  await request('/api/admin/logout',{method:'POST',body:{},expected:204});await request('/api/admin/session',{expected:401});
  await faults({});await login();
  console.log('Native OIDC discovery/code/PKCE/JWKS/session flow, browser binding/replay, restart, outage guest upload, password fallback and local logout passed.');
  if(process.argv.includes('--browser')){
    console.log('Disposable browser fixture: http://localhost:8085/admin/login (password+oidc).');
    // Explicit interactive validation keeps only this named disposable project.
    console.log(`Cleanup: docker rm -fv ${app} ${provider}; docker volume rm ${data}; docker image rm ${image} ${tools}`);
  }
}finally{
  if(!process.argv.includes('--browser')){
    try{stop()}catch{}
    for(const name of [app,provider,`${id}-compile`])try{docker(['rm','-fv',name])}catch{}
    try{docker(['volume','rm',data])}catch{}
    for(const name of [image,tools])try{docker(['image','rm',name])}catch{}
  }
}
