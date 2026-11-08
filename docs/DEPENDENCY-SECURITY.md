# Dependency security checks

Run `npm ci && npm audit --audit-level=low && npm test && npm run build`
from the repository root. In `dashboard`, run `npm ci`,
`npm audit --audit-level=low`, `npm run test:security` and `npm run build`.
The Node.js CI job repeats these checks on Node 24.

## Overrides

The Google SDKs still pull rimraf 5, whose glob range selects an affected
major. Root npm overrides require glob 14.0.4+ and minimatch 11.0.5+.
Tests exercise rimraf's real glob cleanup, cancellation during traversal,
and matching behavior across the major-version upgrade. Remove the overrides
only when the parent dependencies themselves select corrected versions.

The Svelte Vite plugin still depends on deepmerge 4.3.1. No corrected official
release exists for GHSA-gcqx-w48m-w2h4. Its override uses the MIT-licensed local
patched package in `dashboard/vendor/deepmerge`; the provenance and exact
change are documented there. This is a local maintained patch, not an
upstream release or an audit exception. Security tests resolve the package
from the build plugin so they exercise the code that builds the dashboard.
Review the override when a verified upstream fix is available.

## Patched dependencies

The selected dependency graph also updates undici, qs, @grpc/grpc-js and
Go's github.com/cloudflare/circl to versions beyond the affected ranges
reported when this change was prepared. Audit results are snapshots, not a
guarantee against future advisories. Keep the shared manifests and lockfiles
aligned when porting subsequent generic fixes.
