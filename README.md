# Source controller

> **⚠️ FORK DISCLAIMER**: The `develop` branch of this repository contains custom enhancements and architectural changes that differ from the upstream Flux source-controller. These modifications add functionality beyond the standard Flux toolkit. For the original upstream behavior, use the `main` branch which tracks the official Flux source-controller repository.

[![CII Best Practices](https://bestpractices.coreinfrastructure.org/projects/4786/badge)](https://bestpractices.coreinfrastructure.org/projects/4786)
[![e2e](https://github.com/fluxcd/source-controller/workflows/e2e/badge.svg)](https://github.com/fluxcd/source-controller/actions)
[![report](https://goreportcard.com/badge/github.com/fluxcd/source-controller)](https://goreportcard.com/report/github.com/fluxcd/source-controller)
[![license](https://img.shields.io/github/license/fluxcd/source-controller.svg)](https://github.com/fluxcd/source-controller/blob/main/LICENSE)
[![release](https://img.shields.io/github/release/fluxcd/source-controller/all.svg)](https://github.com/fluxcd/source-controller/releases)

The source-controller is a Kubernetes operator, specialised in artifacts acquisition
from external sources such as Git, OCI, Helm repositories and S3-compatible buckets.
The source-controller implements the
[source.toolkit.fluxcd.io](docs/spec/README.md) API
and is a core component of the [GitOps toolkit](https://fluxcd.io/flux/components/).

## Custom Features (develop branch only)

This fork includes the following architectural enhancements beyond upstream:

- **Enhanced Artifact Serving**: Improved handling of URL prefixes in `--storage-adv-addr` configuration
- **Bug Fixes**: Resolution of double HTTP prefix issues in artifact URL generation
- **Code Quality**: Updated to Go 1.25.1 with modern linting standards (golangci-lint v2.5.0)
- **Improved Testing**: Enhanced test coverage and mock implementations

## Known Issues

### Double HTTP Prefix Bug in --storage-adv-addr

**Bug**: When using `--storage-adv-addr` with an `http://` or `https://` prefix, the source-controller blindly prepends another `http://` prefix to artifact URLs, resulting in malformed URLs like `http://http://hostname:port/path`.

**Example**: 
- Config: `--storage-adv-addr=http://source-controller.flux-system.svc.cluster.local:9090`
- Result: `http://http://source-controller.flux-system.svc.cluster.local:9090/helmchart/...`

**Workaround**: Omit the protocol prefix:
- Config: `--storage-adv-addr=source-controller.flux-system.svc.cluster.local:9090`
- Result: `http://source-controller.flux-system.svc.cluster.local:9090/helmchart/...`

**Fix Needed**: The source-controller should detect existing `http://` or `https://` prefixes in `--storage-adv-addr` and avoid double-prefixing.

![overview](docs/diagrams/source-controller-overview.png)

## APIs

| Kind                                               | API Version                   |
|----------------------------------------------------|-------------------------------|
| [GitRepository](docs/spec/v1/gitrepositories.md)   | `source.toolkit.fluxcd.io/v1` |
| [OCIRepository](docs/spec/v1/ocirepositories.md)   | `source.toolkit.fluxcd.io/v1` |
| [HelmRepository](docs/spec/v1/helmrepositories.md) | `source.toolkit.fluxcd.io/v1` |
| [HelmChart](docs/spec/v1/helmcharts.md)            | `source.toolkit.fluxcd.io/v1` |
| [Bucket](docs/spec/v1/buckets.md)                  | `source.toolkit.fluxcd.io/v1` |

## Features

* authenticates to sources (SSH, user/password, API token, Workload Identity)
* validates source authenticity (PGP, Cosign, Notation)
* detects source changes based on update policies (semver)
* fetches resources on-demand and on-a-schedule
* packages the fetched resources into a well-known format (tar.gz, yaml)
* makes the artifacts addressable by their source identifier (sha, version, ts)
* makes the artifacts available in-cluster to interested 3rd parties
* notifies interested 3rd parties of source changes and availability (status conditions, events, hooks)
* reacts to Git, Helm and OCI artifacts push events (via [notification-controller](https://github.com/fluxcd/notification-controller))

## Guides

* [Get started with Flux](https://fluxcd.io/flux/get-started/)
* [Setup Webhook Receivers](https://fluxcd.io/flux/guides/webhook-receivers/)
* [Setup Notifications](https://fluxcd.io/flux/guides/notifications/)
* [How to build, publish and consume OCI Artifacts with Flux](https://fluxcd.io/flux/cheatsheets/oci-artifacts/)

## Roadmap

The roadmap for the Flux family of projects can be found at <https://fluxcd.io/roadmap/>.

## Contributing

This project is Apache 2.0 licensed and accepts contributions via GitHub pull requests.
To start contributing please see the [development guide](DEVELOPMENT.md).
