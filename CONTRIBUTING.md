# Contributing to MicroCeph

We are an open source project and welcome community contributions, suggestions,
fixes and constructive feedback.

If you'd like to contribute, you will first need to sign the Canonical
contributor agreement. This is the easiest way for you to give us permission to
use your contributions. In effect, you’re giving us a licence, but you still
own the copyright — so you retain the right to modify your code and use it in
other projects.

The agreement can be found, and signed, here:
https://ubuntu.com/legal/contributors


## Contributor guidelines

Contributors can help us by observing the following guidelines:

- Commit messages should be well structured.
- Commit emails should not include non-ASCII characters.
- Commits must be signed off
- Try to open several smaller PRs, rather than one large PR.
- Try not to mix potentially controversial and trivial changes together.
  (Proposing trivial changes separately makes landing them easier and 
  makes reviewing controversial changes simpler)
- Try to write tests to cover the contributed changes

## Coding conventions

Contributions should follow the [Go Style Guide][styleguide].

In addition, contributions should follow the rules below

### Imports

Import statements are grouped into 3 sections: standard library, 3rd party libraries, MicroCeph imports.

The tool "go fmt" can be used to ensure each group is alphabetically sorted. 

For instance:

    import (
        "fmt"
        "os"

        "github.com/pborman/uuid"

        "github.com/canonical/microceph/microceph/common"
        "github.com/canonical/microceph/microceph/database"
    )

### Avoid one line assign/test

If constructs with assign and test on the same line should be avoided for maintainability. 

Use:

	err := doStuff()
	if err != nil {
		return err
	}
    
Instead of:

	if err := doStuff(); err != nil {
		return err
	}


## Doc Comments

"Doc comments" are comments that appear immediately before top-level
package, const, func, type, and var declarations with no intervening
newlines. Every exported (capitalized) name should have a doc comment.

## Pull request guidelines

Contributions are submitted through a [pull request][pull-request] created from
a [fork][fork] of the `MicroCeph` repository (under your GitHub account).

GitHub's documentation outlines the [process][github-pr], but for a more
concise and informative version try [this GitHub gist][pr-gist]. 

### Linear git history

We strive to keep a [linear git history][linear-git]. This makes it easier to
inspect the history, keep related commits next to each other, and make tools
like [git bisect][git-bisect] work intuitively.

### Pull request structure

Squash commits in a PR. 

For non-trivial PRs squash into separate commits, creating commits for:

- API changes
- Documentation (doc: Update XYZ for files in doc/)
- MicroCeph CLI (microceph/client)
- MicroCeph daemon (microcephd)
- Tests (tests/ folder)
- CI changes (.github)

This makes reviewing large PRs easier as it allows to zoom in on specific areas.



[1]: http://www.ubuntu.com/legal/contributors
[styleguide]: https://google.github.io/styleguide/go/guide
[pull-request]: https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/proposing-changes-to-your-work-with-pull-requests/creating-a-pull-request-from-a-fork
[fork]: https://docs.github.com/en/get-started/quickstart/fork-a-repo#forking-a-repository
[github-pr]: https://docs.github.com/en/github/collaborating-with-pull-requests
[pr-gist]: https://gist.github.com/Chaser324/ce0505fbed06b947d962
[linear-git]: https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/defining-the-mergeability-of-pull-requests/about-protected-branches#require-linear-history
[git-bisect]: https://git-scm.com/docs/git-bisect
