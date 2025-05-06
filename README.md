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


# Known Problems

For some reason on the local environment when using the Default Broadcast domain a no route to host error occurs. If Using the Default-1 Broadcast domain everything works.
On the test environment its the exact opposite

# Local Nvme setup

## On worker node gardener

kubectl-node_shell machine-shoot--local--local-local-6cffc-rx7gb
apt-get install nvme-cli
cat /etc/nvme/hostnqn

## On ontap cluster

set -privilege advanced
vserver nvme subsystem create -vserver caa2ef965d4640d6b132de1b413490b8 -subsystem k8s_subsystem -ostype linux

vserver nvme subsystem host add -vserver caa2ef965d4640d6b132de1b413490b8 -subsystem k8s_subsystem -host-nqn nqn.2014-08.org.nvmexpress:uuid:cd399400-960f-11ea-8000-3cecef6b3d04
  

vserver nvme subsystem map add -vserver caa2ef965d4640d6b132de1b413490b8 -subsystem k8s_subsystem -path /vol/<<look tridentfrontend nvme namespaces>>

vserver nvme subsystem show -vserver caa2ef965d4640d6b132de1b413490b8 -subsystem k8s_subsystem

vserver nvme subsystem host show -vserver caa2ef965d4640d6b132de1b413490b8 -subsystem k8s_subsystem



# 1
sudo iptables -t nat -A PREROUTING -i lan0 -p tcp --dport 443 -d 10.130.184.5 -j DNAT --to-destination 192.168.10.11
 sudo iptables -t nat -A PREROUTING -i lan1 -p tcp --dport 443 -d 10.130.184.5 -j DNAT --to-destination 192.168.10.11
 
  sudo iptables -t nat -A POSTROUTING -o lan0 -p tcp --dport 443 -d 192.168.10.11 -j SNAT --to-source 10.130.184.5
 
  sudo iptables -t nat -A POSTROUTING -o lan1 -p tcp --dport 443 -d 192.168.10.11 -j SNAT --to-source 10.130.184.5
 
  sudo iptables -I FORWARD 1 -i lan0 -o br-ontap-data -d 192.168.10.11 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 2 -i lan1 -o br-ontap-data -d 192.168.10.11 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 3 -i br-ontap-data -o lan0 -s 192.168.10.11 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 4 -i br-ontap-data -o lan1 -s 192.168.10.11 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT


# 2
sudo iptables -t nat -A PREROUTING -i lan0 -p tcp --dport 443 -d 10.130.184.6 -j DNAT --to-destination 192.168.10.29
sudo iptables -t nat -A PREROUTING -i lan1 -p tcp --dport 443 -j DNAT -d 10.130.184.6 --to-destination 192.168.10.29
 
  sudo iptables -t nat -A POSTROUTING -o lan0 -p tcp --dport 443 -d 192.168.10.29 -j SNAT --to-source 10.130.184.6
 
  sudo iptables -t nat -A POSTROUTING -o lan1 -p tcp --dport 443 -d 192.168.10.29 -j SNAT --to-source 10.130.184.6
 
  sudo iptables -I FORWARD 1 -i lan0 -o br-ontap-data -d 192.168.10.29 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 2 -i lan1 -o br-ontap-data -d 192.168.10.29 -p tcp --dport 443 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 3 -i br-ontap-data -o lan0 -s 192.168.10.29 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 4 -i br-ontap-data -o lan1 -s 192.168.10.29 -p tcp --sport 443 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT

# 3
sudo iptables -t nat -A PREROUTING -i lan0 -p tcp --dport 4420 -d 10.130.184.7 -j DNAT --to-destination 192.168.10.30
sudo iptables -t nat -A PREROUTING -i lan1 -p tcp --dport 4420 -j DNAT -d 10.130.184.7 --to-destination 192.168.10.30
 
  sudo iptables -t nat -A POSTROUTING -o lan0 -p tcp --dport 4420 -d 192.168.10.30 -j SNAT --to-source 10.130.184.7
 
  sudo iptables -t nat -A POSTROUTING -o lan1 -p tcp --dport 4420 -d 192.168.10.30 -j SNAT --to-source 10.130.184.7
 
  sudo iptables -I FORWARD 1 -i lan0 -o br-ontap-data -d 192.168.10.30 -p tcp --dport 4420 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 2 -i lan1 -o br-ontap-data -d 192.168.10.30 -p tcp --dport 4420 -m conntrack --ctstate NEW,ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 3 -i br-ontap-data -o lan0 -s 192.168.10.30 -p tcp --sport 4420 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT
 
  sudo iptables -I FORWARD 4 -i br-ontap-data -o lan1 -s 192.168.10.30 -p tcp --sport 4420 -m conntrack --ctstate ESTABLISHED,RELATED -j ACCEPT



# On the worker node.

iptables -t nat -A OUTPUT -d 192.168.10.30 -j DNAT --to-destination 10.130.184.7
iptables -t nat -A OUTPUT -d 192.168.10.30 -p tcp --dport 4420 -j DNAT --to-destination 10.130.184.7:4420
iptables -t nat -A POSTROUTING -d 10.130.184.7 -j MASQUERADE

echo "10.130.184.7 192.168.10.30" >> /etc/hosts

## 
  sudo ip addr add 10.130.184.6/32 dev lo
  sudo ip addr add 10.130.184.7/32 dev lo


curl -k -u "admin:fsqe2020" https://10.130.184.6/api/svm/svms/

curl -k -u "admin:fsqe2020" https://10.130.184.5/api/svm/svms/


curl -k -u "svmAdmin:fsqe2020" https://10.130.184.6/api/svm/svms/



curl -k -u "svmAdmin:fsqe2020" https://192.168.10.29/api/svm/svms/


# STEPS


Had to modprobe the nvme on the node first
then restart the trident node linux pod
then it should register
then i create the pvc
 

 nvme connect -t tcp -n nqn.1992-08.com.netapp:sn.75bd03da29a711f09cf403e0f0f1538f:subsystem shoot--pfw245--ontap-group--dcb30443-d9a1-4e10-a725-989ef64ebf08 -a 10.130.184.7 -s 4420 -l -1



# 

CWNP in Shoot:

ebubekir@ebubekir-Dell-G16-7630:~/.kube/configs$ cat cw.yaml 
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
