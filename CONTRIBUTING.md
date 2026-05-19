# Contributing to Fracta

Thanks for your interest in contributing to Fracta!

## Understand the License

Fracta is released under the [Functional Source License v1.1, ALv2
Future License](https://fsl.software) (FSL-1.1-ALv2). This is a
source-available, mostly-permissive non-compete license that
automatically converts each release to the Apache License 2.0 on the
second anniversary of its publication.

In plain English: you can use Fracta internally for almost anything,
modify it, redistribute it, and build on it — but you cannot offer
Fracta (or a derivative) as a hosted commercial product or service
competing with the project, until each release's two-year anniversary
has passed.

See [LICENSE](./LICENSE), [COMMERCIAL.md](./COMMERCIAL.md), and the
[Licensing docs page](https://fracta.quasarops.com/docs/introduction/licensing)
for the full picture.

We chose this license because we want Fracta to be as openly available
as possible while preserving the ability for the project to be
sustainably maintained and developed long-term.

## Before You Start

If you're planning a substantial change — new features, larger
refactors, anything that touches public-facing APIs or architecture —
please open an issue or discussion first so we can confirm the change
is in scope before you spend time on it. Drive-by fixes (typos, small
bugs, documentation improvements, small bug fixes) are welcome without
prior discussion.

For broader context on what areas are open for contribution, see the
[contributing overview in the docs](https://fracta.quasarops.com/docs/contributing/overview).

## Submitting a Change

Once you're ready to contribute code:

1. Fork the repo and create a topic branch off of `main`.
2. Make your changes; keep them focused. One logical change per PR.
3. Sign your commits (`git commit -S`) so they show as **Verified** on
   GitHub. This isn't a legal requirement; it's a traceability one. If
   you have not set up commit signing yet, start with [GitHub's guide
   to commit signature
   verification](https://docs.github.com/en/authentication/managing-commit-signature-verification/about-commit-signature-verification).
4. Run the test suite locally (`go test ./...` for Go code, `pytest`
   for Python strategy code) and make sure it passes.
5. Open a Pull Request describing what the change does and why.

If a maintainer asks for changes during review, please push additional
commits to the same branch rather than force-pushing the branch
history — it makes the review easier to follow. We may squash on merge.

## License of Contributions

By submitting a contribution to Fracta — whether code, documentation,
or other materials — you license your contribution to the project, and
to all downstream users of the project, under the same terms as the
project itself: the Functional Source License v1.1, ALv2 Future
License (FSL-1.1-ALv2).

To be precise about what this means:

- **You retain copyright in the lines you author.** Copyright vests in
  the author of an original work automatically. Contributing to Fracta
  doesn't transfer ownership of your contribution to the project — it
  grants the project a broad license to use your contribution under
  FSL terms.
- **The license you grant is broad enough for the project to
  continue under FSL indefinitely**, including allowing the project to
  grant commercial licenses to third parties (per FSL's "Competing
  Use" provisions) and allowing the project to publish your
  contribution under the Apache License 2.0 once the FSL's 2-year
  future-grant clock has run on the release containing it.
- **By opening a Pull Request, you confirm that** (a) the contribution
  is your original work or you otherwise have the right to submit it,
  and (b) you grant the license described above.

This is sometimes called the **"inbound = outbound" rule**: any
contribution comes in under the same terms it goes back out under. We
do not currently require a separate Contributor License Agreement
(CLA) for this reason — it adds friction without proportional benefit
for a project at our stage. For the reasoning behind this choice, see
[Ben Balter's writeup](https://ben.balter.com/2018/01/02/why-you-probably-shouldnt-add-a-cla-to-your-open-source-project/).

If we ever introduce a CLA in the future, it will only apply to
contributions submitted after that point. Existing contributions
remain under the inbound = outbound terms in effect when they were
submitted.

## Pull Request Process

When you open a PR, GitHub auto-populates the PR description from our
[PR template](.github/PULL_REQUEST_TEMPLATE.md). The template includes
a required checkbox confirming that you have read this CONTRIBUTING.md
and agree to license your contribution under FSL-1.1-ALv2 on the
inbound = outbound basis described above.

**Maintainers will not merge PRs where the licensing checkbox is
unchecked.** This isn't a separate Contributor License Agreement —
it's the same inbound = outbound rule made into an explicit
acknowledgement at the point of submission, which serves as
documentation that you understood the licensing posture when you
contributed.

If GitHub didn't auto-populate the template for some reason (e.g. you
opened the PR via the API or via a tool that bypasses templates),
please edit your PR description to include the checkbox line manually
and tick it. You can copy it from the
[PR template](.github/PULL_REQUEST_TEMPLATE.md).

## Bug Reports

Open a GitHub issue. Helpful issues include:

- What you were doing
- What you expected to happen
- What actually happened
- The Fracta version (`fracta --version`), your OS, your deployment
  mode (local process / Docker Compose / Kubernetes)
- Relevant logs (with secrets redacted)
- A minimal reproduction if you can manage one

## Feature Requests

Open a GitHub issue or discussion. Tell us what problem you're trying
to solve, not just what change you want to see; we'll often find a
simpler or more general solution than the original request once we
understand the underlying need.

## Code Style and Conventions

- **Go**: standard `gofmt` formatting. Run `go fmt ./...` before
  committing. Follow the conventions already present in the codebase
  (`internal/fractalog` for logging, package layout under `internal/`,
  test naming, etc.).
- **Python (strategies)**: `ruff` and `black` for formatting/linting.
  Strategy code follows the contracts and patterns documented in the
  [strategies docs](https://fracta.quasarops.com/docs/strategies/overview).
- **Commit messages**: `<area>: <imperative summary>` (e.g. `cli: ...`,
  `mcpcatalog: ...`, `docs: ...`). Area is the package name or a recognized
  short tag. Detailed body if the change is non-trivial. See the
  [Pull requests section in the docs](https://fracta.quasarops.com/docs/contributing/overview#pull-requests)
  for the full convention and examples.
- **Tests**: new functionality should come with tests where reasonable.

## Development Environment

See the development docs at
<https://fracta.quasarops.com/docs/development/overview> for setup,
build, and testing instructions.

## Code of Conduct

Be respectful, be constructive, assume good faith. We don't have a
formal Code of Conduct file yet; the rule of thumb is "would you say
this to a colleague face-to-face?" If you encounter behavior from
anyone (including a maintainer) that doesn't fit that bar, please
reach out: <diego.perez@quasarops.com>.

## Questions

For licensing questions (including whether your intended use of Fracta
requires a commercial license), see
[COMMERCIAL.md](./COMMERCIAL.md) or contact Diego Perez
(<diego.perez@quasarops.com>,
<https://www.linkedin.com/in/diegope/>).

For everything else, open an issue or a discussion on GitHub.
