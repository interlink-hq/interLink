
```bash
docker run --rm -it \
    -v $(pwd)/engine.toml:/etc/dagger/engine.toml \
    --privileged \
    -p 1234:1234 \
    registry.dagger.io/engine:v0.8.1
```

```bash
_EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://$container_name
```
