/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package node

import (
	"fmt"

	"k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/kubernetes/test/utils/image"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func preparePod(name string, node *v1.Node, propagation v1.MountPropagationMode, hostDir string) *v1.Pod {
	const containerName = "cntr"
	bTrue := true
	var oneSecond int64 = 1
	// The pod prepares /mnt/test/<podname> and sleeps.
	cmd := fmt.Sprintf("mkdir /mnt/test/%[1]s; sleep 3600", name)
	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			NodeName: node.Name,
			Containers: []v1.Container{
				{
					Name:    containerName,
					Image:   image.GetBusyBoxImage(),
					Command: []string{"sh", "-c", cmd},
					VolumeMounts: []v1.VolumeMount{
						{
							Name:             "host",
							MountPath:        "/mnt/test",
							MountPropagation: &propagation,
						},
					},
					SecurityContext: &v1.SecurityContext{
						Privileged: &bTrue,
					},
				},
			},
			Volumes: []v1.Volume{
				{
					Name: "host",
					VolumeSource: v1.VolumeSource{
						HostPath: &v1.HostPathVolumeSource{
							Path: hostDir,
						},
					},
				},
			},
			// speed up termination of the pod
			TerminationGracePeriodSeconds: &oneSecond,
		},
	}
	return pod
}

var _ = SIGDescribe("Mount propagation [Feature:MountPropagation]", func() {
	f := framework.NewDefaultFramework("mount-propagation")

	It("should propagate mounts to the host", func() {
		// This test runs two pods: master and slave with respective mount
		// propagation on common /var/lib/kubelet/XXXX directory. Both mount a
		// tmpfs to a subdirectory there. We check that these mounts are
		// propagated to the right places.

		// Pick a node where all pods will run.
		nodes := framework.GetReadySchedulableNodesOrDie(f.ClientSet)
		Expect(len(nodes.Items)).NotTo(BeZero(), "No available nodes for scheduling")
		node := &nodes.Items[0]

		// hostDir is the directory that's shared via HostPath among all pods.
		// Make sure it's random enough so we don't clash with another test
		// running in parallel.
		hostDir := "/var/lib/kubelet/" + f.Namespace.Name
		defer func() {
			cleanCmd := fmt.Sprintf("sudo rm -rf %q", hostDir)
			framework.IssueSSHCommand(cleanCmd, framework.TestContext.Provider, node)
		}()

		podClient := f.PodClient()
		master := podClient.CreateSync(preparePod("master", node, v1.MountPropagationBidirectional, hostDir))
		defer podClient.Delete(master.Name, nil)

		slave := podClient.CreateSync(preparePod("slave", node, v1.MountPropagationHostToContainer, hostDir))
		defer podClient.Delete(slave.Name, nil)

		// Check that the pods sees directories of each other. This just checks
		// that they have the same HostPath, not the mount propagation.
		podNames := []string{master.Name, slave.Name}
		for _, podName := range podNames {
			for _, dirName := range podNames {
				cmd := fmt.Sprintf("test -d /mnt/test/%s", dirName)
				_ = f.ExecShellInPod(podName, cmd)
			}
		}

		// Each pod mounts one tmpfs to /mnt/test/<podname> and puts a file there.
		for _, podName := range podNames {
			cmd := fmt.Sprintf("mount -t tmpfs e2e-mount-propagation-%[1]s /mnt/test/%[1]s; echo %[1]s > /mnt/test/%[1]s/file", podName)
			_ = f.ExecShellInPod(podName, cmd)

			// unmount tmpfs when the test finishes
			cmd = fmt.Sprintf("umount /mnt/test/%s", podName)
			defer f.ExecShellInPod(podName, cmd)
		}

		// The host mounts one tmpfs to testdir/host and puts a file there so we
		// can check mount propagation from the host to pods.
		cmd := fmt.Sprintf("sudo mkdir %[1]q/host; sudo mount -t tmpfs e2e-mount-propagation-host %[1]q/host; echo host > %[1]q/host/file", hostDir)
		err := framework.IssueSSHCommand(cmd, framework.TestContext.Provider, node)
		Expect(err).NotTo(HaveOccurred())

		defer func() {
			cmd := fmt.Sprintf("sudo umount %q/host", hostDir)
			framework.IssueSSHCommand(cmd, framework.TestContext.Provider, node)
		}()

		// Now check that mounts are propagated to the right containers.
		// expectedMounts is map of pod name -> expected mounts visible in the
		// pod.
		expectedMounts := map[string]sets.String{
			// Master sees only its own mount and not the slave's one.
			"master": sets.NewString("master", "host"),
			// Slave sees master's mount + itself.
			"slave": sets.NewString("master", "slave", "host"),
		}
		dirNames := append(podNames, "host")
		for podName, mounts := range expectedMounts {
			for _, mountName := range dirNames {
				cmd := fmt.Sprintf("cat /mnt/test/%s/file", mountName)
				stdout, stderr, err := f.ExecShellInPodWithFullOutput(podName, cmd)
				framework.Logf("pod %s mount %s: stdout: %q, stderr: %q error: %v", podName, mountName, stdout, stderr, err)
				msg := fmt.Sprintf("When checking pod %s and directory %s", podName, mountName)
				shouldBeVisible := mounts.Has(mountName)
				if shouldBeVisible {
					Expect(err).NotTo(HaveOccurred(), "failed to run %q", cmd, msg)
					Expect(stdout, Equal(mountName), msg)
				} else {
					Expect(err).To(HaveOccurred(), msg)
				}
			}
		}
		// Check that the mounts are/are not propagated to the host.
		// Host can see mount from master
		cmd = fmt.Sprintf("test `cat %q/master/file` = master", hostDir)
		err = framework.IssueSSHCommand(cmd, framework.TestContext.Provider, node)
		Expect(err).NotTo(HaveOccurred(), "host should see mount from master")

		// Host can't see mount from slave
		cmd = fmt.Sprintf("test ! -e %q/slave/file", hostDir)
		err = framework.IssueSSHCommand(cmd, framework.TestContext.Provider, node)
		Expect(err).NotTo(HaveOccurred(), "host shouldn't see mount from slave")
	})
})
