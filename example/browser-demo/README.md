# Browser-backed text demo

An isolated interLink plugin proof of concept: consenting visitors count words in
**public text** in a browser Web Worker. The Go gateway queues bounded chunks,
merges counts and reports container-shaped status and snapshot logs to interLink.
It never pulls images, executes commands, or implements a container runtime.
Existing plugins are unchanged. No new dependencies or frontend build are needed.

## Run locally

From the repository root, using the Go version specified in `go.mod`:

```sh
go run ./example/browser-demo
```

Open `http://127.0.0.1:8080` and click **I consent — join as a worker**.
Nothing connects or computes before that click. **Stop participating** closes
the socket and terminates the worker; closing the page also withdraws.
Idle/expired connections require another explicit Join; there is no auto-rejoin.

The **private** plugin API listens on `127.0.0.1:4000`; the participant page and
WebSocket use a separate listener, `127.0.0.1:8080`. Try the plugin without a
cluster (keep an opted-in page open):

```sh
curl -sS http://127.0.0.1:4000/create -H 'Content-Type: application/json' -d '{
  "pod": {
    "metadata": {
      "uid": "9f60656e-6a25-4ad1-8b15-56ded9771090",
      "name": "browser-text-demo", "namespace": "default",
      "annotations": {
        "browser-demo.interlink.eu/public": "true",
        "browser-demo.interlink.eu/public-text": "Hello browser! Hello interLink!"
      }
    },
    "spec": {"containers": [{"name": "words", "image": "browser-demo:ignored"}]}
  },
  "container": []
}'

curl -sS -X GET http://127.0.0.1:4000/status -H 'Content-Type: application/json' \
  -d '[{"metadata":{"uid":"9f60656e-6a25-4ad1-8b15-56ded9771090"}}]'

curl -sS -X GET http://127.0.0.1:4000/getLogs -H 'Content-Type: application/json' \
  -d '{"PodUID":"9f60656e-6a25-4ad1-8b15-56ded9771090","ContainerName":"words","Opts":{}}'

curl -sS http://127.0.0.1:4000/delete -H 'Content-Type: application/json' \
  -d '{"metadata":{"uid":"9f60656e-6a25-4ad1-8b15-56ded9771090"}}'
```

Final logs are alphabetically sorted word counts. Before completion logs show
state, completed/total chunks and connected workers. `/healthz` is available on
the private API. `GET /status` with `[]` returns `[]`, including interLink's ping.

## Use with interLink

Run this instead of the normal provider sidecar and set these fields in your
existing interLink configuration (all other settings stay as appropriate for
your deployment):

```yaml
SidecarURL: http://127.0.0.1
SidecarPort: "4000"
```

These addresses assume interLink and the demo share a host/network namespace.
For separate containers, bind `-api 0.0.0.0:4000` **only on a private network**
and set `SidecarURL` to `http://<gateway-private-host>` with `SidecarPort: "4000"`.
Do not use the participant port as the sidecar port.
Do not configure a job script builder/template for this demo.

Replace `nodeName` in `pod.yaml` with your interLink virtual node name, then:

```sh
kubectl apply -f example/browser-demo/pod.yaml
kubectl get pod browser-text-demo -w
kubectl logs browser-text-demo
kubectl delete pod browser-text-demo
```

Use snapshot `kubectl logs`, not `-f`. The pod must have exactly one regular
container and no init containers. Commands, images, env and volumes are ignored.
The text source is **only** the `browser-demo.interlink.eu/public-text`
annotation; `browser-demo.interlink.eu/public: "true"` is mandatory as the
submitter's declaration that the text may be disclosed to any participant.
Do not put confidential text in that annotation. Consent is not a secret scanner.

## Message flow and deterministic operation

```text
interLink -> private POST /create {pod, container, ...}
          <- {PodUID, PodJID} immediately (in-memory queue)
visitor clicks Join -> public /ws?consent=yes
gateway -> {type:"task", attempt:"opaque-unique-id", text:"normalized words"}
page -> Web Worker -> {type:"result", attempt:"...", counts:{"word":2}}
gateway validates lease/result shape, merges each accepted chunk once
interLink -> GET /status [pods] -> waiting/running/terminated container state
interLink -> GET /getLogs {PodUID, ContainerName, Opts} -> progress / sorted counts
interLink -> POST /delete pod -> forget job; close its active worker connections
```

The gateway extracts ASCII `[A-Za-z0-9]+` tokens, lowercases them, and packs
up to 256 whole words per chunk. Punctuation and non-ASCII characters separate
words; this is intentionally not natural-language Unicode tokenization.
Browsers actually compute the per-chunk frequency map, which the gateway merges
by addition. Chunk completion order does not change the sorted final output.
Only normalized text and an opaque attempt ID reach browsers, **never** a pod
UID/name, env, Secrets, ConfigMaps, projected tokens or job scripts.

## Bounds, public hosting and limitations

- 64 KiB UTF-8 text per job, 64 ASCII characters per word, 256 KiB HTTP requests.
- At most 32 retained jobs and 64 connected workers; one chunk per worker.
- 10-second leases, three attempts per chunk, two-minute job deadline (including
  waiting for visitors), five-minute idle worker lifetime, ten-minute terminal
  job retention. Expiry is applied lazily on API calls and worker ticks.
- Disconnects/invalid results/lease expiry release work for reassignment. Each
  assignment has a new ID, so late/duplicate replies cannot add counts twice.
  Deleted/timed-out jobs cannot accept results. The supplied UI stops computation
  after nine seconds or whenever the connection closes.
- Create retries with the same UID return the existing job ID until deletion or
  expiry. Memory and results are lost on restart; logs are snapshots, supporting
  `Tail` and `Bytes`, not follow/previous/timestamps/since filters.
- Browser counts are **untrusted**: the gateway checks permitted words, positive
  counts and total word count, but a malicious participant can redistribute
  counts. No redundant verification, authentication, fairness or durable queue.
  FIFO scheduling and small chunks suit a demo, not production workloads.

For an audience, bind `-web 0.0.0.0:8080` and place **only that listener** behind
a trusted HTTPS reverse proxy with WebSocket support, connection/rate limits,
and upgrade idle timeouts long enough for participation. Browsers use WSS when
the page uses HTTPS. The default WebSocket origin check requires same-origin
connections; preserve the public Host/Origin when proxying.
Never proxy/expose port 4000: its unauthenticated server-to-server API can
create/delete jobs and read results. It rejects browser Origin headers, but
that is not authentication; use network isolation (and authenticated transport
if crossing hosts). Participants should trust the organizer and gateway.

## WASM integration seam

`web/worker.js` contains `async processText(text)`. Replace its body with a
**fixed, trusted** WASM module, loaded and instantiated inside the worker, which
accepts UTF-8 text and returns the same plain word-to-integer object. Keep the
token definition, bounds and result protocol unchanged. Serve the module as a
static asset, add it to the embedded `web/` files, and explicitly allow
`'wasm-unsafe-eval'` in the worker's CSP if required by your target browsers.
Do not accept modules/code from job requests. JS is used here to avoid adding
a compiler/toolchain for a tiny operation; no WASM speedup is claimed.

This demonstrates **offloaded, sandboxed text computation**, not Linux containers,
arbitrary images, shell execution, networking workloads or privileged pod access.

## Validate

```sh
go test -race ./example/browser-demo
go build -o /tmp/interlink-browser-demo ./example/browser-demo
```

Tests cover plugin wire formats, deterministic aggregation, bounds, payload
isolation, lease retries, duplicate/late/invalid results, expiry, and actual
WebSocket disconnect/reassignment.
