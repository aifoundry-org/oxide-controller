# oxide-controller

This is the server that controls the Ainekko stack deployment on an Oxide sled. It serves two purposes:

1. It provides a REST API for the Ainekko stack, to enable users to interact with the sled, adding or removing VMs to act as workers.
1. It provides a loop that checks the state of the cluster and keeps it alive.

This can run standalone on your own device, in a VM, or inside the Kubernetes cluster itself.

All options are configured via CLI flags. Run `oxide-controller --help` to see the available options.

The production-recommended way to run this is:

1. Run this locally, pointing to the Oxide sled, which will cause a 3-node kubernetes cluster to be deployed.
1. Stop running this locally, deploy it as a `StatefulSet` of 1 replica in the Kubernetes cluster.
1. Profit!
