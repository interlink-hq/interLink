"""
 This script should be executed by end-user while creating a pod
"""
import pyunicore.credentials as uc_credentials
import pyunicore.client as uc_client

#site name
base_url = "https://zam2125.zam.kfa-juelich.de:9112/HDFML/rest/core"

# Configure your Judoor username and password below
credential = uc_credentials.UsernamePassword("<USERNAME>", "<PASSWORD>")

client = uc_client.Client(credential, base_url)
my_auth_token = client.issue_auth_token(lifetime = 172800, renewable = False, limited = True)
print("Token: ",my_auth_token)