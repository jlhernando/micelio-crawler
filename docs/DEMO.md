# Try Micelio

## A first audit in the dashboard

The [Universe animation](../README.md) and [screenshots](../README.md#screenshots) show the existing dashboard. For a first audit, run Micelio, open http://localhost:3100 and choose **New Crawl**. Use a site you own or have permission to crawl, limit it to 100 pages and leave JavaScript rendering off at first.

After the crawl finishes, choose an issue under **Issues**, inspect its affected URLs, then open **Links** to explore the internal-link map. Counts depend on the site, the crawl limits and the pages reached. This is an audit workflow, not a promise to find every issue or improve search rankings.

## A log-analysis demo without crawling a site

[demo-access.log](examples/demo-access.log) is a **synthetic Apache Combined log** with four requests. It contains documentation-only IP addresses, not customer data. Download the file or clone this repository, then run:

```bash
# From the repository root, with Micelio installed
micelio logs docs/examples/demo-access.log --output demo-bots.json
```

This creates `demo-bots.json` in your current directory. The sample has three successful responses and one 404, two requests labelled Googlebot, one labelled GPTBot and one ordinary browser request. Bot labels come from User-Agent matching; these synthetic addresses do not prove a bot's ownership. Nothing here is a live-site performance benchmark.

You can also upload the sample file through **Logs** in your dashboard. Use the same four requests to learn the tables before uploading a real access log. Real logs may contain personal or customer data; keep those on your own machine.

## Record a short demonstration

For a 45–60 second recording, use your own small site and show the following sequence:

1. Enter the homepage in **New Crawl** and show the page limit.
2. Open the completed crawl's **Issues** and choose a real issue.
3. Open one affected URL and explain what you would check or fix.
4. Show that page in the **Links** view and its neighbouring pages.

Label sample data as sample data. Display the tested version and settings. Do not present synthetic dashboard fixtures as a customer audit or a measurement of speed, traffic or rankings.

Native Chrome is optional. It needs Chrome and a visible desktop session; it does not support device emulation or comparison, custom User-Agent or manual headers, manual credentials, snippets, sitemap discovery, page weight or external-link checks. See the [user guide](USER-GUIDE.md) before recording that mode.
