# InterLink Project Governance

This document defines the governance structure and processes for the interLink
project, a Cloud Native Computing Foundation (CNCF) Sandbox project that
provides abstraction for executing Kubernetes pods on remote resources capable
of managing container execution lifecycles.

## Table of Contents

- [Values and Principles](#values-and-principles)
- [Project Scope](#project-scope)
- [Roles and Responsibilities](#roles-and-responsibilities)
- [Decision Making Process](#decision-making-process)
- [Leadership Selection](#leadership-selection)
- [Conflict Resolution](#conflict-resolution)
- [Communication](#communication)
- [Governance Changes](#governance-changes)
- [Code of Conduct](#code-of-conduct)

## Values and Principles

The interLink project operates under the following core principles:

- **Openness**: All project activities occur in the open. Design discussions,
  development, and decision-making processes are transparent and accessible to
  the community.
- **Technical Excellence**: Decisions are made based on technical merit,
  performance, security, and alignment with project goals.
- **Collaboration**: We foster an inclusive environment where contributors from
  research institutions, industry, and individual developers can collaborate
  effectively.
- **Innovation**: We encourage experimentation and innovation in bridging
  Kubernetes and HPC workloads while maintaining stability.
- **Vendor Neutrality**: The project remains neutral with respect to any single
  vendor or organization while leveraging institutional expertise.
- **Sustainability**: We prioritize long-term project health through
  maintainable code, clear documentation, and community growth.

## Project Scope

interLink aims to provide a seamless bridge between Kubernetes container
orchestration and heterogeneous computing resources, particularly
High-Performance Computing (HPC) systems. The project scope includes:

### In Scope

- Virtual Kubelet provider implementation for remote resource management
- Plugin architecture supporting multiple backend systems (SLURM, HTCondor,
  Docker, etc.)
- REST API for container lifecycle management
- Integration tools and documentation for HPC centers or other backends
- Security frameworks for multi-tenant environments
- Helm charts and deployment tools

### Out of Scope

- Direct replacement of existing HPC schedulers
- Kubernetes core functionality modifications
- Proprietary vendor-specific implementations (unless contributed as open
  source)

## Roles and Responsibilities

### Contributors

Anyone who contributes to the project through code, documentation, issue
reporting, community support, or other means. No formal approval required.

**Responsibilities:**

- Follow the project's Code of Conduct
- Adhere to contribution guidelines
- Respect maintainer decisions and project direction

### Reviewers

Reviewers are active contributors who help the project's issue and PR review
process.

**Qualifications:**

- Demonstrated technical expertise in the project domain
- Understanding of project architecture and goals
- Commitment to project values and community health
- Available to respond to issues and reviews within reasonable timeframes

### Maintainers

Maintainers are active reviewers with write access to the repository who help
guide the project's technical direction and make routine decisions.

**Qualifications:**

- Active reviewers since at least 3 months

**Responsibilities:**

- Participate in technical discussions and decision-making
- Maintain code quality and project standards
- Mentor new contributors
- Manage project roadmap and release planning
- Interface with CNCF on project matters

**Current Maintainers** (as of current governance adoption):

- Diego Ciangottini (@dciangot)
- Daniele Spiga (@spigad)
- Giulio Bianchini (@Bianco95)
- Additional contributors listed in [MAINTAINERS.md](MAINTAINERS.md)

## Decision Making Process

The project uses a **lazy consensus** model for most decisions, with escalation
paths for complex issues.

### Routine Decisions

For day-to-day technical decisions (bug fixes, minor features, documentation
updates):

- Any maintainer may approve and merge after appropriate review
- Minimum 24-hour comment period for non-trivial changes
- At least one approval from a maintainer required

### Significant Decisions

For major features, architectural changes, or policy updates:

- Proposal must be submitted as an issue or RFC document
- Minimum 5-day comment period
- Requires approval from at least 2 maintainers
- No objections from other maintainers (lazy consensus)

### Major Decisions

For significant architectural changes, security policies, or governance
modifications:

- Formal proposal required with design document
- Minimum 14-day comment period
- Supermajority (2/3) approval from active maintainers

## Leadership Selection

### Maintainer Selection

New maintainers are nominated by existing maintainers based on the
qualifications listed above.

**Process:**

1. Current maintainer nominates candidate via GitHub issue
2. Nomination includes justification and candidate's consent
3. 14-day discussion period for community input
4. Decision by lazy consensus among current maintainers

### Maintainer Emeritus

Maintainers who become inactive or wish to step down:

- May voluntarily transition to emeritus status
- Automatically moved to emeritus after 12 months of inactivity
- Emeritus maintainers retain recognition but lose voting privileges
- May return to active status upon request and maintainer approval

## Conflict Resolution

### Technical Disputes

1. **Discussion**: Open technical discussion in appropriate forum (GitHub issue,
   mailing list)
2. **Maintainer Review**: If no consensus, maintainers attempt to resolve
3. **CNCF Escalation**: Unresolved conflicts may be escalated to CNCF TOC

### Code of Conduct Violations

1. **Reporting**: Use confidential reporting mechanisms defined in Code of
   Conduct
2. **Investigation**: maintainers designated committee investigates
3. **Resolution**: Appropriate actions taken based on severity and impact
4. **Appeals**: Appeals process available for disputed decisions

## Communication

### Primary Channels

- **GitHub Issues**: Technical discussions, bug reports, feature requests
- **GitHub Discussions**: Community questions, announcements, general topics
- **Slack**: Real-time communication (CNCF Slack #interlink channel)
- **Mailing Lists**: Project announcements and governance discussions
- **Monthly Meetings**: Community calls for updates and discussions

### Decision Visibility

- All significant decisions documented in GitHub
- Meeting notes published for community calls
- Governance changes announced across all channels

## Governance Changes

This governance document may be modified through the following process:

1. **Proposal**: Changes proposed via GitHub issue with rationale
2. **Community Input**: Minimum 21-day comment period
3. **Maintainer Review**: Discussion among maintainers
4. **Community Notification**: Changes announced across all communication
   channels

### Major Governance Changes

Changes to fundamental structure (roles, voting processes, etc.) require:

- Extended 30-day comment period
- Approval from 2/3 of all active maintainers
- CNCF TOC notification

## Code of Conduct

The InterLink project adheres to the
[CNCF Code of Conduct](https://github.com/cncf/foundation/blob/master/code-of-conduct.md).
All participants are expected to uphold these standards in all project-related
activities.

### Enforcement

Code of Conduct violations should be reported to:

- CNCF Code of Conduct Committee: <conduct@cncf.io>

---

## Implementation Notes

This governance document takes effect by December 2025.

**Document History:**

- v0.1: Initial governance framework (June 2025)

**Next Review:** December 2025

For questions about this governance structure, please open an issue or contact
the maintainers cncf-interlink-maintainers<at>lists.cncf.io
