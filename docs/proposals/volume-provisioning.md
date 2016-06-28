<!-- BEGIN MUNGE: UNVERSIONED_WARNING -->

<!-- BEGIN STRIP_FOR_RELEASE -->

<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">
<img src="http://kubernetes.io/img/warning.png" alt="WARNING"
     width="25" height="25">

<h2>PLEASE NOTE: This document applies to the HEAD of the source tree</h2>

If you are using a released version of Kubernetes, you should
refer to the docs that go with that version.

Documentation for other releases can be found at
[releases.k8s.io](http://releases.k8s.io).
</strong>
--

<!-- END STRIP_FOR_RELEASE -->

<!-- END MUNGE: UNVERSIONED_WARNING -->

## Abstract

Real Kubernetes clusters have a variety of volumes which differ widely in
size, iops performance, retention policy, and other characteristics.
Administrators need a way to dynamically provision volumes of these different
types to automatically meet user demand.

A new mechanism called 'storage classes' is proposed to provide this
capability.  Storage classes build upon the label selection feature added in
Kubernetes 1.3.

## Motivation

In Kubernetes 1.2, an alpha form of limited dynamic provisioning was added
that allows a single volume type to be provisioned in clouds that offer
special volume types.

In Kubernetes 1.3, a label selector was added to persistent volume claims to
allow administrators to create a taxonomy of volumes based on the
characteristics important to them, and to allow users to make claims on those
volumes based on those characteristics.  This allows flexibility when claiming
existing volumes; the same flexibility is needed when dynamically provisioning
volumes.

After gaining experience with dynamic provisioning after the 1.2 release, we
want to create a more flexible feature that allows configuration of how
different storage classes are provisioned and supports provisioning multiple
types of volumes within a single cloud.

## Constraints and Assumptions

The proposed design should:

1.  Build upon the label selection capabilities added in Kubernetes 1.3
2.  Not interfere with existing semantics of manually-created volumes
3.  Not require users to know or understand the differences between
    volumes (ie, Kubernetes should not dictate any particular set of
    characteristics to administrators to think in terms of)

This proposal will **not** deal with out-of-tree provisioners, but we should
understand that they are desired by the community, and so we should be careful
not to make them impossible to implement in the future.

## Use Cases

1.  As an administrator, I want to be able to provision multiple configurations
    of various volume types within a single cluster, and to parameterize and
    configure the particulars of what is provisioned

### Use Case: Define how volumes are provisioned

Currently, there is an alpha version of dynamically provisioning, which allows
provisioning of a **certain, single** volume type in clouds which offer
natively supported persistent volumes.  For example, in clusters running on
AWS, it is possible to provision AWS EBS volumes dynamically.  Real clusters
require additional flexibility, such as the ability to differentiate different
classes of storage to be provisioned, and the ability to provision volumes of
different types.

Using the new label selection feature for persistent volumes will allow
storage classes to function as an extension of the existing API.  Storage
classes should be matched with persistent volume claims using the same
selector field as volumes, if no volumes are available that match a claim's
selector.

#### Multiple volume types

Many administrators want the ability to dynamically provision multiple volume
types (meaning, volumes that use different types of plugin) in the same
cluster.  Some examples:

1.  An administrator hosts their cluster in AWS and wants to provision EBS
    volumes and GlusterFS volumes using a Gluster Virtual Storage Appliance
    running in AWS
2.  An administrator hosts their cluster in OpenStack and wants to provision
    Cinder and NFS volumes

In order to do this, the identity of the provisioner must be a property of the
storage class (as opposed to implicit, as it is in the alpha feature).

#### Parameterizing provisioners

It will be common for a cluster to have multiple storage classes which
leverage the same provisioner to create volumes with different characteristics
for the different classes.  For example, an administrator running a cluster
hosted in AWS might want to have two storage classes, `slow` and `fast`, that
both leverage the same EBS provisioner to provision spinning disk volumes and
provisioned-iops volumes, respectively.  From this, it follows that:

1.  Storage classes should be able to hold a set of provisioner parameters
2.  The provisioner should be passed the claim and the details of the storage
    class it is provisioning for

#### Out-of-tree provisioners

One of our goals is to enable administrators to create out-of-tree
provisioners, that is, provisioners whose code does not live in the Kubernetes
project.  Our experience since the 1.2 release with dynamic provisioning has
shown that it is impossible to anticipate every aspect and manner of
provisioning that administrators will want to perform.  The proposed design
should not prevent future work to allow out-of-tree provisioners.

## Analysis

### Summary of alpha functionality

The alpha functionality for dynamic provisioning works as follows:

1.  A cluster has only a single provisioner at a time, dictated by the cloud
    provider
2.  When a new claim is submitted, the controller checks for annotation
    `volume.alpha.kubernetes.io/storage-class`. If present in the claim, the
    provisioner is invoked to provision a volume to fill that claim regardless
    whether there is already existing volume that could satisfy the claim
    without provisioning
3.  All provisioners are in-tree; they implement an interface called
    `ProvisionableVolumePlugin` which has a method, `NewProvisioner()`,
    that returns a new `Provisioner` instance
4.  The controller calls the provisioner's `Provision()` method; `Provision`
    is responsible for provisioning the volume in the cloud provider and
    returns an `api.PersistentVolume` and an error
5.  If `Provision` returns an error, the controller stops trying to provision
6.  If `Provision` returns no error, the controller creates the returned
    `api.PersistentVolume`, already bound to the claim
  1.  If the create operation fails, it is retried
  2.  If the create operation never succeeds, the controller attempts to delete
      the provisioned volume and creates an event on the claim

### When should we provision volumes?

It is sensible to provision volumes only when an existing volume cannot
satisfy a claim.  This ensures that existing, already allocated resources are
used before additional resources are allocated.  In this regard, the existing
logic need to be changed to look for available volume first and to provision
a new volume only if no available matches the claim. The primary
difference is that in the existing design, there is no way to parameterize
provisioners or have more than one provisioner per cluster.  Therefore, once
we know that we have to provision a volume to satisfy a claim, we need to
determine which storage class to use for the claim.

### How should a claim be matched to a storage class?

We've discussed so far that volume claims and storage classes will be matched
using the claim's label selector.  With that said, there are a number of small
details to think about:

1.  What should happen if there are multiple storage classes that match a
    claim's selector?
2.  Should we provide a way to specify a default storage class for claims
    without a selector
3.  Should we provide a way to specify a default storage class for claims
    whose selector does not match any storage classes?

For now, we will think of the problem of matching a claim to a storage class
as a black box that solves the problem of: _given a claim and a list of
storage classes that match that claim's selector, find a storage class to
provision a volume for the claim with_.  Initially, we will implement the
following behavior for this black box:

1.  Allow cluster administrator to specify a default storage class
2.  If multiple storage classes match the claim's selector and the default one
    is among them, use the default one. This allows claims without any selector
    to be provisioned by the default storage class
3.  If multiple storage classes match the claim's selector and the default one
    is **not** among them, choose a storage class at random
2.  If no storage class matches the claim's selector, no volume is provisioned
    and the claim remains in `Pending` phase until a new matching storage class
    or avaliable volume appears.

### Controller workflow for provisioning volumes

The workflow for provisioning does not need to change much to use storage
classes.  The new workflow will be:

1.  When a new claim is submitted, the controller attempts to find an existing
    volume that will fulfill the claim.  If a suitable volume is found, the
    controller binds the claim and volume together and carries on normally.
2.  If no volume is found for the claim, the controller will attempt to
    determine a storage class for the volume
3.  If no storage class is found, the controller goes back to step 1. and
    periodically retries finding a matching volume or storage class again
    until a match is found. The claim is `Pending` during this period
4.  All provisioners are in-tree; they implement an interface called
    `ProvisionableVolumePlugin`, which has a method called `NewProvisioner`
    that returns a new provisioner.
5.  The provisioner's `Provision` method has the same responsibility as it
    does now, but it is now passed both the claim and storage class as
    parameters
6.  If `Provision` returns an error, the controller goes back to step 1., i.e.
    it will retry provisioning periodically
7.  If `Provision` returns no error, the controller creates the returned
    `api.PersistentVolume`, already bound to the claim
  1.  If the create operation for the `api.PersistentVolume` fails, it is
      retried
  2.  If the create operation does not succeed in reasonable time, the
      controller attempts to delete the provisioned volume and creates an event
      on the claim

## Proposed Design

We propose that:

1.  A new API group called `storage` be created to hold the a `StorageClass`
    API resource
2.  The persistent volume controller be modified to dynamically provision
    volumes using storage classes
3.  The existing provisioner plugin implementations be modified to allow
    parameterization as appropriate via storage classes
4.  The existing alpha dynamic provisioning feature be phased out in the
    next release

### `StorageClass` API

A new API group should hold the API for storage classes, following the pattern
of autoscaling, metrics, etc.  To allow for future storage-related APIs, we
should call this new API group `storage`.

Storage classes will be represented by an API object called `StorageClass`:

```go
package storage

// StorageClass describes the parameters for a class of storage for
// which PersistentVolumes can be dynamically provisioned.
//
// StorageClasses are non-namespaced; the name of the storage class
// according to etcd is in ObjectMeta.Name.
type StorageClass struct {
  unversioned.TypeMeta `json:",inline"`
  ObjectMeta           `json:"metadata,omitempty"`

  // ProvisionerType indicates the type of the provisioner.
  ProvisionerType ProvisionerType `json:"provisionerType,omitempty"`

  // ProvisionerParameters holds the parameters for the provisioner that should
  // create volumes of this storage class.
  ProvisionerParameters map[string]string `json:"provisionerParameters,omitempty"`
}

// ProvisionerType describes the type of a provisioner
type ProvisionerType string

const (
  ProvisionerTypeAWSEBS ProvisionerType = "kubernetes.io/aws-ebs"
  ProvisionerTypeGCEPD  ProvisionerType = "kubernetes.io/gce-pd"
)
```

Storage classes are natural to think of as a global resource, since they:

1.  Align with PersistentVolumes, which are a global resource
2.  Are administrator controlled

Implementation tasks associated with adding a new API group:

1.  Add a new internal API at `pkg/apis/storage/types.go`
2.  Add generation of deep copy / type information
3.  Add API installation and validations
4.  Add generated clients
5.  Add `v1alpha1` API at `pkg/apis/storage/v1alpha1`
6.  Add `kubectl` tool support
7.  Add API group into API server

### Provisioner interface changes

The `Provisioner` interface will be modified so that `Provision` accepts new
parameters for storage class and claim:

```go
type Provisioner interface {
  Provision(claim api.PersistentVolumeClaim, storageClass storageapi.StorageClass) (api.PersistentVolume, error)
}
```

The existing provisioner implementations will be modified to implement this
new interface and become sensitive to parameters of storage classes.

### PV Controller Changes

The persistent volume controller will be modified to implement the new
workflow described in this proposal.  The changes will be limited to the
`provisionClaimOperation` method, which is responsible for invoking the
provisioner, and to favor existing volumes before provisioning a new one.

## Examples

Let's take a look at a few examples:

### AWS provisioners with distinct QoS and zones

This example shows four storage classes with "fast" and "slow" drives and
availability zones "zone1" and "zone2".

```
apiVersion: v1
kind: StorageClass
metadata:
  name: aws-fast-zone1
  labels:
    speed: fast
    zone: zone1
provisionerType: kubernetes.io/aws-ebs
provisionerParameters: "type=gp2&zone=us-east-1a"

apiVersion: v1
kind: StorageClass
metadata:
  name: aws-fast-zone2
  labels:
    speed: fast
    zone: zone2
provisionerType: kubernetes.io/aws-ebs
provisionerParameters: "type=gp2&zone=us-east-1b"

apiVersion: v1
kind: StorageClass
metadata:
  name: aws-slow-zone1
  labels:
    speed: slow
    zone: zone1
provisionerType: kubernetes.io/aws-ebs
provisionerParameters: "type=sc1&zone=us-east-1a"

apiVersion: v1
kind: StorageClass
metadata:
  name: aws-slow-zone2
  labels:
    speed: slow
    zone: zone2
provisionerType: kubernetes.io/aws-ebs
provisionerParameters: "type=sc1&zone=us-east-1b"
```

Kubernetes have been configured to use `aws-slow-zone1` as default:

```
kube-controller-manager --default-storage-class="aws-slow-zone1"
```

Assuming that there are no volumes available for binding, following claims are
evaluated:

* PVC with no `Selector`. All storage classes are matching this empty selector
  and `aws-slow-zone1` is used as it is the default one. The controller finds
  a provisioner that is responsible for provisioning `kubernetes.io/aws-ebs`
  volume type and invokes its `Provision` method, passing parameters
  `type=sc1&zone=us-east-1a` as opaque string. It's up to the provisioner
  to parse these values and provision appropriate volume or return an error.
  Any error is sent as event on the PVC so user can see what's going on.

* PVC with `Selector: MatchLables: zone=zone1`, i.e. requesting anything in
  `zone1`. Both `aws-slow-zone1` and `aws-fast-zone1` match. `aws-slow-zone1`
  is chosen as it is the default one.

* PVC with `Selector: MatchLables: zone=zone2`, i.e. requesting anything in
  `zone2`. Both `aws-slow-zone2` and `aws-fast-zone2` match and the controller
  **chooses one randomly**, as the default one is not among these.

* PVC with `Selector: MatchLables: zone=zone3`, i.e. requesting anything in
  `zone3`. No storage class matches and the PVC is `Pending` until someone
  creates a storage class with label `zone=zone3`.

<!-- BEGIN MUNGE: GENERATED_ANALYTICS -->
[![Analytics](https://kubernetes-site.appspot.com/UA-36037335-10/GitHub/docs/proposals/volume-provisioning.md?pixel)]()
<!-- END MUNGE: GENERATED_ANALYTICS -->
