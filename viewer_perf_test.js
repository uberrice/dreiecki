// Exercise viewer code itself with deterministic browser mocks. No browser timing claims.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync('viewer.html', 'utf8');
const between = (start, end) => html.slice(html.indexOf(start), html.indexOf(end, html.indexOf(start)));
const pixels = between('<script id="pixcode">', '</script>').replace('<script id="pixcode">', '');
function context(values = {}) {
  return vm.createContext({ assert, console, performance: { now: () => 1000 }, ...values });
}
function execute(ctx, code) { return vm.runInContext(code, ctx); }
function tileTests() {
  const ctx = context({
    OffscreenCanvas: class { getContext() { return {}; } },
    DATA: { pages: [{ width: 297, height: 210 }] }, state: { page: 0 },
    rt: new Map([[0, { ov: { R: 6, a: {}, b: {}, d: {} }, an: {} }]]),
    requestDraw() {}, paperColor() { return '#fff'; },
  });
  execute(ctx, between('const TILE =', '// ---------- drawing ----------') +
    between('function drawLayer(', 'function drawOverlays('));
  execute(ctx, `
    let rendered = 0;
    renderTile = (t, i, L, x, y, kind) => { rendered++; t[kind] = { close() {} }; };
    const canvas = { drawImage() {}, fillRect() {} };
    for (const kinds of [['d'], ['a', 'b'], ['a'], ['b']]) {
      tiles.clear(); rendered = 0;
      for (let frame = 0; frame < 8; frame++) {
        queue = []; wantedTiles.clear();
        for (const kind of kinds) drawLayer(canvas, kind, rt.get(0), DATA.pages[0], 16.1, 0, 0, 3000, 1800, 1);
        pumpTiles();
        assert(tiles.size <= MAX_TILES);
        if (frame === 0) assert.equal(rendered, 24 * kinds.length);
        else assert.equal(rendered, 24 * kinds.length, 'stationary viewport must settle');
      }
    }
    // Edge positions and larger device sizes must also settle without exceeding the budget.
    for (const [w, h, x, y] of [[7680,4320,-300,-400],[2560,1440,-93,-172],[1200,800,500,500]]) {
      tiles.clear(); rendered = 0; let first;
      for(let frame=0;frame<8;frame++) {
        queue=[];wantedTiles.clear();
        drawLayer(canvas,'d',rt.get(0),DATA.pages[0],32.1,x,y,w,h,1);pumpTiles();
        assert(tiles.size<=MAX_TILES);
        if(frame===0)first=rendered;else assert.equal(rendered,first);
      }
    }
  `);
}
function pixelTests() {
  const ctx = context(); execute(ctx, pixels);
  execute(ctx, `
    const W=16,H=12,R=6,a=new Uint8ClampedArray(W*H*4),b=new Uint8ClampedArray(a);
    a[3]=255;b[19]=255;b[24]=200;b[27]=255;
    const original=analyseBuffers({a,b,W,H,R});
    const cached=analyseBuffers({a,b,W,H,R,analysis:original});
    assert.deepEqual(cached.diff,original.diff);
    const faintA=a.map((n,i)=>i%4===3?n/2:n), faintB=b.map((n,i)=>i%4===3?n/2:n);
    const display=analyseBuffers({a:faintA,b:faintB,W,H,R,analysis:original});
    assert.equal(display.regions,original.regions);
    assert.equal(display.washRem,original.washRem);
    assert.equal(display.changed,original.changed);
    assert.notDeepEqual(display.diff,original.diff);
  `);
}
async function workerTests() {
  const messages = [];
  const mockWorker = { postMessage(msg, buffers) { messages.push(structuredClone(msg, { transfer: buffers })); }, terminate() {} };
  const ctx = context({ worker: mockWorker }); execute(ctx, pixels);
  execute(ctx, between('const pending = new Map();', 'function loadPage(i)'));
  execute(ctx, `
    let allocations=0;
    const producer=()=>{allocations++;return {a:new Uint8ClampedArray(16),b:new Uint8ClampedArray(16),W:2,H:2,R:1};};
    let transferred;
    const first=runAnalysis(0,()=>{const msg=producer();transferred=msg.a;return msg;});
    assert.equal(transferred.byteLength,0,'input RGBA should be transferred');
    const replaced=runAnalysis(0,producer);
    const last=runAnalysis(0,producer);
    assert.equal(allocations,1,'queued producers should allocate only at dispatch');
  `);
  assert.equal(await execute(ctx, 'replaced'), null);
  mockWorker.onmessage({ data: { ...execute(ctx, 'analyseBuffers')(messages[0]), id: messages[0].id } });
  await execute(ctx, 'first');
  assert.equal(messages.length, 2);
  mockWorker.onerror();
  const result = await execute(ctx, 'last');
  assert.equal(result.diff.length, 16);
  assert.equal(execute(ctx, 'allocations'), 3, 'worker fallback recreates detached buffers');
  assert.equal(execute(ctx, 'pending.size'), 0);
  assert.equal(execute(ctx, 'activeJob'), null);
  const skipped = execute(ctx, 'runAnalysis(2,()=>null)');
  assert.equal(await skipped, null);
}
function rasterContext(view) {
  let readbacks = 0, paints = 0, analyses = 0;
  class Canvas {
    constructor(w,h) { this.width=w;this.height=h; }
    getContext() { return {clearRect(){},drawImage(){paints++;},getImageData:()=>{readbacks++;return {data:new Uint8ClampedArray(this.width*this.height*4)};}}; }
    transferToImageBitmap() { return {close(){}}; }
  }
  const ctx = context({ VIEW:view, OffscreenCanvas:Canvas, DATA:{pages:[{width:3,height:2,stack:[{layer:'F.Cu',copper:true}]}]}, state:{top:'F.Cu',opacity:1},rt:new Map(),requestDraw(){},ImageData:class {},createImageBitmap:async()=>({close(){}})});
  execute(ctx,pixels+between('function paint(', 'const pixSrc =')+between('async function rasterPage(', '// ---------- tile cache ----------'));
  execute(ctx, 'const r={imgA:{},imgB:{},an:null,ov:null};rt.set(0,r);');
  ctx.runAnalysis=async(key,makeMessage)=>{analyses++;return execute(ctx,'analyseBuffers')(makeMessage());};
  return {ctx, counts:()=>({readbacks,paints,analyses})};
}
async function rasterTests() {
  const view=rasterContext(true); await execute(view.ctx,'rasterPage(0,r)');
  assert.deepEqual(view.counts(),{readbacks:0,paints:1,analyses:0});
  const diff=rasterContext(false); await execute(diff.ctx,'rasterPage(0,r)');
  execute(diff.ctx, 'const originalRegions=r.an.regions;const originalMasks=r.an.washRem;state.opacity=.5;findRegions=()=>{throw Error("repeated invariant analysis")};dilate=()=>{throw Error("repeated dilation")};');
  await execute(diff.ctx,'rasterPage(0,r)');
  execute(diff.ctx,'assert.equal(r.an.regions,originalRegions);assert.equal(r.an.washRem,originalMasks);');
  assert.equal(diff.counts().analyses,2);
}
function sidebarTests() {
  class Element {
    constructor(tag) { this.tag=tag;this.children=[];this.scrollTop=0;this.classList={add(){},remove(){}}; }
    append(...kids) { for(const k of kids){if(k.tag==='fragment')this.children.push(...k.children);else this.children.push(k);} }
    replaceChildren(...kids){this.children=[];this.append(...kids);}
  }
  const nodes=new Map(['#changes','#filters','#chHead'].map(key=>[key,new Element(key)]));
  const callbacks=[];
  const changes=Array.from({length:1200},(_,i)=>({category:'track',op:'added',title:String(i),details:['a','b']}));
  const ctx=context({ document:{createDocumentFragment:()=>new Element('fragment')},
    $:key=>nodes.get(key), el:(tag,attrs,...kids)=>{const e=new Element(tag);e.attrs=attrs;e.append(...kids.filter(k=>k!=null));return e;},
    DATA:{pages:[{name:'F.Cu',changes}]},state:{page:0,filters:{track:true},selected:null},CATS:{track:'Tracks'},OPS:{added:'+'},prefs:{},savePrefs(){},requestAnimationFrame:f=>callbacks.push(f)});
  execute(ctx,between('let changesGeneration =', '// ---------- layer stacking'));
  execute(ctx,'renderChanges()');
  assert.equal(nodes.get('#changes').children.length,101,'first chunk should contain 100 rows and heading');
  // Selection made during population must be reflected by later chunks.
  execute(ctx,'state.selected=DATA.pages[0].changes[150]');
  callbacks.shift()();
  assert.equal(nodes.get('#changes').children[151].attrs.class,'ch on');
  nodes.get('#changes').scrollTop=80;
  execute(ctx,'renderChanges()');
  assert.equal(nodes.get('#changes').scrollTop,80);
  execute(ctx,'state.filters.track=false;renderChanges()');
  while(callbacks.length)callbacks.shift()();
  assert.equal(nodes.get('#changes').children.length,0,'superseded chunks must not repopulate a filtered list');
  execute(ctx,'state.filters.track=true;renderChanges()');
  while(callbacks.length)callbacks.shift()();
  assert.equal(nodes.get('#changes').children.length,1201);
}
(async()=>{tileTests();pixelTests();await workerTests();await rasterTests();sidebarTests();console.log('Viewer performance regressions passed');})().catch(error=>{console.error(error);process.exitCode=1;});
