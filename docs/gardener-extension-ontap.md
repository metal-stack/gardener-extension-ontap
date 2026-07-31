# Gardener Extension for ONTAP

> How `gardener-extension-ontap` turns a shoot configuration into a project-specific ONTAP SVM and a functional Trident stack.

---

## Purpose of the Extension

The extension connects three administrative layers:

1. Gardener declares that a shoot should use ONTAP.
2. The extension establishes the required state in the ONTAP MetroCluster.
3. The extension deploys Trident and its configuration into the shoot.

The extension runs in the seed cluster. It is not a CSI driver and does not carry application data. NetApp Trident in the shoot is the CSI driver; subsequent block I/O flows directly between Kubernetes workers and the ONTAP data LIFs.

| Component                  | Runs in              | Responsibility                                                                     |
|----------------------------|----------------------|------------------------------------------------------------------------------------|
| `gardener-extension-ontap` | Seed                 | Reconciles SVMs, LIFs, users, secrets, and shoot resources                         |
| Trident controller         | Shoot, `kube-system` | Creates, expands, and deletes volumes and snapshots through the SVM management LIF |
| Trident node plugin        | Every shoot worker   | Connects NVMe/TCP volumes and mounts them for pods                                 |
| ONTAP                      | Storage sites        | Stores, replicates, and exposes block volumes                                      |

---

## Activation and Inputs

A shoot activates the extension through an entry of type `ontap`:

```yaml
spec:
  extensions:
    - type: ontap
      providerConfig:
        apiVersion: ontap.metal.extensions.gardener.cloud/v1alpha1
        kind: TridentConfig
        svmIpaddresses:
          managementLif: <management-ip>
          dataLifs:
            - <data-ip-1>
            - <data-ip-2>
```

The controller needs four groups of inputs for reconciliation:

| Input                     | Source                                            | Use                                                      |
|---------------------------|---------------------------------------------------|----------------------------------------------------------|
| Shoot namespace           | `Extension.metadata.namespace`                    | Derives shoot-specific names                             |
| Management and data LIFs  | `providerConfig.svmIpaddresses`                   | Network endpoints of the project SVM                     |
| Metal project ID          | Shoot annotation `cluster.metal-stack.io/project` | Identity and name of the project-specific SVM            |
| ONTAP cluster credentials | Extension controller configuration                | Administrative REST connection to all MetroCluster sites |

`TridentConfig` validation requires exactly one non-empty management LIF and at least one data LIF. Every value must be a syntactically valid IP address. In the production deployment, the Cloud API supplies one management LIF and two data LIFs.

### What the Cloud API Does First

IP allocation is not the responsibility of the extension. When the Cloud API detects an ONTAP storage network, it reserves the following addresses for the Metal project:

- one IP with the tags `ontap` and `managementLif`,
- two IPs with the tags `ontap` and `dataLif`.

Existing project addresses are reused. Consequently, multiple ONTAP-enabled shoots in the same project use the same LIFs and the same project SVM.

### Administrative ONTAP Clients

The controller configuration contains a list of ONTAP clusters with a name, management IP, username, and password. During startup, the extension:

1. validates that at least one cluster is configured,
2. validates credentials and IP syntax,
3. creates an ONTAP REST client for every entry,
4. reads cluster information to verify connectivity.

The current clients use `InsecureTLS: true` and highly privileged cluster credentials. This is an implementation and hardening boundary. The administrative credentials must not be confused with the shoot-specific SVM credentials created later.

---

## Reconciliation

```text
Gardener Extension resource
          |
          v
Validate providerConfig
          |
          v
Read project ID from the shoot
          |
          v
Find project SVM
     +----+----+
     |         |
   found     missing
     |         |
 validate    create
     +----+----+
          |
          v
Ensure shoot user and seed Secret
          |
          v
Deploy Trident ManagedResources
          |
          v
Reconcile shoot webhook
```

### 1 — Read the Provider Configuration and Project

The controller first decodes and validates `TridentConfig`. It then reads the Gardener `Cluster` object and extracts the shoot from it. The project ID comes from the shoot annotation.

ONTAP does not accept the hyphens from the project ID in the SVM name, and the name should begin with a letter. The extension therefore normalizes the value:

```text
Metal project ID:  b5f26a3b-9a4d-48db-a6b3-d1dd4ac4abec
ONTAP SVM name:    pb5f26a3b9a4d48db...
```

### 2 — Find the Project SVM

The extension searches for the SVM through all configured ONTAP clients. It considers both the normalized name and the MetroCluster sync destination with the `-mc` suffix.

