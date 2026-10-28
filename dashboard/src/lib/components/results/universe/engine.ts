// Three.js engine for the Universe view.
//
// Pages and links arrive as binary chunks (see internal/server/universe.go)
// and are copied into preallocated typed arrays the GPU reads directly. Links
// are an index buffer into the same vertex data as the points, so they cost
// 8 bytes each and no positions are duplicated. No JS object is created per
// page, colour and size are computed in shaders, and the scene only renders
// when something changes.
import * as THREE from 'three';
import { OrbitControls } from 'three/examples/jsm/controls/OrbitControls.js';
import { CSS2DRenderer, CSS2DObject } from 'three/examples/jsm/renderers/CSS2DRenderer.js';
import { GRAPH_IN_COLOR, GRAPH_PALETTE, GRAPH_RAMP, STATUS_LINK_COLORS } from '../../../palette';

export interface UniverseCard {
  i: number; url: string; title: string; pr: number; in: number; out: number; status: number; depth: number;
}
export interface UniverseCluster {
  key: string; label: string; count: number; prSum: number; inAvg: number; outAvg: number; errors: number;
  center: [number, number, number]; spread: number; top: UniverseCard[];
}
export interface UniverseLink { a: number; b: number; count: number }
export interface UniverseMeta {
  dim: string; dims: string[]; total: number; errors: number; bound: number;
  center?: [number, number, number]; extent?: number; // camera frame
  clusters: UniverseCluster[]; maxChunk: number; links: UniverseLink[] | null;
  hasGraph: boolean; edgesTotal: number; edgesContextual: number; sitewideTargets: number;
  // Pages left out as not architecture (shown when all is set).
  hidden?: { redirects: number; errors: number; blocked: number }; all?: boolean;
  statusCounts?: { redirects: number; errors: number; blocked: number }; // drawn pages by status
  layout: 'links' | 'folders'; unreached: number; maxLevel: number;
}
export interface NodeLinks {
  page: UniverseCard; cluster: number; out: number[]; in: number[]; outTotal: number; inTotal: number;
  level?: number; parent?: UniverseCard; // links layout: click level and the page it is reached from
  orphan?: boolean; // not reachable from the start page
}
export interface Progress {
  loaded: number; target: number; edges: number; edgeTarget: number; phase: 'pages' | 'tree' | 'links' | 'done'; reason: string;
  treeEdges?: number;
  linkError?: string; // set when links could not be loaded
}

// Neighbouring entries contrast, since the biggest clusters take the first colours.
export const PALETTE = GRAPH_PALETTE;
export const DEPTH_COLORS = ['#4361ee', '#2ec4b6', '#f5a623', '#e63946', '#9b59b6', '#7d828a']; // 0..4, 5+
export const RAMP = GRAPH_RAMP; // low, mid, high
export type ColorMode = 'cluster' | 'depth' | 'pagerank' | 'authority' | 'hub';
export const OUT_COLOR = '#f2c46d';
export const IN_COLOR = GRAPH_IN_COLOR;
const MAX_FRAME_MS = 14; // stop loading more when a frame gets slower than this

export function clusterColor(i: number) { return PALETTE[i % PALETTE.length]; }

/** Page and link budgets from what the browser reports about the device. */
export function deviceBudget(total: number, edgesTotal: number, gpu: string) {
  const nav = navigator as Navigator & { deviceMemory?: number };
  const mem = nav.deviceMemory ?? 8; // GB, Chrome reports at most 8
  const cores = nav.hardwareConcurrency ?? 4;
  let cap = mem >= 8 ? 2_000_000 : mem >= 4 ? 750_000 : 250_000;
  let edges = mem >= 8 ? 1_500_000 : mem >= 4 ? 500_000 : 150_000;
  let reason = `device memory (${mem} GB)`;
  if (cores <= 4 && cap > 500_000) { cap = 500_000; edges = Math.min(edges, 500_000); reason = `${cores} CPU cores`; }
  if (/swiftshader|llvmpipe|software|basic render/i.test(gpu)) { cap = Math.min(cap, 150_000); edges = Math.min(edges, 50_000); reason = 'software GPU'; }
  return { cap: Math.min(total, cap), edges: Math.min(edgesTotal, edges), reason };
}


// Colour of a page: by cluster (mode 0), crawl depth (1), PageRank (2) or a
// metric streamed on demand such as authority or hub (3), on a dark-to-bright scale.
const colorGLSL = /* glsl */ `
  uniform vec3 pal[8];
  uniform vec3 depthPal[6];
  uniform vec3 ramp[3];
  uniform float colorMode;
  vec3 rampColor(float v) {
    return v < 0.5 ? mix(ramp[0], ramp[1], v * 2.0) : mix(ramp[1], ramp[2], v * 2.0 - 1.0);
  }
  vec3 pageColor(float cluster, float rank, float metric) {
    if (colorMode > 2.5) return metric > 0.0 ? rampColor(metric) : vec3(0.22, 0.23, 0.25); // no score
    if (colorMode > 1.5) return rampColor(rank);
    if (colorMode > 0.5) return depthPal[int(min(metric * 255.0 + 0.5, 5.0))];
    return pal[int(mod(cluster, 8.0))];
  }`;

