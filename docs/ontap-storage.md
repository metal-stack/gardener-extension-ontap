# ONTAP Storage

> How NetApp ONTAP works as a MetroCluster and how it is provided to Kubernetes through Gardener and Trident.

---

## Context

ONTAP is the platform's persistent, network-attached block storage system. Kubernetes workers do not hold the volume locally. They connect to a block device in the ONTAP system over NVMe/TCP.

This is the essential difference from local LVM storage:

| Property | ONTAP | Local LVM |
|----------|-------|-----------|
| Storage location | External storage system | Local worker disk |
| Access | NVMe/TCP over the network | Directly on the node |
| Node loss | Data remains in the storage system | Local data can be lost with the node |
| Site protection | Synchronous MetroCluster replication | No cross-site protection |
| Kubernetes integration | NetApp Trident CSI | CSI Driver LVM |

This page explains ONTAP and the end-to-end data path. The automation is documented separately in [`gardener-extension-ontap.md`](gardener-extension-ontap.md).

---

## ONTAP Concepts and Object Model

ONTAP separates physical capacity, tenants, network endpoints, and the volumes used by Kubernetes. The following objects are important for understanding the platform:

| Term | Meaning |
|------|---------|
| **Node / controller** | Physical ONTAP instance that provides disks, network ports, and storage services |
| **HA pair** | Two controllers at one site that can take over for each other if a node fails |
| **ONTAP cluster** | Administrative grouping of the local HA pair |
| **MetroCluster** | Cross-site grouping of two ONTAP clusters with synchronous replication |
| **Aggregate** | ONTAP capacity pool built from the physical disks of a node |
| **SVM** | Storage Virtual Machine; the logical and administrative tenant boundary for a Metal project |
| **Volume** | Logical storage area within an SVM that draws capacity from its assigned aggregates |
| **LIF** | Logical Interface; an IP-based SVM interface for management or data access |
| **NVMe subsystem / namespace** | ONTAP objects through which a block device is made available to an NVMe host |
| **Trident backend** | Connection between Trident in the shoot and the project-specific SVM |

The simplified hierarchy is:

```text
MetroCluster
├── ONTAP cluster, site A
│   ├── Controller A
│   ├── Controller B
│   └── Aggregates
└── ONTAP cluster, site B
    ├── Controller A
    ├── Controller B
    └── Aggregates

Project SVM
├── Management LIF
├── Data LIF 1
├── Data LIF 2
├── Volumes
└── NVMe configuration
```

An SVM is not separate hardware and does not own exclusive disks. It is a logical view of the cluster resources assigned to it. In this integration, an SVM belongs to a Metal project and can be used by multiple shoots in that project.

---

## Physical Topology

A MetroCluster consists of two ONTAP clusters at separate sites:

| Location | Topology | Role |
|----------|----------|------|
| Site A | Two ONTAP controllers as a local HA pair | One MetroCluster site |
| Site B | Two ONTAP controllers as a local HA pair | Second MetroCluster site |
| Third failure domain | Optional ONTAP Mediator | Quorum and switchover support |

The controllers at a site are connected through a local cluster interconnect. The two sites communicate through redundant inter-site links. These links carry synchronous replication and cross-site MetroCluster metadata. Bandwidth, switch models, and specific network segments depend on the deployment.

Several management and access networks exist separately:

- controller and cluster management for administration and the ONTAP REST API,
- intercluster communication between the storage systems,
- BGP-routed SVM LIFs for Trident and the Kubernetes workers,
- BMC access for out-of-band administration.

This separation is important: Kubernetes data traffic does not use the global cluster management addresses, and MetroCluster replication does not pass through the shoot cluster.

---

## How ONTAP MetroCluster Works

A MetroCluster spans two sites. At each site, two controllers form a local HA pair. MetroCluster replicates data synchronously between the sites.

| Layer | Responsibility |
|-------|----------------|
| **Local HA pair** | Handles the failure of a single controller. |
| **MetroCluster between sites** | Maintains a synchronous copy of the data at the second site. |
| **ONTAP Mediator** | Supports the switchover decision if an entire site fails. |

A project-specific Storage Virtual Machine (SVM) is active at one site. The corresponding MetroCluster sync destination with the `-mc` suffix exists at the other site. Applications do not write to both sides simultaneously: they access the active SVM while ONTAP performs synchronous replication to the other side.

### Local HA and Cross-Site Replication

Local HA and MetroCluster address different failure classes:

- If one controller fails, its partner in the local HA pair takes over.
- If an entire site fails, the previously passive MetroCluster site can take over the storage functions.
- Until a switchover occurs, exactly one side of the SVM remains active and the other side remains the sync destination.

A successful synchronous write has reached both MetroCluster sites. For replicated writes, the architecture therefore targets an RPO of 0: an acknowledged write should not be missing after the loss of a site. This protects the stored blocks, but it does not replace recovery of the Kubernetes and network paths.

### ONTAP Mediator

The ONTAP Mediator runs separately from the two storage sites. It observes both sides and supports the decision to perform an automatic switchover when an entire site fails. This helps prevent both sides from becoming active simultaneously.