Only a running SVM can be used as the active SVM. If no matching SVM exists, the create path begins. If it exists, the controller checks whether its state still matches the declaration.

### 3a — Create a New SVM

For a new SVM, the extension queries the `volume-count` of every aggregate on each configured ONTAP cluster. It sums the values by cluster and chooses the site with the lower total number of volumes. If the totals are equal, the first configured client wins.

The extension then performs these steps:

1. Query the ONTAP nodes and aggregates of the selected site.
2. Create an SVM with the normalized project name.
3. Assign all available aggregates to the SVM.
4. Enable NVMe with `enabled=true` and `allowed=true`.
5. Wait until the SVM reaches the `running` state.
6. Create data LIFs named `datalif+0`, `datalif+1`, and so forth.
7. Create the management LIF named `managementlif`.
8. Create a shoot-specific ONTAP user and the seed Secret.

The data LIFs are distributed across the supplied ONTAP nodes using a modulo operation. The extension assigns the `default-data-nvme-tcp` service policy to them. The management LIF is placed on the first supplied node and uses `default-management`.

When ONTAP reports a BGP peer group, the extension creates the LIF as a `/32` VIP. Without a BGP peer group, it falls back to a `/24` netmask. In production, the project-specific LIFs are used as BGP-routed `/32` VIPs.

### 3b — Reconcile an Existing SVM

Existing state is not accepted without inspection. The extension checks the state and repairs the parts it can reconstruct safely:

| State                                    | Reconciliation behavior                                   |
|------------------------------------------|-----------------------------------------------------------|
| SVM is not `running`                     | Reconciliation fails with an error                        |
| NVMe is not enabled                      | Reconciliation fails with an error                        |
| A new aggregate is missing from the SVM  | The complete aggregate list is updated                    |
| An expected LIF is missing               | The LIF is created                                        |
| A LIF exists with a different IP         | The discrepancy is logged but not corrected automatically |
| The shoot user or seed Secret is missing | The missing state is reconstructed                        |

This behavior makes the controller idempotent: a subsequent reconciliation should not create a second set of resources blindly, but inspect and complete the existing state.

---

## User and Credential Lifecycle

The SVM is project-specific, while the user is shoot-specific. Multiple shoots can therefore share one SVM without sharing Trident credentials.

The username is derived from the shoot namespace. It must not begin with a hyphen and is limited to 25 characters. The ONTAP account is created with these properties:

- role `vsadmin`,
- application `http`,
- authentication method `password`,
- account unlocked,
- owner set to the UUID of the project SVM.

The corresponding Secret is stored in the seed namespace `kube-system`. Its name combines the project SVM and shoot namespace. The extension subsequently transfers the credentials into the shoot's `kube-system` namespace as a separate `ManagedResource`.

| ONTAP user | Seed Secret               | Action                                               |
|------------|---------------------------|------------------------------------------------------|
| missing    | missing                   | Generate a password, then create the user and Secret |
| missing    | present                   | Create the user with the password from the Secret    |
| present    | missing or password empty | Reset the password and restore the Secret            |
| present    | present                   | Accept the state                                     |

> **Known limitation:** If both the user and Secret exist, the current implementation does not verify whether their passwords actually match.

---

## Trident as ManagedResources

The extension does not execute an external Helm command in the shoot. It reads embedded YAML resources, substitutes project-specific values, and creates Gardener `ManagedResource` objects from them.

| ManagedResource       | Content                                                           | `keepObjects` |
|-----------------------|-------------------------------------------------------------------|:-------------:|
| `snapshot-crds`       | Kubernetes VolumeSnapshot CRDs                                    |      Yes      |
| `trident-crds`        | Trident CustomResourceDefinitions                                 |      Yes      |
| `snapshot-controller` | Kubernetes snapshot controller                                    |      Yes      |
| `trident-backends`    | `TridentBackendConfig`, StorageClasses, and `VolumeSnapshotClass` |      Yes      |
| `trident-svm-secret`  | Shoot-specific SVM credentials                                    |      No       |
| `trident-cwnp`        | `allow-to-ontap` network policy                                   |      No       |
| `trident-init`        | Trident operator and `TridentOrchestrator`                        |      Yes      |

The backend configuration uses:

```yaml
spec:
  storageDriverName: ontap-san
  sanType: nvme
  managementLIF: <project-management-lif>
  credentials:
    name: <shoot-svm-secret>
```

The two backend pools are distinguished by the `luks` label. `ontap-gold` selects `luks=false`, while `ontap-encrypted` selects `luks=true`.

### Network Policy

