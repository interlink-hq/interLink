from os import environ
from fastapi.openapi.utils import get_openapi
from main import app
import json
import os


with open('openapi.json', 'w') as f:
    json.dump(
        get_openapi(
            title='interLink sidecar',
            version=os.environ.get("VERSION", 'v0.0.0'),
            openapi_version=app.openapi_version,
            description='openapi spec for interLink apis <-> provider sidecar communication',
            routes=app.routes,
        ), 
        f
    )
