# Contributing to Obsidian Sentinel WAF

>**Note**: We take Obsidian Sentinel's security and the trust of our community seriously. If
> you believe you have found a security issue in Obsidian Sentinel or any of its
> components, please responsibly disclose by contacting us. See
> [SECURITY.md](SECURITY.md)
> for details.


We are striving to support an open community for the Obsidian Sentinel WAF Project. We support
our contributors, please don't feel afraid or unsure of submitting feedback or
asking a question.

## Community

* Get in touch via [GitHub Discussions](https://github.com/Abhishek-yadav04/Obsidian/discussions)
* Report issues on [GitHub Issues](https://github.com/Abhishek-yadav04/Obsidian/issues)
* Join the cybersecurity community discussions

## Contributions

* Provide feedback and report potential bugs
* Suggest enhancements to the project
* Perform tests and increase test coverage
* Fix a [Bug](https://github.com/Abhishek-yadav04/Obsidian/issues?q=is%3Aopen+is%3Aissue+label%3Abug) or implement an [Enhancement](https://github.com/Abhishek-yadav04/Obsidian/issues?q=is%3Aopen+is%3Aissue+label%3Aenhancement)
* Improve our Documentation
* Add new WAF rules or security features

## Reporting an Issue

* Security related issues are covered by the [Security Policy](SECURITY.md)
* Make sure you test against the latest version, it's possible the issue was
  already fixed. However if you are on an older version and feel the
  issue is critical, please let us know.
* Check existing [Issues](https://github.com/Abhishek-yadav04/Obsidian/issues) (open and closed) to ensure it was not already reported.
* Provide a detailed description and a reproducible test case in a new [Issue](https://github.com/Abhishek-yadav04/Obsidian/issues/new).
  Be sure to include as much relevant information as possible, a **code sample** or an **test case** demonstrating the fault helps us to reproduce your problem.

## Patches

Did you write a patch that fixes a bug?

* Open a new GitHub pull request which includes your changes.
* Please include a description which clearly describes the change. Include the relevant issue number if applicable.
* You may consider installing a pre-commit hook to automatically run required checks with `go run mage.go precommit`

## Enhancements

Do you intend to add a new feature or change an existing one?
* Suggest your change in the [GitHub Discussions](https://github.com/Abhishek-yadav04/Obsidian/discussions/categories/ideas) and start writing code.
* Do not open an issue on GitHub until you have collected positive feedback about the change. GitHub issues are primarily intended for bug reports and fixes.
* There are many TODOs, functionalities, fixes, bug reports, and any help you can provide. Just send your pull request.

Run from the repository root:
```sh
egrep -Rin "TODO|FIXME" -R --exclude-dir=vendor *
```

## Questions

Do you have questions about the source code? Ask any question about how to use Obsidian Sentinel in the community [Discussions](https://github.com/Abhishek-yadav04/Obsidian/discussions/categories/q-a).

## Testing

Obsidian Sentinel uses Go's built-in test tool. Examples (run from the repository root):

- `go test -v` or `go run mage.go test`
- `go test -v -race ` use to enable the built-in data race detector
- `go test -run TestDefaultWriters -v ./loggers` run all tests loggers package with name substring `TestDefaultWriters`

- `go run mage.go lint` run code style checks
- `go run mage.go check` run tests and code style checks

- `go run mage.go precommit` install the pre-commit git hook

### Development Setup

**Prerequisites:**
- Go 1.23+ (required for all development and CI)
- PostgreSQL 12+ (optional, for testing database features)
- Redis 6+ (optional, for testing caching and rate limiting)

**Quick Start:**
```bash
# Clone and setup
git clone https://github.com/Abhishek-yadav04/Obsidian.git
cd obsidian

# Install dependencies
go mod tidy

# Run directly (recommended for development)
go run ./cmd/obsidian

# Or build and run
go build -o obsidian ./cmd/obsidian
./obsidian

# Run tests
go test ./...
```

**Environment Variables for Development:**
```bash
export OBSIDIAN_JWT_SECRET="your-development-secret-at-least-32-chars"
export DATABASE_URL="postgres://user:pass@localhost/obsidian"  # Optional
export REDIS_URL="redis://localhost:6379"                    # Optional
```

_________________

The Coraza project is a community effort. We encourage you to pitch in and join the team!

Thanks! :heart: :heart: :heart:

Coraza Team