The Mediator does not transfer application data and is not in the I/O path. If the Mediator is unavailable, some failure scenarios can require a manual switchover.

The active MetroCluster site depends on the current state. When creating a new project SVM, the Gardener extension selects the ONTAP cluster with the lower current total number of volumes.

---

## Failure Behavior

Three separate layers must work together during failover:

1. ONTAP performs controller takeover or a MetroCluster switchover.
2. The management and data LIFs must be reachable through the network at the active site.
3. Trident and the Linux NVMe/TCP stack reconnect the volumes in the shoot cluster.

MetroCluster protects the data and makes it available at the second site. Kubernetes, Trident, and the network then restore workload access.

| Failure | Primary response | What matters next |
|---------|------------------|-------------------|
| Single ONTAP controller | Local partner takes over | LIF and NVMe/TCP connections must remain reachable |
| Entire storage site | MetroCluster switchover | BGP routing, LIF reachability, and Trident reconnect |
| Kubernetes worker | Workload starts on another worker | The new worker establishes an NVMe/TCP connection to the volume |
| Management path disruption | Existing I/O can continue independently | New volumes, expansion, and snapshot operations can fail |
| Data path disruption | Worker cannot reach the block device | Inspect the data LIF, TCP 4420, and NVMe session |

A switchover therefore does not guarantee an interruption-free application. ONTAP, routing, CSI reconnection, Kubernetes scheduling, and the application itself all contribute to the actual recovery time.

MetroCluster also does not switch automatically to the other site solely because latency has increased. Latency is an operational and diagnostic condition, not an automatic switchover trigger.

---

## SVMs as the Project Boundary

The SVM is the central handoff point between the platform and ONTAP:

- It contains the project-specific network endpoints.
- It has its own NVMe configuration.
- It has its own users and roles.
- Trident manages volumes only within this SVM.
- It can draw capacity from every aggregate assigned to it.

The Gardener extension assigns all available aggregates at the selected ONTAP site to a new SVM. The aggregates remain cluster resources and are not reserved exclusively for the project. If another aggregate is added later, reconciliation can add it to the SVM assignment.

### Project-Specific, Not Shoot-Specific

The Cloud API reserves LIF addresses for the Metal project. The extension also derives the SVM name from the project ID. The resulting model is:

```text
Metal project
    |
    +--> one project SVM
    |      +--> one management LIF
    |      +--> two data LIFs
    |
    +--> Shoot A --> dedicated SVM user + dedicated Trident backend
    |
    +--> Shoot B --> dedicated SVM user + dedicated Trident backend
```

This model avoids creating an SVM for every shoot. Their lifecycles are not identical, however: a shoot can be deleted while another shoot still requires the project SVM.

---

## How We Use ONTAP

ONTAP is not provisioned directly for each Kubernetes PVC. Instead, an automated chain connects the project configuration to the mounted block device:

```text
Metal project
    |
    v
1 management LIF + 2 data LIFs
    |
    v
Gardener extension in the seed
    |
    +--> project-specific SVM in ONTAP
    |
    +--> Trident + backend + StorageClasses in the shoot
             |
             v
        Application PVC
             |
             v
      ONTAP volume over NVMe/TCP
```

### Project-Specific SVM

The Cloud API reserves three BGP-routed `/32` addresses for an ONTAP-enabled project:

- one management LIF for ONTAP operations,
- two data LIFs for NVMe/TCP traffic.

The Gardener extension reads the project ID and these IP addresses from the shoot configuration. If the SVM is missing, the extension creates it, enables NVMe, assigns the ONTAP aggregates, and creates the LIFs. If the SVM already exists, the extension inspects and completes its state.

The SVM belongs to the Metal project and can be used by multiple ONTAP-enabled shoot clusters in that project. The ONTAP user and corresponding credential Secret are shoot-specific.

This separation provides two security boundaries:

- The project determines which SVM contains its volumes.
- Each shoot receives only its own Trident credentials.

The complete reconciliation and credential logic is described in [`Gardener Extension for ONTAP`](gardener-extension-ontap.md).

### Trident in the Shoot Cluster

The extension deploys NetApp Trident and the required resources into the shoot as Gardener `ManagedResource` objects. These include:

- the Trident operator, controller, and node plugin,
- `TridentBackendConfig` for the project-specific SVM,
- the shoot-specific credential Secret,
- the `ontap-gold` and `ontap-encrypted` StorageClasses,
- snapshot resources,
- a network policy for the management and data LIFs when the `firewall` namespace exists.

The backend uses the Trident `ontap-san` driver with `sanType: nvme`. A shoot webhook also prepares the Trident node DaemonSet so the `nvme-tcp` kernel module is available on every worker.

### Management and Data Paths

Runtime communication is deliberately separated:

| Source | Destination | Protocol | Purpose |
|--------|-------------|----------|---------|
| Gardener extension in the seed | ONTAP cluster management | HTTPS / TCP 443 | Manage SVMs, LIFs, aggregates, and users |
| Trident controller in the shoot | SVM management LIF | HTTPS / TCP 443 | Manage volumes and snapshots |
| Kubernetes worker | SVM data LIFs | NVMe/TCP / TCP 4420 | Workload block I/O |

