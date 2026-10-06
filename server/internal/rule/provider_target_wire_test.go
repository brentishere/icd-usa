package rule

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/singhand-labs/AegisCrawler/internal/models"
)

func TestProviderOrdinaryTargetWireRoundTripsRecursively(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorElementForTest(
			"html", nil, selectorElementForTest("body", nil,
				selectorElementForTest("main", map[string]string{"id": "content"}),
			),
		)),
	)
	prompt, _, err := catalog.PrepareProviderPrompt(nil)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	steps := []any{
		map[string]any{"action": "click", "target": map[string]any{"$ref": "next"}},
		map[string]any{"action": "waitForElementVisible", "target": map[string]any{"selector": "#content"}},
		map[string]any{
			"action": "waitForElementHidden",
			"target": map[string]any{"selector": "#loading", "visible": false},
		},
		map[string]any{
			"action": "loop", "type": "fixedCount", "count": 2,
			"steps": []any{
				map[string]any{
					"action": "if",
					"condition": map[string]any{
						"type":   "elementNotExists",
						"target": map[string]any{"text": "Next", "visible": true},
					},
					"then": []any{map[string]any{"action": "break"}},
					"else": []any{
						map[string]any{
							"action": "click",
							"target": map[string]any{"ariaLabel": "Next page"},
						},
					},
				},
			},
		},
		map[string]any{
			"action": "switch",
			"cases": []any{
				map[string]any{
					"steps": []any{
						map[string]any{
							"action": "click",
							"target": map[string]any{
								"selector": ".item", "visible": true,
							},
						},
					},
				},
			},
		},
		map[string]any{
			"action": "handleDownload",
			"trigger": map[string]any{
				"action": "click",
				"target": map[string]any{"text": "Download"},
			},
		},
	}
	hooks := map[string]any{
		"beforeAll": []any{
			map[string]any{
				"action": "click",
				"target": map[string]any{"role": "link", "roleName": "Start"},
			},
		},
		"afterAll": []any{
			map[string]any{
				"action": "click",
				"target": map[string]any{"text": "Done"},
			},
		},
		"onError": []any{
			map[string]any{
				"action": "waitForElementHidden",
				"target": map[string]any{"selector": "#error", "visible": false},
			},
		},
		"cleanup": []any{
			map[string]any{
				"action": "click",
				"target": map[string]any{"$ref": "next"},
			},
		},
	}
	selectors, _ := json.Marshal(map[string]any{
		"next": map[string]any{"selector": "li.next > a", "visible": true},
	})
	encodedSteps, _ := json.Marshal(steps)
	encodedHooks, _ := json.Marshal(hooks)
	rule := &models.Rule{
		Selectors: models.JSON(selectors),
		Steps:     models.JSON(encodedSteps),
		Hooks:     models.JSON(encodedHooks),
	}
	originalSteps := append([]byte(nil), rule.Steps...)
	originalHooks := append([]byte(nil), rule.Hooks...)

	if err := EncodeProviderOrdinaryTargets(rule); err != nil {
		t.Fatal(err)
	}
	var providerSteps []any
	if err := json.Unmarshal(rule.Steps, &providerSteps); err != nil {
		t.Fatal(err)
	}
	assertProviderWireTargetsForTest(t, providerSteps, "steps")
	var providerHooks map[string]any
	if err := json.Unmarshal(rule.Hooks, &providerHooks); err != nil {
		t.Fatal(err)
	}
	for _, name := range selectorCandidateHookNames {
		assertProviderWireTargetsForTest(t, providerHooks[name].([]any), "hooks."+name)
	}

	report, err := catalog.ResolveProviderCandidates(rule, scope.CatalogHash)
	if err != nil {
		t.Fatal(err)
	}
	if report.OrdinaryTargets != 11 || report.Targets != 0 || report.Fields != 0 {
		t.Fatalf("unexpected provider resolution report: %+v", report)
	}
	if !bytes.Equal(rule.Steps, originalSteps) {
		t.Fatalf("provider target round trip changed canonical steps:\nwant %s\ngot  %s", originalSteps, rule.Steps)
	}
	if !bytes.Equal(rule.Hooks, originalHooks) {
		t.Fatalf("provider target round trip changed canonical hooks:\nwant %s\ngot  %s", originalHooks, rule.Hooks)
	}
}

