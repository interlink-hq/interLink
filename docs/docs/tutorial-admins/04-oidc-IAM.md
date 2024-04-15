---
sidebar_position: 4 
---

# Configure OpenID connect identity providers

In alternative of the Github authentication flow, we support any OpenID compliant identity provider. The following are a few examples.

## EGI Check-in

If you have an account for [EGI check-in](https://aai.egi.eu), you should be able to set it for authenticating the virtual kubelet with the interLink remote components with the following piece of configuration to be passed to the [installation script](./01-deploy-interlink.mdx).

```yaml
oauth:
  provider: oidc
  issuer: https://aai.egi.eu/auth/realms/egi
  scopes:
    - "openid"
    - "email"
    - "offline_access"
    - "profile"
  audience: interlink
  group_claim: email
  group: "YOUR EMAIL HERE"
  token_url: "https://aai.egi.eu/auth/realms/egi/protocol/openid-connect/token"
  device_code_url: "https://aai.egi.eu/auth/realms/egi/protocol/openid-connect/auth/device"
  client_id: "oidc-agent"
  client_secret: ""
```

:::danger
Remember to put your email in the group field!
:::

## Indigo IAM

:::warning
TBD
:::
