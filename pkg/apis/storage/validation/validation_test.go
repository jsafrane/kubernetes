/*
Copyright 2016 The Kubernetes Authors.

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

package validation

import (
	"fmt"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/kubernetes/pkg/api"
	"k8s.io/kubernetes/pkg/apis/storage"
)

func TestValidateStorageClass(t *testing.T) {
	deleteReclaimPolicy := api.PersistentVolumeReclaimPolicy("Delete")
	retainReclaimPolicy := api.PersistentVolumeReclaimPolicy("Retain")
	recycleReclaimPolicy := api.PersistentVolumeReclaimPolicy("Recycle")
	successCases := []storage.StorageClass{
		{
			// empty parameters
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "kubernetes.io/foo-provisioner",
			Parameters:    map[string]string{},
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		{
			// nil parameters
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "kubernetes.io/foo-provisioner",
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		{
			// some parameters
			ObjectMeta:  metav1.ObjectMeta{Name: "foo"},
			Provisioner: "kubernetes.io/foo-provisioner",
			Parameters: map[string]string{
				"kubernetes.io/foo-parameter": "free/form/string",
				"foo-parameter":               "free-form-string",
				"foo-parameter2":              "{\"embedded\": \"json\", \"with\": {\"structures\":\"inside\"}}",
			},
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		{
			// retain reclaimPolicy
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "kubernetes.io/foo-provisioner",
			ReclaimPolicy: &retainReclaimPolicy,
		},
	}

	// Success cases are expected to pass validation.
	for k, v := range successCases {
		if errs := ValidateStorageClass(&v); len(errs) != 0 {
			t.Errorf("Expected success for %d, got %v", k, errs)
		}
	}

	// generate a map longer than maxProvisionerParameterSize
	longParameters := make(map[string]string)
	totalSize := 0
	for totalSize < maxProvisionerParameterSize {
		k := fmt.Sprintf("param/%d", totalSize)
		v := fmt.Sprintf("value-%d", totalSize)
		longParameters[k] = v
		totalSize = totalSize + len(k) + len(v)
	}

	errorCases := map[string]storage.StorageClass{
		"namespace is present": {
			ObjectMeta:    metav1.ObjectMeta{Name: "foo", Namespace: "bar"},
			Provisioner:   "kubernetes.io/foo-provisioner",
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		"invalid provisioner": {
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "kubernetes.io/invalid/provisioner",
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		"invalid empty parameter name": {
			ObjectMeta:  metav1.ObjectMeta{Name: "foo"},
			Provisioner: "kubernetes.io/foo",
			Parameters: map[string]string{
				"": "value",
			},
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		"provisioner: Required value": {
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "",
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		"too long parameters": {
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "kubernetes.io/foo",
			Parameters:    longParameters,
			ReclaimPolicy: &deleteReclaimPolicy,
		},
		"invalid reclaimpolicy": {
			ObjectMeta:    metav1.ObjectMeta{Name: "foo"},
			Provisioner:   "kubernetes.io/foo",
			ReclaimPolicy: &recycleReclaimPolicy,
		},
	}

	// Error cases are not expected to pass validation.
	for testName, storageClass := range errorCases {
		if errs := ValidateStorageClass(&storageClass); len(errs) == 0 {
			t.Errorf("Expected failure for test: %s", testName)
		}
	}
}

func TestAlphaExpandPersistentVolumesFeatureValidation(t *testing.T) {
	deleteReclaimPolicy := api.PersistentVolumeReclaimPolicy("Delete")
	falseVar := false
	testSC := &storage.StorageClass{
		// empty parameters
		ObjectMeta:           metav1.ObjectMeta{Name: "foo"},
		Provisioner:          "kubernetes.io/foo-provisioner",
		Parameters:           map[string]string{},
		ReclaimPolicy:        &deleteReclaimPolicy,
		AllowVolumeExpansion: &falseVar,
	}

	// Enable alpha feature ExpandPersistentVolumes
	err := utilfeature.DefaultFeatureGate.Set("ExpandPersistentVolumes=true")
	if err != nil {
		t.Errorf("Failed to enable feature gate for ExpandPersistentVolumes: %v", err)
		return
	}
	if errs := ValidateStorageClass(testSC); len(errs) != 0 {
		t.Errorf("expected success: %v", errs)
	}
	// Disable alpha feature ExpandPersistentVolumes
	err = utilfeature.DefaultFeatureGate.Set("ExpandPersistentVolumes=false")
	if err != nil {
		t.Errorf("Failed to disable feature gate for ExpandPersistentVolumes: %v", err)
		return
	}
	if errs := ValidateStorageClass(testSC); len(errs) == 0 {
		t.Errorf("expected failure, but got no error")
	}

}

func TestVolumeAttachmentValidation(t *testing.T) {
	volumeName := "pv-name"
	empty := ""
	successCases := []storage.VolumeAttachment{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "mynode",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "foo-with-status"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "mynode",
			},
			Status: storage.VolumeAttachmentStatus{
				Attached: true,
				AttachmentMetadata: map[string]string{
					"foo": "bar",
				},
				AttachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
				DetachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
			},
		},
	}

	for _, volumeAttachment := range successCases {
		if errs := ValidateVolumeAttachment(&volumeAttachment); len(errs) != 0 {
			t.Errorf("expected success: %v", errs)
		}
	}
	errorCases := []storage.VolumeAttachment{
		{
			// Empty attacher name
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "",
				NodeName: "mynode",
			},
		},
		{
			// Invalid attacher name
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "invalid!@#$%^&*()",
				NodeName: "mynode",
			},
		},
		{
			// Empty node name
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				NodeName: "",
			},
		},
		{
			// No volume name
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				NodeName: "node",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: nil,
				},
			},
		},
		{
			// Empty volume name
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				NodeName: "node",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &empty,
				},
			},
		},
		{
			// Too long error message
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				NodeName: "node",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
			},
			Status: storage.VolumeAttachmentStatus{
				Attached: true,
				AttachmentMetadata: map[string]string{
					"foo": "bar",
				},
				AttachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
				DetachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: strings.Repeat("a", maxVolumeErrorMessageSize+1),
				},
			},
		},
		{
			// Too long metadata
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				NodeName: "node",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
			},
			Status: storage.VolumeAttachmentStatus{
				Attached: true,
				AttachmentMetadata: map[string]string{
					"foo": strings.Repeat("a", maxAttachedVolumeMetadataSize),
				},
				AttachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
				DetachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
			},
		},
	}

	for _, volumeAttachment := range errorCases {
		if errs := ValidateVolumeAttachment(&volumeAttachment); len(errs) == 0 {
			t.Errorf("Expected failure for test: %v", volumeAttachment)
		}
	}
}

func TestVolumeAttachmentUpdateValidation(t *testing.T) {
	volumeName := "foo"
	newVolumeName := "bar"

	old := storage.VolumeAttachment{
		ObjectMeta: metav1.ObjectMeta{Name: "foo"},
		Spec: storage.VolumeAttachmentSpec{
			Attacher: "myattacher",
			AttachedVolumeSource: storage.AttachedVolumeSource{
				PersistentVolumeName: &volumeName,
			},
			NodeName: "mynode",
		},
	}
	successCases := []storage.VolumeAttachment{
		{
			// no change
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "mynode",
			},
		},
		{
			// modify status
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "mynode",
			},
			Status: storage.VolumeAttachmentStatus{
				Attached: true,
				AttachmentMetadata: map[string]string{
					"foo": "bar",
				},
				AttachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
				DetachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
			},
		},
	}

	for _, volumeAttachment := range successCases {
		if errs := ValidateVolumeAttachmentUpdate(&volumeAttachment, &old); len(errs) != 0 {
			t.Errorf("expected success: %v", errs)
		}
	}

	errorCases := []storage.VolumeAttachment{
		{
			// change attacher
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "another-attacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "mynode",
			},
		},
		{
			// change volume
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &newVolumeName,
				},
				NodeName: "mynode",
			},
		},
		{
			// change node
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "anothernode",
			},
		},
		{
			// add invalid status
			ObjectMeta: metav1.ObjectMeta{Name: "foo"},
			Spec: storage.VolumeAttachmentSpec{
				Attacher: "myattacher",
				AttachedVolumeSource: storage.AttachedVolumeSource{
					PersistentVolumeName: &volumeName,
				},
				NodeName: "mynode",
			},
			Status: storage.VolumeAttachmentStatus{
				Attached: true,
				AttachmentMetadata: map[string]string{
					"foo": "bar",
				},
				AttachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: strings.Repeat("a", maxAttachedVolumeMetadataSize),
				},
				DetachError: &storage.VolumeError{
					Time:    metav1.Time{},
					Message: "hello world",
				},
			},
		},
	}

	for _, volumeAttachment := range errorCases {
		if errs := ValidateVolumeAttachmentUpdate(&volumeAttachment, &old); len(errs) == 0 {
			t.Errorf("Expected failure for test: %v", volumeAttachment)
		}
	}
}