func TestCurrentProviderOrdinaryTargetWireRejectsCanonicalButHistoricalReconstructs(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorElementForTest("html", nil)),
	)
	prompt, _, err := catalog.PrepareProviderPrompt(nil)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "click",
			"target": map[string]any{"selector": "#legacy"},
		},
	})
	current := &models.Rule{Steps: models.JSON(steps)}
	before := append([]byte(nil), current.Steps...)
	if _, err := catalog.ResolveProviderCandidates(current, scope.CatalogHash); !errors.Is(err, ErrInvalidProvisionalRule) ||
		!strings.Contains(err.Error(), "must use the branch-free provider target wire") {
		t.Fatalf("current canonical provider target was not rejected: %v", err)
	}
	if !bytes.Equal(current.Steps, before) {
		t.Fatalf("failed current resolution mutated the source:\nwant %s\ngot  %s", before, current.Steps)
	}

	historical := &models.Rule{Steps: models.JSON(append([]byte(nil), steps...))}
	report, err := catalog.ResolveHistoricalProviderCandidates(historical, scope.CatalogHash)
	if err != nil {
		t.Fatalf("historical canonical target was not reconstructable: %v", err)
	}
	if report.OrdinaryTargets != 0 || !bytes.Equal(historical.Steps, steps) {
		t.Fatalf("historical reconstruction changed canonical provider IR: report=%+v steps=%s", report, historical.Steps)
	}
}

func TestProviderOrdinaryTargetWireRejectsCapturedMixedShapeWithoutMutation(t *testing.T) {
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "click",
			"target": map[string]any{
				"selector": "li.next > a",
				"text":     "next",
				"visible":  true,
			},
		},
	})
	rule := &models.Rule{Steps: models.JSON(steps)}
	before := append([]byte(nil), rule.Steps...)
	err := EncodeProviderOrdinaryTargets(rule)
	if !errors.Is(err, ErrInvalidProvisionalRule) ||
		!strings.Contains(err.Error(), "not one exact provider ordinary-target family") {
		t.Fatalf("captured attempt-6 mixed target was not rejected: %v", err)
	}
	if !bytes.Equal(rule.Steps, before) {
		t.Fatalf("failed provider encoding mutated the source rule:\nwant %s\ngot  %s", before, rule.Steps)
	}
}

func TestProviderOrdinaryTargetWireRejectsInvalidSlotsTransactionally(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorElementForTest("html", nil)),
	)
	prompt, _, err := catalog.PrepareProviderPrompt(nil)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		target map[string]any
	}{
		{name: "unknown family", target: map[string]any{"family": "xpath", "value": "//a", "name": ""}},
		{name: "blank value", target: map[string]any{"family": "selector", "value": " ", "name": ""}},
		{name: "wrong unused slot", target: map[string]any{"family": "selector", "value": "a", "name": "Next"}},
		{name: "blank role name", target: map[string]any{"family": "role", "value": "link", "name": ""}},
		{name: "extra canonical key", target: map[string]any{
			"family": "selector", "value": "a", "name": "", "text": "Next",
		}},
		{name: "unknown ref", target: map[string]any{"family": "ref", "value": "missing", "name": ""}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			steps, _ := json.Marshal([]any{
				map[string]any{"action": "click", "target": testCase.target},
			})
			rule := &models.Rule{Steps: models.JSON(steps)}
			before := append([]byte(nil), rule.Steps...)
			if _, err := catalog.ResolveProviderCandidates(rule, scope.CatalogHash); !errors.Is(err, ErrInvalidProvisionalRule) {
				t.Fatalf("invalid provider target was not rejected: %v", err)
			}
			if !bytes.Equal(rule.Steps, before) {
				t.Fatalf("failed provider resolution mutated the source rule:\nwant %s\ngot  %s", before, rule.Steps)
			}
		})
	}
}

