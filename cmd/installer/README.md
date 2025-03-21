# interLink Installer

The interLink installer is a command-line tool that simplifies the deployment of 
interLink components across different environments. It automates the generation 
of configuration files, deployment manifests, and installation scripts needed to 
set up interLink in various deployment scenarios.

## Overview

The interLink installer:

- Generates configuration files for interLink deployment
- Handles OAuth authentication setup
- Creates Helm chart values for Kubernetes deployment
- Generates installation scripts for remote interLink APIs
- Supports different deployment scenarios (Edge-node, In-cluster, Tunneled)

## Installation

The installer is built as part of the interLink project. To build it:

```bash
# From the root of the interLink repository
go build -o interlink-installer ./cmd/installer
```

## Usage

### Initialize a Configuration

Create a default configuration file with placeholder values:

```bash
./interlink-installer --init --config /path/to/config.yaml
```

This creates a configuration file with default values 
that you must edit to match your environment.

> It `--config` is not given, default location is `$HOME/.interlink.yaml

### Generate Deployment Manifests

After editing the configuration file, generate the deployment manifests:

```bash
./interlink-installer --config /path/to/config.yaml --output-dir /path/to/output
```

This will:

1. Read the configuration file
2. Handle OAuth authentication if needed
3. Generate Helm chart values at `/path/to/output/values.yaml`
4. Generate an installation script at `/path/to/output/interlink-remote.sh`

### Deploy interLink

After generating the manifests:

1. Deploy to Kubernetes:

   ```bash
   helm --debug upgrade --install --create-namespace -n <namespace> <node-name> \
     oci://ghcr.io/intertwin-eu/interlink-helm-chart/interlink \
     --values /path/to/output/values.yaml
   ```

2. Install on the remote server:

   ```bash
   # Copy the script to the remote server
   scp /path/to/output/interlink-remote.sh user@remote-server:~/
   
   # On the remote server
   chmod +x interlink-remote.sh
   ./interlink-remote.sh install
   ./interlink-remote.sh start
   ```

## Command-Line Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `$HOME/.interlink.yaml` | Path to the configuration file |
| `--output-dir` | `$HOME/.interlink/manifests` | Directory where deployment manifests will be stored |
| `--init` | `false` | Initialize a new configuration file with default values |

## Configuration File

The configuration file is in YAML format and contains the following sections:

### Virtual Kubelet Configuration

```yaml
kubelet_node_name: my-vk-node
kubernetes_namespace: interlink
node_limits:
  cpu: "10"
  memory: "256"
  pods: "10"
```

### interLink API Configuration

```yaml
interlink_ip: PUBLIC_IP_HERE
interlink_port: 8443
interlink_version: 0.3.3
insecure_http: true
```

### OAuth Configuration

```yaml
oauth:
  provider: oidc  # or github
  grant_type: authorization_code  # or client_credentials
  client_id: OIDC_CLIENT_ID_HERE
  client_secret: OIDC_CLIENT_SECRET_HERE
  scopes:
    - openid
    - email
    - offline_access
    - profile
  token_url: https://my_oidc_idp.com/token
  device_code_url: https://my_oidc_idp/auth/device
  issuer: https://my_oidc_idp.com/
  # For GitHub provider
  # github_user: username
```

## Deployment Scenarios

The installer supports all three deployment scenarios described in the interLink documentation:

1. **Edge-node**: Deploy interLink API and plugin on a dedicated edge node
2. **In-cluster**: Deploy all components inside the Kubernetes cluster
3. **Tunneled**: Deploy interLink API in the cluster and plugin remotely with a secure tunnel

*The specific scenario is determined by how you configure the interLink IP and 
port in the configuration file and where you run the installation script.*


## Template Files

The installer includes several embedded template files:

- `values.yaml`: Helm chart values for Kubernetes deployment
- `interlink-install.sh`: Installation script for remote interLink APIs
- `interlink.service`: SystemD service file for interLink
- `oauth2-proxy.service`: SystemD service file for OAuth2 proxy

These templates are processed with the configuration data to generate the final deployment files.
