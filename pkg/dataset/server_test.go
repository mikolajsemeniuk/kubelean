package dataset

import (
	"strings"
	"testing"

	"github.com/mikolajsemeniuk/kubelean/pkg/heatmap"
)

// TestCatalogDeterministic: the whole catalog must render byte-identical on
// every call — no clock, no randomness.
func TestCatalogDeterministic(t *testing.T) {
	a, b := All(), All()
	for i := range a {
		if a[i].YAML != b[i].YAML {
			t.Errorf("%s: two renders differ", a[i].Name)
		}
	}
}

// TestServerMetadataPresent: every scenario carries the server-assigned fields
// kubectl get would show.
func TestServerMetadataPresent(t *testing.T) {
	for _, s := range All() {
		for _, want := range []string{"creationTimestamp:", "resourceVersion:", "uid:"} {
			if !strings.Contains(s.YAML, want) {
				t.Errorf("%s: missing %s", s.Name, want)
			}
		}
	}
}

// TestServerMetaOmittedByDefault: params without ServerMeta/Status render the
// pre-decoration shape — the backward-compat contract for optional fields.
func TestServerMetaOmittedByDefault(t *testing.T) {
	dep := NewDeployment(DeploymentParams{
		Name: "web", Namespace: "production", App: "web", Replicas: 1,
		SelectorApp: "web", PodApp: "web",
		ContainerName: "web", Image: "nginx:1.25", ContainerPort: 80,
	})
	for _, no := range []string{"uid:", "resourceVersion:", "creationTimestamp:", "status:", "generation:"} {
		if strings.Contains(dep, no) {
			t.Errorf("zero-value ServerMeta rendered %q:\n%s", no, dep)
		}
	}
}

// TestStatuses: failing docs show symptoms, healthy ones do not, and Kinds
// without a status subresource get none.
func TestStatuses(t *testing.T) {
	byName := map[string]Scenario{}
	for _, s := range All() {
		byName[s.Name] = s
	}

	cases := []struct {
		scenario string
		contains []string
		absent   []string
	}{
		{"healthy-bundle",
			[]string{"readyReplicas: 2", "MinimumReplicasAvailable"},
			[]string{"unavailableReplicas", "MinimumReplicasUnavailable", "stringData"}},
		{"secret-ref-wrong-name",
			[]string{"unavailableReplicas: 2", "MinimumReplicasUnavailable"},
			[]string{"readyReplicas"}},
		{"storageclass-wrong-name",
			[]string{"phase: Pending"},
			[]string{"phase: Bound"}},
		{"pvc-claim-wrong-name",
			[]string{"phase: Bound", "unavailableReplicas: 2"},
			nil},
		{"hpa-target-wrong-name",
			[]string{"FailedGetScale", "readyReplicas: 2"},
			[]string{"not found"}}, // symptom only — the real message names the target
		{"rolebinding-role-wrong-name",
			nil,
			[]string{"status:"}}, // RBAC + SA: no status subresource at all
	}

	for _, c := range cases {
		s, ok := byName[c.scenario]
		if !ok {
			t.Fatalf("scenario %s not in catalog", c.scenario)
		}
		for _, want := range c.contains {
			if !strings.Contains(s.YAML, want) {
				t.Errorf("%s: missing %q", c.scenario, want)
			}
		}
		for _, no := range c.absent {
			if strings.Contains(s.YAML, no) {
				t.Errorf("%s: unexpectedly contains %q", c.scenario, no)
			}
		}
	}
}

// TestDecidingRoles: every faulty scenario must have at least one
// fault-deleting locus (or the flip has no honest control at all), and a
// target object's metadata.name is always evidence-hiding — removing it leaves
// the reference dangling.
func TestDecidingRoles(t *testing.T) {
	for _, s := range All() {
		if s.FaultClass == FaultNoFault {
			continue
		}

		deleting := 0
		for _, df := range s.DecidingFields {
			if !df.Hides {
				deleting++
			}
			if df.Path == "metadata.name" && !df.Hides {
				t.Errorf("%s: %s metadata.name must be evidence-hiding", s.Name, df.Kind)
			}
		}
		if deleting == 0 {
			t.Errorf("%s: no fault-deleting locus", s.Name)
		}
	}
}

