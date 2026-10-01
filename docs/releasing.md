# Releasing codex-acp

`baldaworks/codex-acp` is the canonical implementation and release source.
Its Go module is `github.com/baldaworks/codex-acp`; new releases use normal
root-module tags `vX.Y.Z`. Migration starts at `v1.9.3`, following historical
`v1.9.2`. Keep existing tags and published artifacts unchanged.

The Omnidist profile stages seven npm packages: `codex-acp`, legacy
`@normahq/codex-acp-bridge` and five `@baldaworks/codex-acp-*` native packages.
All share one exact version. Native packages publish before both meta packages.
The configured description is `Run Codex as an Agent Client Protocol (ACP) agent.`

## Prepare and verify

Choose an unused version and a reviewed, committed checkout. Create the tag
locally before release validation; publication occurs only when it is pushed.
For the current release:

```bash
go test -race ./...
go tool golangci-lint run
git tag -a v1.10.0 -m 'Release codex-acp 1.10.0'
export OMNIDIST_VERSION="$(./scripts/release-version.sh v1.10.0)"
npx -y @omnidist/omnidist@latest --profile codex-acp build
npx -y @omnidist/omnidist@latest --profile codex-acp npm stage
node scripts/npm-legacy-bin.cjs
npx -y @omnidist/omnidist@latest --profile codex-acp npm verify
```

`scripts/release-version.sh` rejects malformed tags, missing tags and a tag
whose commit differs from HEAD. Both GitHub workflows check out the release
revision and use its numeric version for native binary ldflags. Ordinary
`go install` obtains the version from Go build info; the legacy executable
reports its pinned canonical dependency version.

Omnidist supports npm package aliases directly. Its generated aliases expose
the canonical command; `scripts/npm-legacy-bin.cjs` adds `codex-acp-bridge`
pointing to the same generated JS launcher and marks the alias deprecated in
favor of `codex-acp`, before verify/pack/publish. Preserve
this explicit step when regenerating workflows. Inspect every manifest's name,
version, description, repository, bin entries, platform constraints, optional
dependencies and license. Test packed installations of both meta packages and
both legacy command names.

## Trusted publishers and bootstrap

Register and read back GitHub Actions trusted publishing for every package:
repository `baldaworks/codex-acp`, workflow filename `omnidist-release.yml`,
publish permission. Review the complete seven-package plan:

```bash
npx -y @omnidist/omnidist@latest --profile codex-acp npm trust --workflow-file omnidist-release.yml
```

For interactive administration, run the printed npm commands directly. Example:

```bash
npm_config_browser=false npx -y npm@11.16.0 trust github @normahq/codex-acp-bridge \
  --repo baldaworks/codex-acp --file omnidist-release.yml --allow-publish --yes
npx -y npm@11.16.0 trust list @normahq/codex-acp-bridge
```

Complete browser 2FA when prompted. Avoid `--json` on an interactive challenge:
npm buffers the authentication URL until completion. After authentication,
JSON readback is useful for checking every registration. Owner npm access is
required for the unscoped main package and both scoped namespaces. The CI job
uses `id-token: write` and `publish-auth: trusted`, with no npm token secret.
See the [npm trust reference](https://docs.npmjs.com/cli/v11/commands/npm-trust/).

A package that does not yet exist needs an initial npm CLI publication before
its trusted publisher can be registered. Login with web 2FA:

```bash
npm_config_browser=false npx -y npm@11.16.0 login --auth-type=web
```

Pack verified artifacts into a version-specific directory. Publish the five
platform tarballs, then `codex-acp`, then the legacy meta package using
`npx -y npm@11.16.0 publish <tarball> --access public --tag latest`.
Do not log credentials or mix older retained tarballs into a new release.

## Publish canonical release

Verify GitHub repository identity/permissions and all trusted publisher
registrations after any fork detachment. Then push the reviewed tag:

```bash
git push origin refs/tags/v1.10.0
```

This starts npm OIDC publication and the GitHub native-archive release.
`omnidist-release.yml` also supports manual dispatch with `release_version`
(e.g. `1.10.0`), checking out its existing `v1.10.0` tag. Use dispatch to resume a
failed workflow only after inspecting accepted package/version pairs.

Verify published npm manifest metadata and tarball hashes. In clean directories,
install both npm meta packages and run help/version. Check canonical public Go
installation without a local workspace or replace directive:

```bash
go install github.com/baldaworks/codex-acp/cmd/codex-acp@v1.10.0
codex-acp version
```

Also check `@latest`, public `pkg/cobracmd` consumer compilation and the native
archive checksums. A live successful npm workflow confirms OIDC publication.

## Synchronize legacy Go and GitHub archives

The original repository retains module
`github.com/normahq/codex-acp-bridge` and thin public API/CLI adapters. Its
`sync-canonical-release.yml` runs hourly at minute 23 or by explicit dispatch:

```bash
gh workflow run sync-canonical-release.yml \
  --repo normahq/codex-acp-bridge -f version=v1.10.0
```

It requires an existing non-draft, non-prerelease canonical GitHub release,
pins that exact Go dependency, runs race tests/lint and atomically pushes an
immutable matching tag. It uses only its own repository's GitHub token; there
is no cross-repository write secret. Invalid input, downgrades, dirty checkouts
and conflicting existing tags fail. Repeating a synchronized version verifies
the dependency and preserves the original tag.

The asset helper verifies canonical checksums and repackages those same binary
bytes under historical `codex-acp-bridge-*` archive/executable names. Archives
are reproducible and include docs/license from the legacy tag. Existing assets
must match; they are never clobbered. The old repository does not publish npm
or build a separate bridge implementation.

Verify both pinned and latest legacy `go install`, old command-package and
`pkg/cobracmd` imports without consumer replace directives, and legacy archive
checksums. Initial migration preparation uses an isolated legacy checkout and
local workspace; once the canonical tag exists, populate real dependency
checksums before pushing the legacy module and synchronizing its release.

## Partial-release recovery

Registry versions, Go tags and accepted GitHub assets are immutable. Retain
verified tarballs and exact commit/registry receipts. Resume only missing npm
package/version pairs with identical contents; do not rebuild changed artifacts
or increment a version merely to retry. Check registry propagation before
assuming a newly published optional dependency is absent.

A legacy synchronization retry verifies an existing tag, then resumes missing
archive assets. Fix failures with additive commits and a subsequent version
when necessary; never force-push a distributed version. Historical legacy npm
platform packages, original tags and GitHub assets remain available. See
[migration.md](migration.md) for compatibility policy.