const pointVertex = /* glsl */ `
  attribute float cluster;
  attribute float rank;
  attribute float flags;
  attribute float metric;
  ${colorGLSL}
  uniform vec3 errColor;
  uniform vec3 redirColor;
  uniform vec3 blockedColor;
  uniform float showStatus; // 1 with "Redirects, errors & blocked" on
  uniform float highlight;  // status pill: 0 none, 1 redirects, 2 errors, 3 blocked
  uniform float focus;
  uniform float projScale;
  uniform float sizeScale;
  varying vec3 vColor;
  varying float vAlpha;
  varying float vMinAlpha;
  void main() {
    vec4 mv = modelViewMatrix * vec4(position, 1.0);
    gl_Position = projectionMatrix * mv;
    float status = mod(flags, 4.0);
    bool blocked = mod(floor(flags / 4.0), 2.0) > 0.5;
    // Status pill: 1 redirects, 2 errors, 3 blocked; others fade.
    float kind = status > 1.5 ? 2.0 : status > 0.5 ? 1.0 : blocked ? 3.0 : 0.0;
    bool lit = highlight > 0.5 && abs(kind - highlight) < 0.5;
    bool dimmed = (focus >= 0.0 && abs(cluster - focus) > 0.5) || (highlight > 0.5 && !lit);
    float world = mix(24.0, 80.0, rank) * sizeScale;
    // Every page stays a visible dot. On big crawls low-PageRank pages
    // shrank to a faint pixel while their links still showed, so links
    // seemed to fan out of nothing.
    gl_PointSize = clamp(world * projScale / -mv.z, lit ? 8.0 : dimmed ? 2.0 : 4.0, dimmed ? 6.0 : 20.0);
    vColor = colorMode > 0.5 || showStatus < 0.5 ? (status > 1.5 && colorMode < 0.5 ? errColor : pageColor(cluster, rank, metric))
      : status > 1.5 ? errColor
      : status > 0.5 ? redirColor
      : blocked ? blockedColor
      : pageColor(cluster, rank, metric);
    vAlpha = lit ? 1.0 : dimmed ? 0.12 : 0.55 + rank * 0.45;
    vMinAlpha = lit ? 1.0 : dimmed ? 0.0 : 0.6;
  }`;

const pointFragment = /* glsl */ `
  uniform float alpha;
  varying vec3 vColor;
  varying float vAlpha;
  varying float vMinAlpha;
  void main() {
    vec2 d = gl_PointCoord - 0.5;
    float r = dot(d, d);
    if (r > 0.25) discard;
    gl_FragColor = vec4(vColor, max(alpha * vAlpha, vMinAlpha) * smoothstep(0.25, 0.08, r));
  }`;

// Links reuse the point attributes; each end takes its own cluster colour, so
// a link between two clusters fades from one colour to the other.
const linkVertex = /* glsl */ `
  attribute float cluster;
  attribute float rank;
  attribute float metric;
  ${colorGLSL}
  uniform vec3 tint;
  uniform float useTint;
  uniform float focus;
  varying vec3 vColor;
  varying float vDim;
  void main() {
    gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
    vColor = useTint > 0.5 ? tint : pageColor(cluster, rank, metric);
    vDim = focus >= 0.0 && abs(cluster - focus) > 0.5 ? 0.25 : 1.0;
  }`;

const linkFragment = /* glsl */ `
  uniform float alpha;
  varying vec3 vColor;
  varying float vDim;
  void main() { gl_FragColor = vec4(vColor, alpha * vDim); }`;

// Status links: own vertices (both ends), kind 1 redirect, 2 error,
// 3 blocked. Dashes are measured in screen pixels along the line.
const statusVertex = /* glsl */ `
  attribute float kind;
  attribute float end;   // 0 at the source, 1 at the target
  attribute vec3 other;  // the line's other end
  uniform vec3 colors[3];
  uniform vec2 viewport; // drawing buffer size in pixels
  varying vec3 vColor;
  varying float vDist;   // pixels from the source, for dashes
  varying float vKind;
  vec2 screen(vec4 c) { return (c.xy / c.w * 0.5 + 0.5) * viewport; }
  void main() {
    gl_Position = projectionMatrix * modelViewMatrix * vec4(position, 1.0);
    vec4 o = projectionMatrix * modelViewMatrix * vec4(other, 1.0);
    vKind = kind;
    vColor = kind < 1.5 ? colors[0] : kind < 2.5 ? colors[1] : colors[2];
    vDist = end * length(screen(gl_Position) - screen(o));
  }`;

const statusFragment = /* glsl */ `
  uniform float alpha;
  uniform float period;
  uniform float highlight; // status pill: only that kind is drawn
  varying vec3 vColor;
  varying float vDist;
  varying float vKind;
  void main() {
    if (highlight > 0.5 && abs(vKind - highlight) > 0.5) discard;
    float t = fract(vDist / period);
    if (vKind < 1.5 && t > 0.6) discard;              // redirect: dashes
    if (vKind > 2.5 && t > 0.35) discard;             // blocked: dots
    gl_FragColor = vec4(vColor, alpha * (vKind > 1.5 && vKind < 2.5 ? 1.0 : 0.85));
  }`;

export interface EngineUrls {
  nodes: (offset: number, limit: number) => string;
  metric: (name: string, n: number) => string;
  edges: (q: { n: number; offset: number; limit: number; per: number; c?: number; sitewide: boolean; tree?: boolean }) => string;
  nodeLinks: (i: number) => string;
}
export interface EngineEvents {
  onFocus: (index: number | null) => void;
  onSelect: (links: NodeLinks | null) => void;
  onProgress: (p: Progress) => void;
}

function newStatusData() {
  return { pos: [] as number[], other: [] as number[], kind: [] as number[], end: [] as number[] };
}

export class UniverseEngine {
  private renderer: THREE.WebGLRenderer;
  private css: CSS2DRenderer;
  private scene = new THREE.Scene();
  private camera: THREE.PerspectiveCamera;
  private controls: OrbitControls;
  private geometry = new THREE.BufferGeometry();
  private pointMat!: THREE.ShaderMaterial;
  private points!: THREE.Points;
  private arrays!: { pos: Int16Array; cl: Uint16Array; rank: Uint8Array; flags: Uint8Array; metric: Uint8Array };
  private colorMode: ColorMode = 'cluster';
  private metricLoaded = '';
  private edgeIndex!: Uint32Array;
  private edgeGeo = new THREE.BufferGeometry();
  private edgeMat!: THREE.ShaderMaterial;
  // Links layout: the link tree (each page -> the page it is reached from).
  private treeIndex!: Uint32Array;
  private treeGeo = new THREE.BufferGeometry();
  private treeMat!: THREE.ShaderMaterial;
  private treeCount = 0;
  private clusterLinks: THREE.LineSegments | null = null; // all real links of the focused cluster
  private clusterLinkCount = 0;
  private selLinks: THREE.LineSegments[] = []; // inlinks/outlinks of the selected page
  private highways: THREE.LineSegments | null = null;
  // Links to redirects, errors and blocked URLs, drawn on top (status layer).
  private status: THREE.LineSegments | null = null;
  // Per source, so reloading cross links does not duplicate tree ones.
  private statusData = { tree: newStatusData(), edges: newStatusData() };
  private viewport = new THREE.Vector2(1, 1); // drawing buffer px, for screen-space dashes
  private highlight = 0; // status pill: 0 none, 1 redirects, 2 errors, 3 blocked
  private marker: CSS2DObject | null = null;
  private chips: { el: HTMLElement; obj: CSS2DObject; c: UniverseCluster }[] = [];
  private progress: Progress = { loaded: 0, target: 0, edges: 0, edgeTarget: 0, phase: 'pages', reason: '' };
  private fly: { t: number; fp: THREE.Vector3; ft: THREE.Vector3; tp: THREE.Vector3; tt: THREE.Vector3 } | null = null;
  private frame = 0;
  private visible = true;
  private abort = new AbortController();
  private overlayAbort: AbortController | null = null;
  private resizeObs: ResizeObserver;
  private visObs: IntersectionObserver;
  private radius = 1000;
  private placed = false;
  private panelPx = 470;
  private focused: number | null = null;
  private selected: number | null = null;
  private down: { x: number; y: number } | null = null;
  private sitewide = false;
  // Links layout draws only the link tree unless cross links are asked for.
  private crossLinks: boolean;
  private linkAbort: AbortController | null = null;
  private edgeBudget = 0;
  readonly gpu: string;

