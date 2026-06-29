package heatmap

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// requiredPaths lists, per Kind, the field paths that must resolve for a
// manifest to be structurally valid. A "[]" segment means "a non-empty sequence
// whose every element satisfies the remainder".
//
// Why a required-field check is enough — and why we do NOT need kubeconform or a
// full schema validator: ablation only ever REMOVES fields, never changes a type
// or value. So the one and only way a variant turns invalid is by dropping a
// required field. Checking required-field presence is therefore both necessary
// and sufficient here. Extend this table as new Kinds are generated; it lives
// next to the generator's knowledge of what it emits.
var requiredPaths = map[string][]string{
	"Deployment": {
		"/metadata/name",
		"/spec/selector",
		"/spec/template/spec/containers/[]/name",
	},
	"ConfigMap": {"/metadata/name"},
	"Secret":    {"/metadata/name"},
}

// Valid reports whether every document in the stream still satisfies its Kind's
// required-field set, returning a human-readable problem per missing field. A
// Kind with no entry in requiredPaths is treated as valid (we only assert what
// we know). Use it to route invalid ablation variants to a bucket and keep them
// out of the saliency map.
func Valid(src string) (bool, []string, error) {
	roots, err := parseDocs(src)
	if err != nil {
		return false, nil, err
	}

	var problems []string
	for i, root := range roots {
		kind := kindOf(root)
		for _, p := range requiredPaths[kind] {
			segs := strings.Split(strings.TrimPrefix(p, "/"), "/")
			if !resolves(root, segs) {
				problems = append(problems, fmt.Sprintf("doc %d (%s): missing required %s", i, kind, p))
			}
		}
	}

	return len(problems) == 0, problems, nil
}

// resolves reports whether the path segs is present under n. "[]" requires a
// non-empty sequence and is satisfied only if EVERY element satisfies the rest
// (a single nameless container makes the whole manifest invalid).
func resolves(n *yaml.Node, segs []string) bool {
	if len(segs) == 0 {
		return true
	}
	seg, rest := segs[0], segs[1:]

	if seg == "[]" {
		if n.Kind != yaml.SequenceNode || len(n.Content) == 0 {
			return false
		}

		for _, el := range n.Content {
			if !resolves(el, rest) {
				return false
			}
		}

		return true
	}

	if n.Kind != yaml.MappingNode {
		return false
	}

	v := mapValue(n, seg)
	if v == nil {
		return false
	}

	return resolves(v, rest)
}
