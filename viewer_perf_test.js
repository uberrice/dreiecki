// Exercise viewer code itself with deterministic browser mocks. No browser timing claims.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const html = fs.readFileSync('viewer.html', 'utf8');
const between = (start, end) => html.slice(html.indexOf(start), html.indexOf(end, html.indexOf(start)));
const pixels = between('<script id="pixcode">', '</script>').replace('<script id="pixcode">', '');
// The mode list is read from the viewer, never copied, so the two cannot drift apart.
const MODES = JSON.parse(html.match(/const MODES = (\[[^\]]*\])/)[1]);
function context(values = {}) {
  return vm.createContext({ assert, console, performance: { now: () => 1000 }, ...values });
}
function execute(ctx, code) { return vm.runInContext(code, ctx); }
function tileTests() {
  const timers = [];
  const ctx = context({
    OffscreenCanvas: class { getContext() { return {}; } },
    DATA: { pages: [{ width: 297, height: 210 }] }, state: { page: 0 },
    rt: new Map([[0, { ov: { R: 6, a: {}, b: {}, d: {} }, an: {} }]]),
    requestDraw() {}, paperColor() { return '#fff'; },
    setTimeout: (fn) => { timers.push(fn); return timers.length; },
    clearTimeout: (h) => { if (h) timers[h - 1] = null; },
    pendingTimers: () => timers.filter((t) => t != null).length,
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
    // Dropped tiles must release their bitmaps.
    const closed=[];
    const bmp=()=>({close(){closed.push(1);}});
    tiles.clear();
    tiles.set('0|0|0|0',{a:bmp(),b:bmp(),d:bmp()});
    tiles.set('1|0|0|0',{a:bmp()});
    dropTiles(0);
    assert.equal(tiles.size,1,'only the tiles of the dropped page are removed');
    assert.equal(closed.length,3,'dropped tiles must be closed');
    // Interaction must not pile up a timer per frame.
    lastInteract=performance.now();
    queue=[{key:'0|0|0|0',kind:'d',page:0,L:0,x:0,y:0,prio:0}];
    pumpTiles();pumpTiles();pumpTiles();
    assert.equal(pendingTimers(),1,'only one timer may be pending during interaction');
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
  // Polarity: ink only in A is red, ink only in B is green.
  execute(ctx, `(function(){
    const W=32,H=32,R=6;
    const a=new Uint8ClampedArray(W*H*4),b=new Uint8ClampedArray(W*H*4);
    const ink=(buf,x,y)=>{const j=(y*W+x)*4;buf[j]=buf[j+1]=buf[j+2]=0;buf[j+3]=255;};
    ink(a,2,2); ink(b,28,28);
    const res=analyseBuffers({a,b,W,H,R});
    const at=(x,y)=>{const j=(y*W+x)*4;return [res.diff[j],res.diff[j+1],res.diff[j+2]];};
    const removed=at(2,2), added=at(28,28);
    assert.deepEqual(removed,REM_RGB,'removed ink must be composited red');
    assert.deepEqual(added,ADD_RGB,'added ink must be composited green');
  })()`);
  // Alpha threshold: ink needs an alpha above 60.
  execute(ctx, `(function(){
    const W=8,H=8,R=6;
    const mk=()=>new Uint8ClampedArray(W*H*4);
    const at60=mk(); at60[3]=60;
    const low=analyseBuffers({a:at60,b:mk(),W,H,R});
    assert.equal(low.changed,0,'alpha at the threshold is not ink');
    assert.equal(low.regions.length,0,'alpha at the threshold must not produce a region');
    const above=mk(); above[3]=61;
    const high=analyseBuffers({a:above,b:mk(),W,H,R});
    assert.equal(high.changed,1,'ink above the threshold is a change');
    assert.equal(high.regions.length,1,'ink above the threshold must produce a region');
  })()`);
  // Colour delta threshold: shared ink only differs above a total delta of 90.
  execute(ctx, `(function(){
    const W=8,H=8,R=6;
    const mk=()=>new Uint8ClampedArray(W*H*4);
    const a=mk(), near=mk(), far=mk();
    a[0]=a[1]=a[2]=100; a[3]=255;
    near[0]=near[1]=near[2]=130; near[3]=255;
    far[0]=far[1]=far[2]=140; far[3]=255;
    assert.equal(analyseBuffers({a,b:near,W,H,R}).changed,0,'a delta of 90 is not a change');
    const res=analyseBuffers({a,b:far,W,H,R});
    assert.equal(res.changed,1,'a delta of 120 is a change');
    assert.equal(res.regions.length,1,'a colour change must produce a region');
  })()`);
  // findRegions: known input, expected region count and bounding box in mm.
  execute(ctx, `(function(){
    const W=64,H=64,R=6;
    const rem=new Uint8Array(W*H), add=new Uint8Array(W*H);
    rem[5*W+5]=1; add[50*W+50]=1;
    const regions=findRegions(rem,add,W,H,R);
    assert.equal(regions.length,2,'two distant areas are two regions');
    const near=(got,want)=>Math.abs(got-want)<1e-9;
    assert.ok(near(regions[0][0],5/R-0.8)&&near(regions[0][1],5/R-0.8)&&
      near(regions[0][2],6/R+0.8)&&near(regions[0][3],6/R+0.8),'the first region bounds the removed pixel');
    assert.ok(near(regions[1][0],50/R-0.8)&&near(regions[1][1],50/R-0.8)&&
      near(regions[1][2],51/R+0.8)&&near(regions[1][3],51/R+0.8),'the second region bounds the added pixel');
  })()`);
}
function stackTests() {
  const ctx = context({ state: { top: 'F.Cu', opacity: 1 } });
  execute(ctx, between('function paint(', 'const pixSrc ='));
  execute(ctx, `(function(){
    const layers=[
      {layer:'F.Cu',copper:true},
      {layer:'B.Cu',copper:true},
      {layer:'F.SilkS',copper:false},
      {layer:'Edge.Cuts',copper:false},
    ];
    const names=(l)=>l.map((x)=>x.layer).join(',');
    assert.equal(names(stackOrder(layers,null)),'B.Cu,F.Cu,F.SilkS,Edge.Cuts','copper is drawn back to front');
    assert.equal(names(stackOrder(layers,'F.Cu')),'B.Cu,F.Cu,F.SilkS,Edge.Cuts','the front layer stays on top');
    assert.equal(names(stackOrder(layers,'B.Cu')),'F.Cu,B.Cu,F.SilkS,Edge.Cuts','the chosen top layer is drawn last');
  })()`);
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
  const bitmaps = [];
  const bitmap = () => { const b = { closed: false, close() { b.closed = true; } }; bitmaps.push(b); return b; };
  class Canvas {
    constructor(w,h) { this.width=w;this.height=h; }
    getContext() { return {clearRect(){},drawImage(){paints++;},getImageData:()=>{readbacks++;return {data:new Uint8ClampedArray(this.width*this.height*4)};}}; }
    transferToImageBitmap() { return bitmap(); }
  }
  const ctx = context({ VIEW:view, OffscreenCanvas:Canvas, DATA:{pages:[{width:3,height:2,stack:[{layer:'F.Cu',copper:true}]}]}, state:{top:'F.Cu',opacity:1},rt:new Map(),requestDraw(){},ImageData:class {},createImageBitmap:async()=>bitmap()});
  execute(ctx,pixels+between('function paint(', 'const pixSrc =')+between('async function rasterPage(', '// ---------- tile cache ----------'));
  execute(ctx, 'const r={imgA:{},imgB:{},an:null,ov:null};rt.set(0,r);');
  ctx.runAnalysis=async(key,makeMessage)=>{analyses++;return execute(ctx,'analyseBuffers')(makeMessage());};
  return {ctx, counts:()=>({readbacks,paints,analyses}), bitmaps};
}
async function rasterTests() {
  const view=rasterContext(true); await execute(view.ctx,'rasterPage(0,r)');
  assert.deepEqual(view.counts(),{readbacks:0,paints:1,analyses:0});
  const diff=rasterContext(false); await execute(diff.ctx,'rasterPage(0,r)');
  const first=diff.bitmaps.slice();
  assert.equal(first.length,3,'the overview renders one bitmap per side plus the diff');
  execute(diff.ctx, 'const originalRegions=r.an.regions;const originalMasks=r.an.washRem;state.opacity=.5;findRegions=()=>{throw Error("repeated invariant analysis")};dilate=()=>{throw Error("repeated dilation")};');
  await execute(diff.ctx,'rasterPage(0,r)');
  assert.ok(first.every((b)=>b.closed),'superseded overview and diff bitmaps must be closed');
  assert.ok(diff.bitmaps.slice(3).every((b)=>!b.closed),'the current bitmaps must stay open');
  execute(diff.ctx,'assert.equal(r.an.regions,originalRegions);assert.equal(r.an.washRem,originalMasks);');
  assert.equal(diff.counts().analyses,2);
}
// The page cache is bounded and releases the bitmaps of the pages it drops.
async function pageCacheTests() {
  const created = new Map();
  const bitmap = (page) => {
    const b = { closed: false, close() { b.closed = true; } };
    if (!created.has(page)) created.set(page, []);
    created.get(page).push(b);
    return b;
  };
  const ctx = context({
    DATA: { pages: Array.from({ length: 8 }, (_, i) => ({ name: 'P' + i, width: 10, height: 10, svgA: 'a', svgB: 'b' })) },
    state: { page: 0 }, rt: new Map(), KEEP_PAGES: 3, VIEW: false,
    unpack: async () => ({}),
    rasterPage: async (i, r) => { r.ov = { a: bitmap(i), b: bitmap(i), d: bitmap(i) }; },
    dropTiles() {}, requestDraw() {},
  });
  execute(ctx, between('function loadPage(i)', 'async function rasterPage('));
  await execute(ctx, `(async function(){
    for (let i=0;i<DATA.pages.length;i++){ state.page=i; await loadPage(i); }
  })()`);
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(execute(ctx, 'rt.size'), 3, 'the page cache must stay within KEEP_PAGES');
  for (const i of [0, 1, 2, 3, 4]) {
    assert.ok(created.get(i).every((b) => b.closed), `evicted page ${i} must release its bitmaps`);
  }
  for (const i of [5, 6, 7]) {
    assert.ok(created.get(i).every((b) => !b.closed), `cached page ${i} must keep its bitmaps`);
  }
}
// The toolbar, the keyboard shortcuts and the mode list must agree.
function modeListTests() {
  assert.deepEqual([...html.matchAll(/data-mode="([^"]+)"/g)].map((m) => m[1]), MODES,
    'the toolbar and the keyboard shortcuts must list the same modes');
}
// Shortcuts must not fire while a form control has the focus.
function keyboardTests() {
  const calls = [];
  let handler = null;
  const state = { page: 0, mode: 'diff', measure: { on: false, p1: null } };
  const ctx = context({
    document: { addEventListener: (type, fn) => { if (type === 'keydown') handler = fn; } },
    VIEW: false, MODES,
    DATA: { pages: [{ name: 'A' }] },
    state,
    paneSize: () => ({ w: 100, h: 100 }),
    setMode: (m) => { state.mode = m; calls.push('mode:' + m); },
    gotoRegion: (d) => calls.push('region:' + d),
    selectPage: (i) => calls.push('page:' + i),
    fit: () => calls.push('fit'),
    toggleBoxes: () => calls.push('boxes'),
    cycleTop: () => calls.push('top'),
    setMeasure: (v) => calls.push('measure:' + v),
    zoomAt: () => calls.push('zoom'),
    $: () => ({ classList: { contains: () => false, toggle: () => {} } }),
  });
  execute(ctx, between('document.addEventListener("keydown"', 'function renderHeader()'));
  assert.ok(handler, 'the keydown handler must be registered');
  const press = (key, target) => {
    let prevented = false;
    handler({ key, ctrlKey: false, metaKey: false, altKey: false, target, preventDefault() { prevented = true; } });
    return prevented;
  };
  const form = { closest: () => ({}) }, body = { closest: () => null };
  press('1', body);
  assert.deepEqual(calls, ['mode:a'], 'the shortcut must work without a focused control');
  press('1', form);
  press(' ', form);
  assert.deepEqual(calls, ['mode:a'], 'shortcuts must not fire while an input has the focus');
  // A focused button keeps the space key, so it can still be activated by it.
  const button = { tagName: 'BUTTON', closest: () => null };
  assert.equal(press(' ', button), false, 'space on a focused button must not be swallowed');
  assert.equal(state.mode, 'a', 'space on a focused button must not flip the view');
  press('2', button);
  assert.equal(state.mode, 'b', 'the number shortcuts must work with a focused button');
  assert.equal(press(' ', body), true, 'space must be swallowed elsewhere so the page does not scroll');
  assert.equal(state.mode, 'a', 'space must flip between A and B');
}
// A crafted deep link must not step through an unbounded number of areas.
async function deepLinkTests() {
  for (const hash of ['#area=1000000000', '#area=Infinity']) {
    const steps = [];
    const ctx = context({
      URLSearchParams,
      localStorage: { getItem: () => null, setItem() {} },
      location: { hash },
      DATA: { pages: [{ name: 'A', status: 'changed' }] },
      VIEW: false,
      selectPage: async () => { steps.push('page'); },
      gotoRegion: () => { steps.push('region'); if (steps.length > 300) throw new Error('too many area steps'); },
    });
    execute(ctx, between('const MODES =', 'const PAPER_PREF'));
    const link = between('(async () => {\n  const q = new URLSearchParams', '})();') + '})();';
    await execute(ctx, link).then(() => {}, () => {});
    assert.equal(execute(ctx, 'MAX_AREA_STEPS'), 200, 'the deep link cap must stay small');
    assert.ok(steps.length <= 201, `the deep link must not step through unbounded areas (${steps.length})`);
  }
}
// A corrupt payload must be reported instead of rendering nothing.
async function loadFailureTests() {
  const ctx = context({
    DATA: { pages: [{ name: 'A', width: 10, height: 10, svgA: 'a', svgB: 'b' }, { name: 'B', width: 10, height: 10, svgA: 'a', svgB: 'b' }] },
    state: { page: 0 }, rt: new Map(), KEEP_PAGES: 3, VIEW: false,
    unpack: async () => { throw new Error('corrupt payload'); },
    rasterPage: async () => {},
    dropTiles() {}, requestDraw() {},
  });
  execute(ctx, between('function loadPage(i)', 'async function rasterPage('));
  await execute(ctx, 'loadPage(0)').then(() => {}, () => {});
  assert.match(execute(ctx, 'rt.get(0).error'), /Cannot render A/, 'the failure must be recorded for the status bar');
  ctx.unpack = async () => null;
  await execute(ctx, 'loadPage(1)').then(() => {}, () => {});
  assert.match(execute(ctx, 'rt.get(1).error'), /could not be decoded/, 'an undecodable image must be reported');
  // A retry must rebuild the page instead of reusing the failed record.
  const failed = execute(ctx, 'rt.get(0)');
  ctx.unpack = async () => ({});
  await execute(ctx, 'loadPage(0)').then(() => {}, () => {});
  assert.notEqual(execute(ctx, 'rt.get(0)'), failed, 'a retry must build a new record');
  assert.equal(execute(ctx, 'rt.get(0).error'), undefined, 'a successful retry must clear the error');
  // The status area shows the failure of the page on screen.
  const status = { textContent: '' };
  const shown = context({
    DATA: { pages: [{ name: 'A' }] }, state: { page: 0, view: { s: 1 } },
    rt: new Map([[0, { error: 'Cannot render A: boom' }]]),
    measureEnds: () => null, DEBUG: false, tiles: new Map(), stats: { rendered: 0, ms: 0 },
    $: () => status,
  });
  execute(shown, between('function updateStatus()', '// ---------- sidebar ----------'));
  execute(shown, 'updateStatus()');
  assert.match(status.textContent, /Cannot render A/, 'the status area must show the failure');
  // The page on screen reports its own failure, and the status follows the page that is shown.
  const liveStatus = { textContent: '' };
  const live = context({
    DATA: { pages: [{ name: 'A', width: 10, height: 10, svgA: 'a', svgB: 'b' }, { name: 'B', width: 10, height: 10, svgA: 'a', svgB: 'b' }] },
    state: { page: 0, fitted: false, region: -1, selected: null, focus: null, view: { s: 1 },
      measure: { on: false, p1: null, p2: null, live: null } },
    rt: new Map(), KEEP_PAGES: 3, VIEW: false,
    unpack: async () => { throw new Error('corrupt payload'); },
    rasterPage: async () => {},
    dropTiles() {}, renderPages() {}, renderChanges() {}, updateLayerCtl() {}, applyMode() {}, fit() {}, updateRegInfo() {},
    measureEnds: () => null, DEBUG: false, tiles: new Map(), stats: { rendered: 0, ms: 0 },
    $: () => liveStatus,
  });
  execute(live, between('function loadPage(i)', 'async function rasterPage('));
  execute(live, between('function updateStatus()', '// ---------- sidebar ----------'));
  execute(live, between('// ---------- page selection ----------', '// ---------- wiring ----------'));
  live.requestDraw = () => { execute(live, 'updateStatus()'); };
  await execute(live, 'selectPage(0)').then(() => {}, () => {});
  assert.match(liveStatus.textContent, /Cannot render A/, 'the failing page must be reported in the status bar');
  await execute(live, 'selectPage(1)').then(() => {}, () => {});
  assert.match(liveStatus.textContent, /Cannot render B/, 'the status bar must show the page on screen');
  assert.doesNotMatch(liveStatus.textContent, /Cannot render A/, 'the failure of a page that was left must not stay');
}
// The deep link, the persisted mode and the persisted filters must be validated.
function modeTests() {
  const stored = '{"mode":"nope","filters":{"__proto__":{"hacked":true},"component":false,"wire":"yes"}}';
  const ctx = context({
    localStorage: { getItem: () => stored, setItem() {} },
    location: { hash: '' },
    CATS: { component: 'Components', wire: 'Wires' },
  });
  execute(ctx, between('const MODES =', 'const PAPER_PREF'));
  assert.equal(execute(ctx, 'areaSteps("2")'), 2);
  assert.equal(execute(ctx, 'areaSteps(null)'), 0);
  assert.equal(execute(ctx, 'areaSteps("Infinity")'), 0);
  assert.equal(execute(ctx, 'areaSteps("1e999")'), 0);
  assert.equal(execute(ctx, 'areaSteps("2.5")'), 2, 'a fractional count must be rounded down, not dropped');
  assert.equal(execute(ctx, 'areaSteps("-3")'), 0);
  assert.equal(execute(ctx, 'areaSteps("1000000000")'), 200);
  const filters = execute(ctx, 'filterState()');
  assert.equal(filters.component, false, 'a stored false must hide the category');
  assert.equal(filters.wire, true, 'a non-boolean stored value must be ignored');
  assert.equal(Object.getPrototypeOf(filters), null, 'the filter map must not inherit a prototype');
  assert.deepEqual(Object.keys(filters), ['component', 'wire']);
  assert.ok(!('hacked' in filters), 'a stored __proto__ must not reach the filter map');
  // The state itself must be built from the validated values.
  let saved = '';
  const shown = context({
    DATA: { kind: 'schematic', view: false }, PCB: false, VIEW: false,
    CATS: { component: 'Components', wire: 'Wires' },
    localStorage: { getItem: () => stored, setItem: (k, v) => { saved = v; } },
    location: { hash: '' },
  });
  execute(shown, between('const MODES =', 'const PAPER_PREF'));
  execute(shown, between('const PAPER_PREF', '// ---------- page loading'));
  const state = execute(shown, 'state');
  assert.equal(state.mode, 'diff', 'a corrupted persisted mode must fall back to diff');
  // The filter of a category the report adds later must survive a reload.
  execute(shown, 'state.filters.future = false; prefs.filters = state.filters; savePrefs();');
  const reloaded = context({
    localStorage: { getItem: () => saved, setItem() {} },
    location: { hash: '' },
    CATS: { component: 'Components', wire: 'Wires' },
  });
  execute(reloaded, between('const MODES =', 'const PAPER_PREF'));
  assert.equal(execute(reloaded, 'filterState().future'), false, 'the filter of an unknown category must survive a reload');
}
function sidebarTests() {
  class Element {
    constructor(tag) { this.tag=tag;this.children=[];this.scrollTop=0;this.classList={add(){},remove(){}}; }
    append(...kids) { for(const k of kids){if(k.tag==='fragment')this.children.push(...k.children);else this.children.push(k);} }
    // Replacing the content empties the box, so the scroll position resets like in a real DOM.
    replaceChildren(...kids){this.children=[];this.scrollTop=0;this.append(...kids);}
  }
  const nodes=new Map(['#changes','#filters','#chHead'].map(key=>[key,new Element(key)]));
  const callbacks=[];
  const changes=Array.from({length:1200},(_,i)=>({category:'track',op:'added',title:String(i),details:['a','b']}));
  const future=[{category:'future',op:'added',title:'new category'},{category:'track',op:'added',title:'known'}];
  const proto=[{category:'toString',op:'added',title:'prototype name'}];
  const ctx=context({ document:{createDocumentFragment:()=>new Element('fragment')},
    $:key=>nodes.get(key), el:(tag,attrs,...kids)=>{const e=new Element(tag);e.attrs=attrs;e.append(...kids.filter(k=>k!=null));return e;},
    DATA:{pages:[{name:'F.Cu',changes},{name:'B.Cu',changes:future},{name:'X',changes:proto}]},state:{page:0,filters:{track:true},selected:null},CATS:{track:'Tracks'},OPS:{added:'+'},prefs:{},savePrefs(){},requestAnimationFrame:f=>callbacks.push(f)});
  execute(ctx,between('let changesGeneration =', '// ---------- layer stacking'));
  execute(ctx,'renderChanges()');
  assert.equal(nodes.get('#changes').children.length,101,'first chunk should contain 100 rows and heading');
  // Selection made during population must be reflected by later chunks.
  execute(ctx,'state.selected=DATA.pages[0].changes[150]');
  callbacks.shift()();
  assert.equal(nodes.get('#changes').children[151].attrs.class,'ch on');
  nodes.get('#changes').scrollTop=80;
  execute(ctx,'renderChanges()');
  assert.equal(nodes.get('#changes').scrollTop,80,'the scroll position must be restored');
  execute(ctx,'state.filters.track=false;renderChanges()');
  while(callbacks.length)callbacks.shift()();
  assert.equal(nodes.get('#changes').children.length,0,'superseded chunks must not repopulate a filtered list');
  execute(ctx,'state.filters.track=true;renderChanges()');
  while(callbacks.length)callbacks.shift()();
  assert.equal(nodes.get('#changes').children.length,1201);
  // A category the report adds later is shown and gets a chip of its own.
  execute(ctx,'state.page=1;renderChanges()');
  while(callbacks.length)callbacks.shift()();
  assert.equal(nodes.get('#changes').children.length,4,'an unknown category must not be filtered out');
  const chips=nodes.get('#filters').children;
  assert.equal(chips.length,2,'every category of the page needs a chip');
  assert.equal(chips[1].children[0],'future','the unknown category is labelled with its own name');
  assert.equal(chips[1].attrs.class,'chip on','an unknown category is visible by default');
  // A category name that collides with Object.prototype must still be counted.
  execute(ctx,'state.page=2;renderChanges()');
  while(callbacks.length)callbacks.shift()();
  const protoChips=nodes.get('#filters').children;
  assert.equal(protoChips.length,1,'the unknown category needs a chip');
  assert.equal(protoChips[0].children[0],'toString','the category is labelled with its own name');
  assert.equal(protoChips[0].children[1].children[0],'1','the count must be a number, not an inherited function');
}
// A category the report adds later has no filter entry yet, so the first click on its chip
// must hide it.
function filterChipTests() {
  class Element {
    constructor(tag) { this.tag=tag;this.children=[];this.scrollTop=0;this.classList={add(){},remove(){}}; }
    append(...kids) { for(const k of kids){if(k.tag==='fragment')this.children.push(...k.children);else this.children.push(k);} }
    replaceChildren(...kids){this.children=[];this.scrollTop=0;this.append(...kids);}
  }
  const nodes=new Map(['#changes','#filters','#chHead'].map(key=>[key,new Element(key)]));
  const callbacks=[];
  const ctx=context({ document:{createDocumentFragment:()=>new Element('fragment')},
    $:key=>nodes.get(key), el:(tag,attrs,...kids)=>{const e=new Element(tag);e.attrs=attrs;e.append(...kids.filter(k=>k!=null));return e;},
    DATA:{pages:[{name:'F.Cu',changes:[{category:'future',op:'added',title:'new category'}]}]},
    state:{page:0,filters:{},selected:null},CATS:{},OPS:{added:'+'},prefs:{},savePrefs(){},requestAnimationFrame:f=>callbacks.push(f)});
  execute(ctx,between('let changesGeneration =', '// ---------- layer stacking'));
  execute(ctx,'renderChanges()');
  while(callbacks.length)callbacks.shift()();
  assert.equal(nodes.get('#changes').children.length,2,'the change is listed with its group heading');
  const chip=nodes.get('#filters').children[0];
  assert.ok(chip,'the category of the page needs a chip');
  assert.equal(chip.attrs.class,'chip on','a category the report adds later is visible by default');
  chip.attrs.onclick();
  while(callbacks.length)callbacks.shift()();
  assert.equal(execute(ctx,'state.filters.future'),false,'the first click hides the category');
  assert.equal(nodes.get('#changes').children.length,0,'a hidden category is not listed');
  chip.attrs.onclick();
  while(callbacks.length)callbacks.shift()();
  assert.equal(execute(ctx,'state.filters.future'),true,'the second click shows the category again');
  assert.equal(nodes.get('#changes').children.length,2,'the change is listed again');
}
// A stored value that is not an object must not kill the viewer.
function prefsGuardTests() {
  const ctx = context({
    localStorage: { getItem: () => "null", setItem() {} },
    location: { hash: '' },
    DATA: { kind: 'schematic', view: false }, PCB: false, VIEW: false, CATS: {},
  });
  let error = null;
  try {
    execute(ctx, between('const MODES =', '// ---------- page loading'));
  } catch (e) { error = e; }
  assert.ok(!error, 'a stored value that is not an object must not kill the viewer: ' + error);
  assert.equal(execute(ctx, 'state.mode'), 'diff', 'the viewer falls back to the diff mode');
}
// A category whose name collides with Object.prototype must be shown under its own name.
function catNameTests() {
  const ctx = context({ CATS: { component: 'Components' } });
  execute(ctx, between('let changesGeneration =', '// ---------- layer stacking'));
  assert.equal(execute(ctx, 'catName("component")'), 'Components', 'a known category keeps its name');
  assert.equal(execute(ctx, 'catName("toString")'), 'toString', 'a category that collides with Object.prototype keeps its own name');
  assert.equal(typeof execute(ctx, 'catName("toString")'), 'string', 'the name of a category must be a string');
}
// A change without a box (a net, a title block) has nothing to zoom to.
function selectChangeTests() {
  let fitCalls = 0, fitted = null;
  const ctx = context({
    document: { querySelectorAll: () => [] },
    state: { page: 0, mode: 'diff', selected: null, focus: null, filters: {} },
    DATA: { pages: [{ name: 'F.Cu', changes: [] }] },
    CATS: {}, OPS: {}, prefs: {}, savePrefs() {}, requestAnimationFrame() {},
    setMode() {}, requestDraw() {},
    fit(box) { fitCalls++; fitted = box; },
  });
  execute(ctx, between('// ---------- areas and focus ----------', '// ---------- sidebar ----------'));
  execute(ctx, between('let changesGeneration =', '// ---------- layer stacking'));
  execute(ctx, 'selectChange({category:"net",op:"removed",title:"Nets: GND removed"}, null)');
  assert.equal(fitCalls, 0, 'a change without a box must not move the viewport');
  assert.equal(execute(ctx, 'state.selected.title'), 'Nets: GND removed', 'the change is still selected');
  execute(ctx, 'selectChange({category:"net",op:"removed",title:"x",boxA:[1,2,3,4]}, null)');
  assert.equal(fitCalls, 1, 'a change with a box is fitted');
  assert.equal(fitted.join(','), '1,2,3,4', 'the viewport is fitted to the box of the change');
}
(async()=>{tileTests();pixelTests();stackTests();await workerTests();await rasterTests();await pageCacheTests();await loadFailureTests();modeListTests();keyboardTests();await deepLinkTests();modeTests();sidebarTests();filterChipTests();prefsGuardTests();catNameTests();selectChangeTests();console.log('Viewer performance regressions passed');})().catch(error=>{console.error(error);process.exitCode=1;});