// TestTwins: every faulty scenario has exactly one healthy twin — same bundle,
// anomaly fixed, expected NoFaultFound, no deciding fields, no failure symptom
// left in any status.
func TestTwins(t *testing.T) {
	byName := map[string]Scenario{}
	twins := 0
	for _, s := range All() {
		byName[s.Name] = s
		if s.TwinOf != "" {
			twins++
		}
	}

	faulty := 0
	for _, s := range All() {
		if s.FaultClass == FaultNoFault {
			continue
		}
		faulty++

		tw, ok := byName[s.Name+"-twin"]
		if !ok {
			t.Errorf("%s: no twin in catalog", s.Name)
			continue
		}
		if tw.TwinOf != s.Name {
			t.Errorf("%s-twin: TwinOf = %q, want %q", s.Name, tw.TwinOf, s.Name)
		}
		if tw.FaultClass != FaultNoFault {
			t.Errorf("%s-twin: FaultClass = %q, want NoFaultFound", s.Name, tw.FaultClass)
		}
		if len(tw.DecidingFields) != 0 {
			t.Errorf("%s-twin: has deciding fields", s.Name)
		}
		if tw.Group != s.Group {
			t.Errorf("%s-twin: group %q differs from %q", s.Name, tw.Group, s.Group)
		}
		if tw.YAML == s.YAML {
			t.Errorf("%s-twin: YAML identical to the faulty scenario — anomaly not fixed", s.Name)
		}
		for _, symptom := range []string{"unavailableReplicas", "phase: Pending", `status: "False"`, "numberUnavailable"} {
			if strings.Contains(tw.YAML, symptom) {
				t.Errorf("%s-twin: still shows symptom %q", s.Name, symptom)
			}
		}
	}

	if twins != faulty {
		t.Errorf("catalog has %d twins for %d faulty scenarios", twins, faulty)
	}
}

// TestNoLeakingBlocks: managedFields and last-applied must never appear — both
// would leak a removed field back into every ablation variant (see ServerMeta).
func TestNoLeakingBlocks(t *testing.T) {
	for _, s := range All() {
		for _, no := range []string{"managedFields", "last-applied-configuration"} {
			if strings.Contains(s.YAML, no) {
				t.Errorf("%s: contains %s", s.Name, no)
			}
		}
	}
}

// TestDecidingFieldsResolve: the kubectl-get shape must not break a single
// ground-truth locus — every deciding field still resolves to a pointer.
func TestDecidingFieldsResolve(t *testing.T) {
	for _, s := range All() {
		for _, df := range s.DecidingFields {
			ls, err := heatmap.ResolveLeaves(s.YAML, df.Kind, df.Path)
			if err != nil {
				t.Fatalf("%s %s: %v", s.Name, df.Path, err)
			}
			if len(ls) == 0 {
				t.Errorf("%s: deciding field %s/%s resolves to nothing", s.Name, df.Kind, df.Path)
			}
		}
	}
}

// TestKeysAndRemoveOnServerShape: the ablation kernel must see the server-side
// fields as targets and remove each cleanly.
func TestKeysAndRemoveOnServerShape(t *testing.T) {
	s := healthyBundle()
	targets, err := heatmap.Keys(s.YAML)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]bool{}
	for _, tg := range targets {
		seen[tg.Pointer] = true
	}
	for _, want := range []string{"/metadata/uid", "/status/readyReplicas", "/status/conditions/*/message"} {
		if !seen[want] {
			t.Errorf("healthy-bundle: no ablation target %s", want)
		}
	}

	for _, tg := range targets {
		reduced, err := heatmap.Remove(s.YAML, tg)
		if err != nil {
			t.Errorf("remove %s: %v", tg.Pointer, err)
			continue
		}
		if reduced == s.YAML {
			t.Errorf("remove %s: output unchanged", tg.Pointer)
		}
	}
}
