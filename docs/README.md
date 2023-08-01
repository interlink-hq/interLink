## Quick-start: InterLink

### Provider requirements

TBD

### Setup and deploy Interlink's edge service with mock plugin 

Following are the steps to setup and deploy edge service, which offload's a docker container. The virtual K8S node is deployed elsewhere:
* Make sure to have Docker installed on the target machine
* Execute the following command: 
  
        $> sudo usermod -aG docker $USER
  
* Open and edit the following lines in the script  ``` interlink/docs/itwinctl.sh ``` to update authentication proxy settings
  * Depending on your network policy, update the port number with the exposed port of your OAuth-Proxy service: 
 
         --API_HTTPS_PORT="${API_HTTPS_PORT:-7002}"
  * Set correct the host's PKI credentials (or certificates and private key) path: 
  
        export HOSTKEY="${HOSTKEY:-/$HOME/hostkey.pem}"
        export HOSTCERT="${HOSTCERT:-/$HOME/hostcert.pem}"
  * To generate demo certificates, run the following openssl command: 
    
        $> openssl req -x509 -sha256 -newkey rsa:4096 -nodes -keyout key.pem -days 11688 -out cert.pem -subj "/C=DE/CN=JSC-INTERTWIN-COMPUTE" -addext "basicConstraints=CA:FALSE" -addext "keyUsage=digitalSignature, nonRepudiation, keyEncipherment, dataEncipherment" -addext "extendedKeyUsage=clientAuth"

        
* After installation, start the docker-sidecar (or mock plugin)
        
        VERSION=0.0.1 SIDECAR=docker ./itwinctl.sh start
* Please check the logs if the services are properly running, they can be found under: ``` /$HOME/.local/interlink/logs/ ```
* Execute offloading a compute task from application layer and check if the docker container is running, by:
  
        docker ps -a

### Install binaries

```bash
curl -sfL https://intertwin-eu.github.io/interLink/itwinctl.sh | sh -s - install
```

### Start daemons

```bash
curl -sfL https://intertwin-eu.github.io/interLink/itwinctl.sh | sh -s - start
```

### Restart daemons

```bash
curl -sfL https://intertwin-eu.github.io/interLink/itwinctl.sh | sh -s - restart
```

### Stop daemons

```bash
curl -sfL https://intertwin-eu.github.io/interLink/itwinctl.sh | sh -s - stop
```

## Configuration options

Please see [here](../README.md#information_source-environment-variables-list) for setting ENVIRONMENT options.

### :closed_lock_with_key: Authentication
InterLink supports OAuth2 proxy authentication, allowing you to set up an authorized group (or managing single-user access) to access services. In order to use it, set the InterLinkPort field to 8080 and run InterLink executable by executing the docs/itwinctl.sh script. The provided script will run InterLink and Slurm sidecar binaries, but you can easily edit it to run another sidecar.
First time running the script, run ```source itwinctl.sh install```, to download and setup the OAuth2 proxy.
From now on, you can just use ```source itwinctl.sh start/stop/restart``` to manage your applications.
Remember to generate your token and set the VKTOKENFILE environment variable, otherwise any connection between VK and InterLink will be refused with a 403 Fobidden reply.