  constructor(private host: HTMLElement, private meta: UniverseMeta, private urls: EngineUrls, private ev: EngineEvents) {
    this.crossLinks = meta.layout !== 'links';
    this.renderer = new THREE.WebGLRenderer({ antialias: false, alpha: true, powerPreference: 'high-performance' });
    this.renderer.setPixelRatio(Math.min(devicePixelRatio, 2));
    host.appendChild(this.renderer.domElement);
    const gl = this.renderer.getContext();
    const dbg = gl.getExtension('WEBGL_debug_renderer_info');
    this.gpu = dbg ? String(gl.getParameter(dbg.UNMASKED_RENDERER_WEBGL)) : '';

    this.css = new CSS2DRenderer();
    Object.assign(this.css.domElement.style, { position: 'absolute', inset: '0', pointerEvents: 'none' });
    host.appendChild(this.css.domElement);

    this.radius = Math.max(...meta.clusters.map((c) => Math.hypot(...c.center) + c.spread), 1000);
    this.camera = new THREE.PerspectiveCamera(50, 1, 10, this.radius * 12);
    this.controls = new OrbitControls(this.camera, this.renderer.domElement);
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.08;
    this.controls.addEventListener('change', () => this.request());

    this.buildPoints();
    this.buildHighways();
    this.buildChips();
    const el = this.renderer.domElement;
    el.addEventListener('pointerdown', this.onDown);
    el.addEventListener('pointerup', this.onUp);
    this.resizeObs = new ResizeObserver(() => this.resize());
    this.resizeObs.observe(host);
    this.visObs = new IntersectionObserver(([e]) => { this.visible = e.isIntersecting; if (this.visible) this.request(); });
    this.visObs.observe(host);
    document.addEventListener('visibilitychange', this.onVisibility);
    this.resize();
  }

  private onVisibility = () => { if (!document.hidden) this.request(); };

  // Colour uniforms are shared objects, so every material follows setColorMode.
  private colorUniforms = {
    pal: { value: PALETTE.map((c) => new THREE.Color(c)) },
    depthPal: { value: DEPTH_COLORS.map((c) => new THREE.Color(c)) },
    ramp: { value: RAMP.map((c) => new THREE.Color(c)) },
    colorMode: { value: 0 },
  };

  private uniformsBase() {
    return { ...this.colorUniforms, focus: { value: -1 }, alpha: { value: 1 } };
  }

  private buildPoints() {
    const b = deviceBudget(this.meta.total, this.meta.edgesTotal, this.gpu);
    this.progress.target = b.cap;
    this.edgeBudget = b.edges;
    this.progress.reason = b.reason;
    this.arrays = { pos: new Int16Array(b.cap * 3), cl: new Uint16Array(b.cap), rank: new Uint8Array(b.cap), flags: new Uint8Array(b.cap), metric: new Uint8Array(b.cap) };
    const g = this.geometry;
    g.setAttribute('position', new THREE.BufferAttribute(this.arrays.pos, 3, true));
    g.setAttribute('cluster', new THREE.BufferAttribute(this.arrays.cl, 1, false));
    g.setAttribute('rank', new THREE.BufferAttribute(this.arrays.rank, 1, true));
    g.setAttribute('flags', new THREE.BufferAttribute(this.arrays.flags, 1, false));
    g.setAttribute('metric', new THREE.BufferAttribute(this.arrays.metric, 1, true));
    g.setDrawRange(0, 0);
    this.pointMat = new THREE.ShaderMaterial({
      vertexShader: pointVertex, fragmentShader: pointFragment, transparent: true, depthWrite: false,
      uniforms: {
        ...this.uniformsBase(), errColor: { value: new THREE.Color(STATUS_LINK_COLORS.error) },
        redirColor: { value: new THREE.Color(STATUS_LINK_COLORS.redirect) }, blockedColor: { value: new THREE.Color(STATUS_LINK_COLORS.blocked) },
        showStatus: { value: this.meta.all ? 1 : 0 }, highlight: { value: 0 }, projScale: { value: 1 }, sizeScale: { value: 1 },
      },
    });
    this.points = this.scaled(new THREE.Points(g, this.pointMat));
    this.points.renderOrder = 2;
    this.scene.add(this.points);

    // Sampled page-to-page links: an index buffer over the same vertices.
    this.edgeIndex = new Uint32Array(Math.max(2, b.edges * 2));
    for (const name of ['position', 'cluster', 'rank', 'metric']) this.edgeGeo.setAttribute(name, g.getAttribute(name));
    this.edgeGeo.setIndex(new THREE.BufferAttribute(this.edgeIndex, 1));
    this.edgeGeo.setDrawRange(0, 0);
    this.edgeMat = this.linkMaterial(0.08);
    const lines = this.scaled(new THREE.LineSegments(this.edgeGeo, this.edgeMat));
    this.scene.add(lines);

    this.treeIndex = new Uint32Array(Math.max(2, b.cap * 2));
    for (const name of ['position', 'cluster', 'rank', 'metric']) this.treeGeo.setAttribute(name, g.getAttribute(name));
    this.treeGeo.setIndex(new THREE.BufferAttribute(this.treeIndex, 1));
    this.treeGeo.setDrawRange(0, 0);
    this.treeMat = this.linkMaterial(0.3);
    this.scene.add(this.scaled(new THREE.LineSegments(this.treeGeo, this.treeMat)));
  }

