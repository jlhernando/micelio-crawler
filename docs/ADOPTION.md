# Maintainer guide to adoption

Measure whether people can find Micelio, install it and finish a useful task. Stars and binary downloads alone do not establish active use.

## Capture a private baseline

The capture script needs Python 3 and GitHub CLI authenticated for the repository. Reading Traffic requires repository access; GitHub's fine-grained tokens require **Administration: read** for those endpoints. Public repository data can still be saved if Traffic returns 403.

```bash
python3 scripts/capture-github-metrics.py
```

It reads stars, forks, the rolling 14-day traffic window, referring sites, popular paths and release assets. Binary download events, checksum downloads and other attachments have separate totals. Draft releases are excluded; prereleases are marked in the stored asset records.

Snapshots are private JSON files outside this repository:

```text
~/.micelio/metrics/github/jlhernando/micelio-crawler/
```

The script creates restricted directories and files on Unix. On Windows, keep the user-profile directory protected by the account's normal access controls. Do not commit or publish snapshots containing referrers or popular paths. No app telemetry, account tracking, scheduled task or GitHub workflow is installed by the capture script.

Each new snapshot compares only matching asset IDs with the previous snapshot. New, removed and decreasing-count assets are recorded separately, because replacing a release asset can restart its counter. Queries run in sequence, not as a single atomic measurement.

## Four weekly checks

Run the capture manually at the beginning, then once a week for four weeks. Record which change shipped between captures, such as a clearer download page or an installation fix. Compare the following observations:

- Unique visitors and referring sites in each window.
- Binary download events on unchanged asset IDs, with asset discontinuities noted.
- New stars, installation problems and first-crawl feedback.
- Which questions repeat in issues or user conversations.

Do not sum unique visitors from overlapping 14-day windows or add daily unique counts to infer unique people across a week. Clones may include automation. Download counts are file events, not installations, unique users or completed audits. Visits and downloads do not establish a conversion rate. Do not subtract aggregate totals across asset replacements.

Ask volunteers to try one task, then describe where they got stuck. Start with installation and a first 100-page audit. This provides feedback without adding tracking to Micelio.

## Demonstrations and distribution

Use the [sample walkthrough](DEMO.md) and [prepared outreach copy](growth/outreach.md). A real demonstration should identify the tested version, settings and data source. Synthetic examples must be labelled. Compare another tool only on verified behavior; do not invent benchmarks or promises about search rankings.

Before submitting to a directory, reread its current contribution rules. Before posting from an account, choose the channel and review the final text. A prepared draft does not mean a submission or post has been sent.

## Sources

- [GitHub repository Traffic](https://docs.github.com/en/rest/metrics/traffic)
- [GitHub releases](https://docs.github.com/en/rest/releases/releases)
- [GitHub release assets](https://docs.github.com/en/rest/releases/assets)
- [GitHub topics](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/classifying-your-repository-with-topics)
