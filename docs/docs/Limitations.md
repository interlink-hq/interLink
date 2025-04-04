---
sidebar_position: 6
---

# Current limitations

It's not black magic, we have to pay something:

- __InCluster network__: we are in the middle of the beta period to release this feature! Reach out to us if you are willing to test it!
- __Cluster wide shared FS__: there is no support for cluster-wide filesystem mounting on the remote container. The only volumes supported are: `Secret`, `ConfigMap`, `EmptyDir`

That's all. If you find anything else, feel free to let it know filing a github issue.

