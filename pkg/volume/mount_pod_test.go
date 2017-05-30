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

package volume

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/kubernetes/pkg/api/v1"
)

const (
	mountNamespace  = "kube-mount"
	fakePluginName1 = "kubernetes.io/fake-plugin1"
	fakePluginName2 = "kubernetes.io/fake-plugin2"
	fakePluginName3 = "kubernetes.io/fake-plugin3"
)

func assertPlugins(mgr *mountPodManager, expectedPods map[string]map[types.UID]*v1.Pod) error {
	if !reflect.DeepEqual(expectedPods, mgr.pods) {
		return fmt.Errorf("Expected pods:\n%+v\ngot:\n%+v", expectedPods, mgr.pods)
	}

	// Check that GetPod returns the right pods for all plugins
	plugins := []string{fakePluginName1, fakePluginName2, fakePluginName3}
	for _, pluginName := range plugins {
		pod := mgr.GetPod(pluginName)
		if len(expectedPods[pluginName]) == 0 {
			// no pod for plugin, expect nil
			if pod != nil {
				return fmt.Errorf("Expected nil for %s, got %s", pluginName, pod.Name)
			}
		} else {
			// plugin has one or more plugins, expect one of them
			found := false
			expectedPodNames := []string{}
			for _, p := range expectedPods[pluginName] {
				expectedPodNames = append(expectedPodNames, p.Name)
				if p == pod {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("Expected %s for %s, got %s", strings.Join(expectedPodNames, ", "), pluginName, pod.Name)
			}
		}
	}
	return nil
}

func TestMountPodManager(t *testing.T) {
	pluginMgr := &VolumePluginMgr{
		plugins: map[string]VolumePlugin{
			fakePluginName1: nil,
			fakePluginName2: nil,
			fakePluginName3: nil,
		},
	}
	mgr := NewMountPodManager(pluginMgr, mountNamespace).(*mountPodManager)

	// Pod1 has mount utilities for plugin1
	pod1 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod1",
			Namespace: mountNamespace,
			Labels: map[string]string{
				"mount." + fakePluginName1: "true",
				"mount." + fakePluginName2: "false",
			},
			UID: "1",
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
		},
	}

	// Pod2 has mount utilities for plugin2
	pod2 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod2",
			Namespace: mountNamespace,
			Labels: map[string]string{
				"mount." + fakePluginName2: "true",
			},
			UID: "2",
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
		},
	}

	// Pod12 has mount utilities for plugin 1 and plugin2
	pod12 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod12",
			Namespace: mountNamespace,
			Labels: map[string]string{
				"mount." + fakePluginName1: "true",
				"mount." + fakePluginName2: "true",
			},
			UID: "12",
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
		},
	}

	// Pod3 has mount utilities for plugin1 and plugin2, but runs in a wrong
	// namespace
	pod3 := &v1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod12",
			Namespace: "xxx",
			Labels: map[string]string{
				"mount." + fakePluginName1: "true",
				"mount." + fakePluginName2: "true",
			},
			UID: "3",
		},
		Status: v1.PodStatus{
			Phase: v1.PodRunning,
		},
	}

	// Test1: All pods are running, all of them are registered
	mgr.AddPod(pod1)
	mgr.AddPod(pod2)
	mgr.AddPod(pod12)
	mgr.AddPod(pod3)

	expectedPods := map[string]map[types.UID]*v1.Pod{
		fakePluginName1: map[types.UID]*v1.Pod{
			pod1.UID:  pod1,
			pod12.UID: pod12,
		},
		fakePluginName2: map[types.UID]*v1.Pod{
			pod2.UID:  pod2,
			pod12.UID: pod12,
		},
	}
	if err := assertPlugins(mgr, expectedPods); err != nil {
		t.Errorf(err.Error())
	}

	// Test2: pod1 is stopped, pod2 and pod12 remain
	pod1.Status.Phase = v1.PodSucceeded
	mgr.AddPod(pod1)
	expectedPods = map[string]map[types.UID]*v1.Pod{
		fakePluginName1: map[types.UID]*v1.Pod{
			pod12.UID: pod12,
		},
		fakePluginName2: map[types.UID]*v1.Pod{
			pod2.UID:  pod2,
			pod12.UID: pod12,
		},
	}
	if err := assertPlugins(mgr, expectedPods); err != nil {
		t.Errorf(err.Error())
	}

	// Test3: pod2 is deleted, pod12 remains
	mgr.DeletePod(pod2)
	expectedPods = map[string]map[types.UID]*v1.Pod{
		fakePluginName1: map[types.UID]*v1.Pod{
			pod12.UID: pod12,
		},
		fakePluginName2: map[types.UID]*v1.Pod{
			pod12.UID: pod12,
		},
	}
	if err := assertPlugins(mgr, expectedPods); err != nil {
		t.Errorf(err.Error())
	}

	// Test4: all pods are deleted, nothing remains
	mgr.DeletePod(pod1)
	mgr.DeletePod(pod12)
	mgr.DeletePod(pod3)
	expectedPods = map[string]map[types.UID]*v1.Pod{
		fakePluginName1: map[types.UID]*v1.Pod{},
		fakePluginName2: map[types.UID]*v1.Pod{},
	}
	if err := assertPlugins(mgr, expectedPods); err != nil {
		t.Errorf(err.Error())
	}
}
