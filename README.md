# Gardener Extension for NetApp ONTAP CSI Plugin

This repository contains the Gardener extension controller for managing the NetApp ONTAP CSI Plugin.

## Development Workflow

### Prerequisites

- A local Gardener setup.

### Steps to Run Locally

1. **Clone the Gardener Repository**

```bash
git clone git@github.com:gardener/gardener.git
```

2. Set Up Gardener Locally

### Start a local Kubernetes cluster:

```bash
make kind-up
```

1. Deploy Gardener:

```bash
    make gardener-up
```

2. Generate Helm Charts

Run the following command to generate the required Helm charts:
```bash
make generate
```

### Deploy the Example Configuration

1. Apply the example configuration to your Gardener setup:
```bash
kubectl apply -k example/
```

2. Apply the shoot cluster configuration:
```bash
kubectl apply -f example/shoot.yaml
```

### Pushing Code Changes Locally

When making changes to the code, you can build and deploy the changes locally using:

```bash
make push-to-gardener-local
```


# Sequence Diagram

<img src="sequence_diagram.drawio.svg">


# Notes

doc.go has been temporarily modified to bypass the use of VERSION. This needs to be fixed.

./hack/usage/delete shoot local  garden-local # Deleting shoot cluster


kubectl -n garden-<project-name> annotate shoot <shoot-name> gardener.cloud/operation=reconcile


Still a bug:

    Message:               error during apply of object "v1/ServiceAccount/trident/trident-operator": unable to get: trident/trident-operator because of unknown namespace for the cache