If the `firewall` namespace exists in the shoot, the extension creates a `ClusterwideNetworkPolicy` named `allow-to-ontap`:

- management LIF `/32` on TCP 443,
- every data LIF `/32` on TCP 4420.

If the namespace does not exist, this resource is skipped. A later connection failure must therefore always be evaluated together with the shoot's effective firewall configuration.

---

## Shoot Webhook and Node Preparation

Trident can connect an NVMe/TCP volume only when the `nvme-tcp` kernel module is available on the worker. The shoot webhook therefore mutates the `kube-system/trident-node-linux` DaemonSet:

- sets `DNSPolicy` to `Default`,
- adds a privileged `init-nvme-tcp` init container,
- mounts `/lib/modules` from the host,
- runs `modinfo nvme-tcp` and, when necessary, `modprobe nvme-tcp`,
- instructs Gardener to wait for `csi.trident.netapp.io` through the `node.gardener.cloud/wait-for-csi-node-trident` annotation,
- adds Gardener labels for critical node components and for DNS and API access.

If loading the module fails, the Trident node pod does not start successfully. This is a node preparation failure, not an ONTAP provisioning failure.

---

## Delete Lifecycle and Responsibility Boundary

When a Gardener `Extension` is deleted, the current controller removes its `ManagedResource` objects and removes the webhook resource last. `ForceDelete` and `Migrate` do not perform ONTAP-side cleanup.

The following are not deleted automatically:

- the project SVM and MetroCluster sync destination,
- management and data LIFs,
- SVM users,
- ONTAP volumes and snapshots,
- IP addresses reserved through the Cloud API or Metal API.

This boundary prevents deletion of one shoot from destroying the shared project SVM used by other shoots. Complete project deletion requires a separate project-level cleanup procedure.

---

## State Inspection

### In the Shoot

```bash
kubectl -n kube-system get torc,tridentbackendconfig,tridentbackend
kubectl -n kube-system get pods -l app.kubernetes.io/name=trident
kubectl get storageclass ontap-gold ontap-encrypted
kubectl get volumesnapshotclass ontap-snapshot
kubectl -n firewall get clusterwidenetworkpolicy allow-to-ontap
```

### In the Seed

```bash
kubectl get extension -A | grep ontap
kubectl get managedresource -A | grep -E 'trident|ontap'
kubectl -n <extension-namespace> logs deploy/gardener-extension-ontap
kubectl -n kube-system get secret -l app.kubernetes.io/part-of=gardener-extension-ontap
```

### Common Failure Chains

| Symptom                                  | Inspection chain                                                                        |
|------------------------------------------|-----------------------------------------------------------------------------------------|
| Backend does not become online           | Shoot Secret → management LIF TCP 443 → SVM state → user → Trident logs                 |
| PVC remains `Pending`                    | StorageClass selector → backend state → aggregates/capacity → controller events         |
| Mount fails                              | Trident node pod → `nvme-tcp` → data LIF TCP 4420 → NVMe session → optional LUKS Secret |
| Reconciliation reports a LIF discrepancy | ProviderConfig IP → LIF name → ONTAP IP; the discrepancy is not currently repaired      |

---

## Known Technical Limitations

- The REST clients disable TLS certificate verification.
- The extension uses administrative ONTAP cluster credentials instead of a dedicated least-privilege account.
- An existing SVM without NVMe enabled is not corrected automatically.
- An existing LIF with the wrong IP is only logged.
- Password consistency between ONTAP and the seed Secret is not actively verified.
- Extension deletion does not include ONTAP or project cleanup.
- Monitoring and alerting for SVM, LIF, and Trident state are not part of the controller.

---

## Source Code Map

| Topic                             | Path in `gardener-extension-ontap`   |
|-----------------------------------|--------------------------------------|
| Reconcile, restore, and delete    | `pkg/controller/ontap/actuator.go`   |
| SVM creation and state inspection | `pkg/trident/svm.go`                 |
| SVM users and seed Secrets        | `pkg/trident/user.go`                |
| Trident ManagedResources          | `pkg/trident/deploy_trident.go`      |
| Backend and StorageClasses        | `charts/trident/resources/backends/` |
| Shoot network policy              | `charts/trident/resources/cwnps/`    |
| Shoot webhook                     | `pkg/webhook/shoot/mutator.go`       |
| ProviderConfig API                | `pkg/apis/ontap/v1alpha1/types.go`   |
| Controller configuration          | `pkg/apis/config/v1alpha1/types.go`  |

---

> Back to the [`documentation index`](README.md) · ONTAP concepts: [`ontap-storage.md`](ontap-storage.md)
