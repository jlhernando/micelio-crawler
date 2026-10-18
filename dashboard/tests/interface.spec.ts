import { test, expect } from '@playwright/test';
import { mockApi } from './fixtures';
import { mkdirSync } from 'node:fs';

const screens = [
  ['setup','New crawl'], ['monitor/live','Crawl monitor'], ['results/crawl-6','Site overview'],
  ['history','Crawl history'], ['schedules','Schedules'], ['settings','Settings'],
  ['logs','Log analysis'], ['logs/log-1','Log insights'], ['diff/crawl-5/crawl-6','Crawl comparison'],
];
for (const [route, heading] of screens) {
  test(`${route}: desktop and mobile`, async ({page}) => {
    const errors: string[]=[]; page.on('pageerror',e=>errors.push(e.message));
    await mockApi(page);
    await page.goto('/#/'+route);
    await expect(page.getByRole('heading',{name:heading,exact:true})).toBeVisible();
    await expect(page.locator('.workspace-page')).not.toContainText('Loading results...');
    await page.waitForTimeout(1200);
    if(process.env.SCREENSHOT_DIR){mkdirSync(process.env.SCREENSHOT_DIR,{recursive:true});await page.screenshot({path:`${process.env.SCREENSHOT_DIR}/${route.replaceAll('/','-')}.png`});}
    await page.setViewportSize({width:390,height:844});
    await expect(page.getByRole('button',{name:'Open navigation',exact:true})).toBeVisible();
    const overflow = await page.locator('.workspace-content').evaluate(el=>el.scrollWidth>el.clientWidth+1);

    expect(overflow,`Horizontal page overflow on ${route}`).toBe(false);
    await page.getByRole('button',{name:'Open navigation',exact:true}).click();
    await expect(page.getByRole('navigation',{name:'Main navigation'})).toBeVisible();
    await page.keyboard.press('Escape');
    await expect(page.getByRole('navigation',{name:'Main navigation'})).not.toBeVisible();
    expect(errors).toEqual([]);
  });
}

test('Results filters and lazy tabs preserve the chart workflow', async ({page})=>{
  await mockApi(page); await page.goto('/#/results/crawl-6');
  await expect(page.getByRole('button',{name:'Overview',exact:true})).toHaveAttribute('aria-pressed','true');
  await expect(page.getByRole('heading',{name:'Indexability',exact:true})).toBeVisible();
  const request=page.waitForRequest(r=>r.url().includes('/pages?')&&r.url().includes('indexable=no'));
  await page.getByRole('button',{name:'Non-indexable 108 ↗',exact:true}).click();await request;
  await expect(page.getByRole('button',{name:'All Pages',exact:true})).toHaveAttribute('aria-pressed','true');
  await expect(page.getByRole('heading',{name:'Indexability',exact:true})).not.toBeVisible();
  await page.getByRole('button',{name:'Overview',exact:true}).click();
  await page.getByRole('button',{name:'All issues ↗'}).click();
  await expect(page.getByRole('button',{name:'Issues',exact:true})).toHaveAttribute('aria-pressed','true');
});

test('Setup presets and submit keep the crawl configuration',async({page})=>{
  const writes=await mockApi(page);await page.goto('/#/setup');
  await page.getByRole('button',{name:/Technical Audit/}).click();
  await expect(page.getByRole('button',{name:/Technical Audit/})).toHaveAttribute('aria-pressed','true');
  await page.locator('#url').fill('https://new.example');
  await page.getByRole('button',{name:'Start Crawl',exact:true}).click();
  await expect(page).toHaveURL(/monitor\/live/);
  expect(writes.find(w=>w.path==='/api/crawl/start')?.body.seedUrl).toBe('https://new.example');
});

