<script lang="ts">
  import { onMount } from 'svelte';
  import { api } from '../lib/api';
  import { navigate } from '../lib/router';
  import { createWsClient, type WsMessage } from '../lib/ws';

  interface LogJobFile {
    filename: string;
    size: number;
    bytesRead?: number;
    lines?: number;
    status?: string;
    error?: string;
  }

  interface LogJob {
    id: string;
    filename: string;
    format: string;
    status: string;
    createdAt: string;
    completedAt?: string;
    fileSize: number;
    totalLines: number;
    processedLines: number;
    durationMs?: number;
    uploadMs?: number;
    parseMs?: number;
    analysisMs?: number;
    errorMsg?: string;
    files?: LogJobFile[];
  }

  interface UploadFileState {
    name: string;
    size: number;
    bytesUploaded: number;
    bytesParsed: number;
    lines: number;
    status: 'queued' | 'uploading' | 'uploaded' | 'parsing' | 'completed' | 'failed';
    error?: string;
  }

  const LOG_FORMATS = [
    { value: '', label: 'Auto-detect' },
    { value: 'apache_combined', label: 'Apache Combined' },
    { value: 'apache_clf', label: 'Apache CLF' },
    { value: 'nginx_combined', label: 'Nginx Combined' },
    { value: 'cloudfront', label: 'AWS CloudFront (TSV / CSV export)' },
    { value: 'cloudflare', label: 'Cloudflare (JSON)' },
    { value: 'alb', label: 'AWS ALB/ELB' },
    { value: 'w3c', label: 'W3C / IIS' },
  ];

  type Phase = 'uploading' | 'parsing' | 'persisting_urls';

  let jobs = $state<LogJob[]>([]);
  let loading = $state(true);
  let uploading = $state(false);
  let phase = $state<Phase>('uploading');
  let uploadProgress = $state(0); // total lines processed across all files during parsing phase
  let persistUrlCount = $state(0); // number of unique URLs being saved to DB
  let uploadJobId = $state<string | null>(null);
  let uploadBytes = $state(0);       // total bytes uploaded across all files
  let uploadTotalBytes = $state(0);  // total expected bytes (sum of file.size)
  let parsedBytes = $state(0);       // total bytes parsed across all files
  let parsedFileSize = $state(0);    // sum of all file sizes (parsing denominator)
  let uploadFiles = $state<UploadFileState[]>([]);
  let filesDone = $state(0);
  let filesTotal = $state(0);
  let currentParseFile = $state<string | null>(null);
  let dragOver = $state(false);
  let error = $state<string | null>(null);
  let selectedFormat = $state('');

  function formatBytes(n: number): string {
    if (!n || n <= 0) return '0 B';
    if (n < 1024) return n + ' B';
    if (n < 1048576) return (n / 1024).toFixed(1) + ' KB';
    if (n < 1073741824) return (n / 1048576).toFixed(1) + ' MB';
    return (n / 1073741824).toFixed(2) + ' GB';
  }

  async function loadJobs() {
    try {
      const data = await api.listLogs();
      jobs = (data.jobs as LogJob[]) || [];
      loading = false;
    } catch (err) {
      error = (err as Error).message;
      loading = false;
    }
  }

  async function handleFiles(files: File[]) {
    if (!files || files.length === 0) return;
    uploading = true;
    phase = 'uploading';
    uploadProgress = 0;
    uploadBytes = 0;
    uploadTotalBytes = files.reduce((sum, f) => sum + f.size, 0);
    parsedBytes = 0;
    parsedFileSize = uploadTotalBytes;
    filesDone = 0;
    filesTotal = files.length;
    currentParseFile = null;
    uploadFiles = files.map<UploadFileState>(f => ({
      name: f.name, size: f.size, bytesUploaded: 0, bytesParsed: 0, lines: 0, status: 'queued',
    }));
    error = null;
    try {
      const result = await api.uploadLogs(files, selectedFormat || undefined);
      uploadJobId = result.id;
      const displayName = files.length === 1 ? files[0].name : `${files[0].name} (+${files.length - 1} more)`;
      // Insert an optimistic row so the Past Analyses table updates immediately.
      const optimisticFiles: LogJobFile[] = files.map(f => ({ filename: f.name, size: f.size, status: 'parsing' }));
      jobs = [
        { id: result.id, filename: displayName, format: selectedFormat || '', status: 'processing',
          createdAt: new Date().toISOString(), fileSize: uploadTotalBytes, totalLines: 0, processedLines: 0,
          files: optimisticFiles },
        ...jobs,
      ];
      // Upload is complete client-side (fetch resolved) — switch to parsing phase.
      phase = 'parsing';
      uploadBytes = uploadTotalBytes;
      for (const f of uploadFiles) { f.status = 'uploaded'; f.bytesUploaded = f.size; }
      startStatusPoll(result.id);
    } catch (err) {
      error = (err as Error).message;
      uploading = false;
    }
  }

  let pollTimer: ReturnType<typeof setInterval> | null = null;
  function stopStatusPoll() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
  }
  async function checkStatus(id: string): Promise<boolean> {
    try {
      const status = await api.getLogStatus(id) as { status?: string; errorMsg?: string; processedLines?: number; totalLines?: number };
      // Refresh the row in the jobs table with whatever the server reports.
      const idx = jobs.findIndex(j => j.id === id);
      if (idx >= 0) {
        if (typeof status.processedLines === 'number') jobs[idx].processedLines = status.processedLines;
        if (typeof status.totalLines === 'number') jobs[idx].totalLines = status.totalLines;
        if (typeof status.status === 'string') jobs[idx].status = status.status;
        if (typeof status.errorMsg === 'string') jobs[idx].errorMsg = status.errorMsg;
      }
      if (status.status === 'failed') {
        stopStatusPoll();
        uploading = false;
        error = status.errorMsg || 'Log analysis failed';
        uploadJobId = null;
        return true;
      }
      if (status.status === 'completed') {
        stopStatusPoll();
        uploading = false;
        navigate(`/logs/${id}`);
        uploadJobId = null;
        return true;
      }
    } catch {
      /* transient network error — keep polling */
    }
    return false;
  }
  function startStatusPoll(id: string) {
    stopStatusPoll();
    // Immediate check in case the parse already finished before we set uploadJobId.
    void checkStatus(id);
    pollTimer = setInterval(() => {
      if (!uploading || uploadJobId !== id) { stopStatusPoll(); return; }
      void checkStatus(id);
    }, 2000);
  }

  function onDrop(e: DragEvent) {
    e.preventDefault();
    dragOver = false;
    const files = Array.from(e.dataTransfer?.files ?? []);
    if (files.length > 0) handleFiles(files);
  }

  function onFileSelect(e: Event) {
    const input = e.target as HTMLInputElement;
    const files = Array.from(input.files ?? []);
    if (files.length > 0) handleFiles(files);
    input.value = '';
  }

  function handleMessage(msg: WsMessage) {
    // Upload-phase progress: while the multipart body is being read.
    if (msg.type === 'log_upload_progress' && uploading) {
      const data = msg.data as {
        jobId?: string; fileIndex?: number; filename?: string;
        bytesReceived?: number; totalBytes?: number; totalExpected?: number;
        complete?: boolean; fileCount?: number;
      };
      if (uploadJobId && data.jobId && data.jobId !== uploadJobId) return;
      if (typeof data.totalBytes === 'number') uploadBytes = data.totalBytes;
      if (data.totalExpected && data.totalExpected > 0) uploadTotalBytes = data.totalExpected;
      if (typeof data.fileIndex === 'number' && data.fileIndex < uploadFiles.length) {
        const f = uploadFiles[data.fileIndex];
        if (typeof data.bytesReceived === 'number') f.bytesUploaded = data.bytesReceived;
        f.status = 'uploading';
      }
      if (data.complete) {
        phase = 'parsing';
        for (const f of uploadFiles) {
          if (f.status === 'queued' || f.status === 'uploading') {
            f.status = 'uploaded';
            f.bytesUploaded = f.size;
          }
        }
      }
      return;
    }
    if (msg.type === 'log_progress' && msg.data.jobId === uploadJobId) {
      const data = msg.data as {
        processed?: number; bytesRead?: number; fileSize?: number;
        filesDone?: number; filesTotal?: number; phase?: string; urlCount?: number;
        currentFile?: { index?: number; filename?: string; bytesRead?: number; fileSize?: number; lines?: number };
      };
      if (data.phase === 'persisting_urls') {
        phase = 'persisting_urls';
        persistUrlCount = data.urlCount ?? 0;
        return;
      }
      phase = 'parsing';
      uploadProgress = data.processed ?? 0;
      if (typeof data.bytesRead === 'number') parsedBytes = data.bytesRead;
      if (typeof data.fileSize === 'number' && data.fileSize > 0) parsedFileSize = data.fileSize;
      if (typeof data.filesDone === 'number') filesDone = data.filesDone;
      if (typeof data.filesTotal === 'number') filesTotal = data.filesTotal;
      if (data.currentFile) {
        const idx = data.currentFile.index;
        if (typeof idx === 'number' && idx >= 0 && idx < uploadFiles.length) {
          const f = uploadFiles[idx];
          f.status = 'parsing';
          if (typeof data.currentFile.bytesRead === 'number') f.bytesParsed = data.currentFile.bytesRead;
          if (typeof data.currentFile.lines === 'number') f.lines = data.currentFile.lines;
          currentParseFile = f.name;
        }
      }
      const idx = jobs.findIndex(j => j.id === uploadJobId);
      if (idx >= 0) jobs[idx].processedLines = uploadProgress;
    } else if (msg.type === 'log_complete' && msg.data.jobId === uploadJobId) {
      stopStatusPoll();
      uploading = false;
      if (msg.data.error) {
        error = msg.data.error as string;
      } else {
        navigate(`/logs/${uploadJobId}`);
      }
      uploadJobId = null;
    } else if (msg.type === 'log_delete_done') {
      const deletedId = msg.data.jobId as string;
      if (msg.data.error) {
        // Deletion failed — revert status so the user can retry.
        const idx = jobs.findIndex(j => j.id === deletedId);
        if (idx >= 0) jobs[idx].status = 'failed';
      } else {
        jobs = jobs.filter(j => j.id !== deletedId);
      }
    }
  }

  async function deleteJob(id: string) {
    const idx = jobs.findIndex(j => j.id === id);
    if (idx >= 0) jobs[idx].status = 'deleting';
    await api.deleteLog(id);
    // Job removal happens via WS log_delete_done event.
  }

  const formatSize = formatBytes;

  function formatDuration(ms: number | undefined | null): string {
    if (!ms) return '-';
    if (ms < 1000) return ms + 'ms';
    const totalSec = Math.floor(ms / 1000);
    if (totalSec < 60) return totalSec + 's';
    const mins = Math.floor(totalSec / 60);
    const secs = totalSec % 60;
    if (mins < 60) return secs > 0 ? `${mins}m ${secs}s` : `${mins}m`;
    const hours = Math.floor(mins / 60);
    const remMins = mins % 60;
    return remMins > 0 ? `${hours}h ${remMins}m` : `${hours}h`;
  }

  function timeAgo(dateStr: string): string {
    const diff = Date.now() - new Date(dateStr).getTime();
    const mins = Math.floor(diff / 60000);
    if (mins < 1) return 'just now';
    if (mins < 60) return mins + 'm ago';
    const hrs = Math.floor(mins / 60);
    if (hrs < 24) return hrs + 'h ago';
    return Math.floor(hrs / 24) + 'd ago';
  }

  let wsClient: { close: () => void } | null = null;
  onMount(() => {
    loadJobs();
    wsClient = createWsClient(handleMessage);
    return () => {
      stopStatusPoll();
      wsClient?.close();
    };
  });
