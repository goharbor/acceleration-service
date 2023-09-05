# Garbage Collection

`Acceleration Service` has a garbage collector capable of removing resources
which are not used for a long time or unfrequently. The garbage collector consists
of leaseManager and leaseCache. The `acceld` has responsible for
ensuring that all resources(blobs) which are stored locally are held by
[leases](https://github.com/containerd/containerd/blob/main/docs/garbage-collection.md#what-is-a-lease)
at all times, else they will be cleared.

## What is leaseManager and leaseCache ?

`Acceleration Service` uses `leaseManager` and `leaseCache` to manager all leases
which are persisted in boltdb. The `leaseManager` will creat a new lease for the
new blob and delete leases of the blobs which should be cleared away.
The `leaseCache` will sync with the leases which are persisted in boltdb and provide
the lease that should be cleared in LFRU(LFU first, LRU second) order. The `acceld` will
init `leaseCache` when daemon started.

## Garbage Collection Policy

Garbage Collection can be triggered in two ways. Firstly, gc will be triggered after each
task completed. Secondly, gc has scheduled tasks every hourly since dameon started. GC clears
the blobs until local storage size below 80% of the threshold. Specially, scheduled gc tasks
clear the blobs until local storage size below 40% of the threshold.

## Garbage Collection Configuration

| Configuration | Example |Description |
|---|---|---|
| `gcpolicy.threshold` | "100MB" |size threshold that triggers GC, the `gc` will start reclaim blobs if exceeds the size. |