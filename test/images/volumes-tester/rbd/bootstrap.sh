#!/usr/bin/env bash

# Copyright 2015 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

#
# Bootstraps a CEPH server.
# It creates two OSDs on local machine, creates RBD pool there
# and imports 'block' device there.
#
# We must create fresh OSDs and filesystem here, because shipping it
# in a container would increase the image by ~300MB.
#

# Kubernetes must provide unique pool and image names.

POOL="$1"
IMAGE="$2"

function start()
{
    # Create /etc/ceph/ceph.conf
    sh ./ceph.conf.sh `hostname -i`

    # Configure and start ceph-mon
    sh ./mon.sh `hostname -i`

    # Configure and start 2x ceph-osd
    mkdir -p /var/lib/ceph/osd/ceph-0 /var/lib/ceph/osd/ceph-1
    sh ./osd.sh 0
    sh ./osd.sh 1

    # Configure and start cephfs metadata server
    sh ./mds.sh

    # Prepare a RBD poold and image (only with layering feature, the others may
    # require newer clients).
    # NOTE: we need Ceph kernel modules on the host that runs the client!
    ceph osd pool create "$POOL" 64
    rbd import --image-feature layering --dest-pool="$POOL" block "$IMAGE"

    # Prepare a cephfs volume
    ceph osd pool create cephfs_data 4
    ceph osd pool create cephfs_metadata 4
    ceph fs new cephfs cephfs_metadata cephfs_data
    # Put index.html into the volume
    # It takes a while until the volume created above is mountable,
    # 1 second is usually enough, but try indefinetily.
    sleep 1
    while ! ceph-fuse -m `hostname -i`:6789 /mnt; do
        echo "Waiting for cephfs to be up"
        sleep 1
    done
    echo "Hello Ceph!" > /mnt/index.html
    chmod 644 /mnt/index.html
    umount /mnt

    echo "Ceph is ready"
}

function stop()
{
    echo "Shutting down"
    # Remove CephFS
    killall ceph-mds
    ceph mds cluster_down
    ceph mds fail 0
    ceph fs rm cephfs --yes-i-really-mean-it
    ceph osd pool delete cephfs_data cephfs_data  --yes-i-really-really-mean-it
    ceph osd pool delete cephfs_metadata cephfs_metadata  --yes-i-really-really-mean-it

    # Remove CephRBD
    rbd rm --pool="$POOL" "$IMAGE"
    ceph osd pool rm "$POOL" "$POOL" --yes-i-really-really-mean-it
    killall ceph-osd
    killall ceph-mon
    echo "Ceph stopped"
}

trap stop TERM
start

while true; do
    sleep 1
done
