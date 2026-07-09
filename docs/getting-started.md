# Getting Started

In this guide we'll cover the basics of how to use the Cluster API Oxide provider. 

In summary, we will:
 1. Build an Oxide VM Image w/ Kubernetes Assets
 2. Create and Configure a Management Cluster w/ Kind
 3. Provision a Workload Cluster with the Oxide CAPI provider

## Build an Oxide VM Image w/ Kubernetes Assets

The Oxide CAPI provider requires a VM image to exist in the Oxide Silo or Project where your clusters will be provisioned with the relevant Kubernetes binaries (kubeadm, kubelet, kubectl, containerd, etc.). We use [image-builder](https://github.com/kubernetes-sigs/image-builder) to build the VM images which depends on Packer and Ansible.

Choose an Oxide Project in which to build the image, as well as a base image. Currently, Ubuntu 24.04 is the only supported base image. If you don't have an Ubuntu 24.04 image loaded on your Oxide Silo, upload one by following the [Oxide image guide](https://docs.oxide.computer/guides/creating-and-sharing-images). 

After a supported base image has been uploaded to your Oxide Silo, export environment variables indicating your project and base image disk ID:

```
export OXIDE_PROJECT=<my-project>
export OXIDE_BOOT_DISK_IMAGE_ID=<my-base-image-id>
```

You'll also need to export environment variables for Packer to authenticate to the Oxide API:

```
export OXIDE_HOST=<my-oxide-host>
export OXIDE_TOKEN=<my-oxide-token>
```

or

```
export OXIDE_PROFILE=<my-profile>
```

Clone the image builder:

```
git clone https://github.com/kubernetes-sigs/image-builder.git
```

Change directory to images/capi within the image builder repository:

```
cd image-builder/images/capi
```

Build an Oxide image:

```
make build-oxide-ubuntu-2404
```

Export the ID of the image built:

```
export OXIDE_IMAGE_ID=<my-image-id>
```

## Create and Configure a Management Cluster w/ Kind

CAPI uses a management cluster to configure and deploy workload clusters. Any Kubernetes cluster can serve as the management cluster, but we'll use [kind](https://kind.sigs.k8s.io/) here:

```
export OXIDE_MANAGEMENT_CLUSTER=capi-management
kind create cluster --name $OXIDE_MANAGEMENT_CLUSTER --wait 30s
kubectl config use-context kind-$OXIDE_MANAGEMENT_CLUSTER
```

Next, install the `clusterctl` tool by following the [installation docs](https://cluster-api.sigs.k8s.io/user/quick-start.html#install-clusterctl). Use the `clusterctl` CLI to install the core CAPI Kubernetes resources and controllers to the management cluster. 

```
clusterctl init
```

Add the Oxide credentials to the management cluster to allow the Oxide CAPI provider to call the Oxide API. 
Ensure that the `$OXIDE_HOST` and `$OXIDE_TOKEN` environment variables are set and run:

```
kubectl create secret generic oxide-credentials \
    --from-literal=oxide-host=${OXIDE_HOST} \
    --from-literal=oxide-token=${OXIDE_TOKEN}
```

Now, install Cluster API Provider for Oxide:

```
helm upgrade --install \
    --namespace capox-system \
    --create-namespace \
    --version 0.0.12  \
    --wait \
    --set image.repository=ghcr.io/bwagner5/capox/cluster-api-provider-oxide \
    capox \
    oci://ghcr.io/bwagner5/capox/helm-charts/cluster-api-provider-oxide
```


## Provision a Workload Cluster with the Oxide CAPI provider

The Oxide CAPI provider uses a floating IP for the workload cluster's kube-apiserver. We need to create a firewall rule that allows inbound TCP communication over port 6443. This will allow the nodes to join to the cluster and for you to use kubectl to interact with the workload cluster. 

The command below will list all firewall rules currently configured for your VPC and add one to allow TCP/6443 for any node in the VPC. You may want to constrain the IP range of your control plane nodes by placing them in a separate subnet so that a more narrow firewall rule can be applied.

```
export OXIDE_VPC="default"
OXIDE_RULES_FILE="$(mktemp)"

oxide vpc firewall-rules view --project "$OXIDE_PROJECT" --vpc "$OXIDE_VPC" \
| jq --arg vpc "$OXIDE_VPC" --arg name "allow-kube-apiserver" '{
    rules: (
      [ .rules[]
        | select(.name != $name)
        | {action, description, direction, filters, name, priority, status, targets} ]
      + [ {
          name: $name,
          description: "Allow kube-apiserver (TCP 6443) from anywhere (https://github.com/oxidecomputer/cluster-api-provider-oxide/docs/getting-started.md)",
          action: "allow",
          direction: "inbound",
          priority: 0,
          status: "enabled",
          filters: { protocols: [ {type:"tcp"} ], ports: ["6443"] },
          targets: [ { type: "vpc", value: $vpc } ]
        } ]
    )
  }' > "$OXIDE_RULES_FILE"


oxide vpc firewall-rules update --project "$OXIDE_PROJECT" --vpc "$OXIDE_VPC" --json-body "$OXIDE_RULES_FILE"
```

A CAPI workload cluster is comprised of several different resource types. We'll generate a manifest describing all the necessary resources using `clusterctl` to render the provided template:

```
OXIDE_CLUSTER_QUICKSTART_MANIFEST="$(mktemp)"
OXIDE_KUBERNETES_VERSION="$(curl -L -s https://dl.k8s.io/release/stable.txt)"

clusterctl generate cluster \
    quickstart \
    --from templates/cluster-template.yaml \
    --kubernetes-version "$OXIDE_KUBERNETES_VERSION" \
    --control-plane-machine-count 3 \
    --worker-machine-count 3 \
    > "$OXIDE_CLUSTER_QUICKSTART_MANIFEST"
```

Apply the rendered quickstart manifest to the management cluster:

```
kubectl apply -f "$OXIDE_CLUSTER_QUICKSTART_MANIFEST"
```

It will take a few minutes for a control plane node to start and get a floating IP attached. Nodes will not get to a "Ready" state until we install the Oxide CCM and a CNI. Let's do that now.

In order to configure the workload cluster, we'll need to fetch its kubeconfig:

```
export OXIDE_QUICKSTART_KUBECONFIG="$(mktemp)"
kubectl get secret quickstart-kubeconfig -o jsonpath='{.data.value}' | base64 -d > "$OXIDE_QUICKSTART_KUBECONFIG"
```

In order for Kubernetes nodes in the workload cluster to become healthy, we need to run a CNI plugin (so that nodes can pass their readiness check).  We'll install the Cilium CNI with the `cilium` CLI. Installation instructions are [here](https://docs.cilium.io/en/stable/gettingstarted/k8s-install-default/#install-the-cilium-cli).

```
KUBECONFIG="$OXIDE_QUICKSTART_KUBECONFIG" cilium install
```

Next, we need to install the [Oxide Cloud Controller Manager](https://github.com/oxidecomputer/oxide-cloud-controller-manager) (CCM). The CCM provides information about nodes from the cloud provider like the `providerID`. 

The CCM requires a Kubernetes secret with credentials to authenticate to the Oxide API, so we'll create that secret first:

```
KUBECONFIG="$OXIDE_QUICKSTART_KUBECONFIG" kubectl create secret -n kube-system generic quickstart-oxide-cloud-controller-manager \
    --from-literal=oxide-host=${OXIDE_HOST} \
    --from-literal=oxide-token=${OXIDE_TOKEN} \
    --from-literal=oxide-project=${OXIDE_PROJECT}

KUBECONFIG="$OXIDE_QUICKSTART_KUBECONFIG" helm upgrade --install quickstart \
    oci://ghcr.io/oxidecomputer/helm-charts/oxide-cloud-controller-manager \
    --namespace kube-system
```

Nodes should now be transitioning to a "Ready" state. You can monitor this with clusterctl or by watching nodes in the workload cluster:

```
clusterctl describe cluster quickstart
```

## Deploy an Application to the Workload Cluster

To see the workload cluster in action, we'll deploy nginx. 

```
kubectl create deployment hello-oxide \
    --image=mirror.gcr.io/library/nginx \
    --port=80 \
    --kubeconfig="$OXIDE_QUICKSTART_KUBECONFIG"

kubectl expose deployment hello-oxide \
    --port=80 \
    --target-port=80 \
    --type=LoadBalancer \
    --kubeconfig="$OXIDE_QUICKSTART_KUBECONFIG"
```

Watch the external IPs of the service. Once they are populated, you can access it via your browser or a curl:

```
kubectl get service hello-oxide -w --kubeconfig="$OXIDE_QUICKSTART_KUBECONFIG"
```

## Tear Down the Workload Cluster

When you're done experimenting with the workload cluster, you can easily delete it by deleting the cluster resource from the management cluster. 

Make sure that you are using the management cluster's kubectl context. If you are, then you should see the quickstart cluster when listing clusters:

```
kubectl get clusters
```

Now, delete the cluster:

```
kubectl delete cluster quickstart
```

