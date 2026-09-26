// New-user Compose install from public files only; never reads the developer .env.
import assert from 'node:assert/strict';
import { execFile, execFileSync } from 'node:child_process';
import { promisify } from 'node:util';
import { randomBytes, createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, copyFileSync, readFileSync, writeFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { dirname, join, resolve, relative } from 'node:path';
import { photo } from './photo-fixture.mjs';

const runAsync = promisify(execFile);
const root=process.cwd(), dir=mkdtempSync(join(tmpdir(),'photodrop-fresh-'));
const project=`photodrop-fresh-${randomBytes(5).toString('hex')}`;
const password=randomBytes(24).toString('hex'), base='http://localhost:8080';
// Only public source files: ignored credentials, databases, binaries, logs, and
// screenshots can never be copied. The entire candidate tree is built afresh.
const files=execFileSync('git',['ls-files','--cached','--others','--exclude-standard','-z'],{encoding:'utf8'}).split('\0').filter(Boolean);
for(const file of files) {
  const source=resolve(root,file), target=resolve(dir,file);
  assert.ok(!relative(root,source).startsWith('..') && !relative(dir,target).startsWith('..'));
  mkdirSync(dirname(target),{recursive:true}); copyFileSync(source,target);
}
writeFileSync(join(dir,'.env'),readFileSync(join(dir,'.env.example'),'utf8').replace(/^PHOTODROP_ADMIN_PASSWORD=$/m,`PHOTODROP_ADMIN_PASSWORD=${password}`).replace(/^PHOTODROP_BASE_URL=$/m,`PHOTODROP_BASE_URL=${base}`),{mode:0o600});
const env=Object.fromEntries(Object.entries(process.env).filter(([key])=>!key.startsWith('PHOTODROP_') && !key.startsWith('COMPOSE_')));
env.COMPOSE_PROJECT_NAME=project; env.PHOTODROP_IMAGE=`${project}:candidate`;
const compose=(args, s3=false, capture=false, run=execFileSync)=>run('docker',['compose','--project-directory',dir,'-f',join(dir,'compose.yml'),...(s3?['-f',join(dir,'compose.fixture.yml')]:[]),...args],{cwd:dir,env,encoding:'utf8',stdio:capture?['ignore','pipe','pipe']:'inherit'});
let cookie='',csrf='';
async function json(method,path,body,status=200,admin=true) {
  const headers={Origin:base,'Content-Type':'application/json'};
  if(admin){headers.Cookie=cookie;headers['X-CSRF-Token']=csrf;}
  const response=await fetch(base+path,{method,headers,body:body===undefined?undefined:JSON.stringify(body)});
  assert.equal(response.status,status,`${method} ${path}: unexpected status`);
  return {response,body:status===204?null:await response.json()};
}
writeFileSync(join(dir,'compose.fixture.yml'),`# Disposable fixture only, never a production dependency.
services:
  objects:
    build:
      context: .
      target: backend
    command: [sh, -ec, 'go build -o /tmp/s3test ./scripts/s3test && exec /tmp/s3test -listen :18095 -origin http://localhost:8080 -delay 0s']
    ports: ['127.0.0.1:8080:8080', '127.0.0.1:18095:18095']
    healthcheck:
      test: [CMD-SHELL, 'wget -q -O /dev/null http://localhost:18095/__test/requests']
      interval: 2s
      retries: 90
  photodrop:
    ports: !reset []
    network_mode: service:objects
    depends_on:
      objects:
        condition: service_healthy
    environment:
      PHOTODROP_STORAGE_PROVIDER: s3
      PHOTODROP_STORAGE_BACKEND_KEY: fresh-s3
      PHOTODROP_S3_BUCKET: fresh-photos
      PHOTODROP_S3_ENDPOINT: http://localhost:18095
      PHOTODROP_S3_PATH_STYLE: 'true'
      PHOTODROP_S3_ACCESS_KEY_ID: test-s3-access
      PHOTODROP_S3_SECRET_ACCESS_KEY: test-s3-secret-for-local-tests-only
    volumes: !override
      - ./s3-data:/data
`);
try {
  if (process.argv.includes('--compose-smoke')) {
    const shell = process.platform === 'win32' ? (process.env.PHOTODROP_TEST_BASH || 'C:/Program Files/Git/bin/bash.exe') : 'sh';
    execFileSync(shell, ['scripts/smoke-compose.sh'], {cwd:dir, env:{...env,MSYS_NO_PATHCONV:'1'},stdio:'inherit'});
  } else for(const s3 of [false,true]) {
    compose(['config','--quiet'],s3);
    // Allow fetch to process socket closure between the two disposable installs.
    await compose(['up','--build','--wait','--wait-timeout','180','-d'],s3,false,runAsync);
    assert.equal((await json('GET','/healthz')).body.status,'ok');
    const login=await json('POST','/api/admin/login',{password},200,false);
    cookie=login.response.headers.getSetCookie()[0].split(';')[0];csrf=login.body.csrf_token;
    const event=(await json('POST','/api/admin/events',{name:'Fresh documented install',enabled:true,max_assets:10},201)).body.event;
    assert.equal(event.id,1,'Each storage mode must begin with empty state');
    assert.equal(event.public_url,base+'/e/'+event.public_id);
    assert.equal((await fetch(event.public_url)).status,200);
    const bytes=photo();
    const session=(await json('POST',`/api/public/events/${event.public_id}/upload-sessions`,{contributor_name:'Fresh guest'},201,false)).body;
    assert.equal(session.upload_strategy,s3?'direct':'local');
    const path=`/api/public/events/${event.public_id}/upload-sessions/${session.upload_session.id}/assets`;
    if(s3) {
      const prepared=(await json('POST',path+'/prepare',{filename:'fresh.png',content_type:'image/png',size:bytes.length,request_id:randomBytes(16).toString('hex')},201,false)).body;
      assert.equal(new URL(prepared.upload.url).origin,'http://localhost:18095');
      assert.equal((await fetch(prepared.upload.url,{method:'PUT',headers:prepared.upload.headers,body:bytes})).status,200);
      await json('POST',path+`/${prepared.asset.id}/complete`,{},200,false);
    } else {
      assert.equal((await fetch(base+path,{method:'POST',headers:{Origin:base,'Content-Type':'image/png','Content-Disposition':'attachment; filename=fresh.png'},body:bytes})).status,201);
    }
    const details=(await json('GET',`/api/admin/events/${event.id}`)).body.event;
    assert.equal(details.media.photo_count,1);assert.equal(details.contributors[0].name,'Fresh guest');
    compose(['exec','-T','--user','10001','photodrop','photodrop','export','--event',String(event.id),'--output','/data/fresh-export'],s3);
    const manifest=JSON.parse(compose(['exec','-T','photodrop','cat','/data/fresh-export/photodrop-manifest.json'],s3,true));
    const hash=compose(['exec','-T','photodrop','sha256sum','/data/fresh-export/photos/'+manifest.assets[0].exportFilename],s3,true).split(' ')[0];
    assert.equal(hash,createHash('sha256').update(bytes).digest('hex'));
    console.log(`Fresh ${s3?'S3':'local'} install: documented files, no old .env, health, login, event URL, named guest upload, export hash passed.`);
    await compose(['down'],s3,false,runAsync);
  }
} finally {
  try {compose(['down','--remove-orphans'],true);} catch {}
  // Delete only data beneath this mkdtemp-owned mount. It may be UID 10001-owned
  // on Linux, so use the test image before removing the owned host directory.
  try {execFileSync('docker',['run','--rm','-v',`${dir}:/fixture`,'--entrypoint','sh',`${project}:candidate`,'-ec','rm -rf /fixture/data /fixture/s3-data'],{stdio:'pipe'});} catch {}
  try {execFileSync('docker',['image','rm',`${project}:candidate`,`${project}-objects`],{stdio:'pipe'});} catch {}
  rmSync(dir,{recursive:true,force:true});
}
