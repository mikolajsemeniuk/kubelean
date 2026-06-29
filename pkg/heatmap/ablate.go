// Package heatmap enumerates and removes individual fields from a (possibly
// multi-document) Kubernetes YAML stream, for leave-one-out ablation tests.
//
// It works at the yaml.Node level: a field is removed surgically, then any
// ancestor left empty is collapsed, so a removal leaves no husk. Each operation
// reparses the source from scratch, so variants are fully isolated.
package heatmap

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Category classifies a Target by its structural role. The three kinds measure
// different things (a scalar field vs a whole label map vs a scalar list element)
// and are meant to be reported as separate populations.
type Category string

const (
	CategoryScalar    Category = "scalar"     // a leaf scalar field: image, replicas, name
	CategoryAtomicMap Category = "atomic-map" // a flat user-key map removed whole: labels, data
	CategorySeqElem   Category = "seq-elem"   // a scalar element of a sequence: an arg, a command word
)

// An atomic map is one whose value is an *open, user-defined key set* — you
// invent the keys (labels, annotations, ConfigMap data) — so ablating a single
// inner key is meaningless; the whole map is the unit, emitted whole and not
// descended into. Closed-schema maps with fixed fields (metadata,
// resources{requests,limits}, secretRef{name}) are NOT atomic: they are
// descended so each field is measured separately, and collapse cleans up husks.

// universalAtomic are open-key-set maps that are atomic in EVERY Kind — there is
// no cross-Kind collision, so they need no Kind scoping. Matched by key name.
var universalAtomic = map[string]bool{
	"labels":      true,
	"matchLabels": true,
	"annotations": true,
	"data":        true,
	"stringData":  true,
}

// kindAtomic handles keys that are an open key set in some Kinds but a
// closed-schema structural node in others, so a bare name is ambiguous. The
// canonical case: a Service's spec.selector is a flat label map (atomic), while
// a Deployment/StatefulSet/Job selector wraps matchLabels (structural — must be
// descended). Add an entry per Kind that genuinely needs it; do not duplicate
// the universals here.
var kindAtomic = map[string]map[string]bool{
	"Service": {"selector": true},
}

// isAtomicMap reports whether the map at key, inside a document of the given
// Kind, is an atomic unit (emitted whole, not descended).
func isAtomicMap(kind, key string) bool {
	return universalAtomic[key] || kindAtomic[kind][key]
}

// Target identifies one removable field in the stream.
type Target struct {
	Doc      int      // index of the document in the --- joined stream
	Kind     string   // resource kind (Deployment, ConfigMap, ...), for disambiguation
	Pointer  string   // RFC 6901 JSON Pointer within the document, e.g. /spec/replicas
	Category Category // structural role of this target
}

// Keys returns the removable fields of the stream: scalar leaves, atomic maps,
// and scalar sequence elements are emitted; structural maps, whole sequences, and
// composite sequence elements (a container, an envFrom entry) are descended into
// but not emitted — removing a whole subtree is a trivial, information-free
// ablation, and the subtree's meaningful leaves are emitted on their own.
func Keys(src string) ([]Target, error) {
	roots, err := parseDocs(src)
	if err != nil {
		return nil, err
	}

	var raw []Target
	for i, v := range roots {
		kind := kindOf(v)
		walk(v, "", i, kind, &raw)
	}

	// Collapse per-instance pointers to canonical field-keys (array indices -> *),
	// so each key is scored once and its removal spans every instance: two ports
	// become one containers/*/ports/*/containerPort, not two position-bound fields.
	seen := map[string]bool{}
	var out []Target
	for _, t := range raw {
		t.Pointer = NormalizeKey(t.Pointer)
		id := strconv.Itoa(t.Doc) + "\x00" + t.Pointer
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, t)
	}

	return out, nil
}

// NormalizeKey collapses array indices in a JSON Pointer to "*", yielding the
// canonical field-key shared across instances:
// /spec/.../containers/0/ports/0/containerPort -> /spec/.../containers/*/ports/*/containerPort.
// This is the unit the heatmap scores and reduce drops — its removal takes every
// instance, so signal is never misattributed to "the field at position 0". Only
// sequence indices are numeric in emitted pointers (open-key maps are atomic and
// never descended), so a purely numeric segment is always an index.
func NormalizeKey(ptr string) string {
	segs := strings.Split(ptr, "/")
	for i, s := range segs {
		if _, err := strconv.Atoi(s); err == nil {
			segs[i] = "*"
		}
	}
	return strings.Join(segs, "/")
}

