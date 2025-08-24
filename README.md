# Overview (big picture)

High-level overview of how it should work.

---

## 1. Components diagram
A static view of the main Kubernetes actors and how they interact.

![K8s Components Overview](resources/k8-flow-overview.svg)

---

## 2. Sequence diagram
A simple “happy path” scenario:  
*Create a Deployment with N replicas → Freeze Deployment → Unfreeze Deployment after some time.*

> ⚠️ For simplicity, this diagram omits ReplicaSets and HPAs.

![K8s Sequence Diagram](resources/k8-sequence-diagram.svg)

---

## 3. Space identification: what exactly we are doing

We are lucky that Kubernetes architecture is already defined 😁  
The core idea is **observation of CREATE / DELETE / EDIT actions** on Kubernetes objects stored in `etcd`.

### VERY SIMPLY:
- Our custom DeploymentFreezer Controller changes Kubernetes objects **via the API Server**.  
- The API Server persists these changes in `etcd`. 
- And then the Kubernetes ecosystem (built-in controllers, scheduler, kubelets) does the appropriate work to apply them in the cluster.

---

## 4. DeploymentFreezer CRD Specification

### Resource Structure

```yaml
apiVersion: freeze.dev/v1
kind: DeploymentFreezer
metadata:
  name: freeze-web
  namespace: shop
spec:
  targetRef:
    name: web          
  durationSeconds: 600 
status:
  phase: Frozen
  observedGeneration: 3
  targetRef:
    name: web
    uid: 7c3d8c1f-...  
  originalReplicas: 6
  freezeUntil: "2025-08-24T18:45:12Z"
  conditions:
    - type: Ownership
      status: "True"
      reason: "Acquired"
      message: "DFZ freeze-web owns Deployment shop/web"
      lastTransitionTime: "2025-08-24T18:00:01Z"
```

| Field                         | Type              | Description                                                                                                            |
| ----------------------------- | ----------------- | ---------------------------------------------------------------------------------------------------------------------- |
| **spec.targetRef.name**       | string            | Name of the target Deployment (must be in the same namespace as this CR).                                              |
| **spec.durationSeconds**      | integer           | Duration of the freeze in seconds. After this period, the operator will unfreeze the Deployment.                       |
| **status.phase**              | string            | High-level lifecycle summary (see [Phase Values](#phase-values)).                                                      |
| **status.observedGeneration** | integer           | Last `metadata.generation` of the CR spec observed by the operator.                                                    |
| **status.targetRef.name**     | string            | Cached name of the target Deployment.                                                                                  |
| **status.targetRef.uid**      | string            | UID of the Deployment when the freeze began (detects if the Deployment was deleted and recreated under the same name). |
| **status.originalReplicas**   | integer           | Number of replicas of the target Deployment before freezing.                                                           |
| **status.freezeUntil**        | RFC3339 timestamp | Absolute time when the Deployment should be unfrozen.                                                                  |
| **status.conditions\[]**      | array             | Fine-grained condition objects representing current state (see [Conditions](#conditions)).                             |

### Phase Values
| Value   | Meaning                                                                                     |
| ------- | ------------------------------------------------------------------------------------------- |
| Pending | CR accepted; operator has not yet acted.                                                    |
| Freezing | Deployment is being scaled down.                                                            |
| Frozen  | Deployment is fully frozen (replicas=0) until `freezeUntil`.                                |
| Unfreezing | Deployment is being restored to its original replica count.                                 |
| Completed | Freeze/unfreeze cycle finished successfully.                                                |
| Denied  | Operator refused action (e.g., Deployment already frozen, not found, or multiple freezers). |
| Aborted | Operator stopped due to ownership loss, deletion, or unrecoverable error.                   |

### Conditions
```yaml
- type: <string>             # category of fact
  status: "True|False|Unknown"
  reason: <CamelCaseString>  # short, enum-like reason
  message: <string>          # human-readable explanation
  lastTransitionTime: <RFC3339>
```

| Field        | Type              | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| ------------ | ----------------- |------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| type         | string            | Category of condition. Possible values:<br>• **`TargetFound`** – target Deployment existence/UID check<br>• **`Ownership`** – CR’s lock/ownership status on target<br>• **`FreezeProgress`** – progress of scaling down<br>• **`UnfreezeProgress`** – progress of scaling back up<br>• **`Health`** – reconciliation health<br>• **`SpecChangedDuringFreeze`** – spec/template change detected during freeze                                                                                                     |
| status       | string            | Current state of the condition:<br>• **`"True"`** – condition is satisfied.<br>• **`"False"`** – condition is not satisfied.<br>• **`"Unknown"`** – operator cannot determine the state.                                                                                                                                                                                                                                                                                                                         |
| reason       | string            | Short, CamelCase identifier describing why the condition has its current status. Possible values:<br>• **TargetFound:** `Found`, `NotFound`, `UIDMismatch`<br>• **Ownership:** `Acquired`, `DeniedAlreadyFrozen`, `Lost`, `Released`<br>• **FreezeProgress:** `ScalingDown`, `ScaledToZero`, `AwaitingPDB`<br>• **UnfreezeProgress:** `ScalingUp`, `ScaledUp`, `QuotaExceeded`, `PartialRestore`<br>• **Health:** `Normal`, `Degraded`, `APIConflict`, `RBACDenied`<br>• **SpecChangedDuringFreeze:** `Observed` |
| message      | string            | Human-readable explanation for the condition (intended for users/operators).                                                                                                                                                                                                                                                                                                                                                                                                                                     |
| lastTransitionTime | RFC3339 timestamp | Time when this condition last changed status.                                                                                                                                                                                                                                                                                                                                                                                                                                                                    |
