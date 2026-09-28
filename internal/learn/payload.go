package learn

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/radutopala/loop/internal/agentgate"
	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/scheduler"
	"github.com/radutopala/loop/internal/types"
)

// Limits on what a proposal may carry.
const (
	MaxTitleLen       = 200
	MaxNameLen        = 100
	MaxDescriptionLen = 500
)

// PromptShortcut is a prompt_shortcut proposal's payload.
type PromptShortcut struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Prompt      string `json:"prompt"`
}

// BashShortcut is a bash_shortcut proposal's payload.
type BashShortcut struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Command     string `json:"command"`
}

// ScheduledTask is a scheduled_task proposal's payload.
type ScheduledTask struct {
	Type          string `json:"type"`
	Schedule      string `json:"schedule"`
	Prompt        string `json:"prompt,omitempty"`
	BashScript    string `json:"bash_script,omitempty"`
	AutoDeleteSec int    `json:"auto_delete_sec,omitempty"`
}

// GateRule is a gate_rule proposal's payload: an agentgate rule and the list
// it goes in.
type GateRule struct {
	Type string          `json:"type"`
	Rule json.RawMessage `json:"rule"`
}

// Mount is a mount proposal's payload.
type Mount struct {
	Mount string `json:"mount"`
}

// Rename is a rename proposal's payload.
type Rename struct {
	Name string `json:"name"`
}

// Description is a description proposal's payload.
type Description struct {
	Description string `json:"description"`
}

// TicketURL is a ticket_url proposal's payload.
type TicketURL struct {
	TicketURL string `json:"ticket_url"`
}

// Validate checks a proposal's kind, title and payload. It returns the
// payload's canonical JSON, which is what gets stored and what Decode reads
// back when the proposal is applied.
func Validate(kind, title string, payload json.RawMessage) (json.RawMessage, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, errors.New("title is required")
	}
	if utf8.RuneCountInString(title) > MaxTitleLen {
		return nil, fmt.Errorf("title is longer than %d characters", MaxTitleLen)
	}
	decoded, err := Decode(kind, payload)
	if err != nil {
		return nil, err
	}
	canonical, _ := json.Marshal(decoded)
	return canonical, nil
}

// Decode decodes and checks a stored or proposed payload of the given kind.
func Decode(kind string, payload json.RawMessage) (any, error) {
	var (
		v     any
		check func() error
	)
	switch kind {
	case db.LearnKindPromptShortcut:
		p := &PromptShortcut{}
		v, check = p, func() error { return checkShortcut(p.Name, p.Prompt, "prompt") }
	case db.LearnKindBashShortcut:
		p := &BashShortcut{}
		v, check = p, func() error { return checkShortcut(p.Name, p.Command, "command") }
	case db.LearnKindScheduledTask:
		p := &ScheduledTask{}
		v, check = p, p.check
	case db.LearnKindGateRule:
		p := &GateRule{}
		v, check = p, func() error { _, _, err := p.ConfigRule(); return err }
	case db.LearnKindMount:
		p := &Mount{}
		v, check = p, func() error { return checkMount(p.Mount) }
	case db.LearnKindRename:
		p := &Rename{}
		v, check = p, func() error { return checkText("name", p.Name, MaxNameLen, true) }
	case db.LearnKindDescription:
		p := &Description{}
		v, check = p, func() error { return checkText("description", p.Description, MaxDescriptionLen, true) }
	case db.LearnKindTicketURL:
		p := &TicketURL{}
		v, check = p, p.check
	default:
		return nil, fmt.Errorf("unknown kind %q (must be one of %s)", kind, strings.Join(Kinds, ", "))
	}
	if err := decodeStrict(payload, v); err != nil {
		return nil, fmt.Errorf("%s payload: %w", kind, err)
	}
	if err := check(); err != nil {
		return nil, fmt.Errorf("%s payload: %w", kind, err)
	}
	return v, nil
}

// decodeStrict decodes data into v, rejecting unknown fields so a payload
// shaped for another kind fails instead of silently dropping its fields.
func decodeStrict(data json.RawMessage, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func checkShortcut(name, body, bodyField string) error {
	if err := checkText("name", name, MaxNameLen, true); err != nil {
		return err
	}
	if strings.TrimSpace(body) == "" {
		return fmt.Errorf("%s is required", bodyField)
	}
	return nil
}

// checkText checks a trimmed text field's length, and that it's set when
// required.
func checkText(field, value string, maxLen int, required bool) error {
	value = strings.TrimSpace(value)
	if required && value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if utf8.RuneCountInString(value) > maxLen {
		return fmt.Errorf("%s is longer than %d characters", field, maxLen)
	}
	return nil
}

func (t *ScheduledTask) check() error {
	switch db.TaskType(t.Type) {
	case db.TaskTypeCron, db.TaskTypeInterval, db.TaskTypeOnce:
	default:
		return fmt.Errorf("type %q must be cron, interval or once", t.Type)
	}
	if strings.TrimSpace(t.Schedule) == "" {
		return errors.New("schedule is required")
	}
	if err := scheduler.ValidateSchedule(db.TaskType(t.Type), t.Schedule); err != nil {
		return err
	}
	if (strings.TrimSpace(t.Prompt) == "") == (strings.TrimSpace(t.BashScript) == "") {
		return errors.New("give exactly one of prompt or bash_script")
	}
	if t.AutoDeleteSec < 0 {
		return errors.New("auto_delete_sec must not be negative")
	}
	return nil
}

func (t *TicketURL) check() error {
	u, err := types.NormalizeTicketURL(t.TicketURL)
	if err != nil {
		return err
	}
	if u == "" {
		return errors.New("ticket_url is required")
	}
	return nil
}

// ConfigRule returns the agentgate list the rule goes in (e.g.
// "command_rules") and the typed rule, after checking it compiles.
func (g *GateRule) ConfigRule() (string, any, error) {
	var (
		key   string
		rule  any
		paths []types.PathRule
		cmds  []types.CommandRule
		files []types.FileRule
	)
	switch g.Type {
	case "path":
		key, rule = "path_rules", &types.PathRule{}
	case "command":
		key, rule = "command_rules", &types.CommandRule{}
	case "file":
		key, rule = "file_rules", &types.FileRule{}
	default:
		return "", nil, fmt.Errorf("type %q must be path, command or file", g.Type)
	}
	if err := decodeStrict(g.Rule, rule); err != nil {
		return "", nil, fmt.Errorf("rule: %w", err)
	}
	switch r := rule.(type) {
	case *types.PathRule:
		paths = []types.PathRule{*r}
	case *types.CommandRule:
		cmds = []types.CommandRule{*r}
	case *types.FileRule:
		files = []types.FileRule{*r}
	}
	if _, err := agentgate.CompilePolicy(types.DecisionAllow, paths, cmds, files); err != nil {
		return "", nil, fmt.Errorf("rule: %w", err)
	}
	return key, rule, nil
}

// checkMount checks a host_path:container_path[:ro|rw] spec.
func checkMount(spec string) error {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || len(parts) > 3 || parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("mount %q must be host_path:container_path[:ro|rw]", spec)
	}
	if len(parts) == 3 && parts[2] != "ro" && parts[2] != "rw" {
		return fmt.Errorf("mount mode %q must be ro or rw", parts[2])
	}
	return nil
}