func TestProviderOrdinaryTargetResolutionRollsBackOnExtractionFailure(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorResultsForTest(
			"results",
			[]selectorCardForTest{
				{classes: "card", title: true},
				{classes: "card", title: true},
			},
		)),
	)
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "click",
			"target": map[string]any{"selector": "#results", "visible": true},
		},
		map[string]any{
			"action": "extract", "name": "items", "multiple": true,
			"target": map[string]any{"selector": "#results > .card", "visible": true},
			"fields": map[string]any{
				"title": map[string]any{"type": "text", "selector": ".title"},
			},
		},
	})
	trusted := &models.Rule{Steps: models.JSON(steps)}
	prompt, provider, err := catalog.PrepareProviderPrompt(trusted)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	if err := EncodeProviderOrdinaryTargets(provider); err != nil {
		t.Fatal(err)
	}
	var providerSteps []any
	if err := json.Unmarshal(provider.Steps, &providerSteps); err != nil {
		t.Fatal(err)
	}
	extractTarget := providerSteps[1].(map[string]any)["target"].(map[string]any)
	extractTarget["rowCandidateId"] = "r_unknown"
	provider.Steps, _ = json.Marshal(providerSteps)
	before := append([]byte(nil), provider.Steps...)

	report, err := catalog.ResolveProviderCandidates(provider, scope.CatalogHash)
	if !errors.Is(err, ErrInvalidProvisionalRule) ||
		!strings.Contains(err.Error(), "unknown or out-of-scope selector candidate") {
		t.Fatalf("later extraction failure was not preserved: %v", err)
	}
	if report.OrdinaryTargets != 1 {
		t.Fatalf("ordinary resolution was not attempted on the transactional clone: %+v", report)
	}
	if !bytes.Equal(provider.Steps, before) {
		t.Fatalf("later extraction failure committed ordinary lowering:\nwant %s\ngot  %s", before, provider.Steps)
	}
}

// Providers repeatedly put the FIELD candidate id (f_*) of the element they
// mean into a target slot (observed twice consecutively in live workflows as
// "references unknown or out-of-scope selector candidate f_…"). The catalog
// knows the parentage, so resolution must deterministically recover the
// parent target candidate, record the coercion, and keep the workflow alive.
func TestProviderFieldCandidateInTargetSlotIsCoercedToParentTarget(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorResultsForTest(
			"results",
			[]selectorCardForTest{
				{classes: "card", title: true},
				{classes: "card", title: true},
			},
		)),
	)
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "extract", "name": "items", "multiple": true,
			"target": map[string]any{"selector": "#results > .card", "visible": true},
			"fields": map[string]any{
				"title": map[string]any{"type": "text", "selector": ".title"},
			},
		},
	})
	trusted := &models.Rule{Steps: models.JSON(steps)}
	prompt, provider, err := catalog.PrepareProviderPrompt(trusted)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	if err := EncodeProviderOrdinaryTargets(provider); err != nil {
		t.Fatal(err)
	}
	var providerSteps []any
	if err := json.Unmarshal(provider.Steps, &providerSteps); err != nil {
		t.Fatal(err)
	}
	extract := providerSteps[0].(map[string]any)
	target := extract["target"].(map[string]any)
	originalRowID, _ := target["rowCandidateId"].(string)
	if originalRowID == "" {
		t.Fatal("provider extract target lost its row candidate id")
	}
	// Grab the field candidate id the same provider emitted for the title
	// field and plant it in the target slot — the observed failure shape.
	fields := extract["fields"].(map[string]any)
	titleField, _ := fields["title"].(map[string]any)
	fieldID, _ := titleField["fieldCandidateId"].(string)
	if fieldID == "" || !strings.HasPrefix(fieldID, "f_") {
		t.Fatalf("provider field candidate id missing or unexpected: %q", fieldID)
	}
	target["rowCandidateId"] = fieldID
	provider.Steps, _ = json.Marshal(providerSteps)
	// Control copy left intact to prove the coerced resolution is identical.
	controlBytes := append([]byte(nil), provider.Steps...)
	control := &models.Rule{Steps: models.JSON(controlBytes)}

	report, err := catalog.ResolveProviderCandidates(provider, scope.CatalogHash)
	if err != nil {
		t.Fatalf("field-candidate-in-target-slot was not recovered: %v", err)
	}
	if report.FieldToTargetCoercions != 1 {
		t.Fatalf("coercion was not recorded for audit: %+v", report)
	}
	if _, err := catalog.ResolveProviderCandidates(control, scope.CatalogHash); err != nil {
		t.Fatalf("control resolution failed: %v", err)
	}
	if !bytes.Equal(provider.Steps, control.Steps) {
		t.Fatalf("coerced resolution differs from the clean control:\ncoerced %s\ncontrol %s", provider.Steps, control.Steps)
	}
}

