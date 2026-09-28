package learn

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	"github.com/radutopala/loop/internal/db"
	"github.com/radutopala/loop/internal/types"
)

type PayloadSuite struct {
	suite.Suite
}

func TestPayloadSuite(t *testing.T) {
	suite.Run(t, new(PayloadSuite))
}

func (s *PayloadSuite) TestValidate() {
	long := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name      string
		kind      string
		title     string
		payload   string
		want      any
		canonical string
		wantErr   string
	}{
		{
			name: "prompt shortcut", kind: db.LearnKindPromptShortcut, title: "Add fix-tests",
			payload:   `{"name":"fix-tests","prompt":"make test"}`,
			want:      &PromptShortcut{Name: "fix-tests", Prompt: "make test"},
			canonical: `{"name":"fix-tests","prompt":"make test"}`,
		},
		{
			name: "bash shortcut", kind: db.LearnKindBashShortcut, title: "Add lint",
			payload: `{"name":"lint","description":"Run the linter","command":"make lint"}`,
			want:    &BashShortcut{Name: "lint", Description: "Run the linter", Command: "make lint"},
		},
		{
			name: "scheduled task", kind: db.LearnKindScheduledTask, title: "Nightly deps",
			payload: `{"type":"cron","schedule":"0 9 * * *","prompt":"check deps","auto_delete_sec":60}`,
			want:    &ScheduledTask{Type: "cron", Schedule: "0 9 * * *", Prompt: "check deps", AutoDeleteSec: 60},
		},
		{
			name: "bash task", kind: db.LearnKindScheduledTask, title: "Hourly poll",
			payload: `{"type":"interval","schedule":"1h","bash_script":"curl -s x"}`,
			want:    &ScheduledTask{Type: "interval", Schedule: "1h", BashScript: "curl -s x"},
		},
		{
			name: "mount", kind: db.LearnKindMount, title: "Mount aws",
			payload: `{"mount":"~/.aws:~/.aws:ro"}`, want: &Mount{Mount: "~/.aws:~/.aws:ro"},
		},
		{
			name: "mount without mode", kind: db.LearnKindMount, title: "Mount cache",
			payload: `{"mount":"cache:/cache"}`, want: &Mount{Mount: "cache:/cache"},
		},
		{
			name: "rename", kind: db.LearnKindRename, title: "Rename",
			payload: `{"name":"login bug"}`, want: &Rename{Name: "login bug"},
		},
		{
			name: "description", kind: db.LearnKindDescription, title: "Describe",
			payload: `{"description":"chasing the login bug"}`, want: &Description{Description: "chasing the login bug"},
		},

		{
			name: "ticket url", kind: db.LearnKindTicketURL, title: "Link ticket",
			payload: `{"ticket_url":"https://tracker.example.com/T-1"}`, want: &TicketURL{TicketURL: "https://tracker.example.com/T-1"},
		},

		{name: "no title", kind: db.LearnKindRename, title: "  ", payload: `{"name":"x"}`, wantErr: "title is required"},
		{name: "long title", kind: db.LearnKindRename, title: long(201), payload: `{"name":"x"}`, wantErr: "title is longer than 200"},
		{name: "unknown kind", kind: "wish", title: "t", payload: `{}`, wantErr: `unknown kind "wish"`},
		{name: "bad json", kind: db.LearnKindRename, title: "t", payload: `{`, wantErr: "rename payload"},
		{name: "unknown field", kind: db.LearnKindRename, title: "t", payload: `{"name":"x","command":"y"}`, wantErr: `unknown field "command"`},
		{name: "shortcut without name", kind: db.LearnKindPromptShortcut, title: "t", payload: `{"prompt":"p"}`, wantErr: "name is required"},
		{name: "shortcut without prompt", kind: db.LearnKindPromptShortcut, title: "t", payload: `{"name":"n"}`, wantErr: "prompt is required"},
		{name: "bash without command", kind: db.LearnKindBashShortcut, title: "t", payload: `{"name":"n"}`, wantErr: "command is required"},
		{name: "task type", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"manual","schedule":"x","prompt":"p"}`, wantErr: `type "manual" must be`},
		{name: "task schedule", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"cron","prompt":"p"}`, wantErr: "schedule is required"},
		{name: "task bad cron", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"cron","schedule":"every day","prompt":"p"}`, wantErr: `invalid task schedule: cron schedule "every day"`},
		{name: "task bad interval", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"interval","schedule":"daily","prompt":"p"}`, wantErr: `invalid task schedule: interval "daily"`},
		{name: "task bad once", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"once","schedule":"tomorrow","prompt":"p"}`, wantErr: `once schedule "tomorrow" must be RFC3339`},
		{name: "task both bodies", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"cron","schedule":"0 9 * * *","prompt":"p","bash_script":"b"}`, wantErr: "exactly one of prompt or bash_script"},
		{name: "task no body", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"cron","schedule":"0 9 * * *"}`, wantErr: "exactly one of prompt or bash_script"},
		{name: "task auto delete", kind: db.LearnKindScheduledTask, title: "t", payload: `{"type":"cron","schedule":"0 9 * * *","prompt":"p","auto_delete_sec":-1}`, wantErr: "auto_delete_sec"},
		{name: "mount one part", kind: db.LearnKindMount, title: "t", payload: `{"mount":"/a"}`, wantErr: "must be host_path:container_path"},
		{name: "mount empty side", kind: db.LearnKindMount, title: "t", payload: `{"mount":":/a"}`, wantErr: "must be host_path:container_path"},
		{name: "mount four parts", kind: db.LearnKindMount, title: "t", payload: `{"mount":"a:b:ro:x"}`, wantErr: "must be host_path:container_path"},
		{name: "mount mode", kind: db.LearnKindMount, title: "t", payload: `{"mount":"a:b:z"}`, wantErr: `mount mode "z"`},
		{name: "long name", kind: db.LearnKindRename, title: "t", payload: `{"name":"` + long(101) + `"}`, wantErr: "name is longer than 100"},
		{name: "empty description", kind: db.LearnKindDescription, title: "t", payload: `{"description":" "}`, wantErr: "description is required"},
		{name: "long description", kind: db.LearnKindDescription, title: "t", payload: `{"description":"` + long(501) + `"}`, wantErr: "description is longer than 500"},
		{name: "empty ticket url", kind: db.LearnKindTicketURL, title: "t", payload: `{"ticket_url":" "}`, wantErr: "ticket_url is required"},
		{name: "bad ticket url", kind: db.LearnKindTicketURL, title: "t", payload: `{"ticket_url":"T-1"}`, wantErr: "absolute http(s) URL"},
		{name: "gate rule", kind: db.LearnKindGateRule, title: "t", payload: `{"type":"socket","rule":{}}`, wantErr: `type "socket" must be`},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			canonical, err := Validate(tc.kind, tc.title, json.RawMessage(tc.payload))
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			// What's stored decodes back to the proposed payload.
			got, err := Decode(tc.kind, canonical)
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.want, got)
			if tc.canonical != "" {
				require.JSONEq(s.T(), tc.canonical, string(canonical))
			}
		})
	}
}

func (s *PayloadSuite) TestGateRuleConfigRule() {
	tests := []struct {
		name     string
		payload  string
		wantKey  string
		wantRule any
		wantErr  string
	}{
		{
			name:     "command",
			payload:  `{"type":"command","rule":{"commands":["git"],"args_patterns":["^push"],"decision":"approve","message":"git push"}}`,
			wantKey:  "command_rules",
			wantRule: &types.CommandRule{Commands: []string{"git"}, ArgsPatterns: []string{"^push"}, Decision: types.DecisionApprove, Message: "git push"},
		},
		{
			name:     "file",
			payload:  `{"type":"file","rule":{"paths":["**/.env"],"operations":["read"],"decision":"deny"}}`,
			wantKey:  "file_rules",
			wantRule: &types.FileRule{Paths: []string{"**/.env"}, Operations: []string{"read"}, Decision: types.DecisionDeny},
		},
		{
			name:     "command by args only",
			payload:  `{"type":"command","rule":{"args_patterns":["--force"],"decision":"deny"}}`,
			wantKey:  "command_rules",
			wantRule: &types.CommandRule{ArgsPatterns: []string{"--force"}, Decision: types.DecisionDeny},
		},
		{
			name:     "path",
			payload:  `{"type":"path","rule":{"pattern":"/run/x.sock","decision":"allow"}}`,
			wantKey:  "path_rules",
			wantRule: &types.PathRule{Pattern: "/run/x.sock", Decision: types.DecisionAllow},
		},
		{name: "bad decision", payload: `{"type":"command","rule":{"commands":["git"],"decision":"maybe"}}`, wantErr: "unknown decision"},
		{name: "bad regex", payload: `{"type":"command","rule":{"args_patterns":["("],"decision":"deny"}}`, wantErr: "args_patterns"},
		{name: "bad operation", payload: `{"type":"file","rule":{"paths":["/x"],"operations":["fly"],"decision":"deny"}}`, wantErr: `unknown operation "fly"`},
		{name: "command without subject", payload: `{"type":"command","rule":{"decision":"allow"}}`, wantErr: "needs commands or args_patterns"},
		{name: "file without paths", payload: `{"type":"file","rule":{"operations":["write"],"decision":"allow"}}`, wantErr: "needs paths"},
		{name: "path without pattern", payload: `{"type":"path","rule":{"decision":"deny"}}`, wantErr: "pattern is required"},
		{name: "unknown rule field", payload: `{"type":"path","rule":{"pattern":"/x","decision":"deny","paths":[]}}`, wantErr: `unknown field "paths"`},
	}
	for _, tc := range tests {
		s.Run(tc.name, func() {
			v, err := Decode(db.LearnKindGateRule, json.RawMessage(tc.payload))
			if tc.wantErr != "" {
				require.ErrorContains(s.T(), err, tc.wantErr)
				return
			}
			require.NoError(s.T(), err)
			key, rule, err := v.(*GateRule).ConfigRule()
			require.NoError(s.T(), err)
			require.Equal(s.T(), tc.wantKey, key)
			require.Equal(s.T(), tc.wantRule, rule)
		})
	}
}
