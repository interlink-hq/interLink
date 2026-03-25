# Generating interLink OpenAPI Specifications

This directory contains the OpenAPI specifications for interLink's two HTTP APIs:

| File | Description |
|---|---|
| `interlink-openapi.json` | Virtual Kubelet → interLink API server communication |
| `plugin-openapi.json` | interLink API server → plugin (sidecar) communication |

Both specs are generated automatically from the Go type definitions in
[`pkg/interlink/types.go`](../../pkg/interlink/types.go) and
[`pkg/interlink/config.go`](../../pkg/interlink/config.go).

## Quick Start

From the repository root, run:

```bash
make openapi
```

This runs `go run cmd/openapi-gen/main.go` and overwrites both JSON files.

## Specifying a Version

Pass a `--version` flag to stamp a different version into the spec:

```bash
go run cmd/openapi-gen/main.go --version 0.6.0
```

The default version matches the current release (see `CHANGELOG.md`).

## How It Works

The generator in [`cmd/openapi-gen/main.go`](../../cmd/openapi-gen/main.go) uses
[`swaggest/openapi-go`](https://github.com/swaggest/openapi-go) to reflect over
the interLink Go types and produce OpenAPI 3.0 schemas.

### interLink server API (`interlink-openapi.json`)

Describes how the Virtual Kubelet talks to the interLink API server:

| Endpoint | Method | Request type | Response type |
|---|---|---|---|
| `/create` | POST | `PodCreateRequests` | `CreateStruct` |
| `/delete` | POST | `v1.Pod` | — |
| `/pinglink` | POST | — | — |
| `/status` | POST | `[]v1.Pod` | `[]PodStatus` |
| `/getLogs` | POST | `LogStruct` | `string` |

### Plugin API (`plugin-openapi.json`)

Describes how the interLink API server talks to a sidecar plugin:

| Endpoint | Method | Request type | Response type |
|---|---|---|---|
| `/create` | POST | `RetrievedPodData` | `CreateStruct` |
| `/delete` | POST | `v1.Pod` | — |
| `/status` | POST | `[]v1.Pod` | `[]PodStatus` |
| `/getLogs` | POST | `LogStruct` | `string` |

`RetrievedPodData` includes the `jobConfig` (`ScriptBuildConfig`) and `jobScript`
fields that allow interLink to pass a pre-built job script to the plugin.

## Keeping the Specs Up to Date

Whenever you modify any of the types used by these APIs, regenerate the specs:

1. Edit the relevant struct in `pkg/interlink/types.go` or `pkg/interlink/config.go`.
2. Run `make openapi`.
3. Commit both the changed source file and the updated JSON spec files together.

The Makefile target is:

```makefile
openapi:
	go run cmd/openapi-gen/main.go
```