test('Schedules can create and settings can save',async({page})=>{
  const writes=await mockApi(page);await page.goto('/#/schedules');
  await page.getByRole('button',{name:'New Schedule',exact:true}).click();
  await page.locator('#sched-url').fill('https://new.example');
  await page.getByRole('button',{name:'Create Schedule',exact:true}).click();
  await expect.poll(()=>writes.some(w=>w.path==='/api/schedules')).toBe(true);
  await page.goto('/#/settings');
  await page.getByRole('button',{name:'Reset Defaults',exact:true}).first().click();
  await page.getByLabel('Depth',{exact:true}).fill('6');
  await page.getByRole('button',{name:'Save Settings',exact:true}).first().click();
  await expect.poll(()=>writes.find(w=>w.path==='/api/settings')?.body.defaultDepth).toBe(6);
});

test('Empty and failed history states stay usable',async({page})=>{
  await mockApi(page,'empty');await page.goto('/#/history');
  await expect(page.getByRole('heading',{name:'No Crawls Yet'})).toBeVisible();
  await page.unroute('**/api/**');await mockApi(page,'error');await page.reload();
  await expect(page.getByText('Unable to load crawls')).toBeVisible();
});

test('Missing result metrics show no-data messages instead of charts',async({page})=>{
  await mockApi(page,'empty');await page.goto('/#/results/crawl-6');
  await expect(page.getByText('Indexability data is not available for this crawl.')).toBeVisible();
  await expect(page.getByText('Response-time data is not available for this crawl.')).toBeVisible();
});

test('Coverage explains limits and does not invent whole-site completeness', async ({page}) => {
  await mockApi(page);
  await page.route('**/api/crawl/crawl-6/status', route => route.fulfill({json:{status:'completed',config:{maxPages:1248,maxDepth:3,respectRobots:true}}}));
  await page.goto('/#/results/crawl-6');
  const coverage=page.getByRole('region',{name:'Crawl coverage'});
  await expect(coverage.getByText('Page limit reached',{exact:true})).toBeVisible();
  await expect(coverage).toContainText('Whole-site coverage is unknown.');
  await expect(coverage).toContainText('Not recorded');
  await expect(coverage).toContainText('Not measured');
  await page.setViewportSize({width:390,height:844});
  expect(await coverage.evaluate(el=>el.scrollWidth>el.clientWidth+1)).toBe(false);
});

test('Priority findings show scoped examples, action and retry on failure', async ({page}) => {
  await mockApi(page);
  let attempts=0;
  await page.route('**/api/crawl/crawl-6/pages?*',route=>{
    const url=new URL(route.request().url());
    if(url.searchParams.get('issue')!=='5xx')return route.fallback();
    expect(url.searchParams.get('limit')).toBe('5');
    attempts++;
    return attempts===1?route.fulfill({status:500,json:{error:'Example load failed'}}):route.fulfill({json:{pages:[{url:'https://studio.example/server-error'}],total:4}});
  });
  await page.goto('/#/results/crawl-6');
  const panel=page.getByRole('region',{name:'Problems by impact'});
  await expect(panel.locator('article').first()).toContainText('Broken internal links');
  await panel.getByRole('button',{name:/Server errors/}).click();
  await expect(panel.getByRole('alert')).toContainText('Could not load examples');
  await panel.getByRole('button',{name:'Retry',exact:true}).click();
  await expect(panel).toContainText('https://studio.example/server-error');
  await expect(panel).toContainText('Check server logs');
  await panel.getByRole('button',{name:/Broken internal links/}).click();
  await expect(panel).toContainText('https://studio.example/archive/0');
  expect(attempts).toBe(2);
});

test('Missing scope preserves results and orphan candidates stay qualified', async ({page}) => {
  await mockApi(page);
  await page.route('**/api/crawl/crawl-6/status',route=>route.fulfill({status:500,json:{error:'Scope unavailable'}}));
  await page.route('**/api/crawl/crawl-6/stats',route=>route.fulfill({json:{stats:{orphanPages:['https://studio.example/candidate'],alertSummary:{alerts:[{id:'orphan-pages',name:'Orphan Pages Detected',severity:'warning',message:'Orphan detected'}]}},pageCount:1,pagesReady:true}}));
  await page.goto('/#/results/crawl-6');
  await expect(page.getByText('Could not load crawl settings. Results are still available.')).toBeVisible();
  const panel=page.getByRole('region',{name:'Problems by impact'});
  await panel.getByRole('button',{name:/Possible orphan pages/}).click();
  await expect(panel).toContainText('Incomplete link evidence');
  await expect(panel).toContainText('before calling these pages orphaned');
  await expect(page.getByText('Orphan Pages Detected',{exact:true})).toHaveCount(0);
  await page.getByRole('button',{name:'All issues ↗'}).click();
  await page.getByRole('button',{name:/Possible Orphan Pages/}).click();
  await expect(page.getByText(/Limited coverage can hide links/)).toBeVisible();
});

