package distill

import "k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

// losslessMetadataNoise are metadata fields the API server injects or
// maintains. Removing them loses no diagnostic information by construction:
// they are either reconstructable (selfLink) or pure server bookkeeping.
var losslessMetadataNoise = [][]string{
	{"metadata", "managedFields"},
	{"metadata", "resourceVersion"},
	{"metadata", "uid"},
	{"metadata", "generation"},
	{"metadata", "creationTimestamp"},
	{"metadata", "selfLink"},
}

const lastAppliedAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// stripLossless applies the L1 transform in place.
func stripLossless(o *unstructured.Unstructured) {
	m := o.Object
	for _, path := range losslessMetadataNoise {
		unstructured.RemoveNestedField(m, path...)
	}

	removeAnnotation(m, lastAppliedAnnotation)
}

// removeAnnotation deletes one annotation key, dropping the annotations map
// entirely if it becomes empty.
func removeAnnotation(m map[string]any, key string) {
	ann, found, err := unstructured.NestedMap(m, "metadata", "annotations")
	if !found || err != nil {
		return
	}

	delete(ann, key)
	if len(ann) == 0 {
		unstructured.RemoveNestedField(m, "metadata", "annotations")
		return
	}

	_ = unstructured.SetNestedMap(m, ann, "metadata", "annotations")
}
