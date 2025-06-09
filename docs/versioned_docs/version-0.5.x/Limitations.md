---
sidebar_position: 6
---

# Current limitations

It's not black magic, we have to pay something:

- **Cluster wide shared FS**: there is no support for cluster-wide filesystem
  mounting on the remote container. The only volumes supported are: `Secret`,
  `ConfigMap`, `EmptyDir`
- **InCluster pod-to-pod network**: we are in the middle of the beta period to
  release this feature!

:::note

Reach out to us if you are willing to test the network implementation as beta
users!

:::

That's all. If you find anything else, feel free to let it know filing a github
issue.