  private scaled<T extends THREE.Object3D>(o: T): T {
    o.scale.setScalar(this.meta.bound); // int16 normalised [-1,1] -> world units
    o.frustumCulled = false;
    return o;
  }

  private linkMaterial(alpha: number, tint?: string) {
    return new THREE.ShaderMaterial({
      vertexShader: linkVertex, fragmentShader: linkFragment, transparent: true, depthWrite: false,
      uniforms: { ...this.uniformsBase(), alpha: { value: alpha }, tint: { value: new THREE.Color(tint ?? '#ffffff') }, useTint: { value: tint ? 1 : 0 } },
    });
  }

  /**
   * Moves the links whose target is a redirect, error or blocked URL to the
   * status layer and returns the others. Only with "Redirects, errors &
   * blocked" on; a status link is drawn once, in its own style.
   */
  private splitStatusLinks(pairs: Uint32Array, source: 'tree' | 'edges'): Uint32Array {
    if (!this.meta.all) return pairs;
    const { pos, flags } = this.arrays;
    const d = this.statusData[source];
    const keep = new Uint32Array(pairs.length);
    let kept = 0;
    for (let k = 0; k < pairs.length; k += 2) {
      const a = pairs[k], b = pairs[k + 1];
      const f = flags[b];
      const kind = (f & 3) === 2 ? 2 : (f & 3) === 1 ? 1 : f & 4 ? 3 : 0;
      if (!kind) {
        keep[kept++] = a;
        keep[kept++] = b;
        continue;
      }
      const ax = pos[a * 3] / 32767, ay = pos[a * 3 + 1] / 32767, az = pos[a * 3 + 2] / 32767;
      const bx = pos[b * 3] / 32767, by = pos[b * 3 + 1] / 32767, bz = pos[b * 3 + 2] / 32767;
      d.pos.push(ax, ay, az, bx, by, bz);
      d.other.push(bx, by, bz, ax, ay, az);
      d.kind.push(kind, kind);
      d.end.push(0, 1);
    }
    if (kept < pairs.length) this.rebuildStatus();
    return keep.subarray(0, kept);
  }

  /** Drops the status links that came with the cross links (they reload). */
  private clearEdgeStatus() {
    const d = this.statusData.edges;
    if (!d.kind.length) return;
    this.statusData.edges = newStatusData();
    this.rebuildStatus();
  }

