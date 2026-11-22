# Outreach drafts

These are prepared drafts, not posts or claims of directory acceptance. Adapt them to the channel and its self-promotion rules. Use the [verified sample-log walkthrough](../DEMO.md) or a recording of your own audit, with its settings and source identified.

## LinkedIn, Spanish

```text
En una auditoría SEO necesito saber qué páginas tienen un problema y cómo se llega a ellas.

Micelio rastrea un sitio, muestra sus enlaces internos en un mapa y agrupa los problemas con sus URLs afectadas. También analiza logs, compara dispositivos y permite consultar los rastreos desde un asistente mediante MCP.

Es open source, tiene un panel local y guarda los rastreos en tu equipo. Hay binarios para macOS, Windows y Linux.

La versión 1.16.0 añade una vista previa antes de empezar y rastreo con Chrome visible.

Busco comentarios sobre una tarea concreta: instalarlo y completar vuestro primer rastreo. ¿Dónde os atascáis?

https://miceliocrawler.com
https://github.com/jlhernando/micelio-crawler
```

## LinkedIn, English

```text
An SEO audit should help you decide which page to fix next.

Micelio crawls a site, maps its internal links and groups technical issues with the affected URLs. It also analyses server logs, compares devices and exposes crawl results to AI assistants through MCP.

It is open source, runs with a local web dashboard and stores crawls on your computer. Binaries are available for macOS, Windows and Linux.

Version 1.16.0 adds a homepage preview and an option to crawl through a visible Chrome window.

I would like feedback on one task: install it and finish your first crawl. Where does the process get confusing?

https://miceliocrawler.com
https://github.com/jlhernando/micelio-crawler
```

## Short announcement, Spanish

```text
Micelio es un crawler SEO open source con panel local.

Rastrea, revisa problemas técnicos, dibuja los enlaces internos y analiza logs. También puedes consultar tus rastreos desde un asistente mediante MCP.

Busco gente que pruebe la instalación y el primer rastreo.

https://github.com/jlhernando/micelio-crawler
```

## Short announcement, English

```text
Micelio is an open-source SEO crawler with a local dashboard.

Crawl a site, inspect technical issues, map internal links and analyse server logs. AI assistants can read your crawl results through MCP.

Looking for feedback on installation and the first crawl.

https://github.com/jlhernando/micelio-crawler
```

## MCP community, English

```text
I maintain Micelio, an open-source SEO crawler with a local MCP server.

An agent can list crawls, summarise issues, query affected pages and explain why a URL is or is not indexable. Starting crawls requires an explicit --allow-crawl flag; the default connection is read-only.

The MCP server uses the same local dashboard and crawl history as the user. It can start that dashboard automatically if needed.

Setup and limits:
https://github.com/jlhernando/micelio-crawler/blob/main/docs/MCP.md

I would appreciate feedback on the setup instructions and whether the returned data is useful for diagnosing a real SEO issue.
```

## Directory preparation

Checked on 22 November 2026. Requirements can change.

| Directory | Readiness |
|---|---|
| [awesome-mcp-servers](https://github.com/punkpeye/awesome-mcp-servers) | Public, locally installable Micelio fits the stated scope. Before submitting, verify a real Glama listing and score badge, the category, alphabetical order, owner/repo link label, language/deployment markers and absence of duplicates. No listing or score is assumed. |
| [awesome-seo-tools](https://github.com/Suganthan-Mohanadasan/awesome-seo-tools) | Defer until the project can demonstrate the existing users and reviews required by the directory. Repository activity and download counters alone do not prove that requirement. |

Read the MCP list's [contribution guide](https://github.com/punkpeye/awesome-mcp-servers/blob/main/CONTRIBUTING.md) and [badge validation workflow](https://github.com/punkpeye/awesome-mcp-servers/blob/main/.github/workflows/check-glama.yml), and the SEO list's [current rules](https://github.com/Suganthan-Mohanadasan/awesome-seo-tools#contributing). Do not invent a badge or add an entry that fails the requirements.

Base description for a future eligible submission:

```text
Micelio is an MIT-licensed SEO crawler with a local dashboard and MCP server. Agents can list crawls, summarise technical issues, query affected pages and inspect indexability. Crawl execution is opt-in and capped; the default MCP connection is read-only.
```