The management LIF does not carry application data. Conversely, the data LIFs are not used for administrative ONTAP operations. This separation is also the most important starting point for troubleshooting.

### Why One Management LIF and Two Data LIFs?

The management LIF provides a stable, project-specific API endpoint for Trident. Trident therefore does not need to use the global ONTAP cluster administrative addresses.

The data LIFs provide the SVM's NVMe/TCP endpoints. Two LIFs permit multiple network paths and distribute the endpoints across the ONTAP nodes. They are not a substitute for MetroCluster: LIF redundancy protects the access path, while MetroCluster protects the storage site and data copy.

In production, the three SVM LIFs are announced through BGP as `/32` VIPs. The Trident controller and workers can therefore reach the currently routed endpoints without knowing the internal ONTAP management networks at either site.

### Which Component Uses Which Address?

- The Gardener extension connects to the ONTAP cluster management addresses because it manages privileged objects such as SVMs and users.
- Trident connects to the project-specific management LIF because it manages only that SVM's backend.
- The worker connects to the data LIFs because the NVMe/TCP I/O terminates there.

If a PVC cannot be created, inspect the management path first. If the volume exists but cannot be mounted, focus on the data path and the node.

### From a PVC to a Volume

1. An application creates a PVC with `storageClassName: ontap-gold` or `ontap-encrypted`.
2. Kubernetes calls the Trident CSI controller.
3. Trident selects the appropriate storage pool and creates the volume through the SVM management LIF.
4. The Trident node plugin connects the worker to a data LIF over NVMe/TCP.
5. The block device is made available on the worker, formatted with `ext4`, and mounted into the pod.
6. With `ontap-encrypted`, the device is additionally opened with LUKS2 before it is mounted.

Kubernetes ultimately sees a `PersistentVolume`, and the pod sees a regular mount. Trident and ONTAP encapsulate the SVM, ONTAP volume, NVMe configuration, and session details.

A simple unencrypted PVC looks like this:

```yaml
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: app-data
spec:
  storageClassName: ontap-gold
  accessModes:
    - ReadWriteOnce
  resources:
    requests:
      storage: 10Gi
```

| StorageClass | Use |
|--------------|-----|
| `ontap-gold` | Thin-provisioned block storage with `ext4`; volume expansion is allowed. |
| `ontap-encrypted` | Client-side LUKS2-encrypted block storage; the application provides the passphrase Secret. |

> **Important:** With `ontap-encrypted`, ONTAP sees only encrypted blocks. If the LUKS Secret is lost, the data cannot be recovered.

### Encrypted Storage

The passphrase is stored in a Secret named `storage-encryption-key` in the PVC namespace:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: storage-encryption-key
  namespace: application
stringData:
  luks-passphrase-name: key-a
  luks-passphrase: <passphrase>
```

The StorageClass references this Secret for node-stage and node-expand operations. Encryption takes place on the Kubernetes worker:

```text
Application
    |
    v
ext4 file system
    |
    v
LUKS2 device
    |
    v
NVMe/TCP
    |
    v
ONTAP sees encrypted blocks
```

Secret backup, access control, and tested passphrase rotation therefore belong to the application lifecycle rather than the ONTAP MetroCluster lifecycle.

### Volume Expansion and Snapshots

Both StorageClasses allow volume expansion. During expansion, Trident first enlarges the backend volume. The block device, LUKS layer when applicable, and file system are then expanded on the worker.

The extension also provides the `ontap-snapshot` `VolumeSnapshotClass`. Its `deletionPolicy` is `Delete`: deleting the Kubernetes snapshot object also deletes the corresponding backend snapshot.

Snapshots and MetroCluster replication solve different problems. MetroCluster protects against storage site failure. A snapshot provides an earlier data state within the storage lifecycle.

---

## The Most Important Operational Boundary

Deleting a shoot cluster does not automatically remove the project-specific SVM, its LIFs, or the ONTAP volumes. This is intentional because other shoots in the project can still use the SVM. Complete project cleanup must therefore happen separately and in a controlled order.

A safe project cleanup must answer at least these questions:

1. Does an ONTAP-enabled shoot still exist in the project?
2. Are any volumes or snapshots still required?
3. Can the project SVM, including its NVMe configuration and LIFs, be removed?
4. Has the MetroCluster sync destination also been cleaned up?
5. Can the three project-specific IP addresses then be released?

IP addresses must not be released while LIFs with those addresses still exist in ONTAP. Otherwise, orphaned ONTAP objects can block later reuse of an address.

---

## Summary

- MetroCluster replicates data synchronously between two sites; local HA pairs additionally protect against controller failures.
- Each Metal project has one ONTAP SVM with one management LIF and two data LIFs.
- The Gardener extension manages the ONTAP-side and shoot-side resources.
- Trident translates Kubernetes PVCs into ONTAP volumes.
- Management uses HTTPS on port 443; block I/O uses NVMe/TCP on port 4420.

---

> Back to the [`documentation index`](README.md) · Implementation details: [`gardener-extension-ontap.md`](gardener-extension-ontap.md)
