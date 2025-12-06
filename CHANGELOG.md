# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
