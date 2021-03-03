package attachdetach

import (
	utilfeature "k8s.io/apiserver/pkg/util/feature"
	"k8s.io/component-base/featuregate"
	"k8s.io/kubernetes/pkg/features"
)

func patchADCFeatureGates() featuregate.FeatureGate {
	forceEnabledFeatureGates := map[string]bool{
		string(features.CSIMigrationOpenStack): true,
		string(features.CSIMigrationGCE):       true,
	}

	features := utilfeature.DefaultMutableFeatureGate.DeepCopy()
	features.SetFromMap(forceEnabledFeatureGates)
	return features
}
