#! /bin/env python3
"""
Functions and scripts to sync OIDC identities on user accounts
"""

import os
import json
import time
import logging
import requests

if __name__ == '__main__':
    """
    sync OIDC identities on user accounts
    """
    try:
        iam_server = os.environ.get(
            "IAM_SERVER", "https://cms-auth.web.cern.ch/")
        iam_client_id = os.environ.get("IAM_CLIENT_ID")
        iam_client_secret = os.environ.get("IAM_CLIENT_SECRET")
        iam_refresh_token = os.environ.get("IAM_REFRESH_TOKEN")
        audience = os.environ.get("IAM_VK_AUD")
        output_file = os.environ.get("TOKEN_PATH", "/opt/interlink/token")
    except Exception as ex:
        print(ex)
        exit(1)

    token = None

    while True:
        try:
            request_data = {
                "grant_type": "refresh_token",
                "refresh_token": iam_refresh_token,
                "scope": "openid profile email address phone offline_access"
            }

            from requests.auth import HTTPBasicAuth
            auth = HTTPBasicAuth(iam_client_id, iam_client_secret)

            r = requests.post(iam_server+"token", data=request_data, auth=auth)
            response = json.loads(r.text)

            #print(iam_client_id, iam_client_secret, response)
            token = response['access_token']

            logging.info("Token retrieved")

            with open(output_file, "w") as text_file:
                text_file.write(token)

            logging.info(f"Token written in {output_file}")

        except Exception as e:
            logging.warn("ERROR oidc get token: {}".format(e))
        
        time.sleep(1000)
