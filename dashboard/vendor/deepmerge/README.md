# Patched deepmerge for the dashboard build

This local package contains the CommonJS bundle and types of upstream
[deepmerge 4.3.1](https://github.com/TehShrike/deepmerge/tree/v4.3.1), under
the included MIT license. It is a private local fork, not an official release.
The dashboard npm override makes the Svelte Vite plugin resolve this package.

[GHSA-gcqx-w48m-w2h4](https://github.com/advisories/GHSA-gcqx-w48m-w2h4) has
no official patched release. All three destination assignments now create
ordinary own data properties with Object.defineProperty. This prevents a
JSON __proto__ key from changing the returned object's prototype, including
when the target has a null prototype, a null placeholder, or an own __proto__
key. The original inherited-key guard and merge options remain in place.

Regression tests use the package actually resolved by the Svelte plugin,
exercise these attack shapes and check normal merge behavior. Run
`npm run test:security` from dashboard after npm ci. Reconsider the override
when upstream ships a verified fix or the plugin removes this dependency.
Do not replace this with the unpatched upstream bundle to silence an audit.