// The other observed shape: a SINGLE extraction referencing a field of a
// row target — recovered as a repeated extraction of the row with that one
// field bound, the overwhelming scraping intent.
func TestProviderSingleExtractionOfRowFieldBecomesRepeatedFieldExtraction(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorResultsForTest(
			"results",
			[]selectorCardForTest{
				{classes: "card", title: true},
				{classes: "card", title: true},
			},
		)),
	)
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "extract", "name": "items", "multiple": true,
			"target": map[string]any{"selector": "#results > .card", "visible": true},
			"fields": map[string]any{
				"title": map[string]any{"type": "text", "selector": ".title"},
			},
		},
	})
	trusted := &models.Rule{Steps: models.JSON(steps)}
	prompt, provider, err := catalog.PrepareProviderPrompt(trusted)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	if err := EncodeProviderOrdinaryTargets(provider); err != nil {
		t.Fatal(err)
	}
	var providerSteps []any
	if err := json.Unmarshal(provider.Steps, &providerSteps); err != nil {
		t.Fatal(err)
	}
	step := providerSteps[0].(map[string]any)
	// Grab the field candidate id of the row's title field from the prompt
	// scope and plant the observed failure shape: a SINGLE extraction whose
	// target slot holds the field candidate id.
	fieldID := ""
	for _, target := range scope.Candidates {
		for _, field := range target.FieldCandidates {
			fieldID = field.FieldCandidateID
			break
		}
		if fieldID != "" {
			break
		}
	}
	if fieldID == "" || !strings.HasPrefix(fieldID, "f_") {
		t.Fatalf("no field candidate id found in scope: %q", fieldID)
	}
	step["multiple"] = false
	step["name"] = "title"
	delete(step, "fields")
	step["target"] = map[string]any{"targetCandidateId": fieldID}
	provider.Steps, _ = json.Marshal(providerSteps)

	report, err := catalog.ResolveProviderCandidates(provider, scope.CatalogHash)
	if err != nil {
		t.Fatalf("single-extraction-of-row-field was not recovered: %v", err)
	}
	if report.FieldToTargetCoercions != 1 {
		t.Fatalf("coercion was not recorded: %+v", report)
	}
	var resolved []any
	if err := json.Unmarshal(provider.Steps, &resolved); err != nil {
		t.Fatal(err)
	}
	resolvedStep := resolved[0].(map[string]any)
	if resolvedStep["multiple"] != true {
		t.Fatalf("step was not rewritten to repeated extraction: %v", resolvedStep)
	}
	fields, _ := resolvedStep["fields"].(map[string]any)
	title, _ := fields["title"].(map[string]any)
	if title == nil || title["selector"] == "" {
		t.Fatalf("bound title field missing its resolved selector: %v", fields)
	}
}

// A single extraction naming a ROW target: the provider forgot multiple:true.
// The step flips to repeated extraction; fields the provider already emitted
// stay untouched.
func TestProviderSingleExtractionOfRowTargetFlipsToRepeated(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorResultsForTest(
			"results",
			[]selectorCardForTest{
				{classes: "card", title: true},
				{classes: "card", title: true},
			},
		)),
	)
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "extract", "name": "items", "multiple": true,
			"target": map[string]any{"selector": "#results > .card", "visible": true},
			"fields": map[string]any{
				"title": map[string]any{"type": "text", "selector": ".title"},
			},
		},
	})
	trusted := &models.Rule{Steps: models.JSON(steps)}
	prompt, provider, err := catalog.PrepareProviderPrompt(trusted)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	if err := EncodeProviderOrdinaryTargets(provider); err != nil {
		t.Fatal(err)
	}
	var providerSteps []any
	if err := json.Unmarshal(provider.Steps, &providerSteps); err != nil {
		t.Fatal(err)
	}
	step := providerSteps[0].(map[string]any)
	target := step["target"].(map[string]any)
	rowID, _ := target["rowCandidateId"].(string)
	if rowID == "" || !strings.HasPrefix(rowID, "r_") {
		t.Fatalf("row candidate id missing: %q", rowID)
	}
	// Plant the failure shape: same row id, but a single extraction.
	step["multiple"] = false
	target["targetCandidateId"] = rowID
	delete(target, "rowCandidateId")
	provider.Steps, _ = json.Marshal(providerSteps)

	report, err := catalog.ResolveProviderCandidates(provider, scope.CatalogHash)
	if err != nil {
		t.Fatalf("single-extraction-of-row-target was not recovered: %v", err)
	}
	if report.RowCardinalityCoercions != 1 {
		t.Fatalf("row cardinality coercion was not recorded: %+v", report)
	}
	var resolved []any
	if err := json.Unmarshal(provider.Steps, &resolved); err != nil {
		t.Fatal(err)
	}
	resolvedStep := resolved[0].(map[string]any)
	if resolvedStep["multiple"] != true {
		t.Fatalf("step was not flipped to repeated extraction: %v", resolvedStep)
	}
	fields, _ := resolvedStep["fields"].(map[string]any)
	if _, ok := fields["title"]; !ok {
		t.Fatalf("provider-emitted title field was lost: %v", fields)
	}
}

