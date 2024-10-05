import json
import os.path
from typing import List

from fastapi import FastAPI
from fastapi.openapi.utils import get_openapi
from fastapi.responses import PlainTextResponse

import interlink

app = FastAPI()


@app.post("/create")
async def create_pod(pod: List[interlink.Pod]) -> interlink.CreateStruct:
    raise NotImplementedError


@app.post("/delete")
async def delete_pod(pod: interlink.PodRequest) -> str:
    raise NotImplementedError


@app.get("/status")
async def status_pod(pods: List[interlink.PodRequest]) -> List[interlink.PodStatus]:
    raise NotImplementedError


@app.get("/getLogs", response_class=PlainTextResponse)
async def get_logs(req: interlink.LogRequest) -> bytes:
    raise NotImplementedError


openapi_schema = os.path.join(
    os.path.dirname(__file__), *["..", "docs", "openapi", "openapi.json"]
)

with open(openapi_schema, "w") as f:
    json.dump(
        get_openapi(
            title="interLink sidecar",
            version=os.environ.get("VERSION", "v0.0.0"),
            openapi_version=app.openapi()["openapi"],
            description="openapi spec for interLink apis <-> provider sidecar communication",
            routes=app.routes,
        ),
        f,
    )