# k3s

How to install k3s on Oxide nodes.

## First control-plane node

The first node should be installed via:

```sh
# curl -sfL https://get.k3s.io | sh -s - server --cluster-init --tls-san <floatingIP>
```

Where `<floatingIP>` is the floating IP of the Oxide sled, e.g. `45.154.216.141`. This will ensure that the certificates are valid for the floating IP.

There are several useful files on the server:

1. `/etc/rancher/k3s/k3s.yaml` - the admin kubeconfig file for the server.
1. `/var/lib/rancher/k3s/server/node-token` - the token to join other server nodes to the cluster.
1. `/var/lib/rancher/k3s/server/agent-token` - the token to join other worker nodes to the cluster.

## Additional control-plane nodes

Additional control-plane nodes should be installed via:

```sh
# curl -sfL https://get.k3s.io | sh -s - server --server https://${SERVER} --token '${TOKEN}'
```

Where:

* `${SERVER}` is the IP address of the first control-plane node, usually the private IP
* `${TOKEN}` is the contents of `/var/lib/rancher/k3s/server/node-token` on the first control-plane node

For example:

```sh
# curl -sfL https://get.k3s.io | sh -s - server --server https://172.30.0.5:6443 --token 'K10e5c026e2bac9d1c12b205e5bc0ac1037616b620701571a5f85234d45809a300c::server:8b30727c41ae9aed923cd9c221570e98'
```

## Worker nodes

Worker nodes should be installed via:

```sh
# curl -sfL https://get.k3s.io | sh -s - agent --server https://${SERVER} --token '${TOKEN}'
```

Where:

* `${SERVER}` is the IP address of the first control-plane node, usually the private IP
* `${TOKEN}` is the contents of `/var/lib/rancher/k3s/server/agent-token` on the first control-plane node

## Token via API

Unfortunately, the tokens do not appear to be stored in Kubernetes anywhere, only on the files in the control system:

```sh
$ kubectl get secret -n kube-system
NAME                                TYPE                 DATA   AGE
chart-values-traefik                Opaque               1      17m
chart-values-traefik-crd            Opaque               0      17m
controlplane-1.node-password.k3s    Opaque               1      17m
controlplane-2.node-password.k3s    Opaque               1      16m
controlplane-3.node-password.k3s    Opaque               1      14m
k3s-serving                         kubernetes.io/tls    2      17m
sh.helm.release.v1.traefik-crd.v1   helm.sh/release.v1   1      17m
sh.helm.release.v1.traefik.v1       helm.sh/release.v1   1      17m
```

Note that you can create the token in advance and feed it to the first node, see [k3s token](https://docs.k3s.io/cli/token).
