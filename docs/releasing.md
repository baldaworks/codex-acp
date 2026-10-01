# Releasing codex-acp

The fork preserves upstream versions. Its initial baseline is
`normahq/codex-acp-bridge` tag `v1.9.2`, so the first npm version is `1.9.2`
for `codex-acp` and all five `@baldaworks/codex-acp-*` binary packages.
Keep original upstream tags unchanged. Do not backfill older npm versions or
invent a new version to retry a failed publication.

## Build and verify

From the fork repository root, select the corresponding upstream tag:

```bash
export OMNIDIST_VERSION="$(./scripts/release-version.sh v1.9.2)"
go test -race ./...
go tool golangci-lint run
npx -y @omnidist/omnidist@latest --profile codex-acp build
npx -y @omnidist/omnidist@latest --profile codex-acp npm stage
npx -y @omnidist/omnidist@latest --profile codex-acp npm verify
```

The version script checks the canonical upstream tag and requires its commit
to be an ancestor of the fork checkout. Omnidist uses the resulting exact
version through `version.source: env`, including the Go version ldflags.
Inspect every generated manifest before packing: names, descriptions,
`1.9.2` versions, repository URL, platform constraints, optional dependencies
and included license. The main description is configured in YAML as
`Run Codex as an Agent Client Protocol (ACP) agent.` Platform descriptions
identify the native target. Do not patch generated descriptions after staging.

## Initial publication with npm web 2FA

The initial publication uses the npm CLI directly. The committed Omnidist
profile remains `publish-auth: trusted` for subsequent automated releases;
its trusted publisher cannot be registered until the package exists.

Use an npm account allowed to create `codex-acp` and all scoped platform names.
Authenticate with the browser flow; do not paste credentials into logs or chat:

```bash
npx -y npm@11.16.0 login --auth-type=web --registry=https://registry.npmjs.org
```

Pack the verified platform packages first, then the meta package:

```bash
mkdir -p .omnidist/codex-acp/tarballs
for package in .omnidist/codex-acp/npm/@baldaworks/* .omnidist/codex-acp/npm/codex-acp; do
  npx -y npm@11.16.0 pack "./$package" --pack-destination .omnidist/codex-acp/tarballs
done
```

Publish the five binaries before the meta package, completing browser 2FA
when npm prompts:

```bash
for package in .omnidist/codex-acp/tarballs/baldaworks-codex-acp-*.tgz; do
  npx -y npm@11.16.0 publish "./$package" --access public --tag latest
done
npx -y npm@11.16.0 publish ./.omnidist/codex-acp/tarballs/codex-acp-1.9.2.tgz --access public --tag latest
```

If a publication fails, stop and inspect which exact package/version pairs
exist in npm. Retain the verified tarballs and resume only missing packages;
do not republish accepted units or rebuild different contents at the same
version. Verify registry descriptions, versions and dependencies, then install
`codex-acp@1.9.2` into a clean directory and run `codex-acp --help` and
`codex-acp version`.

## Configure trusted publishing

After all six packages exist, register GitHub Actions as their trusted
publisher for repository `baldaworks/codex-acp` and workflow filename
`omnidist-release.yml`. Review the generated six-package plan first:

```bash
npx -y @omnidist/omnidist@latest --profile codex-acp npm trust --workflow-file omnidist-release.yml
npx -y @omnidist/omnidist@latest --profile codex-acp npm trust --workflow-file omnidist-release.yml --apply
```

Read back each registration using `npx -y npm@11.16.0 trust list <package>`
or npm package settings. Verify the repository, workflow filename and publish
permission for the meta package and every platform package. Trust administration
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
above with that baseline version. Fork release tags use a separate namespace,
for example `codex-acp/v1.9.3` for upstream `v1.9.3`. This triggers both the
npm OIDC workflow and the GitHub archive release while preserving upstream
`v1.9.3` unchanged.

Alternatively, dispatch `omnidist-release.yml` on the reviewed fork branch with
the corresponding `upstream_tag`. A manual dispatch publishes npm artifacts;
GitHub archives are published only on fork release tags. Run one publication
at a time and never dispatch an already published version as a test.
