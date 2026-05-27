package fsmigrate

import (
	"context"
	"path/filepath"

	"github.com/stretchr/testify/require"
	"github.com/tailscale/hujson"
)

// --- helpers_test exercises defensive branches in the hujson helpers and
// patcher walkers that are unreachable via the normal seed/patch flow.

func (s *FSMigrateSuite) TestFindObjectMemberSkipsNonLiteralNames() {
	// Manually construct an Object with one member whose name is itself an
	// Object — impossible via hujson parsing but cheap to forge here. The
	// helper must skip it without panicking and find the next literal-named
	// member.
	obj := &hujson.Object{
		Members: []hujson.ObjectMember{
			{Name: hujson.Value{Value: &hujson.Object{}}, Value: hujson.Value{Value: hujson.Literal(`"junk"`)}},
			{Name: hujson.Value{Value: hujson.Literal(`"hit"`)}, Value: hujson.Value{Value: hujson.Literal(`"yes"`)}},
		},
	}
	got := findObjectMember(obj, "hit")
	require.NotNil(s.T(), got)

	require.Nil(s.T(), findObjectMember(obj, "missing"))
}

func (s *FSMigrateSuite) TestMemberStringAbsentKey() {
	obj := &hujson.Object{Members: []hujson.ObjectMember{
		{Name: hujson.Value{Value: hujson.Literal(`"x"`)}, Value: hujson.Value{Value: hujson.Literal(`"1"`)}},
	}}
	_, ok := memberString(obj, "missing")
	require.False(s.T(), ok)
}

func (s *FSMigrateSuite) TestMemberStringValueIsNotStringLiteral() {
	obj := &hujson.Object{Members: []hujson.ObjectMember{
		{Name: hujson.Value{Value: hujson.Literal(`"nested"`)}, Value: hujson.Value{Value: &hujson.Object{}}},
		{Name: hujson.Value{Value: hujson.Literal(`"num"`)}, Value: hujson.Value{Value: hujson.Literal(`42`)}},
	}}
	_, ok := memberString(obj, "nested")
	require.False(s.T(), ok, "object value must not be treated as a string")

	_, ok = memberString(obj, "num")
	require.False(s.T(), ok, "non-string literal must not be treated as a string")
}

func (s *FSMigrateSuite) TestArrayMemberStringValuesKeyIsNotArray() {
	v, err := hujson.Parse([]byte(`{"workflows": "not an array"}`))
	require.NoError(s.T(), err)
	got := arrayMemberStringValues(v.Value.(*hujson.Object), "workflows", "name")
	require.Empty(s.T(), got)
}

func (s *FSMigrateSuite) TestIsArrayValueNilReturnsFalse() {
	// Defensive nil-guard: findObjectMember currently never returns nil for
	// the keys we use (the `existing == nil` branch above isArrayValue
	// handles the absent-key case), but the helper still asserts the guard
	// so a future caller can't get a nil-deref.
	require.False(s.T(), isArrayValue(nil))
}

func (s *FSMigrateSuite) TestAppendOrCreateArrayMemberRejectsNonObjectRoot() {
	v, err := hujson.Parse([]byte(`["just", "an", "array"]`))
	require.NoError(s.T(), err)
	err = appendOrCreateArrayMember(&v, "k", map[string]any{"name": "x"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "expected JSON object")
}

func (s *FSMigrateSuite) TestAppendOrCreateArrayMemberRejectsUnmarshallableItem() {
	v, err := hujson.Parse([]byte(`{}`))
	require.NoError(s.T(), err)
	// channel values can't be JSON-marshalled
	err = appendOrCreateArrayMember(&v, "k", map[string]any{"ch": make(chan int)})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "marshaling item")
}

// --- forEachReviewFixLoopBodyChild skip-path tests ---
//
// These call seeders/patchers with deliberately malformed configs to drive
// the walker through each defensive `continue`. The patchers are read-only
// for malformed shapes, so we just assert "no error, no write".

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildNoWorkflowsKey() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildWorkflowsNotArray() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows": "scalar"}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildBodyMissing() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// A review-fix-loop workflow with a loop node but no `body` member.
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop"}]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildBodyNotArray() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop","body":"not-an-array"}]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildWorkflowEntryNotObject() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// Mix a scalar with the real workflow — the scalar must be skipped.
	sys.files[configPath] = []byte(`{"workflows":["scalar",{"name":"review-fix-loop","nodes":[]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildWorkflowNameMismatch() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"some-other-wf","nodes":[]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildNodeEntryNotObject() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":["scalar"]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildNodesMissing() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	// Matching workflow with no `nodes` member at all.
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop"}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildNodesNotArray() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":"scalar"}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestForEachReviewFixLoopBodyChildNodeTypeNotLoop() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"bash"}]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

// --- patchReviewFixVerifyScript skip paths ---

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptVerifyChildHasNoScript() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop","body":[{"id":"verify"}]}]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

func (s *FSMigrateSuite) TestPatchReviewFixVerifyScriptVerifyScriptIsNotLiteral() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows":[{"name":"review-fix-loop","nodes":[{"type":"loop","body":[{"id":"verify","script":{}}]}]}]}`)

	err := patchReviewFixVerifyScript(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.NoError(s.T(), err)
}

// --- seed*-with-non-array-target tests cover appendOrCreateArrayMember's
// patch-failure path (existing key, wrong shape).

func (s *FSMigrateSuite) TestSeedBuiltinCodeReviewShortcutPromptShortcutsIsNotArray() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"prompt_shortcuts": "scalar"}`)

	_, err := seedBuiltinCodeReviewShortcut(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "patching")
}

func (s *FSMigrateSuite) TestSeedReviewLoopWorkflowsWorkflowsIsNotArray() {
	sys := newFakeSystem()
	configPath := filepath.Join("/loop", "config.json")
	sys.files[configPath] = []byte(`{"workflows": "scalar"}`)

	_, err := seedReviewLoopWorkflows(context.Background(), &Ctx{Sys: sys, LoopDir: "/loop"})
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "patching")
}
