package heatmap

import (
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Locus is a concrete field location: a document index and a JSON pointer.
type Locus struct {
	Doc     int
	Pointer string
}

// ResolveLeaves resolves an abstract dotted path (with [] array markers, e.g.
// spec.template.spec.containers[].envFrom[].secretRef.name) to the concrete
// pointers it occupies in src, within documents of the given kind. A [] expands
// to every array index, but only paths that FULLY resolve to an existing node are
// returned — so envFrom[].secretRef.name yields just the envFrom entry that has a
// secretRef, not its configMapRef sibling. This is how a Kind-qualified deciding
// field becomes the exact pointers whose removal deletes the fault.
func ResolveLeaves(src, kind, dottedPath string) ([]Locus, error) {
	roots, err := parseDocs(src)
	if err != nil {
		return nil, err
	}

	segs := splitDotted(dottedPath)
	var out []Locus
	for i, root := range roots {
		if kindOf(root) != kind {
			continue
		}

		var ptrs []string
		resolveLeaf(root, segs, "", &ptrs)
		for _, p := range ptrs {
			out = append(out, Locus{Doc: i, Pointer: p})
		}
	}
	return out, nil
}

// splitDotted turns "a.b[].c" into [a b [] c]: the [] array marker becomes its
// own segment.
func splitDotted(p string) []string {
	var out []string
	for _, seg := range strings.Split(p, ".") {
		if name, ok := strings.CutSuffix(seg, "[]"); ok {
			out = append(out, name, "[]")
		} else {
			out = append(out, seg)
		}
	}

	return out
}

func resolveLeaf(node *yaml.Node, segs []string, prefix string, out *[]string) {
	if len(segs) == 0 {
		*out = append(*out, prefix)
		return
	}

	seg, rest := segs[0], segs[1:]
	if seg == "[]" {
		if node.Kind != yaml.SequenceNode {
			return
		}

		for i, el := range node.Content {
			resolveLeaf(el, rest, prefix+"/"+strconv.Itoa(i), out)
		}

		return
	}

	if node.Kind != yaml.MappingNode {
		return
	}

	if v := mapValue(node, seg); v != nil {
		resolveLeaf(v, rest, prefix+"/"+escape(seg), out)
	}
}
