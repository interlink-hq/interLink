---
sidebar_position: 2
---

# Current limitations

It's not black magic, we have to pay something:

- __InCluster network__: at the moment there is no support for in-cluster communication b/w remote container and k8s cluster network. Meaning that service that are NOT exposed to the external network cannot be accessed from the pod running on the virtual kubelet. There are plans to include the support for this, we are not simply there yet.
- __Cluster wide shared FS__: there is no support for cluster-wide filesystem mounting on the remote container. The only volumes supported are: `Secret`, `ConfigMap`, `EmptyDir`

That's all. If you find anything else, feel free to let it know filing a github issue.