</script>

<div class="space-y-6">
  <!-- Upload Area -->
  <div
    class="border-2 border-dashed rounded-xl p-8 text-center transition-colors
      {dragOver ? 'border-accent bg-accent/5' : 'border-border hover:border-fg-2'}"
    ondragover={(e) => { e.preventDefault(); dragOver = true; }}
    ondragleave={() => dragOver = false}
    ondrop={onDrop}
    role="region"
  >
    {#if uploading}
      {@const uploadPct = uploadTotalBytes > 0 ? Math.min((uploadBytes / uploadTotalBytes) * 100, 100) : 0}
      {@const parsePct = parsedFileSize > 0 ? Math.min((parsedBytes / parsedFileSize) * 100, 100) : 0}
      {@const pct = phase === 'uploading' ? uploadPct : (parsedBytes > 0 ? parsePct : 0)}
      <div class="space-y-4 text-left max-w-2xl mx-auto">
        <div class="text-center">
          <div class="text-fg text-sm font-medium">
            {#if phase === 'uploading'}
              Uploading {uploadFiles.length > 1 ? `${uploadFiles.length} files` : 'log file'}…
            {:else if phase === 'persisting_urls'}
              Saving {persistUrlCount > 0 ? `${persistUrlCount.toLocaleString()} unique URLs` : 'URL stats'} to database…
            {:else}
              Analyzing {uploadFiles.length > 1 ? `${uploadFiles.length} files` : 'log file'} in parallel…
              {#if filesTotal > 0} <span class="text-fg-2">({filesDone}/{filesTotal} done)</span>{/if}
            {/if}
          </div>
          <div class="h-2 rounded-full bg-surface-3 overflow-hidden max-w-md mx-auto mt-3">
            {#if phase === 'persisting_urls'}
              <div class="h-full rounded-full bg-accent animate-pulse" style="width: 100%"></div>
            {:else}
              <div class="h-full rounded-full bg-accent transition-all duration-300" style="width: {pct > 0 ? pct : 3}%"></div>
            {/if}
          </div>
          <div class="text-fg-2 text-xs mt-2">
            {#if phase === 'uploading'}
              {formatBytes(uploadBytes)}{uploadTotalBytes > 0 ? ` / ${formatBytes(uploadTotalBytes)} · ${uploadPct.toFixed(1)}%` : ''}
            {:else if phase === 'persisting_urls'}
              {uploadProgress.toLocaleString()} lines parsed · writing {persistUrlCount > 0 ? `${persistUrlCount.toLocaleString()} URLs` : ''} to SQLite…
            {:else}
              {uploadProgress.toLocaleString()} lines processed{parsedFileSize > 0 ? ` · ${formatBytes(parsedBytes)} / ${formatBytes(parsedFileSize)} · ${parsePct.toFixed(1)}%` : ''}
            {/if}
          </div>
        </div>
        {#if uploadFiles.length > 1}
          <div class="rounded-lg border border-border bg-surface-2/40 p-3 space-y-2 text-xs">
            {#each uploadFiles as f, i}
              {@const filePct = f.size > 0 ? Math.min(((phase === 'uploading' ? f.bytesUploaded : f.bytesParsed) / f.size) * 100, 100) : 0}
              <div class="flex items-center gap-3">
                <div class="flex-1 min-w-0">
                  <div class="flex items-center gap-2">
                    <span class="text-fg truncate">{f.name}</span>
                    <span class="text-fg-2 text-[10px]">{formatBytes(f.size)}</span>
                  </div>
                  <div class="h-1 rounded-full bg-surface-3 overflow-hidden mt-1">
                    <div class="h-full rounded-full {f.status === 'completed' ? 'bg-green-400' : f.status === 'failed' ? 'bg-red-400' : 'bg-accent'} transition-all"
                         style="width: {filePct}%"></div>
                  </div>
                </div>
                <div class="text-right w-28 shrink-0">
                  <div class="text-fg-2 text-[10px] uppercase tracking-wider">{f.status}</div>
                  {#if phase === 'parsing' && f.lines}
                    <div class="text-fg-2 text-[11px]">{f.lines.toLocaleString()} lines</div>
                  {/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {:else}
      <svg xmlns="http://www.w3.org/2000/svg" width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" class="mx-auto text-fg-2 mb-3">
        <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"></path>
        <polyline points="17 8 12 3 7 8"></polyline>
        <line x1="12" y1="3" x2="12" y2="15"></line>
      </svg>
      <div class="text-fg text-sm font-medium mb-1">Drop one or more log files here, or click to browse</div>
      <div class="text-fg-2 text-xs mb-3">Multiple files are merged into a single unified analysis. Supports Apache, Nginx, CloudFront (TSV/CSV), Cloudflare, ALB, W3C/IIS formats (.log, .txt, .csv, .tsv, .gz, .json)</div>
      <div class="flex items-center justify-center gap-3 flex-wrap">
        <select bind:value={selectedFormat} class="px-3 py-1.5 rounded-lg text-sm bg-surface-2 border border-border text-fg focus:outline-none focus:ring-1 focus:ring-accent">
          {#each LOG_FORMATS as fmt}
            <option value={fmt.value}>{fmt.label}</option>
          {/each}
        </select>
        <label class="inline-block px-4 py-1.5 rounded-lg text-sm font-medium bg-accent/20 text-accent hover:bg-accent/30 transition-colors cursor-pointer">
          Browse Files
          <input type="file" multiple accept=".log,.txt,.csv,.tsv,.gz,.json" class="hidden" onchange={onFileSelect} />
        </label>
      </div>
    {/if}
  </div>

  {#if error}
    <div class="p-3 rounded-lg bg-red-500/10 border border-red-500/20 text-red-400 text-sm">{error}</div>
  {/if}

  <!-- Past Analyses -->
  {#if loading}
    <div class="text-fg-2 text-sm">Loading...</div>
  {:else if jobs.length > 0}
    <div class="rounded-xl border border-border overflow-hidden">
      <table class="w-full text-sm">
        <thead>
          <tr class="bg-surface-2 text-fg-2 text-left text-xs uppercase tracking-wider">
            <th class="px-4 py-3">File</th>
            <th class="px-4 py-3">Format</th>
            <th class="px-4 py-3">Status</th>
            <th class="px-4 py-3">Lines</th>
            <th class="px-4 py-3">Size</th>
            <th class="px-4 py-3">Duration</th>
            <th class="px-4 py-3">Date</th>
            <th class="px-4 py-3"></th>
          </tr>
        </thead>
        <tbody>
          {#each jobs as job}
            <tr class="border-t border-border hover:bg-surface-2/50 transition-colors">
              <td class="px-4 py-3">
                {#if job.status === 'completed'}
                  <a href="#/logs/{job.id}" class="text-accent hover:underline">{job.filename}</a>
                {:else}
                  <span class="text-fg">{job.filename}</span>
                {/if}
                {#if job.files && job.files.length > 1}
                  <div class="text-fg-2 text-[10px] mt-0.5">{job.files.length} files merged</div>
                {/if}
              </td>
              <td class="px-4 py-3 text-fg-2">{job.format || '-'}</td>
              <td class="px-4 py-3">
                <span class="px-2 py-0.5 rounded text-xs font-medium
                  {job.status === 'completed' ? 'bg-green-500/20 text-green-400' :
                   job.status === 'processing' ? 'bg-blue-500/20 text-blue-400' :
                   job.status === 'deleting' ? 'bg-yellow-500/20 text-yellow-400 animate-pulse' :
                   job.status === 'failed' ? 'bg-red-500/20 text-red-400' : 'bg-surface-3 text-fg-2'}">
                  {job.status === 'deleting' ? 'deleting...' : job.status}
                </span>
              </td>
              <td class="px-4 py-3 text-fg-2">{job.totalLines ? job.totalLines.toLocaleString() : '-'}</td>
              <td class="px-4 py-3 text-fg-2">{formatSize(job.fileSize)}</td>
              <td class="px-4 py-3 text-fg-2">
                {formatDuration(job.durationMs)}
                {#if job.uploadMs || job.parseMs || job.analysisMs}
                  <div class="text-[10px] text-fg-2/60 mt-0.5">
                    {#if job.uploadMs}<span title="Upload time">up {formatDuration(job.uploadMs)}</span>{/if}
                    {#if job.parseMs}{job.uploadMs ? ' · ' : ''}<span title="Parse time">parse {formatDuration(job.parseMs)}</span>{/if}
                    {#if job.analysisMs}{(job.uploadMs || job.parseMs) ? ' · ' : ''}<span title="Analysis time">stats {formatDuration(job.analysisMs)}</span>{/if}
                  </div>
                {/if}
              </td>
              <td class="px-4 py-3 text-fg-2">{timeAgo(job.createdAt)}</td>
              <td class="px-4 py-3">
                {#if job.status === 'deleting'}
                  <span class="text-yellow-400/60 text-xs">removing...</span>
                {:else}
                  <button
                    class="text-red-400 hover:text-red-300 text-xs cursor-pointer"
                    aria-label="Delete {job.filename}"
                    onclick={() => { if (confirm(`Delete ${job.filename}?`)) deleteJob(job.id); }}
                  >Delete</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="text-center text-fg-2 text-sm py-8">No log analyses yet. Upload a server access log to get started.</div>
  {/if}
</div>