// Remove deletes the field at t and returns the re-serialized stream. After the
// delete it collapses any ancestor that became empty, so removing a sole child
// leaves no husk: removing .../secretRef/name drops the whole secretRef and its
// now-empty envFrom entry, not "secretRef: {}". The husk is what would leak a
// field's former presence and mask its saliency. src is reparsed, never mutated.
func Remove(src string, t Target) (string, error) {
	roots, err := parseDocs(src)
	if err != nil {
		return "", err
	}

	if t.Doc < 0 || t.Doc >= len(roots) {
		return "", fmt.Errorf("doc %d out of range (have %d)", t.Doc, len(roots))
	}

	pat := splitPointer(t.Pointer)
	if len(pat) == 0 {
		return "", errors.New("empty pointer")
	}

	// A field-key may have many instances (containers/*/ports/*/containerPort). We
	// re-find the first match after every deletion rather than precomputing all of
	// them, so the index shifts collapse causes never leave us holding a stale path.
	root := roots[t.Doc]
	removed := 0
	for {
		segs, ok := findFirst(root, pat, nil)
		if !ok {
			break
		}
		parent, err := navigate(root, segs[:len(segs)-1])
		if err != nil {
			return "", err
		}
		if err := removeChild(parent, segs[len(segs)-1]); err != nil {
			return "", err
		}
		collapse(root, segs)
		removed++
	}
	if removed == 0 {
		return "", fmt.Errorf("no field matched %q", t.Pointer)
	}

	return encodeDocs(roots)
}

// findFirst returns the concrete segments of the first node matching pat, where a
// "*" segment matches any sequence index and a concrete index/key matches itself
// (so Remove accepts both a normalized field-key and a plain pointer). ok is false
// when no instance remains.
func findFirst(n *yaml.Node, pat, prefix []string) ([]string, bool) {
	if len(pat) == 0 {
		return prefix, true
	}
	seg := pat[0]
	switch {
	case seg == "*":
		if n.Kind != yaml.SequenceNode {
			return nil, false
		}
		for i, el := range n.Content {
			if r, ok := findFirst(el, pat[1:], append(append([]string{}, prefix...), strconv.Itoa(i))); ok {
				return r, true
			}
		}
		return nil, false
	case n.Kind == yaml.SequenceNode:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(n.Content) {
			return nil, false
		}
		return findFirst(n.Content[idx], pat[1:], append(append([]string{}, prefix...), seg))
	case n.Kind == yaml.MappingNode:
		v := mapValue(n, seg)
		if v == nil {
			return nil, false
		}
		return findFirst(v, pat[1:], append(append([]string{}, prefix...), seg))
	default:
		return nil, false
	}
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
			continue
		}

		cp := n
		roots = append(roots, &cp)
	}

	return roots, nil
}

func kindOf(root *yaml.Node) string {
	if v := mapValue(root, "kind"); v != nil {
		return v.Value
	}

	return ""
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}

	return nil
}

func walk(n *yaml.Node, ptr string, doc int, kind string, out *[]Target) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i].Value
			val := n.Content[i+1]
			child := ptr + "/" + escape(key)
			switch {
			case val.Kind == yaml.ScalarNode:
				*out = append(*out, Target{doc, kind, child, CategoryScalar})
			case val.Kind == yaml.MappingNode && isAtomicMap(kind, key):
				*out = append(*out, Target{doc, kind, child, CategoryAtomicMap})
			default: // structural map or sequence: descend, do not emit
				walk(val, child, doc, kind, out)
			}
		}
	case yaml.SequenceNode:
		for i, el := range n.Content {
			child := ptr + "/" + strconv.Itoa(i)
			if el.Kind == yaml.ScalarNode { // a scalar element (an arg, a command word): atomic, emit
				*out = append(*out, Target{doc, kind, child, CategorySeqElem})
				continue
			}
			walk(el, child, doc, kind, out) // a composite element (a container, an envFrom entry): descend, don't emit the wrapper
		}
	}
}

// collapse walks up the removed node's ancestor chain and drops every ancestor
// that is now an empty map or sequence, stopping at the first that still holds
// something (everything above it is then non-empty too).
func collapse(root *yaml.Node, segs []string) {
	for i := len(segs) - 1; i >= 1; i-- {
		node, err := navigate(root, segs[:i])
		if err != nil || !isEmptyContainer(node) {
			break
		}
		grandparent, err := navigate(root, segs[:i-1])
		if err != nil {
			break
		}
		if err := removeChild(grandparent, segs[i-1]); err != nil {
			break
		}
	}
}

func isEmptyContainer(n *yaml.Node) bool {
	return (n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode) && len(n.Content) == 0
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
