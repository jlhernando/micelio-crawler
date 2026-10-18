<script lang="ts">
  import { api } from '../../api';
  let { stats, crawlId, template = 'all', onIssues }: { stats: Record<string, unknown>; crawlId: string; template?: string; onIssues: () => void } = $props();
  type Finding = { key: string; label: string; count: number; unit: string; priority: string; evidence: string; action: string; filter?: string; urls?: string[] };
  let findings = $derived.by(() => {
    const rows: Finding[] = [];
    const codes = (stats.statusCodes || {}) as Record<string, number>;
    const serverErrors = Object.entries(codes).reduce((n, [code, count]) => n + (Number(code) >= 500 && Number(code) < 600 ? count : 0), 0);
    const broken = (stats.brokenLinks || []) as {url: string}[];
    const orphans = (stats.orphanPages || []) as string[];
    rows.push(
      { key:'server', label:'Server errors', count:serverErrors, unit:'URLs', priority:'High', evidence:'Observed HTTP responses', action:'Check server logs, restore these URLs, then recrawl to verify the response.', filter:'5xx' },
      { key:'broken', label:'Broken internal links', count:broken.length, unit:'destinations', priority:'High', evidence:'Observed broken destinations', action:'Find the linking pages. Update their links or restore the destination.', urls:broken.map(p=>p.url) },
      { key:'title', label:'Missing titles', count:Number(stats.pagesWithoutTitle || 0), unit:'pages', priority:'Medium', evidence:'Observed missing tag', action:'Add a descriptive title to each page that should appear in search.', filter:'missing-title' },
      { key:'duplicates', label:'Duplicate titles', count:Number(stats.duplicateTitleCount || 0), unit:'pages', priority:'Medium', evidence:'Review recommended', action:'Check whether these pages serve different purposes before rewriting titles or consolidating URLs.', filter:'duplicate-titles' },
      { key:'description', label:'Missing descriptions', count:Number(stats.pagesWithoutDescription || 0), unit:'pages', priority:'Low', evidence:'Review recommended', action:'Prioritize useful descriptions for important landing pages. A missing description does not block indexing.', filter:'missing-description' },
      { key:'orphans', label:'Possible orphan pages', count:orphans.length, unit:'candidates', priority:'Verify', evidence:'Incomplete link evidence', action:'No incoming links were recorded in this crawl. Check a broader crawl and sitemap or another URL source before calling these pages orphaned.', urls:orphans },
    );
    const rank: Record<string,number> = {High:0,Medium:1,Low:2,Verify:3};
    return rows.filter(r=>r.count>0).sort((a,b)=>rank[a.priority]-rank[b.priority] || b.count-a.count);
  });
  let max = $derived(Math.max(...findings.map(r=>r.count),1));
  let open = $state<string | null>(null);
  let examples = $state<Record<string,string[]>>({});
  let pending = $state<Record<string,boolean>>({});
  let failures = $state<Record<string,boolean>>({});
  async function load(row: Finding) {
    if (row.urls || examples[row.key] || pending[row.key] || !row.filter) return;
    pending[row.key]=true; failures[row.key]=false;
    try { const data=await api.getCrawlPages(crawlId,{issue:row.filter,limit:5,template}); examples[row.key]=data.pages.map(p=>String(p.url)); }
    catch { failures[row.key]=true; }
    finally { pending[row.key]=false; }
  }
</script>
<section class="chart-panel" aria-label="Problems by impact">
  <div class="chart-panel-heading"><div><h2>Problems by impact</h2><p>Suggested priority within this crawl, not a prediction of traffic loss</p></div><button class="text-xs text-fg-2 cursor-pointer" onclick={onIssues}>All issues ↗</button></div>
  {#if findings.length}
    <div class="priority-list">
      {#each findings as row}
        <article>
          <button class="finding" aria-expanded={open===row.key} onclick={()=>{open=open===row.key?null:row.key;if(open)void load(row);}}>
            <span class="priority" class:high={row.priority==='High'}>{row.priority}</span>
            <span class="finding-name"><strong>{row.label}</strong><small>{row.evidence}</small></span>
            <span class="bar" aria-hidden="true"><i style:width={`${row.count/max*100}%`}></i></span>
            <span class="amount"><strong>{row.count.toLocaleString()}</strong> {row.unit} <span aria-hidden="true">{open===row.key?'−':'+'}</span></span>
          </button>
          {#if open===row.key}
            <div class="finding-details">
              <p>{row.action}</p>
              <h3>Example URLs · up to 5</h3>
              {#if pending[row.key]}<p role="status">Loading examples…</p>
              {:else if failures[row.key]}<p role="alert">Could not load examples. <button class="underline cursor-pointer" onclick={()=>load(row)}>Retry</button></p>
              {:else}
                {@const urls=(row.urls || examples[row.key] || []).slice(0,5)}
                {#each urls as url}<div class="example-url">{url}</div>{:else}<p>No example URLs are available.</p>{/each}
              {/if}
            </div>
          {/if}
        </article>
      {/each}
    </div>
    <p class="chart-note mt-4">Counts overlap and use different units. They cannot be added into a total of affected pages. Expand a row for examples and the next action.</p>
  {:else}<div class="chart-empty">No findings in these priority categories. Other checks may be unavailable; review All issues.</div>{/if}
</section>
<style>
  .chart-panel-heading {flex-wrap:wrap;}
  article + article {border-top:1px solid var(--color-border);}
  .finding {display:grid;grid-template-columns:64px minmax(0,1fr) minmax(80px,180px) 125px;gap:16px;align-items:center;text-align:left;width:100%;padding:16px 0;cursor:pointer;}
  .finding:hover .finding-name strong {text-decoration:underline;}
  .priority {font-size:11px;color:#e5b66e;}.priority.high{color:#f58f98;}
  .finding-name strong {font-size:13px;font-weight:500;} .finding-name small {display:block;color:var(--color-fg-2);font-size:11px;margin-top:4px;}
  .bar {height:8px;border-radius:4px;background:var(--color-surface-3);overflow:hidden;}.bar i{display:block;height:100%;background:#b09aff;border-radius:4px;}
  .amount {text-align:right;font-size:11px;color:var(--color-fg-2);}.amount strong {font-size:17px;color:var(--color-fg);font-weight:500;}
  .finding-details {padding:0 0 18px 80px;font-size:12px;line-height:1.6;color:var(--color-fg-2);}.finding-details h3 {margin:14px 0 6px;color:var(--color-fg);font-size:11px;}
  .example-url {overflow-wrap:anywhere;padding:5px 0;}
  @media(max-width:700px){.finding{grid-template-columns:48px minmax(0,1fr) 90px;gap:8px;}.bar{display:none;}.finding-details{padding-left:0;}}
</style>
