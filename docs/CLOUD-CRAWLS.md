# Crawl in the cloud, analyse on your computer

Crawling needs very little: most of the time Micelio waits for the site, because a polite crawl leaves a delay between requests. The heavy work is the analysis and the dashboard (link graph, PageRank, Universe). So you can crawl on a small cloud server and do the rest on your own computer.

1. **Server:** `micelio crawl --no-analysis --db-only` crawls and writes one file, the crawl database.
2. **Transfer:** copy that file to your computer (scp, rsync, or a bucket).
3. **Your computer:** `micelio import site.db` adds the crawl to the dashboard's History and runs the full analysis there.

## 1. Crawl on the server

Download the Linux binary for the server's CPU (`micelio-linux-amd64` or `micelio-linux-arm64`) from the [releases](https://github.com/jlhernando/micelio-crawler/releases). It is a single file with no dependencies.

```bash
micelio crawl https://example.com \
  --max-pages 50000 --depth 20 -c 4 --delay 500 --adaptive-rate \
  --no-analysis --db-only --db example.db --memlimit 512MiB
```

- `--no-analysis` skips the post-crawl analysis and avoids loading all saved pages into memory, unless an explicitly requested post-crawl feature needs them. `micelio import` runs the analysis later.
- `--db-only` writes only the crawl database (`example.db`), no JSONL.
- `--full-anchors` keeps link anchor texts, so the import can build the anchor reports. It makes the file bigger.
- `--memlimit` sets a soft Go memory limit, not a hard limit on the whole process.

For recurring crawls, run it from cron or use `micelio schedule`.

### How big a server?

Measured on a 1,116-page crawl with `--no-analysis`, limited to 1 CPU core:

| | Value |
|---|---|
| Peak memory | 63 MB |
| CPU time | 3 s user over a 1m52s crawl |
| Crawl database | 4.6 MB (about 4 KB per page) |

Memory grows slowly with the number of URLs found. Rough guide:

| Crawl size | Server |
|---|---|
| Up to 50K pages | 1 vCPU, 512 MB to 1 GB RAM, `--memlimit 384MiB` |
| 50K to 500K pages | 1 to 2 vCPU, 2 GB RAM |
| Disk | about 0.5 GB per 100K pages |

The smallest ARM or x86 instances of most clouds are enough (for example Hetzner CAX11 or AWS t4g.micro).

## 2. Bring the file home

```bash
scp server:example.db .
# or keep a folder in sync
rsync -avz server:crawls/ ~/crawls/
```

With object storage, upload from the server (`aws s3 cp example.db s3://bucket/` or `rclone copy`) and download it on your computer.

## 3. Import on your computer

```bash
micelio import example.db --link-intelligence
```

- If the dashboard is running (default `http://127.0.0.1:3100`), the file is sent to it and appears in History right away. Use `--server` for another address and `--auth-token` if the dashboard has one. The token is only sent over `https://` or to this computer (`127.0.0.1`/`localhost`); plain `http://` to another computer is refused.
- If no dashboard is running, the crawl goes straight into `~/.micelio` and shows up the next time you run `micelio ui`. `--local` forces this.
- `--link-intelligence` adds link analysis (centrality, link suggestions) even if the crawl did not ask for it.

You can also use **History → Import crawl…** in the dashboard and pick the file.

`micelio import` also accepts JSONL output (`.jsonl`, `.jsonl.gz`, `.jsonl.zst`). The database is better: it keeps the crawl settings.

The crawl database keeps the settings without credentials: cookies, headers such as `Authorization`, API keys, passwords, tokens and webhook URLs are left out, and a proxy password is masked. The file is safe to copy around in that respect (it still holds the crawled pages), and is created owner-only (`0600`). `--resume` takes credentials from the flags you pass again, never from the file. Importing a database made by an older version drops its credentials from the copy.

### What the import cannot rebuild

Some data is collected during the crawl and not stored page by page:

- **Anchor reports:** need `--full-anchors` on the server.
- **N-grams:** need the page text, which is not stored. Not available on imported crawls.
- **Page weight, external link checks and integrations** (Search Console, GA4, CrUX, Plausible): explicitly requesting these can still require loading all pages even with `--no-analysis`. Import does not rerun external services; it can only use data already saved in the crawl.
