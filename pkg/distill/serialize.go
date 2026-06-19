package distill

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// ToYAML serializes obj the way an LLM agent sees it: canonical, key-sorted
// YAML. sigs.k8s.io/yaml routes through JSON, which sorts map keys, so the
// output is deterministic and comparable across profiles.
func ToYAML(obj *unstructured.Unstructured) (string, error) {
	b, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", err
	}

	return string(b), nil
}

// FromYAML parses a single Kubernetes object from YAML into an unstructured
// resource.
func FromYAML(data []byte) (*unstructured.Unstructured, error) {
	var m map[string]any
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, err
	}

	return &unstructured.Unstructured{Object: m}, nil
}
