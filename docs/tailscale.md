# Tailscale

If you want to use tailscale to access the nodes in the cluster,
in addition to or in exchange of the public IPs, you can have each node register with a
tailnet.

## Prerequisites

- A tailscale account
- A tailnet
- Appropriate tags for the nodes. By default this is `ainekko-k8s-node`.
- A tailscale [auth key](https://tailscale.com/kb/1085/auth-keys) with rights to join nodes for the provided tags. The key should be ephemeral and reusable.
- A tailscale API key, also known as an access token, with rights to list nodes.

## Requirements

If you want to use tailscale, you **must** provide all of:

* auth key - so nodes can join the tailnet
* API key - so the controller can list the nodes in the tailnet and retrieve the IP address of the first node
* tailnet - so the controller can know which nodes to list

## How it works

When you provide the controller with the tailscale auth key and API key, it will:

1. Install tailscale on each node in the cluster
1. Register the node using the auth key, thus joining the tailnet for that auth key, with the provided tags
1. Use the tailscale API with the API key to list the nodes in the tailnet and retrieve the IP address of the first node
1. Use the IP address of the first node to connect to the node, retrieve the join token and kubeconfig

## Default Tag

The default tag used is `ainekko-k8s-controller`. This must exist in your tailnet's ACLs.
You can override this tag using the `--tailscale-tag` flag.

## Security considerations

The auth key is stored in the following places:

* The `kube-system/oxide-controller` secret in the cluster
* cloud-config of each node

The API key is not stored at all. It is used locally exactly once to retrieve the IP address of the
first node and then discarded.
