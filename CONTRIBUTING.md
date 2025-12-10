# Contributing to ISO

Thank you for your interest in contributing to ISO!

## Project Scope

ISO is an internal tool built by the [Miren](https://miren.dev) team to solve our own development environment needs. We're sharing it publicly to allow others to contribute to the [Miren Runtime](https://github.com/mirendev/runtime) (which uses ISO for development and tests), and in case others find it useful. Our development priorities are focused on our own use cases.

We're happy to accept contributions that align with our goals, but we may decline PRs that add features we don't need or that would increase our maintenance burden.

For context on our longer-term vision for development environments, see [mirendev/roadmap#8](https://github.com/mirendev/roadmap/issues/8). ISO is a pragmatic tool we built along the way—not the full realization of that vision.

## How to Contribute

### Reporting Issues

If you find a bug or have a question, feel free to [open an issue](https://github.com/mirendev/iso/issues). We'll do our best to respond, though our capacity may be limited.

### Pull Requests

Before starting work on a significant change, please open an issue to discuss it first. This helps avoid wasted effort on changes we might not be able to accept.

For small bug fixes or documentation improvements, feel free to submit a PR directly.

### Development Setup

1. Clone the repository
2. Install [quake](https://miren.dev/quake): `go install miren.dev/quake@latest`
3. Build: `quake build`
4. Run tests with the testdata directory

## Code of Conduct

Please review our [Code of Conduct](CODE_OF_CONDUCT.md) before participating.

## License

By contributing to ISO, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