test('Template filtering keeps full crawl coverage and scopes issue examples', async ({page}) => {
  await mockApi(page);
  await page.route('**/stats/filtered?*',route=>route.fulfill({json:{stats:{pagesWithoutTitle:3},pageCount:12}}));
  await page.goto('/#/results/crawl-6');
  await page.getByRole('button',{name:'Product 612',exact:true}).click();
  const panel=page.getByRole('region',{name:'Problems by impact'});
  await expect(panel.getByRole('button',{name:/Missing titles/})).toContainText('3 pages');
  const request=page.waitForRequest(r=>r.url().includes('/pages?')&&r.url().includes('issue=missing-title'));
  await panel.getByRole('button',{name:/Missing titles/}).click();
  expect(new URL((await request).url()).searchParams.get('template')).toBe('Product');
  await expect(page.getByRole('region',{name:'Crawl coverage'})).toContainText('1,248');
  await expect(page.getByRole('region',{name:'Crawl coverage'})).toContainText('Finishing a crawl does not prove');
});

test('Saved exclusion reasons survive reload and show examples without inventing unique totals', async ({page}) => {
  await mockApi(page);
  await page.route('**/api/crawl/crawl-6/status',route=>route.fulfill({json:{status:'completed',config:{maxPages:1248},coverage:{final:true,acceptedUrls:1248,pendingUrls:0,reasons:[{reason:'depth',count:120,examples:['https://studio.example/deep']},{reason:'exclude_filter',rule:'/private',count:30,examples:['https://studio.example/private']},{reason:'robots',count:7,examples:['https://studio.example/blocked']}]}}}));
  await page.goto('/#/results/crawl-6');
  const panel=page.getByRole('region',{name:'Crawl coverage'});
  await expect(panel).toContainText('157 rejected attempts');
  await expect(panel).toContainText('Attempts, not unique URLs');
  await panel.locator('summary').filter({hasText:'Exclude rule matched'}).click();
  await expect(panel.getByText('https://studio.example/private',{exact:true})).toBeVisible();
  await page.reload();
  await expect(panel).toContainText('157 rejected attempts');
  await panel.locator('summary').filter({hasText:'Blocked by robots.txt'}).click();
  await expect(panel.getByText('https://studio.example/blocked',{exact:true})).toBeVisible();
  await page.setViewportSize({width:390,height:844});
  expect(await panel.evaluate(el=>el.scrollWidth>el.clientWidth+1)).toBe(false);
});

test('Failed zero-page crawls still expose saved exclusion evidence', async ({page}) => {
  await mockApi(page);
  await page.route('**/api/crawl/crawl-6/stats',route=>route.fulfill({status:404,json:{error:'results not available'}}));
  await page.route('**/api/crawl/crawl-6/status',route=>route.fulfill({json:{status:'failed',pageCount:0,config:{mode:'sitemap'},coverage:{final:true,acceptedUrls:0,pendingUrls:0,reasons:[{reason:'domain',count:12,examples:['https://outside.example/page']}]}}}));
  await page.goto('/#/results/crawl-6');
  const panel=page.getByRole('region',{name:'Crawl coverage'});
  await expect(panel).toContainText('12 rejected attempts');
  await panel.locator('summary').filter({hasText:'Outside allowed domains'}).click();
  await expect(panel.getByText('https://outside.example/page',{exact:true})).toBeVisible();
});