  private rebuildStatus() {
    const { tree, edges } = this.statusData;
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.Float32BufferAttribute([...tree.pos, ...edges.pos], 3));
    g.setAttribute('other', new THREE.Float32BufferAttribute([...tree.other, ...edges.other], 3));
    g.setAttribute('kind', new THREE.Float32BufferAttribute([...tree.kind, ...edges.kind], 1));
    g.setAttribute('end', new THREE.Float32BufferAttribute([...tree.end, ...edges.end], 1));
    if (this.status) {
      this.status.geometry.dispose();
      this.status.geometry = g;
    } else {
      const c = STATUS_LINK_COLORS;
      this.status = this.scaled(new THREE.LineSegments(g, new THREE.ShaderMaterial({
        vertexShader: statusVertex, fragmentShader: statusFragment, transparent: true, depthWrite: false,
        // Dash period in screen pixels, so dashes read the same at any zoom.
        uniforms: { colors: { value: [c.redirect, c.error, c.blocked].map((x) => new THREE.Color(x)) }, alpha: { value: 0.95 }, period: { value: 9 }, viewport: { value: this.viewport }, highlight: { value: this.highlight } },
      })));
      this.status.renderOrder = 1.5;
      this.scene.add(this.status);
    }
    this.request();
  }

  /** Real links between clusters, one line per pair, brighter for more links. */
  private buildHighways() {
    // In the links layout real page links carry the picture; cluster
    // highways would only add lines between averaged positions.
    const links = this.meta.layout === 'links' ? [] : this.meta.links ?? [];
    if (!links.length) return;
    const max = Math.log(1 + links[0].count);
    const pos: number[] = [];
    const col: number[] = [];
    const c = new THREE.Color();
    for (const l of links) {
      const a = this.meta.clusters[l.a], b = this.meta.clusters[l.b];
      const w = 0.12 + 0.88 * (Math.log(1 + l.count) / max);
      pos.push(...a.center, ...b.center);
      c.set(clusterColor(l.a)).multiplyScalar(w); col.push(c.r, c.g, c.b);
      c.set(clusterColor(l.b)).multiplyScalar(w); col.push(c.r, c.g, c.b);
    }
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.Float32BufferAttribute(pos, 3));
    g.setAttribute('color', new THREE.Float32BufferAttribute(col, 3));
    this.highways = new THREE.LineSegments(g, new THREE.LineBasicMaterial({
      vertexColors: true, transparent: true, opacity: 0.55, depthWrite: false, blending: THREE.AdditiveBlending,
    }));
    this.highways.frustumCulled = false;
    this.scene.add(this.highways);
  }

  private buildChips() {
    const total = this.meta.total;
    this.meta.clusters.forEach((c, i) => {
      const el = document.createElement('button');
      el.className = 'uv-chip';
      el.innerHTML = `<span class="uv-dot" style="background:${clusterColor(i)}"></span><span class="uv-chip-label"></span><b>${c.count.toLocaleString()}</b>`;
      (el.querySelector('.uv-chip-label') as HTMLElement).textContent = c.label;
      el.onclick = (e) => { e.stopPropagation(); this.focus(i); };
      const obj = new CSS2DObject(el);
      obj.position.set(c.center[0], c.center[1] + c.spread * 0.8 + 60, c.center[2]);
      obj.center.set(0.5, 1);
      this.scene.add(obj);
      this.chips.push({ el, obj, c });
      el.dataset.small = String(c.count < total * 0.02);
    });
  }

  private emit() { this.ev.onProgress({ ...this.progress }); }

  /** Streams pages (10K first, then growing batches), then links, until the budget or frame-time limit. */
  async load() {
    const sig = this.abort.signal;
    const p = this.progress;
    let size = 10_000;
    while (p.loaded < p.target && !sig.aborted) {
      const limit = Math.min(size, p.target - p.loaded, this.meta.maxChunk);
      const res = await fetch(this.urls.nodes(p.loaded, limit), { signal: sig });
      if (!res.ok) throw new Error(`pages ${res.status}`);
      this.ingestNodes(await res.arrayBuffer());
      this.emit();
      if (p.loaded >= p.target) break;
      const ms = this.measureFrame();
      if (ms > MAX_FRAME_MS) { p.target = p.loaded; p.reason = `frame time (${ms.toFixed(1)} ms)`; break; }
      size = Math.min(size * 2, this.meta.maxChunk);
      await new Promise((r) => setTimeout(r, 30));
    }
    if (sig.aborted) return;
    if (p.loaded >= this.meta.total) p.reason = '';
    if (this.colorMode !== 'cluster') void this.setColorMode(this.colorMode); // cover pages loaded since

    if (this.meta.layout === 'links') await this.loadTree(sig);
    if (sig.aborted) return;
    if (this.crossLinks) {
      await this.loadLinks();
    } else {
      p.phase = 'done';
      this.emit();
    }
  }

  /** Links layout: add or remove the links that are not part of the link tree. */
  setCrossLinks(on: boolean) {
    if (on === this.crossLinks) return;
    this.crossLinks = on;
    if (this.progress.phase === 'pages' || this.progress.phase === 'tree') return; // load() picks it up
    if (on) {
      void this.loadLinks();
    } else {
      this.linkAbort?.abort();
      this.overlayAbort?.abort(); // a cluster-link download in flight must not draw
      this.progress.edges = 0;
      this.progress.phase = 'done';
      this.edgeGeo.setDrawRange(0, 0);
      this.clearEdgeStatus();
      this.emit();
    }
    if (this.focused !== null) {
      this.drop(this.clusterLinks);
      this.clusterLinks = null;
      if (on) this.loadClusterLinks(this.focused);
    }
    this.request();
  }

  /** Links: spread the budget across loaded pages; small sites get every link. */
  private async loadLinks() {
    this.linkAbort?.abort();
    const ctl = (this.linkAbort = new AbortController());
    const sig = AbortSignal.any([this.abort.signal, ctl.signal]);
    const p = this.progress;
    p.phase = 'links';
    p.edges = 0;
    p.linkError = undefined;
    this.edgeGeo.setDrawRange(0, 0);
    this.clearEdgeStatus();
    const available = this.sitewide ? this.meta.edgesTotal : this.meta.edgesContextual;
    p.edgeTarget = Math.min(this.edgeBudget, available);
    const per = p.loaded <= 5_000 ? 4096 : Math.max(2, Math.min(24, Math.round(p.edgeTarget / p.loaded)));
    let offset = 0;
    try {
      while (this.meta.hasGraph && p.edges < p.edgeTarget && !sig.aborted) {
        const limit = Math.min(200_000, p.edgeTarget - p.edges);
        const res = await fetch(this.urls.edges({ n: p.loaded, offset, limit, per, sitewide: this.sitewide }), { signal: sig });
        if (!res.ok) throw new Error(`links ${res.status}`);
        const parsed = parseEdges(await res.arrayBuffer());
        const { next, done } = parsed;
        const pairs = this.splitStatusLinks(parsed.pairs, 'edges');
        const room = Math.min(pairs.length, this.edgeIndex.length - p.edges * 2);
        this.edgeIndex.set(pairs.subarray(0, room), p.edges * 2);
        const idx = this.edgeGeo.getIndex()!;
        idx.clearUpdateRanges();
        idx.addUpdateRange(p.edges * 2, room);
        idx.needsUpdate = true;
        p.edges += room / 2;
        this.edgeGeo.setDrawRange(0, p.edges * 2);
        this.dimForSelection();
        this.request();
        this.emit();
        if (done) break;
        offset = next;
        const ms = this.measureFrame();
        if (ms > MAX_FRAME_MS) { p.reason ||= `frame time (${ms.toFixed(1)} ms)`; break; }
        await new Promise((r) => setTimeout(r, 30));
      }
    } catch (e) {
      if ((e as Error).name === 'AbortError' || sig.aborted) return;
      // Links are optional: keep the pages on screen and say what failed.
      p.linkError = (e as Error).message || 'request failed';
    }
    if (sig.aborted) return; // a newer load (or dispose) owns the progress now
    p.phase = 'done';
    this.emit();
  }

  /** Links layout: streams the link tree, the backbone every page hangs from. */
  private async loadTree(sig: AbortSignal) {
    const p = this.progress;
    p.phase = 'tree';
    p.treeEdges = 0;
    let offset = 0;
    try {
      while (!sig.aborted) {
        const res = await fetch(this.urls.edges({ n: p.loaded, offset, limit: 200_000, per: 1, sitewide: true, tree: true }), { signal: sig });
        if (!res.ok) throw new Error(`link tree ${res.status}`);
        const parsed = parseEdges(await res.arrayBuffer());
        const { next, done } = parsed;
        const pairs = this.splitStatusLinks(parsed.pairs, 'tree');
        const room = Math.min(pairs.length, this.treeIndex.length - this.treeCount * 2);
        this.treeIndex.set(pairs.subarray(0, room), this.treeCount * 2);
        const idx = this.treeGeo.getIndex()!;
        idx.clearUpdateRanges();
        idx.addUpdateRange(this.treeCount * 2, room);
        idx.needsUpdate = true;
        this.treeCount += room / 2;
        p.treeEdges = this.treeCount;
        this.treeGeo.setDrawRange(0, this.treeCount * 2);
        this.dimForSelection();
        this.emit();
        if (done) break;
        offset = next;
      }
    } catch (e) {
      if ((e as Error).name === 'AbortError' || sig.aborted) return;
      p.linkError = (e as Error).message || 'request failed';
    }
  }

  /** Colours pages by cluster, crawl depth, PageRank, authority or hub. Metrics load on first use. */
  async setColorMode(mode: ColorMode) {
    this.colorMode = mode;
    const id = { cluster: 0, depth: 1, pagerank: 2, authority: 3, hub: 3 }[mode];
    const name = mode === 'depth' || mode === 'authority' || mode === 'hub' ? mode : '';
    if (name && this.metricLoaded !== `${name}:${this.progress.loaded}`) {
      const n = this.progress.loaded;
      const res = await fetch(this.urls.metric(name, n), { signal: this.abort.signal });
      if (!res.ok || this.colorMode !== mode) return;
      const buf = await res.arrayBuffer();
      const count = new DataView(buf).getUint32(4, true);
      this.arrays.metric.set(new Uint8Array(buf, 16, Math.min(count, this.arrays.metric.length)));
      const a = this.geometry.getAttribute('metric') as THREE.BufferAttribute;
      a.clearUpdateRanges();
      a.addUpdateRange(0, count);
      a.needsUpdate = true;
      this.metricLoaded = `${name}:${n}`;
    }
    if (this.colorMode !== mode) return;
    this.colorUniforms.colorMode.value = id;
    this.chips.forEach((ch) => ch.el.classList.toggle('uv-mono', mode !== 'cluster'));
    this.request();
  }

  /** Include or leave out links to sitewide pages (menus, footers) and reload links. */
  setSitewide(on: boolean) {
    if (on === this.sitewide) return;
    this.sitewide = on;
    if (!this.crossLinks) return;
    if (this.progress.phase !== 'pages') void this.loadLinks();
    if (this.focused !== null) {
      this.drop(this.clusterLinks);
      this.clusterLinks = null;
      this.loadClusterLinks(this.focused);
    }
  }

  private ingestNodes(buf: ArrayBuffer) {
    const dv = new DataView(buf);
    if (dv.getUint32(0, true) !== 0x31564e55) throw new Error('bad universe chunk'); // "UNV1"
    const offset = dv.getUint32(4, true);
    const count = dv.getUint32(8, true);
    if (offset + count > this.arrays.cl.length) return;
    let q = 16;
    this.arrays.pos.set(new Int16Array(buf, q, count * 3), offset * 3); q += count * 6;
    this.arrays.cl.set(new Uint16Array(buf, q, count), offset); q += count * 2;
    this.arrays.rank.set(new Uint8Array(buf, q, count), offset); q += count;
    this.arrays.flags.set(new Uint8Array(buf, q, count), offset);
    for (const [name, size] of [['position', 3], ['cluster', 1], ['rank', 1], ['flags', 1]] as const) {
      const a = this.geometry.getAttribute(name) as THREE.BufferAttribute;
      a.clearUpdateRanges();
      a.addUpdateRange(offset * size, count * size);
      a.needsUpdate = true;
    }
    const p = this.progress;
    p.loaded = Math.max(p.loaded, offset + count);
    this.geometry.setDrawRange(0, p.loaded);
    this.pointMat.uniforms.sizeScale.value = Math.max(0.35, Math.min(1, Math.sqrt(60_000 / p.loaded)));
    this.pointMat.uniforms.alpha.value = Math.max(0.3, Math.min(0.95, Math.sqrt(120_000 / p.loaded)));
    this.request();
  }

  /** Renders a few frames synchronously and returns the average GPU-inclusive time. */
  private measureFrame(): number {
    const gl = this.renderer.getContext();
    const px = new Uint8Array(4);
    const t = performance.now();
    for (let i = 0; i < 3; i++) {
      this.renderer.render(this.scene, this.camera);
      gl.readPixels(0, 0, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, px);
    }
    return (performance.now() - t) / 3;
  }

  private resize() {
    const w = this.host.clientWidth, h = this.host.clientHeight;
    if (!w || !h) return;
    this.renderer.setSize(w, h, false);
    this.renderer.domElement.style.width = w + 'px';
    this.renderer.domElement.style.height = h + 'px';
    this.css.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.pointMat.uniforms.projScale.value = (h * this.renderer.getPixelRatio()) / (2 * Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2)));
    this.viewport.set(w * this.renderer.getPixelRatio(), h * this.renderer.getPixelRatio());
    if (!this.placed) {
      this.placed = true;
      const v = this.homeView();
      this.camera.position.copy(v.pos);
      this.controls.target.copy(v.target);
    }
    this.request();
  }

  /** Camera that fits every cluster into the area left of the side panel. */
  private homeView() {
    const w = this.host.clientWidth || 1200;
    const tan = Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2));
    const frac = Math.max(0.4, (w - this.panelPx) / w);
    const r = this.meta.extent ?? this.radius;
    const d = Math.max(r / tan, r / (tan * this.camera.aspect * frac)) * 0.95 + r * 0.4;
    const off = (this.panelPx / 2 / w) * 2 * d * tan * this.camera.aspect;
    const dir = new THREE.Vector3(-0.28, 0.32, 1).normalize();
    const right = new THREE.Vector3().crossVectors(new THREE.Vector3(0, 1, 0), dir).normalize();
    const target = right.multiplyScalar(off).add(new THREE.Vector3(...(this.meta.center ?? [0, 0, 0])));
    return { target, pos: target.clone().add(dir.multiplyScalar(d)) };
  }

  request() {
    if (!this.frame) this.frame = requestAnimationFrame(this.tick);
  }

  private tick = () => {
    this.frame = 0;
    if (!this.visible || document.hidden) return;
    let again = false;
    if (this.fly) {
      const f = this.fly;
      f.t = Math.min(1, f.t + 0.022);
      const e = f.t < 0.5 ? 4 * f.t ** 3 : 1 - Math.pow(-2 * f.t + 2, 3) / 2;
      this.camera.position.lerpVectors(f.fp, f.tp, e);
      this.controls.target.lerpVectors(f.ft, f.tt, e);
      if (f.t >= 1) this.fly = null;
      again = true;
    }
    if (this.controls.update()) again = true;
    this.renderer.render(this.scene, this.camera);
    this.css.render(this.scene, this.camera);
    this.updateChips();
    if (again) this.request();
  };

  /** Shows chips by cluster size and hides any that would overlap a bigger one. */
  private updateChips() {
    const far = (this.camera.far / 12) * 1.6;
    const placed: DOMRect[] = [];
    const order = this.focused === null ? this.chips : [this.chips[this.focused], ...this.chips.filter((_, k) => k !== this.focused)];
    for (const ch of order) {
      const idx = this.meta.clusters.indexOf(ch.c);
      const d = this.camera.position.distanceTo(ch.obj.position);
      let hide = this.focused !== null ? idx !== this.focused && d > far * 0.6 : ch.el.dataset.small === 'true' && d > far;
      if (!hide) {
        const r = ch.el.getBoundingClientRect();
        hide = placed.some((p) => r.left < p.right + 6 && r.right + 6 > p.left && r.top < p.bottom + 4 && r.bottom + 4 > p.top);
        if (!hide) placed.push(r);
      }
      ch.el.classList.toggle('uv-hide', hide);
    }
  }

  private flyTo(target: THREE.Vector3, pos: THREE.Vector3) {
    this.fly = { t: 0, fp: this.camera.position.clone(), ft: this.controls.target.clone(), tp: pos, tt: target };
    this.request();
  }

  private setFocusUniform(v: number) {
    for (const m of [this.pointMat, this.edgeMat, this.treeMat]) m.uniforms.focus.value = v;
    if (this.highways) (this.highways.material as THREE.LineBasicMaterial).opacity = v >= 0 ? 0.18 : 0.55;
    this.dimForSelection();
  }

  private drop(o: THREE.LineSegments | null) {
    if (!o) return;
    this.scene.remove(o);
    o.geometry.dispose();
    (o.material as THREE.Material).dispose();
  }

  /** While a page is selected, fade every other link so its own stand out. */
  /** Status pill: light up redirects (1), errors (2) or blocked URLs (3) and their links; 0 clears. */
  setHighlight(kind: 0 | 1 | 2 | 3) {
    this.highlight = kind;
    this.pointMat.uniforms.highlight.value = kind;
    const sm = this.status?.material as THREE.ShaderMaterial | undefined;
    if (sm) sm.uniforms.highlight.value = kind;
    this.dimForSelection();
  }

  private dimForSelection() {
    const k = (this.selLinks.length ? 0.12 : 1) * (this.highlight ? 0.25 : 1);
    // With a cluster focused its own sample (clusterLinks) carries the detail.
    const tree = this.meta.layout === 'links';
    // With the link tree drawn, extra links are a faint second layer.
    this.edgeMat.uniforms.alpha.value = linkAlpha(this.progress.edges) * k * (this.focused !== null ? 0.2 : 1) * (tree ? 0.5 : 1);
    if (this.treeMat) this.treeMat.uniforms.alpha.value = Math.min(0.55, linkAlpha(this.treeCount) * 2.2) * (this.selLinks.length || this.highlight ? 0.25 : 1);
    const cm = this.clusterLinks?.material as THREE.ShaderMaterial | undefined;
    if (cm) cm.uniforms.alpha.value = Math.min(0.5, linkAlpha(this.clusterLinkCount) * 0.7) * k;
    this.request();
  }

  private clearSelection() {
    this.selLinks.forEach((o) => this.drop(o));
    this.selLinks = [];
    this.dimForSelection();
    if (this.marker) { this.marker.element.remove(); this.scene.remove(this.marker); this.marker = null; }
  }

  private clearOverlays() {
    this.overlayAbort?.abort();
    this.overlayAbort = null;
    this.drop(this.clusterLinks);
    this.clusterLinks = null;
    this.clearSelection();
  }

  /** Index-buffer line set over the shared vertices. */
  private overlay(pairs: Uint32Array, mat: THREE.ShaderMaterial) {
    const g = new THREE.BufferGeometry();
    for (const name of ['position', 'cluster', 'rank', 'metric']) g.setAttribute(name, this.geometry.getAttribute(name));
    g.setIndex(new THREE.BufferAttribute(pairs, 1));
    const lines = this.scaled(new THREE.LineSegments(g, mat));
    lines.renderOrder = 1;
    this.scene.add(lines);
    this.request();
    return lines;
  }

  /** Focus a cluster: dim the rest and draw all of its real links. */
  focus(i: number | null) {
    this.focused = i;
    this.selected = null;
    this.clearOverlays();
    this.setFocusUniform(i ?? -1);
    this.chips.forEach((ch, k) => ch.el.classList.toggle('uv-on', k === i));
    this.ev.onFocus(i);
    this.ev.onSelect(null);
    if (i === null) {
      const v = this.homeView();
      this.flyTo(v.target, v.pos);
      return;
    }
    const c = this.meta.clusters[i];
    const w = this.host.clientWidth || 1200;
    const tan = Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2));
    const frac = Math.max(0.4, (w - this.panelPx) / w);
    const d = Math.max(c.spread * 1.25 / tan, c.spread * 1.25 / (tan * this.camera.aspect * frac)) + c.spread;
    const off = (this.panelPx / 2 / w) * 2 * d * tan * this.camera.aspect;
    const center = new THREE.Vector3(...c.center);
    const dir = this.camera.position.clone().sub(this.controls.target).normalize();
    const right = new THREE.Vector3().crossVectors(this.camera.up, dir).normalize().multiplyScalar(off);
    this.flyTo(center.clone().add(right), center.clone().add(right).add(dir.multiplyScalar(d)));
    if (this.meta.hasGraph && this.crossLinks) this.loadClusterLinks(i);
  }

  private async loadClusterLinks(i: number) {
    this.overlayAbort?.abort(); // a toggle or refocus replaces any load in flight
    const ctl = (this.overlayAbort = new AbortController());
    const parts: Uint32Array[] = [];
    let offset = 0, total = 0;
    // About 30K links per cluster: enough to read its structure without a solid blob.
    const per = Math.max(4, Math.min(256, Math.round(30_000 / Math.max(1, this.meta.clusters[i].count))));
    try {
      while (total < 400_000) {
        const res = await fetch(this.urls.edges({ n: this.progress.loaded, offset, limit: 100_000, per, c: i, sitewide: this.sitewide }), { signal: ctl.signal });
        const { next, done, pairs } = parseEdges(await res.arrayBuffer());
        parts.push(pairs.slice());
        total += pairs.length / 2;
        if (done) break;
        offset = next;
      }
    } catch { return; }
    if (ctl.signal.aborted || ctl !== this.overlayAbort || this.focused !== i || !this.crossLinks) return;
    this.drop(this.clusterLinks);
    this.clusterLinkCount = total;
    const all = new Uint32Array(total * 2);
    let at = 0;
    for (const p of parts) { all.set(p, at); at += p.length; }
    this.clusterLinks = this.overlay(all, this.linkMaterial(Math.min(0.5, linkAlpha(total) * 0.7)));
    this.dimForSelection();
  }

  /** Select a page by order index: marker, its real inlinks/outlinks, and details. */
  async select(i: number | null, fly = false) {
    this.selected = i;
    this.clearSelection();
    if (i === null) { this.ev.onSelect(null); this.request(); return; }
    const res = await fetch(this.urls.nodeLinks(i));
    if (!res.ok || this.selected !== i) return;
    const data: NodeLinks = await res.json();
    if (this.selected !== i) return;
    const n = this.progress.loaded;
    const pairs = (list: number[], outward: boolean) => {
      const keep = list.filter((k) => k < n && k !== i);
      const a = new Uint32Array(keep.length * 2);
      keep.forEach((k, j) => { a[j * 2] = outward ? i : k; a[j * 2 + 1] = outward ? k : i; });
      return a;
    };
    if (i < n) {
      this.selLinks = [
        this.overlay(pairs(data.in, false), this.linkMaterial(0.5, IN_COLOR)),
        this.overlay(pairs(data.out, true), this.linkMaterial(0.6, OUT_COLOR)),
      ];
      this.dimForSelection();
      const el = document.createElement('div');
      el.className = 'uv-marker';
      this.marker = new CSS2DObject(el);
      this.marker.position.copy(this.worldPos(i));
      this.scene.add(this.marker);
      if (fly) {
        const p = this.worldPos(i);
        const dir = this.camera.position.clone().sub(this.controls.target);
        this.flyTo(p, p.clone().add(dir));
      }
    }
    this.ev.onSelect(data);
    this.request();
  }

  private worldPos(i: number) {
    const s = this.meta.bound / 32767, a = this.arrays.pos;
    return new THREE.Vector3(a[i * 3] * s, a[i * 3 + 1] * s, a[i * 3 + 2] * s);
  }

  private onDown = (e: PointerEvent) => { this.down = { x: e.clientX, y: e.clientY }; };
  private onUp = (e: PointerEvent) => {
    if (!this.down || Math.hypot(e.clientX - this.down.x, e.clientY - this.down.y) > 5) return;
    this.down = null;
    const hit = this.pick(e.clientX, e.clientY);
    this.select(hit);
  };

  /** Nearest loaded point under the cursor; with a focused cluster only its pages count. */
  private pick(clientX: number, clientY: number): number | null {
    const rect = this.renderer.domElement.getBoundingClientRect();
    const mx = clientX - rect.left, my = clientY - rect.top;
    const W = rect.width, H = rect.height;
    this.camera.updateMatrixWorld();
    const m = new THREE.Matrix4().multiplyMatrices(this.camera.projectionMatrix, this.camera.matrixWorldInverse).multiply(this.points.matrixWorld).elements;
    const { pos, cl, rank } = this.arrays;
    const s = 1 / 32767;
    const projScale = this.pointMat.uniforms.projScale.value / this.renderer.getPixelRatio();
    const sizeScale = this.pointMat.uniforms.sizeScale.value;
    let best: number | null = null, bestD = Infinity;
    for (let k = 0; k < this.progress.loaded; k++) {
      if (this.focused !== null && cl[k] !== this.focused) continue;
      const x = pos[k * 3] * s, y = pos[k * 3 + 1] * s, z = pos[k * 3 + 2] * s;
      const w = m[3] * x + m[7] * y + m[11] * z + m[15];
      if (w <= 0) continue;
      const sx = ((m[0] * x + m[4] * y + m[8] * z + m[12]) / w * 0.5 + 0.5) * W;
      const sy = (0.5 - (m[1] * x + m[5] * y + m[9] * z + m[13]) / w * 0.5) * H;
      const dx = sx - mx, dy = sy - my;
      const d2 = dx * dx + dy * dy;
      const r = Math.max(6, Math.min(10, ((24 + 56 * rank[k] / 255) * sizeScale * projScale) / w / 2) + 3);
      if (d2 < r * r && d2 < bestD) { bestD = d2; best = k; }
    }
    return best;
  }

  stats() { return { ...this.progress }; }

  dispose() {
    this.abort.abort();
    this.overlayAbort?.abort();
    cancelAnimationFrame(this.frame);
    this.resizeObs.disconnect();
    this.visObs.disconnect();
    document.removeEventListener('visibilitychange', this.onVisibility);
    this.renderer.domElement.removeEventListener('pointerdown', this.onDown);
    this.renderer.domElement.removeEventListener('pointerup', this.onUp);
    this.controls.dispose();
    this.scene.traverse((o) => {
      const m = o as THREE.Mesh;
      m.geometry?.dispose();
      const mat = m.material as THREE.Material | THREE.Material[] | undefined;
      (Array.isArray(mat) ? mat : mat ? [mat] : []).forEach((x) => x.dispose());
    });
    this.renderer.dispose();
    this.renderer.forceContextLoss();
    this.host.replaceChildren();
  }
}

/** Line opacity that keeps dense link sets readable instead of a solid blob. */
function linkAlpha(edges: number) {
  return Math.max(0.035, Math.min(0.35, 30 / Math.sqrt(Math.max(1, edges))));
}

function parseEdges(buf: ArrayBuffer) {
  const dv = new DataView(buf);
  if (dv.getUint32(0, true) !== 0x31454e55) throw new Error('bad links chunk'); // "UNE1"
  const count = dv.getUint32(8, true);
  return { next: dv.getUint32(4, true), done: dv.getUint32(12, true) === 1, pairs: new Uint32Array(buf, 16, count * 2) };
}
