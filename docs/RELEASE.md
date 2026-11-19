# Releases

There is no release of Plumb yet. This file says how one is made and how you check a download. The plan of the first release is `docs/plans/release-slice.md`.

## Verify a download

A release is one file: `plumb-<version>-linux-amd64.tar.gz`. Next to it is `SHA256SUMS`, and inside it is a second `SHA256SUMS` for `plumb`, `plumb-agent` and `install.sh`.

1. **Who built it** (only if the release names a build attestation): `gh attestation verify plumb-<version>-linux-amd64.tar.gz --repo Bernotese/plumb --signer-workflow Bernotese/plumb/.github/workflows/release.yml --source-ref refs/heads/main`. This proves that GitHub's release workflow of this repository built the file from the commit that the attestation names. **Use `gh` 2.102.0 or newer** (`gh --version`). An older `gh` matches `--signer-workflow` only by its start and `--source-ref` without case, so a file of another workflow or branch could pass; the `gh` of most distributions has no `gh attestation` at all. Get a current one from https://cli.github.com (GitHub's packages for Debian, Ubuntu, Fedora and others) or from https://github.com/cli/cli/releases. The two last options matter: without them, an attestation made by any other workflow of the repository (for example on a branch) would pass too.
2. **The file is whole:** `sha256sum -c SHA256SUMS` in the folder of the download.
3. **The files inside are whole:** unpack it, then `sha256sum -c SHA256SUMS` in that folder.

`install.sh` can check itself only while it runs, so steps 1 and 2, **before** you unpack, are what prove the installer. If the installer of a tarball prints "source build" instead of "Bundle check of release …", stop: it is not a release installer. Run it from a clean root shell (`sudo -i`), not through `sudo -E`: `BASH_ENV` and `PATH` act before the script's first line. The installer of the tarball checks this too before it uses a file, and it refuses a `SHA256SUMS` that lists anything but `plumb`, `plumb-agent` and `install.sh`.

`SHA256SUMS` alone shows only that the files are whole: whoever changes the tarball can also change `SHA256SUMS`. Only the attestation shows where the file was built. Neither shows that the code is good. The first versions are below 1.0: the API may still change.

## The version

- The file `VERSION` holds the version of the next release, as `MAJOR.MINOR.PATCH`. `info.version` in `api/openapi.yaml` is the same; a test fails when they differ.
- **It is a proposal (0.1.0) until the user decides the release version.**
- A release build sets it with `-ldflags -X main.version=v<version>`: `plumb --version` prints `plumb v<version>`, and `plumb-agent version` prints `plumb-agent v<version>`. `scripts/package-release.sh` refuses a version whose `MAJOR.MINOR.PATCH` is not the one of `VERSION`, and it checks what both binaries print.
- A build that is no release says so: `make build` gives `dev-<commit>` (with `-dirty` for uncommitted changes), and `go build` without flags gives `dev`.

## The build is reproducible

`scripts/package-release.sh v<version> [dir]` builds the tarball with fixed flags: `-trimpath`, `-buildvcs=false`, an empty build id, a sorted tar with fixed times and owners, and `gzip -n`. The release workflow builds it twice and compares. The CI job "lint, test, build" prints the hashes of a bundle of every commit (version `v<VERSION>-repro`; no release), so a build on another machine can be compared with it.

## Checklist of a release

Only `pl-lead` runs the release workflow, and only after the user's yes. Only the user publishes. Nothing below happens without the user.

| Step | Who               | What                                                                                                                                                                                                                                                                                         |
| ---- | ----------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1    | team              | Every card of the release plan is done: the leftovers, the installer checks the tarball, the QA sweep, the leak hunt (repo part), the README and `CHANGELOG.md`.                                                                                                                             |
| 2    | pl-backend (host) | A release candidate (RC), built **locally** with `package-release.sh`, never with the workflow and never as a tag, passes the full re-run on the test host: the upgrades, every acceptance list, and last a clean install with the restore of a Plumb backup. Its `SHA256SUMS` are recorded. |
| 3    | pl-reviewer       | The leak hunt of the RC (the tarball, the strings of its binaries, the test host's data after step 2).                                                                                                                                                                                       |
| 4    | pl-lead           | One message to the user with the result and the open questions: the version, the release steps, the user's own repair run, and the secret scanning alerts of the repository.                                                                                                                 |
| 5    | pl-lead           | Only after the user's yes: run the release workflow for that version. It makes a **draft**, and it refuses an existing tag. Fill the draft's text from `docs/RELEASE-NOTES-TEMPLATE.md`.                                                                                                     |
| 6    | pl-lead           | The draft's `SHA256SUMS` equal those of the RC of step 2 (if the build is not reproducible across machines: download the draft's tarball and run the install and upgrade check on the test host), and `gh attestation verify` passes.                                                        |
| 7    | **the user**      | Read the draft and publish it. Publishing makes the tag.                                                                                                                                                                                                                                     |
| 8    | **the user**      | The repair run of the user's own install with the release tarball, `--dry-run` first.                                                                                                                                                                                                        |
| 9    | pl-lead           | `docs/STATUS.md`; the README install path points to the release; `CHANGELOG.md` gets the next "Unreleased" section.                                                                                                                                                                          |

## Never (without the user)

No run of the release workflow, no tag, no GitHub release or draft, no change of a repository setting or secret, nothing on the user's own install.
