import type { Page } from '@playwright/test';

// Synthetic data lives in tests only. Production views always use the REST API.
const delta = (old = 0, value = 0) => ({ old, new: value, delta: value - old });
export const crawls = [920, 984, 1036, 1080, 1148, 1200, 1248].map((pageCount, i) => ({
  id: `crawl-${i}`, seedUrl: 'https://studio.example', mode: 'spider', status: 'completed',
  startedAt: new Date(Date.UTC(2026, 8, 6 + i * 7, 10, 30)).toISOString(),
  completedAt: new Date(Date.UTC(2026, 8, 6 + i * 7, 10, 33)).toISOString(),
  pageCount, errorCount: 30 - i, durationMs: 134000, config: { url: 'https://studio.example', depth: 3, limit: 2000 },
}));
export const stats = {
  crawlDurationMs: 134000, statusCodes: { 200: 1140, 301: 84, 404: 20, 500: 4 },
  indexabilityStats: { indexable: 1140, nonIndexable: 108, reasons: { Redirect: 84, 'HTTP error': 24 } },
  responseTimeBuckets: { fast: 700, medium: 310, slow: 150, slowest: 88 },
  responseTimePercentiles: { p50: 284, p90: 1400, p99: 2300 },
  templateTypeDistribution: { Product: 612, Editorial: 324, Category: 216, Other: 96 },
  pagesWithoutTitle: 12, pagesWithoutDescription: 32, pagesWithoutH1: 9,
  duplicateTitleCount: 36, duplicateDescriptionCount: 19,
  brokenLinks: Array.from({length: 24}, (_, i) => ({url:`https://studio.example/archive/${i}`, statusCode: 404, foundOn: ['https://studio.example']})),
  seoFunnelStats: { crawled: 1248, renderable: 1224, indexable: 1140, visible: 0, active: 0, nonIndexable: 108 },
  depthDistribution: { 0: 1, 1: 50, 2: 400, 3: 797 },
  textAmountThresholds: [180, 640],
  renderCompareStats: { pagesCompared: 40, pagesWithDiffs: 6, fieldDiffCounts: { title: 2, word_count: 5, internal_links_count: 3 },
    criticalDiffs: [{ url: 'https://studio.example/products/item-3', field: 'title', original: 'Loading…', rendered: 'Studio product 3' }] },
  linkIntelligenceStats: { avgClickDepth: 2.1, maxClickDepth: 4, unreachablePagesCount: 0, linkSuggestionsCount: 0,
    linkPositionDistribution: { content: 600, navigation: 300, footer: 100 },
    pagesWithNoContentLinks: ['https://studio.example/legal'], pagesWithNoContentLinksCount: 12,
    mostConnected: [{ url: 'https://studio.example/', score: 410 }], mostIsolated: [{ url: 'https://studio.example/archive/9', score: 2 }] },
  autoSegmentStats: [
    { dimension: 'template', totalValues: 3, values: [
      { value: 'pdp', pages: 612, indexablePct: 96.1, errorPct: 0.5, avgWords: 340, avgInlinks: 6.2, avgResponseMs: 280, sampleUrl: 'https://studio.example/products/item-1' },
      { value: 'blog_post', pages: 324, indexablePct: 99, errorPct: 0, avgWords: 1210, avgInlinks: 3.1, avgResponseMs: 310 },
      { value: 'group 1 · /help/', pages: 96, indexablePct: 40, errorPct: 12, avgWords: 90, avgInlinks: 1, avgResponseMs: 220 },
    ] },
    { dimension: 'textAmount', totalValues: 2, unassigned: 108, values: [
      { value: 'low', pages: 400, indexablePct: 90, errorPct: 0, avgWords: 120, avgInlinks: 2, avgResponseMs: 250 },
      { value: 'high', pages: 380, indexablePct: 98, errorPct: 0, avgWords: 1400, avgInlinks: 5, avgResponseMs: 330 },
    ] },
  ],
};
const pages = Array.from({length: 8}, (_, i) => ({
  url: `https://studio.example/products/item-${i}`, statusCode: i < 2 ? 404 : 200,
  title: `Studio product ${i}`, metaDescription: 'Product description', h1: ['Product'],
  indexable: i >= 2, isIndexable: i >= 2, responseTimeMs: 284 + i * 10, wordCount: 340,
  depth: 2, internalLinks: [], externalLinks: [], inlinks: 8,
}));
const urls = { oldCount: 1200, newCount: 1248, addedUrls: ['https://studio.example/new'], removedUrls: [], changedUrls: [], unchangedCount: 1199, fieldSummary: {} };
const comparison: Record<string, unknown> = {
  healthScore: { oldScore: 78, newScore: 85, delta: 7, details: [] },
  coverage: { totalPages: delta(1200,1248), newUrls: 48, disappearedUrls: 0, persistedUrls: 1200, robotsBlocked: delta() },
  httpStatus: { oldStatusCodes: {200: 1100, 404: 28}, newStatusCodes: stats.statusCodes, migrations: [], brokenLinks: delta(28,24), soft404s: delta() },
  indexability: { indexable: delta(1094,1140), nonIndexable: delta(106,108) },
  performance: { responseP50: delta(300,284), responseP90: delta(1500,1400), responseP99: delta(2400,2300), slowPages: delta(100,88) },
  findings: [{section:'performance',message:'Median response time improved by 16 ms.',severity:'info',impact:16}], urls,
};
for (const [name, fields] of Object.entries({ onPageSeo: ['withoutTitle','withoutDescription','withoutH1','duplicateTitles','duplicateDescs','titleTooLong','titleTooShort','descTooLong','multipleH1','withoutOg'], content: ['thinContentPages','duplicateGroups','nearDuplicateGroups','readability','textToCode'], links: ['orphanPages','nearOrphans','dilutionWarnings'], internalLink: ['totalInternal','totalExternal','deadEnds'], redirects: ['redirectChains','longChains'], images: ['totalImages','missingAlt'], security: ['nonHttps','mixedContent'], hreflang: ['issues'] })) comparison[name] = Object.fromEntries(fields.map(f => [f, delta()]));
const logOverview = {
  totalHits: 120000, humanHits: 40000, dateRange: ['2026-10-12T00:00:00Z','2026-10-18T23:59:59Z'],
  statusGroups: {'2xx': 110000, '3xx': 6000, '4xx': 4000}, statusCodes: {200:110000,301:6000,404:4000},
  dailyHits: {'2026-10-12':12000,'2026-10-13':14000,'2026-10-14':18000,'2026-10-15':20000,'2026-10-16':19000,'2026-10-17':17000,'2026-10-18':20000},
  botHits: {Googlebot:{hits:60000,uniqueUrls:1100,category:'search',mobile:true,bytes:5000000,firstSeen:'2026-10-12',lastSeen:'2026-10-18'},Bingbot:{hits:20000,uniqueUrls:750,category:'search',mobile:false,bytes:2000000,firstSeen:'2026-10-12',lastSeen:'2026-10-18'}},
  crawlBudget:{uniqueUrlsCrawled:1140}, topUrls: [{path:'/products',hits:2000,botHits:1500,humanHits:500,topBot:'Googlebot',status:200}],
};
const logJob = {id:'log-1',filename:'access-october.log',format:'nginx_combined',status:'completed',createdAt:'2026-10-18T10:00:00Z',fileSize:24000000,totalLines:120000,processedLines:120000,durationMs:3200};
export async function mockApi(page: Page, mode: 'normal' | 'empty' | 'error' = 'normal') {
  const writes: { path: string; body: any }[] = [];
  await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url()), path = url.pathname;
    if (request.method() !== 'GET') writes.push({path,body:request.postDataJSON()});
    let data: any = {};
    if (mode === 'error' && path === '/api/crawls') return route.fulfill({status:500,json:{error:'Unable to load crawls'}});
    if(path === '/api/crawls') data = {crawls: mode === 'empty' ? [] : crawls,total:mode==='empty'?0:crawls.length};
    else if(path === '/api/settings') data = request.method() === 'PUT' ? request.postDataJSON() : {};
    else if(path === '/api/update/status') data = {current:'dev',isDevBuild:true,updateAvailable:false,platform:'test',repo:'test',canRollback:false};
    else if(path === '/api/presets') data = [];
    else if(path === '/api/schedules') data = request.method()==='POST' ? {id:'schedule-2'} : mode==='empty' ? [] : [{id:'schedule-1',url:'https://studio.example',cron:'@daily',nextRun:'2026-10-19T00:00:00Z',createdAt:'2026-10-01T00:00:00Z',lastRun:'2026-10-18T00:00:00Z',lastStatus:'success',lastPages:1248,lastDurationMs:134000,totalRuns:18,outputDir:'/tmp/test'}];
    else if(path === '/api/crawl/start') data = {id:'live',status:'running'};
    else if(path === '/api/crawl/diff') data = {comparison};
    else if(path.match(/\/crawl\/[^/]+\/status$/)) data = path.includes('/live/') ? {...crawls[6],id:'live',status:'running',statusCodes:{'2xx':700,'3xx':50,'4xx':12}} : crawls.find(c=>path.includes(c.id)) || crawls[6];
    else if(path.match(/\/stats(\/filtered)?$/)) data = {stats:mode==='empty'?{}:stats,pageCount:mode==='empty'?0:1248,pagesReady:true};
    else if(path.endsWith('/pages')) {
      let list=pages;
      if(url.searchParams.get('indexable')==='no') list=pages.filter(p=>!p.isIndexable);
      data = {pages:list,total:list.length,totalAll:1248,offset:0,limit:100,templateTypes:['Product','Editorial']};
    }
    else if(path==='/api/logs') data = {jobs:mode==='empty'?[]:[logJob],total:mode==='empty'?0:1};
    else if(path.endsWith('/overview')) data = logOverview;
    else if(path.endsWith('/bots')) data = {botDailyHits:{Googlebot:{'2026-10-12':7000,'2026-10-13':8000,'2026-10-14':9000,'2026-10-15':9500,'2026-10-16':10000,'2026-10-17':8000,'2026-10-18':8500}}};
    else if(path.match(/\/logs\/[^/]+\/status$/)) data=logJob;
    else if(path.endsWith('/graph')) data={nodes:[],edges:[]};
    else if(path.endsWith('/page-inlinks')) data={sources:[],url:''};
    return route.fulfill({json:data});
  });
  await page.routeWebSocket('**/ws', ws => {
    setTimeout(() => {
      ws.send(JSON.stringify({type:'progress',crawlId:'live',data:{completed:762,failed:12,pending:426,total:1200,elapsedMs:90000}}));
      for(let i=0;i<30;i++) ws.send(JSON.stringify({type:'page',crawlId:'live',data:{url:`https://studio.example/products/${i}`,statusCode:200,responseTimeMs:180+(i%7)*50}}));
    }, 150);
  });
  return writes;
}
