package distill

import (
	"math/rand"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// protectedRoots are fields random-drop never removes, so the result stays a
// recognizable resource (otherwise the model has nothing to reason about at all,
// which would be an unfair comparison rather than an equal-budget control).
var protectedRoots = map[string]bool{"apiVersion": true, "kind": true}

// RandomDrop is the H2 control: it removes randomly-chosen fields from a deep
// copy until the serialized size falls to about targetFrac of the original —
// the SAME budget a structure-aware profile hits, but with random rather than
// salient selection. If structure-aware distillation preserves RCA accuracy and
// equal-budget random-drop does not, the gain comes from WHICH fields are kept,
// not from cutting volume. seed makes it reproducible.
func RandomDrop(obj *unstructured.Unstructured, targetFrac float64, seed int64) *unstructured.Unstructured {
	out := obj.DeepCopy()
	y, err := ToYAML(out)
	if err != nil {
		return out
	}
	target := int(targetFrac * float64(len(y)))
	r := rand.New(rand.NewSource(seed))

	for iter := 0; iter < 100000; iter++ {
		y, err := ToYAML(out)
		if err != nil || len(y) <= target {
			break
		}
		paths := removablePaths(out.Object, nil)
		if len(paths) == 0 {
			break
		}
		pick := paths[r.Intn(len(paths))]
		unstructured.RemoveNestedField(out.Object, pick...)
	}
	return out
}

// removablePaths collects map-key paths eligible for removal: every key under
// any nested map, except the protected roots and metadata.name/namespace (kept
// so the object stays identifiable). Removing a key drops its whole subtree, so
// random-drop can — and sometimes will — delete the field that decides RCA.
func removablePaths(m map[string]any, prefix []string) [][]string {
	var paths [][]string
	for k, v := range m {
		path := append(append([]string{}, prefix...), k)
		if isProtected(path) {
			if child, ok := v.(map[string]any); ok {
				paths = append(paths, removablePaths(child, path)...)
			}
			continue
		}
		paths = append(paths, path)
		if child, ok := v.(map[string]any); ok {
			paths = append(paths, removablePaths(child, path)...)
		}
	}
	return paths
}

func isProtected(path []string) bool {
	if len(path) == 1 {
		return protectedRoots[path[0]]
	}
	if len(path) == 2 && path[0] == "metadata" && (path[1] == "name" || path[1] == "namespace") {
		return true
	}
	return false
}
