package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/events"
)

func TestTaskRegistry_ApplyCreate_HappyPath(t *testing.T) {
	r := newTaskRegistry()
	list, ok := r.applyCreate("ch1",
		`{"subject":"Fix bug","activeForm":"Fixing bug","description":"the bug"}`,
		"Task #42 created successfully: Fix bug",
	)
	require.True(t, ok)
	require.Len(t, list, 1)
	require.Equal(t, "42", list[0].ID)
	require.Equal(t, "Fix bug", list[0].Subject)
	require.Equal(t, "Fixing bug", list[0].ActiveForm)
	require.Equal(t, "the bug", list[0].Description)
	require.Equal(t, "pending", list[0].Status)
}

func TestTaskRegistry_ApplyCreate_NoIDInResult(t *testing.T) {
	r := newTaskRegistry()
	list, ok := r.applyCreate("ch1",
		`{"subject":"Fix bug"}`,
		"unexpected output",
	)
	require.False(t, ok)
	require.Nil(t, list)
}

func TestTaskRegistry_ApplyCreate_BadInputJSON(t *testing.T) {
	r := newTaskRegistry()
	list, ok := r.applyCreate("ch1", `not json`, "Task #1 created successfully: x")
	require.False(t, ok)
	require.Nil(t, list)
}

func TestTaskRegistry_ApplyUpdate_StatusChange(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1",
		`{"subject":"Fix bug","activeForm":"Fixing bug"}`,
		"Task #1 created successfully: Fix bug",
	)
	status := "in_progress"
	in := taskUpdateInput{TaskID: "1", Status: &status}
	list, ok := r.applyUpdate("ch1", marshalJSON(t, in))
	require.True(t, ok)
	require.Len(t, list, 1)
	require.Equal(t, "in_progress", list[0].Status)
}

func TestTaskRegistry_ApplyUpdate_SubjectAndDescription(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1",
		`{"subject":"Old","activeForm":"Doing old"}`,
		"Task #1 created successfully: Old",
	)
	newSubj := "New"
	newDesc := "added"
	newActive := "Doing new"
	in := taskUpdateInput{TaskID: "1", Subject: &newSubj, Description: &newDesc, ActiveForm: &newActive}
	list, ok := r.applyUpdate("ch1", marshalJSON(t, in))
	require.True(t, ok)
	require.Equal(t, "New", list[0].Subject)
	require.Equal(t, "added", list[0].Description)
	require.Equal(t, "Doing new", list[0].ActiveForm)
}

func TestTaskRegistry_ApplyUpdate_Deleted(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1", `{"subject":"a"}`, "Task #1 created successfully: a")
	_, _ = r.applyCreate("ch1", `{"subject":"b"}`, "Task #2 created successfully: b")
	deleted := "deleted"
	in := taskUpdateInput{TaskID: "1", Status: &deleted}
	list, ok := r.applyUpdate("ch1", marshalJSON(t, in))
	require.True(t, ok)
	require.Len(t, list, 1)
	require.Equal(t, "2", list[0].ID)
}

func TestTaskRegistry_ApplyUpdate_UnknownTask(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1", `{"subject":"a"}`, "Task #1 created successfully: a")
	status := "in_progress"
	in := taskUpdateInput{TaskID: "999", Status: &status}
	list, ok := r.applyUpdate("ch1", marshalJSON(t, in))
	require.False(t, ok)
	require.Nil(t, list)
}

func TestTaskRegistry_ApplyUpdate_UnknownChannel(t *testing.T) {
	r := newTaskRegistry()
	status := "in_progress"
	in := taskUpdateInput{TaskID: "1", Status: &status}
	list, ok := r.applyUpdate("ghost", marshalJSON(t, in))
	require.False(t, ok)
	require.Nil(t, list)
}

func TestTaskRegistry_ApplyUpdate_BadJSON(t *testing.T) {
	r := newTaskRegistry()
	list, ok := r.applyUpdate("ch1", "not json")
	require.False(t, ok)
	require.Nil(t, list)
}

func TestTaskRegistry_ApplyUpdate_EmptyTaskID(t *testing.T) {
	r := newTaskRegistry()
	list, ok := r.applyUpdate("ch1", `{"taskId":""}`)
	require.False(t, ok)
	require.Nil(t, list)
}

func TestTaskRegistry_ApplyUpdate_AddBlocksAndBlockedBy(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1", `{"subject":"a"}`, "Task #1 created successfully: a")
	blocks := []string{"2", "3"}
	blockedBy := []string{"4"}
	in := taskUpdateInput{TaskID: "1", AddBlocks: &blocks, AddBlockedBy: &blockedBy}
	list, ok := r.applyUpdate("ch1", marshalJSON(t, in))
	require.True(t, ok)
	require.Equal(t, []string{"2", "3"}, list[0].Blocks)
	require.Equal(t, []string{"4"}, list[0].BlockedBy)

	// Adding again does not duplicate.
	more := []string{"3", "5"}
	in2 := taskUpdateInput{TaskID: "1", AddBlocks: &more}
	list, ok = r.applyUpdate("ch1", marshalJSON(t, in2))
	require.True(t, ok)
	require.Equal(t, []string{"2", "3", "5"}, list[0].Blocks)
}

func TestTaskRegistry_SortOrder(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1", `{"subject":"c"}`, "Task #10 created successfully: c")
	_, _ = r.applyCreate("ch1", `{"subject":"a"}`, "Task #2 created successfully: a")
	list, _ := r.applyCreate("ch1", `{"subject":"b"}`, "Task #7 created successfully: b")
	require.Equal(t, []string{"2", "7", "10"}, taskIDs(list))
}

func TestTaskRegistry_PerChannelIsolation(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1", `{"subject":"a"}`, "Task #1 created successfully: a")
	_, _ = r.applyCreate("ch2", `{"subject":"b"}`, "Task #1 created successfully: b")
	status := "in_progress"
	in := taskUpdateInput{TaskID: "1", Status: &status}
	list1, _ := r.applyUpdate("ch1", marshalJSON(t, in))
	require.Equal(t, "in_progress", list1[0].Status)
	// ch2 entry stays pending.
	deleted := "deleted"
	in2 := taskUpdateInput{TaskID: "1", Status: &deleted}
	list2, ok := r.applyUpdate("ch2", marshalJSON(t, in2))
	require.True(t, ok)
	require.Empty(t, list2)
}

func TestTaskRegistry_Clear(t *testing.T) {
	r := newTaskRegistry()
	_, _ = r.applyCreate("ch1", `{"subject":"a"}`, "Task #1 created successfully: a")
	r.clear("ch1")
	status := "in_progress"
	in := taskUpdateInput{TaskID: "1", Status: &status}
	_, ok := r.applyUpdate("ch1", marshalJSON(t, in))
	require.False(t, ok)
}

func taskIDs(items []events.TaskItem) []string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func marshalJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}
