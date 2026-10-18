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
