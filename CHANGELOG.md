# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.6.3] - 2026-11-16

### Added
- **SSH Shadow Networking**: Added SSH port-forward shadows for air-gapped HPC, optional forwarding through the compute node's SSH server, reverse tunnels, and access from offloaded pods to cluster endpoints (#550, #577, #578, #580)
- **Init Containers on the Cluster**: Support running init containers on the Kubernetes cluster before submitting the offloaded pod (#583)
- **Virtual Node Capacity**: Virtual nodes now advertise ephemeral storage (#585)
- **Server Version API**: Exposed the interLink server version through the API (#552)

### Changed
- **Shadow Networking Stability**: Stabilized shadow pod networking lifecycle, added optional TLS ingress, and reduced wstunnel memory use (#538, #542)
- **Remote Pod Deletion**: Retry failed remote deletion requests before removing pods from the Virtual Kubelet cache (#556)
- **Endpoint Error Reporting**: Include the failing endpoint in non-OK response errors (#582)
- **Tracing Outcomes**: Record span status and exit codes so call outcomes are observable in traces (#575)

### Fixed
- **Shadow Pod Lifecycle**: Prevent stale pod IPs and preserve shadows when pods are recreated with the same name; avoid unreachable exposed ports during wstunnel termination (#563, #571, #584)
- **Kubernetes Field Resolution**: Resolve all supported Downward API environment field references and resource field references as the kubelet does (#594, #605)
- **Projected Secrets and ConfigMaps**: Honor optional volume sources, recreate replicated Secrets when their source type changes, and request the projected service account token audience (#590, #598, #603)
- **Virtual Kubelet Connections**: Drop idle API connections before the interLink API server closes them (#596)

### Documentation
- Added SSH tunnel configuration and reverse tunnel guides, including node targets, socket ports, and host key checks (#551, #578, #581)
- Added guides for running vLLM and LiteLLM on offloaded GPUs (#561)
- Updated local Minikube setup documentation (#537)

## [0.6.2] - 2026-07-10

### Added
- **Per-Pod Mesh Opt-Out**: Added the `interlink.eu/mesh-network: disabled` annotation to disable mesh networking for individual pods (#491, #522)
- **Downward API Volumes**: Added Downward API `fieldRef` support for pod metadata files (#530)
- **Profiling**: Added pprof profiling support to interLink, Virtual Kubelet, and ssh-tunnel components (#534)
- **Dynamic Node Resources**: Update node resource capacity and taints from plugin ping responses (#516)
- **End-to-End Tests**: Added a K3s-based GitHub Actions end-to-end test suite (#514)

### Changed
- **Status Update Synchronization**: Consolidated status update loops and added locking to protect pod state (#490)

### Fixed
- **Projected Volumes**: Correctly handle projected ConfigMap sources without `items` and prevent empty projected volumes (#524, #531)
- **Log Forwarding**: Validate log forwarding input and fix localhost URL handling (#515)
- **HTTP Unix Sockets**: Allow `http+unix` URLs in the SSRF check (#521)

## [0.6.1] - 2026-04-09

### Added
- **Selective Offload Control**: Added pod annotations to selectively skip container offload (#488)
- **Expanded Versioned Documentation**: Added 0.6.x versioned documentation, including mesh networking and certificate management guides (#483, #486)

### Changed
- **Accelerator Configuration**: Changed `Accelerator.Available` from an integer to a Kubernetes quantity string (#492)
- **Virtual Kubelet Configuration**: Improved Kubernetes client setup and added support to disable CSR creation when certificate automation is not desired (#475, #507)
- **Release Assets Alignment**: Updated bundled Slurm plugin and Helm chart references for the 0.6.1 release

### Fixed
- **Pod Lifecycle Readiness**: Containers now start in `Pending`, pod readiness at creation is handled correctly, and pods transition to `Ready` only after container readiness is observed (#476, #477, #478)
- **Plugin Error Reporting**: Plugin submission errors are now propagated to pod status and Virtual Kubelet logs for easier troubleshooting (#501)

## [0.6.0] - 2025-12-06

### Added
- **WireGuard Integration**: Full mesh connectivity support for enhanced networking capabilities (#468)
- **CSR-based SSL Certificate Management**: Automated certificate lifecycle management for Virtual Kubelet using Kubernetes Certificate Signing Requests (#448)
- **Wstunnel Service Exposition**: Automatic service exposition and port forwarding capabilities (#436, #435)
- **Comprehensive Unit Tests**: Added extensive unit test coverage for core packages improving code reliability (#457)
- **Downward API Configuration**: New `skipdownwardapiresolution` configuration option to enable scheduling pods with Downward API (#440)
- **CSR Cleanup**: Automatic cleanup of old Certificate Signing Requests in certificate manager (#456)

### Changed
- **Enhanced Pod Status Behavior**: Improved pod status tracking and probe handling for better reliability (#472)

### Fixed
- **Pod Failure Handling**: Pods now correctly transition to Failed state on creation errors with detailed error messages (#471)
- **Certificate and Deletion Fixes**: Resolved CSR certificate retrieval and pod deletion JSON parsing errors (#466)
- **Wstunnel IPv4 Support**: Fixed IPv4 connectivity issues in wstunnel (#445)
- **Resource Cleanup**: Fixed wstunnel resource cleanup inconsistency preventing resource leaks (#442)
- **Informer Startup**: Corrected secret and configmap informer initialization issues (#439)

### Documentation
- Enhanced code documentation across InterLink and Virtual Kubelet packages (#441)
- Updated configuration examples with corrected interlink configs (#470)
- Documentation updates for 0.5.1 release with improved in-cluster setup instructions (#433)

## [0.5.0] - 2025-06-09

### Added
- Comprehensive OpenAPI specification for interLink API
- Async status update rework with VPN and job script feature preview
- Claude AI integration for development assistance
- Enhanced mTLS integration documentation
- SSH tunnel and SystemD integration documentation

### Changed
- Updated project homepage with improved design and content
- Enhanced development workflow with Claude AI assistance

### Fixed
- OAuth2 audience specification issue (#413)
- Installation script issues (#415)
- golangci-lint compliance issues
- Documentation links for versioned guides
- Plugin development guide links and references

### Documentation
- Added comprehensive CLAUDE.md for AI-assisted development
- Enhanced mTLS integration guides
- Improved SSH tunnel setup documentation
- Updated SystemD service configuration guides
- Added versioned documentation for 0.4.x release series
- Fixed broken documentation links across version guides
- Updated plugin development guide with correct references

## [0.4.2-pre1] - 2025-05-16

Previous release from interlink-hq/interLink repository.

### Added
- Core interLink functionality
- Virtual Kubelet integration
- Plugin system architecture
- SSH tunneling support
- Basic observability features

---

## Release Notes

### Upgrading

#### From 0.5.0 to 0.6.0

1. **WireGuard Support**: If you need full mesh connectivity between nodes, configure WireGuard integration according to the documentation
2. **Certificate Management**: The new CSR-based SSL certificate management is automatic; ensure your cluster has appropriate RBAC permissions for CSR operations
3. **Configuration Options**: Review the new `skipdownwardapiresolution` option if you need to schedule pods with Downward API
4. **Wstunnel**: New automatic service exposition capabilities are available for improved networking

#### From 0.4.2-pre1 to 0.5.0+

1. **API Changes**: Review the new OpenAPI specification for any API updates
2. **Configuration**: Check for any new configuration options in async status handling
3. **Documentation**: Refer to updated mTLS and SSH tunnel documentation for deployment

### Breaking Changes

None identified in 0.6.0 release.

---

For more information about specific changes, see the [project documentation](docs/) or visit our [GitHub repository](https://github.com/interTwin-eu/interLink).
