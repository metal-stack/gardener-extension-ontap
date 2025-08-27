# Gardener Extension for NetApp ONTAP CSI Plugin

This repository contains the Gardener extension controller for managing the NetApp ONTAP CSI Plugin.

## Table of Contents
- [Prerequisites](#prerequisites)
- [Development Workflow](#development-workflow)
- [Test Environment Setup](#test-environment-setup)
- [Known Issues](#known-issues)
- [TODO List](#todo-list)

## Prerequisites

- A local Gardener setup

## Development Workflow

### Setup Gardener Locally

1. Clone the Gardener Repository:
```bash
git clone git@github.com:gardener/gardener.git
```

2. Start a local Kubernetes cluster:
```bash
make kind-up
```

3. Deploy Gardener:
```bash
make gardener-up
```

4. Generate Helm Charts:
```bash
make generate
```

### Deploy the Extension

1. Apply the example configuration:
```bash
kubectl apply -k example/
```

2. Apply the shoot cluster configuration:
```bash
kubectl apply -f example/shoot.yaml
```

### Update Code Changes

When making changes to the code, build and deploy locally using:
```bash
make push-to-gardener-local
```

### Access the Shoot Cluster

1. Adjust your `/etc/hosts` file:
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
# End of Gardener local setup section
EOF
```

2. Generate the kubeconfig for the shoot cluster:
```bash
./hack/usage/generate-admin-kubeconf.sh > admin-kubeconf.yaml
```

3. Trigger a reconciliation if needed:
```bash
kubectl -n garden-<project-name> annotate shoot <shoot-name> gardener.cloud/operation=reconcile
```

## Test Environment Setup

To properly set up the test environment, we need to configure network translation between the external IPs (10.x) and internal KVM network (192.168.x).

### Simulator Host Machine Configuration

#### Cluster Management Interface

```bash
# Port Forward rules
sudo iptables -t nat -A PREROUTING -i lan0 -p tcp --dport 443 -d 10.130.184.5 -j DNAT --to-destination 192.168.10.11
sudo iptables -t nat -A PREROUTING -i lan1 -p tcp --dport 443 -d 10.130.184.5 -j DNAT --to-destination 192.168.10.11

# NAT rules
sudo iptables -t nat -A POSTROUTING -o lan0 -p tcp --dport 443 -d 192.168.10.11 -j SNAT --to-source 10.130.184.5
sudo iptables -t nat -A POSTROUTING -o lan1 -p tcp --dport 443 -d 192.168.10.11 -j SNAT --to-source 10.130.184.5

# Forward rules
sudo iptables -I FORWARD 1 -i lan0 -o br-ontap-data -d 192.168.10.11 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 2 -i lan1 -o br-ontap-data -d 192.168.10.11 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 3 -i br-ontap-data -o lan0 -s 192.168.10.11 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 4 -i br-ontap-data -o lan1 -s 192.168.10.11 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
```

#### SVM Management Interface

```bash
# Port Forward rules
sudo iptables -t nat -A PREROUTING -i lan0 -p tcp --dport 443 -d 10.130.184.6 -j DNAT --to-destination 192.168.10.29
sudo iptables -t nat -A PREROUTING -i lan1 -p tcp --dport 443 -d 10.130.184.6 -j DNAT --to-destination 192.168.10.29

# NAT rules
sudo iptables -t nat -A POSTROUTING -o lan0 -p tcp --dport 443 -d 192.168.10.29 -j SNAT --to-source 10.130.184.6
sudo iptables -t nat -A POSTROUTING -o lan1 -p tcp --dport 443 -d 192.168.10.29 -j SNAT --to-source 10.130.184.6

# Forward rules
sudo iptables -I FORWARD 1 -i lan0 -o br-ontap-data -d 192.168.10.29 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 2 -i lan1 -o br-ontap-data -d 192.168.10.29 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 3 -i br-ontap-data -o lan0 -s 192.168.10.29 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 4 -i br-ontap-data -o lan1 -s 192.168.10.29 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
```

#### SVM Data Interface

```bash
# Port Forward rules
sudo iptables -t nat -A PREROUTING -i lan0 -p tcp --dport 4420 -d 10.130.184.7 -j DNAT --to-destination 192.168.10.30
sudo iptables -t nat -A PREROUTING -i lan1 -p tcp --dport 4420 -d 10.130.184.7 -j DNAT --to-destination 192.168.10.30

# NAT rules
sudo iptables -t nat -A POSTROUTING -o lan0 -p tcp --dport 4420 -j SNAT --to-source 10.130.184.7
sudo iptables -t nat -A POSTROUTING -o lan1 -p tcp --dport 4420 -d 192.168.10.30 -j SNAT --to-source 10.130.184.7

# Forward rules
sudo iptables -I FORWARD 1 -i lan0 -o br-ontap-data -d 192.168.10.30 -p tcp --dport 4420 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 2 -i lan1 -o br-ontap-data -d 192.168.10.30 -p tcp --dport 4420 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 3 -i br-ontap-data -o lan0 -s 192.168.10.30 -p tcp --sport 4420 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
sudo iptables -I FORWARD 4 -i br-ontap-data -o lan1 -s 192.168.10.30 -p tcp --sport 4420 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
```

### Worker Node Configuration

Configure the worker node with these rules:

```bash
iptables -t nat -A OUTPUT -d 192.168.10.30 -j DNAT --to-destination 10.130.184.7
iptables -t nat -A OUTPUT -d 192.168.10.30 -p tcp --dport 4420 -j DNAT --to-destination 10.130.184.7:4420
iptables -t nat -A POSTROUTING -d 10.130.184.7 -j MASQUERADE

echo "10.130.184.7 192.168.10.30" >> /etc/hosts
```

### Required Network Policies

#### Clusterwidewide Network Policy in Shoot:

```bash
apiVersion: metal-stack.io/v1
kind: ClusterwideNetworkPolicy
metadata:
  namespace: firewall
  name: allow-nvme-port
spec:
  egress:
  - to:
    - cidr: 10.130.184.7/32
    ports:
    - protocol: TCP
      port: 4420
```

#### Clusterwidewide Network Policy in Seed:

```bash
apiVersion: metal-stack.io/v1
kind: ClusterwideNetworkPolicy
metadata:
  namespace: firewall
  name: allow-mgmt-port
spec:
  egress:
  - to:
    - cidr: 10.130.184.5/32
    ports:
    - protocol: TCP
      port: 443
```

## Known Issues

- In local environments, using the "Default" broadcast domain can result in "no route to host" errors. Using "Default-1" broadcast domain resolves this issue.
- On test environments, the opposite is true - "Default" works but "Default-1" fails.
- In the simulator, ports e0c and e0d are not functional. Use only e0a and e0b.
- The Trident NVMe driver automatically uses network interfaces that are internally assigned. See [Trident issue #1007](https://github.com/NetApp/trident/issues/1007) for details.
- When an SVM already exists, the secret in the shoot isn't created because it assumes the secret is already in the seed.

## TODO List

- Fix SVM secret creation when SVM already exists
- Implement proper SVM deletion logic
- Add default gateway/routing configuration for SVMs
- Fix hardcoded password in the `GenerateSecurePassword` function
- Implement network route creation after SVM setup
- Add monitoring and alerting for SVM health
- Create proper cleanup and lifecycle management

## Creating ONTAP Encrypted Volumes

To create an encrypted volume using NetApp Trident CSI, you need three components:

1. Secret with LUKS Passphrase

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: storage-encryption-key
  namespace: <namespace-of-the-pvc>
stringData:
  luks-passphrase-name: A
  luks-passphrase: secretA
```

2. StorageClass with Encryption Annotations

The StorageClass must include CSI node stage secret annotations:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: ontap-encrypted
provisioner: csi.trident.netapp.io
parameters:
  selector: "luks=true"
  csi.storage.k8s.io/node-stage-secret-name: storage-encryption-key
  csi.storage.k8s.io/node-stage-secret-namespace: ${pvc.namespace}
  backendType: "ontap-san"
  provisioningType: "thin"
  fsType: "ext4"
allowVolumeExpansion: true

```

3. PVC Using the Encrypted StorageClass

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ontap-encrypted-volume
spec:
  storageClassName: ontap-encrypted
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 1Gi
```

### Key Requirements

- Secret name in StorageClass must match actual secret name
- Secret namespace in StorageClass must match where secret is created
- PVC must reference the StorageClass with encryption annotations
