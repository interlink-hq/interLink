# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

When upgrading from 0.4.2-pre1:

1. **API Changes**: Review the new OpenAPI specification for any API updates
2. **Configuration**: Check for any new configuration options in async status handling
3. **Documentation**: Refer to updated mTLS and SSH tunnel documentation for deployment

### Breaking Changes

None identified in this release.

---

For more information about specific changes, see the [project documentation](docs/) or visit our [GitHub repository](https://github.com/interTwin-eu/interLink).