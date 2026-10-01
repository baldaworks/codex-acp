# Releasing codex-acp

The initial fork release matched upstream. Its initial baseline is
`normahq/codex-acp-bridge` tag `v1.9.2`, so the first npm version is `1.9.2`
for `codex-acp` and all five `@baldaworks/codex-acp-*` binary packages.
Fork patch `1.9.3` adds `@normahq/codex-acp-bridge`, retaining the legacy
`codex-acp-bridge` executable. All seven packages share version `1.9.3` and
the five native binaries. `.omnidist/upstream-tag` records baseline `v1.9.2`.
Keep original upstream tags unchanged. Do not backfill older npm versions or
increment a version merely to retry a failed publication.

## Build and verify

From the fork repository root, select a fork version and its canonical upstream baseline:

```bash
export OMNIDIST_VERSION="$(./scripts/release-version.sh codex-acp/v1.9.3)"
go test -race ./...
go tool golangci-lint run
npx -y @omnidist/omnidist@latest --profile codex-acp build
npx -y @omnidist/omnidist@latest --profile codex-acp npm stage
node scripts/npm-legacy-bin.cjs
npx -y @omnidist/omnidist@latest --profile codex-acp npm verify
```

The version script checks the canonical upstream tag and requires its commit
to be an ancestor of the fork checkout. Omnidist uses the resulting exact
version through `version.source: env`, including the Go version ldflags.
Inspect every generated manifest before packing: names, descriptions,
`1.9.3` versions, repository URL, platform constraints, optional dependencies
and included license. The main description is configured in YAML as
`Run Codex as an Agent Client Protocol (ACP) agent.` Platform descriptions
identify the native target. Do not patch generated descriptions after staging. The explicit legacy-bin
step adds `codex-acp-bridge` pointing to the generated `codex-acp.js` shim;
Omnidist alias generation otherwise provides only the new command name.

## Initial publication with npm web 2FA

The initial publication uses the npm CLI directly. The committed Omnidist
profile remains `publish-auth: trusted` for subsequent automated releases;
its trusted publisher cannot be registered until the package exists.

Use an npm account allowed to create `codex-acp` and all scoped platform names.
Authenticate with the browser flow; do not paste credentials into logs or chat:

```bash
npx -y npm@11.16.0 login --auth-type=web --registry=https://registry.npmjs.org
```

Pack the verified platform packages first, then both meta packages:

```bash
mkdir -p .omnidist/codex-acp/tarballs/1.9.3
for package in .omnidist/codex-acp/npm/@baldaworks/* .omnidist/codex-acp/npm/codex-acp .omnidist/codex-acp/npm/@normahq/codex-acp-bridge; do
  npx -y npm@11.16.0 pack "./$package" --pack-destination .omnidist/codex-acp/tarballs/1.9.3
done
```

Publish the five binaries before both meta packages, completing browser 2FA
when npm prompts:

```bash
for package in .omnidist/codex-acp/tarballs/1.9.3/baldaworks-codex-acp-*.tgz; do
  npx -y npm@11.16.0 publish "./$package" --access public --tag latest
done
npx -y npm@11.16.0 publish ./.omnidist/codex-acp/tarballs/1.9.3/codex-acp-1.9.3.tgz --access public --tag latest
npx -y npm@11.16.0 publish ./.omnidist/codex-acp/tarballs/1.9.3/normahq-codex-acp-bridge-1.9.3.tgz --access public --tag latest
```

If a publication fails, stop and inspect which exact package/version pairs
exist in npm. Retain the verified tarballs and resume only missing packages;
do not republish accepted units or rebuild different contents at the same
version. Verify registry descriptions, versions and dependencies, then install
`codex-acp@1.9.3` into a clean directory and run `codex-acp --help` and
`codex-acp version`. Also install `@normahq/codex-acp-bridge@1.9.3`
in a separate clean directory and run both command names with `--help` and
`version`. The legacy package must depend on the baldaworks native packages.

## Configure trusted publishing

After all seven packages exist, register GitHub Actions as their trusted
publisher for repository `baldaworks/codex-acp` and workflow filename
`omnidist-release.yml`. Review the generated seven-package plan first:

```bash
npx -y @omnidist/omnidist@latest --profile codex-acp npm trust --workflow-file omnidist-release.yml
npx -y @omnidist/omnidist@latest --profile codex-acp npm trust --workflow-file omnidist-release.yml --apply
```

Read back each registration using `npx -y npm@11.16.0 trust list <package>`
or npm package settings. Verify the repository, workflow filename and publish
permission for both meta packages and every platform package. Trust administration
requires package write access, account 2FA and supported authentication; a
publish token with bypass 2FA is not sufficient for this operation. See the
[npm trust reference](https://docs.npmjs.com/cli/v11/commands/npm-trust/).

The CI publish job grants `id-token: write`, uses compatible Node/npm and
`publish-auth: trusted`, and does not require an npm publish token secret.
Workflow permissions alone do not prove that registry registrations exist.
Confirm configuration now; verify a live OIDC publication on the next actual
upstream release rather than incrementing the version solely for a test.

## Subsequent releases

Integrate the intended upstream baseline and its tags, then run the checks
above, updating `.omnidist/upstream-tag` to that canonical baseline.
Fork release tags use a separate namespace, for example `codex-acp/v1.9.3`
for the alias patch on upstream `v1.9.2`. This triggers both the
npm OIDC workflow and the GitHub archive release while preserving upstream
`v1.9.2` unchanged.

Alternatively, dispatch `omnidist-release.yml` on the reviewed fork branch with
both `release_version` and the corresponding `upstream_tag`. A manual dispatch publishes npm artifacts;
GitHub archives are published only on fork release tags. Run one publication
at a time and never dispatch an already published version as a test.