// An extract step that arrives with fields but no target: re-scoped to the
// single catalog target owning those fields.
func TestProviderMissingTargetRecoveredFromFieldParentage(t *testing.T) {
	catalog := selectorCatalogForTest(t,
		selectorSnapshotForTest(0, "https://example.test/", selectorResultsForTest(
			"results",
			[]selectorCardForTest{
				{classes: "card", title: true},
				{classes: "card", title: true},
			},
		)),
	)
	steps, _ := json.Marshal([]any{
		map[string]any{
			"action": "extract", "name": "items", "multiple": true,
			"target": map[string]any{"selector": "#results > .card", "visible": true},
			"fields": map[string]any{
				"title": map[string]any{"type": "text", "selector": ".title"},
			},
		},
	})
	trusted := &models.Rule{Steps: models.JSON(steps)}
	prompt, provider, err := catalog.PrepareProviderPrompt(trusted)
	if err != nil {
		t.Fatal(err)
	}
	var scope SelectorPromptCatalog
	if err := json.Unmarshal([]byte(prompt), &scope); err != nil {
		t.Fatal(err)
	}
	if err := EncodeProviderOrdinaryTargets(provider); err != nil {
		t.Fatal(err)
	}
	var providerSteps []any
	if err := json.Unmarshal(provider.Steps, &providerSteps); err != nil {
		t.Fatal(err)
	}
	step := providerSteps[0].(map[string]any)
	// Plant the failure shape: drop the target entirely.
	delete(step, "target")
	provider.Steps, _ = json.Marshal(providerSteps)

	report, err := catalog.ResolveProviderCandidates(provider, scope.CatalogHash)
	if err != nil {
		t.Fatalf("missing-target extract step was not recovered: %v", err)
	}
	if report.MissingTargetRecoveries != 1 {
		t.Fatalf("recovery was not recorded: %+v", report)
	}
	var resolved []any
	if err := json.Unmarshal(provider.Steps, &resolved); err != nil {
		t.Fatal(err)
	}
	resolvedStep := resolved[0].(map[string]any)
	target, _ := resolvedStep["target"].(map[string]any)
	if target == nil || target["selector"] == "" {
		t.Fatalf("recovered target missing its resolved selector: %v", target)
	}
	fields, _ := resolvedStep["fields"].(map[string]any)
	if _, ok := fields["title"]; !ok {
		t.Fatalf("title field lost after recovery: %v", fields)
	}
}

func assertProviderWireTargetsForTest(t *testing.T, values []any, path string) {
	t.Helper()
	if err := walkSelectorCandidateActions(values, path, func(step map[string]any, stepPath string) error {
		action := strings.TrimSpace(stringValue(step["action"]))
		if !extractionWorkflowActions[action] {
			if target, ok := step["target"].(map[string]any); ok &&
				!exactMapKeys(target, "family", "value", "name") {
				t.Fatalf("%s.target is not exact provider wire: %#v", stepPath, target)
			}
		}
		if condition, ok := step["condition"].(map[string]any); ok {
			if target, ok := condition["target"].(map[string]any); ok &&
				!exactMapKeys(target, "family", "value", "name") {
				t.Fatalf("%s.condition.target is not exact provider wire: %#v", stepPath, target)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
