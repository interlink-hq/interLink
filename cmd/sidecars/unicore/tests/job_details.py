"""
 This script should be executed by end-user to view unicore job details
"""
import pyunicore.credentials as uc_credentials
import pyunicore.client as uc_client
import json



base_url = "https://zam2125.zam.kfa-juelich.de:9112/HDFML/rest/core"


credential = uc_credentials.UsernamePassword("<USERNAME>", "<PASSWORD>")

client = uc_client.Client(credential, base_url)

job = uc_client.Job(security=credential,job_url=base_url+"/jobs/"+"8aae239c-165a-44ea-9222-aa7ecae3c70d")

print("Job status: ",job.status)
print("#################################################")
print(json.dumps(job.properties, indent=2))

print("#################################################")
work_dir = job.working_dir
stderr = work_dir.stat("/stderr")
print(json.dumps(stderr.properties, indent = 2))
content_err = stderr.raw().read()
print("job output: ", content_err)
print("#################################################")
stdout = work_dir.stat("/stdout")
print(json.dumps(stdout.properties, indent = 2))
content_out = stdout.raw().read()
print("job output: ", content_out)
