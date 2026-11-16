# Changelog

Every release of Plumb gets one section here, newest first. A section names the version and the date, then:

- **Added**, **Changed**, **Fixed**, **Removed**: what a user notices, in short lines.
- **Rights:** the Proxmox token rights that the release needs, and every right that changed (an upgrade needs a repair run of the installer then).
- **Upgrade:** what to do from the version before (for example a repair run, or a step per guest).
- **Known limits:** what does not work yet, or was not tested.

How to check a download: `docs/RELEASE.md`.

## Unreleased

The first release. Its version is not decided yet (the proposal is 0.1.0).

### Known issues

- `npm audit` reports one low advisory (GHSA-gcqx-w48m-w2h4, prototype poisoning in `deepmerge` 4.3.1). It comes only with the build tool `@sveltejs/vite-plugin-svelte` 7.3.1 (a dev dependency), it is not in the shipped web files, and no fixed version exists yet.
