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


## Trident Permission for the SVM

https://github.com/NetAppDocs/trident/blob/main/trident-use/ontap-nas.adoc#user-permissions

The docs say that the triden operator needs the default vsadmin role for the svm.

# Notes

doc.go has been temporarily modified to bypass the use of VERSION. This needs to be fixed.


kubectl -n garden-<project-name> annotate shoot <shoot-name> gardener.cloud/operation=reconcile

## Gardener Notes

### Access the shoot

Before fetching the kubeconfig we need to adjust our /etc/hosts.

```bash
cat <<EOF | sudo tee -a /etc/hosts

# Begin of Gardener local setup section
# Shoot API server domains
172.18.255.1 api.local.local.external.local.gardener.cloud
172.18.255.1 api.local.local.internal.local.gardener.cloud

# Ingress
172.18.255.1 p-seed.ingress.local.seed.local.gardener.cloud
172.18.255.1 g-seed.ingress.local.seed.local.gardener.cloud
172.18.255.1 gu-local--local.ingress.local.seed.local.gardener.cloud
172.18.255.1 p-local--local.ingress.local.seed.local.gardener.cloud
172.18.255.1 v-local--local.ingress.local.seed.local.gardener.cloud

# E2E tests
172.18.255.1 api.e2e-managedseed.garden.external.local.gardener.cloud
172.18.255.1 api.e2e-managedseed.garden.internal.local.gardener.cloud
172.18.255.1 api.e2e-hib.local.external.local.gardener.cloud
172.18.255.1 api.e2e-hib.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-hib-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-hib-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-unpriv.local.external.local.gardener.cloud
172.18.255.1 api.e2e-unpriv.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-wake-up.local.external.local.gardener.cloud
172.18.255.1 api.e2e-wake-up.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-wake-up-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-wake-up-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-wake-up-ncp.local.external.local.gardener.cloud
172.18.255.1 api.e2e-wake-up-ncp.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-migrate.local.external.local.gardener.cloud
172.18.255.1 api.e2e-migrate.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-migrate-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-migrate-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-mgr-hib.local.external.local.gardener.cloud
172.18.255.1 api.e2e-mgr-hib.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-rotate.local.external.local.gardener.cloud
172.18.255.1 api.e2e-rotate.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-rotate-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-rotate-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-rot-noroll.local.external.local.gardener.cloud
172.18.255.1 api.e2e-rot-noroll.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-default.local.external.local.gardener.cloud
172.18.255.1 api.e2e-default.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-default-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-default-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-force-delete.local.external.local.gardener.cloud
172.18.255.1 api.e2e-force-delete.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-fd-hib.local.external.local.gardener.cloud
172.18.255.1 api.e2e-fd-hib.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-upd-node.local.external.local.gardener.cloud
172.18.255.1 api.e2e-upd-node.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-upd-node-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-upd-node-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-upgrade.local.external.local.gardener.cloud
172.18.255.1 api.e2e-upgrade.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-upgrade-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-upgrade-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-upg-hib.local.external.local.gardener.cloud
172.18.255.1 api.e2e-upg-hib.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-upg-hib-wl.local.external.local.gardener.cloud
172.18.255.1 api.e2e-upg-hib-wl.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-auth-one.local.external.local.gardener.cloud
172.18.255.1 api.e2e-auth-one.local.internal.local.gardener.cloud
172.18.255.1 api.e2e-auth-two.local.external.local.gardener.cloud
172.18.255.1 api.e2e-auth-two.local.internal.local.gardener.cloud
172.18.255.1 gu-local--e2e-rotate.ingress.local.seed.local.gardener.cloud
172.18.255.1 gu-local--e2e-rotate-wl.ingress.local.seed.local.gardener.cloud
172.18.255.1 gu-local--e2e-rot-noroll.ingress.local.seed.local.gardener.cloud
# End of Gardener local setup section
EOF
```

In the gardener repo for the shoot kubeconfig run:

./hack/usage/generate-admin-kubeconf.sh > admin-kubeconf.yaml

## Ontap Notes:

# For data LIF
network interface create -vserver b5f26a3b9a4d48dba6b3d1dd4ac4abec -lif data_lif -address 192.168.10.40 -netmask 255.255.255.0 -home-node fsqe-snc1-01 -home-port e0b -status-admin up

# For management LIF
network interface create -vserver b5f26a3b9a4d48dba6b3d1dd4ac4abec -lif mgmt_lif -address 192.168.10.41 -netmask 255.255.255.0 -home-node fsqe-snc1-01 -home-port e0b -firewall-policy mgmt -status-admin up

apiVersion: trident.netapp.io/v1
kind: TridentBackendConfig
metadata:
  name: ontap-backend
  namespace: kube-system
spec:
  version: 1
  backendName: testName
  storageDriverName: ontap-nas
  managementLIF: 192.168.10.11
  dataLIF: 192.168.10.21
  svm: vs1
  credentials:
    name: ontap-credentials


apiVersion: v1
kind: Secret
metadata:
  name: ontap-credentials
  namespace: kube-system
type: Opaque
data:
  password: ZnNxZTIwMjA=
  username: YWRtaW4=


kind: PersistentVolumeClaim
apiVersion: v1
metadata:
  name: basic
spec:
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
  storageClassName: ontap-gold

# to do 

bug when svm already exists, secret in shoot inst created bc it assumes bc secret is already in seed and that is used.
add check if seed secret is there if svm is there if not create it again