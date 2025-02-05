# Adopters

## Scientific communities

### INFN

Project: Heterogeneous Resource integration for scientific workflows/pipelines

Used to enable a seamless provisioning of heterogeneous resources to k8s-based workload manager. interLink grant the possibility to offload the execution of parts of the workload to external providers serving suitable hardware. Leveraging the capability to provision any type of backend without customization on the user end, it makes transparent  the exploitation of HPC centers 

INFN adopts InterLink also in the context of the 
[AI_INFN initiative](https://ai-infn.baltig-pages.infn.it/wp-1/docs/) of the Fifth National 
Scientific Committee, to submit Machine Learning pipelines to HPC and HTC centers. 

### CERN

Project: interTwin

We used interLink to offload the execution of ML/AI workloads to HPC in the context of the interTwin project, including use cases from both pysics (CERN, Virgo) and climate research (CMCC, EURAC) communities. interLink allowed us to test the functionalities of [itwinai](https://itwinai.readthedocs.io/) on HPC by running distributed ML training and inference workloads. Moreover, interLink allows us automatically connect our containers CI/CD pipeline with HPC, enabling the execution of integration tests on HPC from the same CI/CD.

### EGI Foundation

We are integrating interLink in order to provide integration of HPC centers with the EGI Cloud Container compute service. interLink is also included as building block on new EC projects starting in 2025 led by EGI Foundation ( RI-SCALE and EOSC Data Commons)

### Universitat Politècnica de València

Project: interTwin

We integrated interLink capabilities in [OSCAR](https://github.com/grycap/oscar) (Open Source Event-Driven Serverless Computing for Data-Processing Applications) to be able to offload workloads defined as OSCAR Services to HPC clusters. This integration allows OSCAR to leverage interLink's seamless provisioning of heterogeneous resources, enabling efficient execution of data-processing applications on HPC infrastructure.

## HPC supercomputing centers

### IJS & IZUM

Project: interTwin

EuroHPC Vega is the first operational system under the EuroHPC initiative and an early adopter of interTwin framework providing resources through interLink service. It provides critical support and counseling from both project partners (JSI & IZUM), infrastructure, and edge VM for the development and utilization of interLink, fostering the exploitation of the HPC Vega environment within the InterTwin project.

### JSC

JSC provides cloud computing resources, known as JSC Cloud, that are seamlessly integrated with its high-performance computing (HPC) infrastructure, including the powerful JUWELS system. This setup also connects to large-capacity file systems through JUDAC, offering users a smooth and efficient experience. At the heart of this integration is UNICORE, JSC’s HPC middleware, which is currently in production. UNICORE simplifies access to HPC resources by enabling job submissions, managing workflows, and facilitating data transfers—all while hiding the complexities of underlying batch systems. Using a specialized Interlink-based plugin deployed as an edge service, pod creation requests are offloaded and transformed into HPC jobs. These jobs are then submitted to downstream HPC resources via the UNICORE middleware, creating a streamlined and efficient bridge between cloud and HPC environments.

### CNES

Project: LISA DDPC

In the context of LISA (Laser Interferometer Space Antenna) DDPC (Distributed Data Processing Center), CNES is using Interlink to prototype an hybrid execution of LISA pipelines on either Kubernetes or Slurm resources.

## Industry

