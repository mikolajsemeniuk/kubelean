// Package heatmap enumerates and removes individual fields from a (possibly
// multi-document) Kubernetes YAML stream, for leave-one-out ablation tests.
//
// It works at the yaml.Node level: a field is removed surgically, leaving every
// other node — order, comments, formatting — untouched. Each operation reparses
// the source from scratch, so variants are fully isolated from one another.
package heatmap

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Target identifies one removable field in the stream.
type Target struct {
	Doc     int    // index of the document in the --- joined stream
	Kind    string // resource kind (Deployment, ConfigMap, ...), for disambiguation
	Pointer string // RFC 6901 JSON Pointer within the document, e.g. /spec/replicas
}

// Keys returns every removable field in the stream: one Target per map key and
// per sequence element, at every depth.
func Keys(src string) ([]Target, error) {
	roots, err := parseDocs(src)
	if err != nil {
		return nil, err
	}

	var out []Target
	for i, v := range roots {
		kind := kindOf(v)
		walk(v, "", i, kind, &out)
	}

	return out, nil
}

// Remove deletes the field at t and returns the re-serialized stream. The input
// is reparsed, so src itself is never mutated.
func Remove(src string, t Target) (string, error) {
	roots, err := parseDocs(src)
	if err != nil {
		return "", err
	}

	if t.Doc < 0 || t.Doc >= len(roots) {
		return "", fmt.Errorf("doc %d out of range (have %d)", t.Doc, len(roots))
	}

	segs := splitPointer(t.Pointer)
	if len(segs) == 0 {
		return "", errors.New("empty pointer")
	}

	parent, err := navigate(roots[t.Doc], segs[:len(segs)-1])
	if err != nil {
		return "", err
	}

	if err := removeChild(parent, segs[len(segs)-1]); err != nil {
		return "", err
	}

	return encodeDocs(roots)
}

func walk(n *yaml.Node, ptr string, doc int, kind string, out *[]Target) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			child := ptr + "/" + escape(n.Content[i].Value)
			*out = append(*out, Target{doc, kind, child})
			walk(n.Content[i+1], child, doc, kind, out)
		}
	case yaml.SequenceNode:
		for i, c := range n.Content {
			child := ptr + "/" + strconv.Itoa(i)
			*out = append(*out, Target{doc, kind, child})
			walk(c, child, doc, kind, out)
		}
	}
}

func navigate(n *yaml.Node, segs []string) (*yaml.Node, error) {
	for _, seg := range segs {
		switch n.Kind {
		case yaml.MappingNode:
			next := mapValue(n, seg)
			if next == nil {
				return nil, fmt.Errorf("key %q not found", seg)
			}
			n = next
		case yaml.SequenceNode:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(n.Content) {
				return nil, fmt.Errorf("bad index %q", seg)
			}
			n = n.Content[idx]
		default:
			return nil, fmt.Errorf("cannot descend into %q", seg)
		}
	}
	return n, nil
}

func removeChild(parent *yaml.Node, seg string) error {
	switch parent.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(parent.Content); i += 2 {
			if parent.Content[i].Value == seg {
				parent.Content = append(parent.Content[:i], parent.Content[i+2:]...)
				return nil
			}
		}
		return fmt.Errorf("key %q not found", seg)
	case yaml.SequenceNode:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(parent.Content) {
			return fmt.Errorf("bad index %q", seg)
		}
		parent.Content = append(parent.Content[:idx], parent.Content[idx+1:]...)
		return nil
	default:
		return errors.New("parent is not a container")
	}
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}

func kindOf(root *yaml.Node) string {
	if v := mapValue(root, "kind"); v != nil {
		return v.Value
	}

	return ""
}

func parseDocs(src string) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(strings.NewReader(src))
	var roots []*yaml.Node
	for {
		var n yaml.Node
		err := dec.Decode(&n)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if n.Kind == yaml.DocumentNode && len(n.Content) == 1 {
			roots = append(roots, n.Content[0]) // unwrap to the root mapping
		} else {
			cp := n
			roots = append(roots, &cp)
		}
	}

	return roots, nil
}

func encodeDocs(roots []*yaml.Node) (string, error) {
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	for _, r := range roots {
		if err := enc.Encode(r); err != nil {
			_ = enc.Close()
			return "", err
		}
	}

	if err := enc.Close(); err != nil {
		return "", err
	}

	return sb.String(), nil
}

// JSON Pointer (RFC 6901) segment escaping: ~ -> ~0, / -> ~1.
func escape(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	return strings.ReplaceAll(s, "/", "~1")
}

func splitPointer(ptr string) []string {
	ptr = strings.TrimPrefix(ptr, "/")
	if ptr == "" {
		return nil
	}

	segs := strings.Split(ptr, "/")
	for i, s := range segs {
		s = strings.ReplaceAll(s, "~1", "/")
		segs[i] = strings.ReplaceAll(s, "~0", "~")
	}

	return segs
}
