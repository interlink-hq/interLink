# InterLink
## :information_source: Overview

This project aims to enable a communication between a Kubernetes VitualKubelet and a container manager, like for example Docker.
The project is based on KNoC, for reference, check https://github.com/CARV-ICS-FORTH/knoc
Everything is thought to be modular and it's divided in different layers. These layers are summarized in the following drawing:

![drawing](imgs/InterLink.svg)

## :information_source: Components

- Virtual kubelet:
We have implemented 3 more functions able to communicate with the InterLink layer; these functions are called createRequest, deleteRequest and statusRequest, which calls through a REST API to the InterLink layer. Request uses a POST, deleteRequest uses a DELETE, statusRequest uses a GET.

- InterLink:
This is the layer managing the communication with the plug-ins. We began implementing a Mock module, to return dummy answers, and then moved towards a Docker plugin, using a library to emulate a shell to call the Docker CLI commands to implement containers creation, deletion and status querying. We chose to not use Docker API to extend modularity and porting to other managers, since we can think to use a job workload queue like Slurm.

- Sidecars: 
Basically, that's the name we refer to each plug-in talking with the InterLink layer. Each Sidecar is inependent and separately talks with the InterLink layer.

## :grey_exclamation: Requirements
- Golang >= 1.18.9 (might work with older version, but didn't test)
- A working Kubernetes instance
- An already set up KNoC environment
- Docker
- Sbatch, Scancel and Squeue (Slurm environment) for the Slurm sidecar
- Kubectl on working node

## Quick references:
- [Quick Start](#fast_forward-quick-start)
- [Building from sources](#hammer-building-from-sources)
- [Kustomize your Virtual Kubelet](#wrench-kustomizing-your-virtual-kubelet)
- [InterLink Config file](#information_source-interlink-config-file)
- [Environment Variables list](#information_source-environment-variables-list)
- [Usage](#question-usage)
- [Authentication](#closed_lock_with_key-authentication)

## :fast_forward: Quick Start
- Fastest way to start using interlink, is by deploying a VK in Kubernetes using the prebuilt image:
    ```bash
    kubectl create ns vk
    kubectl kustomize ./kustomizations
    kubectl apply -n vk -k ./kustomizations
    ```

- Then, use Docker Compose to create and start up containers:
    ```bash
    docker compose -f docker-compose.yaml up -d
    ```
- You are now running:
    - A Virtual Kubelet
    - The InterLink service
    - A Docker Sidecar
- Submit a YAML to your K8S cluster to test it. You could try:
    ```bash
    kubectl apply -f examples/interlink_mock/payloads/busyecho_k8s.yaml -n vk
    ```

## :hammer: Building from sources
It is possible you need to perform some adjustments or any modification to the source code and you want to rebuild it. You can build both binaries, Docker images and even customize your own Kubernetes deployment. 
### :hash: Binaries
Building standalone binaries is way easier and all you need is a simple
 ```bash
make all
```
You will find all VK, InterLink and Sidecars binaries in the bin folder. 
If you want to only build a component, replace 'all' with vk/interlink/sidecars to only build the respective component.

### :whale2: Docker images
Building Docker Images is still simple, but requires 'a little' more effort.
- First of all, login into your Docker Hub account by ```docker login```
- Then you can build and push your new images to your Docker Hub. Remember to specify the correct Dockerfile, according to your needs; here's an example with the Virtual Kubelet image:
    ```bash
    docker build -t *your docker hub username*/vk:latest -f Dockerfile.vk .
    docker push *your docker hub username*/vk:latest
    ```
    Note: After pushing the image, edit the deployment.yaml file, located inside the kustomization sub-folder, to reflect the new image name. Check the [Kustomizing your Virtual Kubelet](#wrench-kustomizing-your-Virtual-Kubelet) section for more informations on how to customize your VK deployment.

You can now run these images standalone with ```docker run *image_tag*``` or you can choose to use Docker Compose, by checking the [Docker Compose](#whale2-docker-compose-interlink-and-sidecars) section.

### :electron: Kubernetes deployment (VK)
It's basically building a Docker image with additional steps. After [building your image](#whale2-docker-images), you only have to deploy it. If you haven't already created the proper namespace, do it now and apply your kustomizations:
```bash
kubectl create ns vk
kubectl kustomize ./kustomizations
```
Then, simply apply. Remember to specify the correct namespace.
```bash
kubectl apply -n vk -k ./kustomizations
```

### :whale2: Docker Compose (InterLink and Sidecars)
If you are reading this section, it's probably because you rebuilt your Docker Images to reflect some changes.
First of all, keep in mind the docker-compose.yaml is a sample file we provided to have a quick start setup, but it's customizable according to any need.
This file is default defining 2 services: the interlink service and the slurm sidecar service. If you rebuilt any of these images, you have to edit the image field to allow docker to be pull the new one from your repo.
After editing it, you can easily start up your container bu running
```bash
docker compose -f docker-compose.yaml up -d
```

### :wrench: Kustomizing your Virtual Kubelet
Since ideally the Virtual Kubelet runs into a Docker Container orchestred by a Kubernetes cluster, it is possible to customize your deployment by editing configuration files within the kustomizations directory:
- kustomization.yaml: here you can specify resource files and generate configMaps
- deployment.yaml: that's the main file you want to edit. Nested into spec -> template -> spec -> containers you can find these fields:
    - name: the container name
    - image: Here you can specify which image to use, if you need another one. 
    - args: These are the arguments passed to the VK binary running inside the container.
    - env: Environment Variables used by kubelet and by the VK itself. Check the ENVS list for a detailed explanation on how to set them.
- knoc-cfg.json: it's the config file for the VK itself. Here you can specify how many resources to allocate for the VK. Note that the name specified here for the VK must match the name given in the others config files.
- InterLinkConfig.yaml: configuration file for the inbound/outbound communication (and not only) to/from the InterLink module. For a detailed explanation of all fields, check the [InterLink Config File](#information_source-interlink-config-file) section.
If you perform any change to the listed files, you will have to
```bash
kubectl apply -n vk -k ./kustomizations
```
You can also use Environment Variables to overwrite the majority of default values and even the ones configured in the InterLink Config file. Check the [Environment Variables list](#information_source-environment-variables-list) for a detailed explanation.

### :question: Usage
You have two possible ways to use it:
- VK: binary or K8S deployment
- InterLink / Sidecars: binaries or Docker container

Since K8S deployment and Docker containers have been explained on how to be deployed in the above sections ([Kubernetes deployment](#electron-kubernetes-deployment-vk), [Docker Compose](#whale2-docker-compose-interlink-and-sidecars)), this section will be about using raw binaries. Remember you can, for example, deploy a VK on a Kubernetes cluster and use binaries for InterLink and Sidecars, if you need a quick test bench, instead of rebuilding images everytime.

#### Virtual Kubelet
VK's binary has to be used in the form of ```vk [args]```. A list of complete arguments con be found with the ```--help``` flag, but here the most important will be listed:
- --nodename -> the name of the node inside the K8S cluster
- --provider -> the provider for the VK. We use knoc
- --provider-config -> path to the config for the VK provider
- --startup-timeout -> how much time to wait at the node startup before a timeout error
- --kubeconfig -> path to your Kubernetes cluster configuration. Omit it if you run it as container in your K8S deployment

To give you a reference, our typical startup command was:
```bash
vk --nodename vk-knoc-debug --provider knoc --provider-config ./kustomizations/knoc-cfg.json --startup-timeout 10s --klog.v "2" --kubeconfig /home/surax/.k3d/kubeconfig-mycluster.yaml --klog.logtostderr --log-level debug
```
With the above command, you will have a running Virtual Kubelet, waiting for Pods to be registered to. Once at least one Pod will be registered, the VK will begin communicating with the InterLink module on port 3000 (by default. Customizable by editing the InterLink config file) using REST APIs. 

Note: remember to run VK, InterLink and a Sidecar before registering any Pod, otherwise you will get HTTPS errors, since there will be a missing reply from at least one component.

#### InterLink / Sidecars
InterLink and Sidecars do not want any argument (for the moment, at least), since any customization is performed through the InterLink config file or by setting the relative Environment Variables. Simply run the InterLink executable and a Sidecar one. Once at least one Pod will be registered, the InterLink will begin communicating with the VK on port 3000 and with Sidecars on ports 4000/4001 (4000 for Docker, 4001 for Slurm. Clearly, default port can be modified using the InterLink config file) through REST APIs.

### :closed_lock_with_key: Authentication
InterLink supports OAuth2 proxy authentication, allowing you to set up an authorized group (or managing single-user access) to access services. In order to use it, set the InterLinkPort field to 8080 and run InterLink executable by executing the docs/itwinctl.sh script. The provided script will run InterLink and Slurm sidecar binaries, but you can easily edit it to run another sidecar.
First time running the script, run ```source itwinctl.sh install```, to download and setup the OAuth2 proxy.
From now on, you can just use ```source itwinctl.sh start/stop/restart``` to manage your applications.
Remember to generate your token and set the VKTOKENFILE environment variable, otherwise any connection between VK and InterLink will be refused with a 403 Fobidden reply.

### :information_source: InterLink Config file
Detailed explanation of the InterLink config file key values.
- InterlinkURL -> the URL to allow the Virtual Kubelet to contact the InterLink module. 
- SidecarURL -> the URL to allow InterLink to communicate with the Sidecar module (docker, slurm, etc). Do not specify port here
- InterlinkPort -> the Interlink listening port. InterLink and VK will communicate over this port.
- SidecarService -> the sidecar service. At the moment, it can be only "slurm" or "docker". According to the specified service, InterLink will automatically set the listening port to 4000 for Docker and 4001 for Slurm. set $SIDECARPORT environment variable to specify a custom one
- SbatchPath -> path to your Slurm's sbatch binary
- ScancelPath -> path to your Slurm's scancel binary 
- VKTokenFile -> path to a file containing your token fot OAuth2 proxy authentication.
- CommandPrefix -> here you can specify a prefix for the programmatically generated script (for the slurm plugin). Basically, if you want to run anything before the script itself, put it here.
- ExportPodData -> a boolean value needed to instruct InterLink if you wish to export ConfigMaps or Secrets.
- DataRootFolder -> The folder where the exported data will be saved. Additional subfolders will be created for each pod and volume.
- ServiceAccount -> The service account for your Kubernetes cluster. You are allowed to customize what any user can do and plugins will use the specified service account to retrieve the appropriate kubeconfig to be used.
- Namespace -> Namespace to be used to set up the virtual kubelet and the sidecars. It will be used to generate a service account kubeconfig.
- Tsocks -> true or false values only. Enables or Disables the use of tsocks library to allow proxy networking. Only implemented for the Slurm sidecar at the moment.
- TsocksPath -> path to your tsocks library.
- TsocksLoginNode -> specify an existing node to ssh to. It will be your "window to the external world"

### :information_source: Environment Variables list
Here's the complete list of every customizable environment variable. When specified, it overwrites the listed key within the InterLink config file.
- $VK_CONFIG_PATH -> VK config file path
- $INTERLINKURL -> the URL to allow the Virtual Kubelet to contact the InterLink module. Do not specify a port here. Overrides InterlinkURL.
- $INTERLINKPORT -> the InterLink listening port. InterLink and VK will communicate over this port. Overrides InterlinkPort.
- $INTERLINKCONFIGPATH -> your InterLink config file path. Default is ./kustomizations/InterLinkConfig.yaml
- $SIDECARURL -> the URL to allow InterLink to communicate with the Sidecar module (docker, slurm, etc). Do not specify port here. Overrides SidecarURL.
- $SIDECARPORT -> the Sidecar listening port. Docker default is 4000, Slurm default is 4001.
- $SIDECARSERVICE -> can be "docker" or "slurm" only (for the moment). If SIDECARPORT is not set, will set Sidecar Port in the code to default settings. Overrides SidecarService.
- $SBATCHPATH -> path to your Slurm's sbatch binary. Overrides SbatchPath.
- $SCANCELPATH -> path to your Slurm's scancel binary. Overrides ScancelPath.
- $VKTOKENFILE -> path to a file containing your token fot OAuth2 proxy authentication. Overrides VKTokenFile.
- SERVICEACCOUNT -> The service account for your Kubernetes cluster. You are allowed to customize what any user can do and plugins will use the specified service account to retrieve the appropriate kubeconfig to be used. Overrides ServiceAccount
- $TSOCKS -> true or false, to use tsocks library allowing proxy networking. Working on Slurm sidecar at the moment. Overrides Tsocks.
- $TSOCKSPATH -> path to your tsocks library. Overrides TsocksPath.
