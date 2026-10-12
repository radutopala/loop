package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/radutopala/loop/internal/config"
	"github.com/radutopala/loop/internal/db"
)

type mockPluginCaller struct {
	mock.Mock
}

func (m *mockPluginCaller) RunPluginAction(ctx context.Context, pluginID, action, channelID, dirPath, callID string, input json.RawMessage) error {
	return m.Called(ctx, pluginID, action, channelID, dirPath, callID, string(input)).Error(0)
}

func (m *mockPluginCaller) CallPluginTool(ctx context.Context, pluginID, tool, channelID, dirPath, callID string, input json.RawMessage) (string, bool, error) {
	args := m.Called(ctx, pluginID, tool, channelID, dirPath, callID, string(input))
	return args.String(0), args.Bool(1), args.Error(2)
}

func (s *EngineSuite) TestWithPlugins() {
	p := new(mockPluginCaller)
	e := NewEngine(s.store, s.runner, s.bashRunner, s.broadcaster, nil, "", config.WorkflowConcurrency{}, slog.Default(), WithPlugins(p))
	require.Equal(s.T(), p, e.(*defaultEngine).plugins)
}

func (s *EngineSuite) TestExecutePluginNode() {
	run := &db.WorkflowRun{ID: "r1", ChannelID: "ch1", DirPath: "/src"}
	tests := []struct {
		name       string
		node       config.NodeDef
		setup      func(p *mockPluginCaller)
		wantOutput string
		wantInput  string
		wantErr    string
	}{
		{
			name: "action",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Action: "daily"},
			setup: func(p *mockPluginCaller) {
				p.On("RunPluginAction", mock.Anything, "quiz", "daily", "ch1", "/src", "workflow:r1:n1", "").Return(nil).Once()
			},
			wantOutput: "accepted",
		},
		{
			name: "action with templated input",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Action: "daily", Input: json.RawMessage(`{"topic":"{{.Inputs.topic}}","count":3}`)},
			setup: func(p *mockPluginCaller) {
				p.On("RunPluginAction", mock.Anything, "quiz", "daily", "ch1", "/src", "workflow:r1:n1", `{"count":3,"topic":"go"}`).Return(nil).Once()
			},
			wantOutput: "accepted",
			wantInput:  `{"count":3,"topic":"go"}`,
		},
		{
			name: "action fails",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Action: "daily"},
			setup: func(p *mockPluginCaller) {
				p.On("RunPluginAction", mock.Anything, "quiz", "daily", "ch1", "/src", "workflow:r1:n1", "").Return(errors.New("not installed")).Once()
			},
			wantErr: "running quiz's action daily: not installed",
		},
		{
			name: "tool with templated input",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask",
				Input: json.RawMessage(`{"topic":"{{.Inputs.topic}}","count":3,"tags":["{{.NodeOutputs.prev}}",true],"opts":{"note":"{{.ChannelID}}"}}`)},
			setup: func(p *mockPluginCaller) {
				p.On("CallPluginTool", mock.Anything, "quiz", "ask", "ch1", "/src", "workflow:r1:n1",
					`{"count":3,"opts":{"note":"ch1"},"tags":["earlier",true],"topic":"go"}`).Return("a question", false, nil).Once()
			},
			wantOutput: "a question",
			wantInput:  `{"count":3,"opts":{"note":"ch1"},"tags":["earlier",true],"topic":"go"}`,
		},
		{
			name: "tool without input",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask"},
			setup: func(p *mockPluginCaller) {
				p.On("CallPluginTool", mock.Anything, "quiz", "ask", "ch1", "/src", "workflow:r1:n1", `{}`).Return("ok", false, nil).Once()
			},
			wantOutput: "ok",
			wantInput:  `{}`,
		},
		{
			name: "tool answers an error",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask"},
			setup: func(p *mockPluginCaller) {
				p.On("CallPluginTool", mock.Anything, "quiz", "ask", "ch1", "/src", "workflow:r1:n1", `{}`).Return("no topic", true, nil).Once()
			},
			wantOutput: "no topic",
			wantInput:  `{}`,
			wantErr:    "quiz's tool ask answered an error: no topic",
		},
		{
			name: "tool call fails",
			node: config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask"},
			setup: func(p *mockPluginCaller) {
				p.On("CallPluginTool", mock.Anything, "quiz", "ask", "ch1", "/src", "workflow:r1:n1", `{}`).Return("", false, errors.New("no such tool")).Once()
			},
			wantInput: `{}`,
			wantErr:   "calling quiz's tool ask: no such tool",
		},
		{
			name:    "input not an object",
			node:    config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`[1]`)},
			wantErr: "input must be a JSON object",
		},
		{
			name:    "bad template",
			node:    config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`{"a":["{{"]}`)},
			wantErr: "rendering input template",
		},
		{
			name:    "bad template in a nested object",
			node:    config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`{"a":{"b":"{{"}}`)},
			wantErr: "rendering input template",
		},
	}
	for _, tt := range tests {
		s.Run(tt.name, func() {
			p := new(mockPluginCaller)
			if tt.setup != nil {
				tt.setup(p)
			}
			e := s.engine.(*defaultEngine)
			e.plugins = p
			runCtx := &RunContext{
				Inputs:      map[string]string{"topic": "go"},
				NodeOutputs: map[string]string{"prev": "earlier"},
				ChannelID:   "ch1",
			}
			var mu sync.Mutex
			res, err := e.executePluginNode(context.Background(), run, &tt.node, runCtx, &mu)
			require.Equal(s.T(), tt.wantOutput, res.output)
			require.Equal(s.T(), tt.wantInput, res.input)
			if tt.wantErr != "" {
				require.ErrorContains(s.T(), err, tt.wantErr)
				require.NotContains(s.T(), runCtx.NodeOutputs, "n1")
			} else {
				require.NoError(s.T(), err)
				require.Equal(s.T(), tt.wantOutput, runCtx.NodeOutputs["n1"])
			}
			p.AssertExpectations(s.T())
		})
	}
}

func (s *EngineSuite) TestExecutePluginNodePluginsOff() {
	e := s.engine.(*defaultEngine)
	node := &config.NodeDef{ID: "n1", Type: config.NodeTypePlugin, Plugin: "quiz", Action: "daily"}
	var mu sync.Mutex
	_, err := e.executePluginNode(context.Background(), &db.WorkflowRun{ID: "r1"}, node, &RunContext{}, &mu)
	require.ErrorIs(s.T(), err, errPluginsOff)
}

// TestStartRunPluginNode runs a workflow whose tool node's output feeds a
// bash node, in the run's own channel.
func (s *EngineSuite) TestStartRunPluginNode() {
	p := new(mockPluginCaller)
	s.engine.(*defaultEngine).plugins = p
	s.workflows = []config.WorkflowDef{{
		Name: "quiz",
		Nodes: []config.NodeDef{
			{ID: "ask", Type: config.NodeTypePlugin, Plugin: "quiz", Tool: "ask", Input: json.RawMessage(`{"topic":"{{.Inputs.topic}}"}`)},
			{ID: "say", Type: config.NodeTypeBash, Script: "echo {{.NodeOutputs.ask}}", DependsOn: []string{"ask"}},
		},
	}}

	s.expectRunPersistence()
	done := s.waitForRunStatus()

	p.On("CallPluginTool", mock.Anything, "quiz", "ask", "ch1", "/src", mock.AnythingOfType("string"), `{"topic":"go"}`).Return("why", false, nil).Once()
	s.bashRunner.On("RunBash", mock.Anything, "echo 'why'", "ch1", "/src", "").Return("why\n", nil)

	runID, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "quiz", ChannelID: "ch1", DirPath: "/src", Inputs: map[string]string{"topic": "go"}})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)
	p.AssertCalled(s.T(), "CallPluginTool", mock.Anything, "quiz", "ask", "ch1", "/src", "workflow:"+runID+":ask", `{"topic":"go"}`)
	s.bashRunner.AssertCalled(s.T(), "RunBash", mock.Anything, "echo 'why'", "ch1", "/src", "")
}

// TestStartRunPluginNodeInvalid rejects a plugin node without a plugin
// before the run starts.
func (s *EngineSuite) TestStartRunPluginNodeInvalid() {
	s.workflows = []config.WorkflowDef{{
		Name:  "bad",
		Nodes: []config.NodeDef{{ID: "n1", Type: config.NodeTypePlugin, Action: "daily"}},
	}}
	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "bad"})
	require.ErrorContains(s.T(), err, `plugin node "n1": plugin is required`)
}

// TestLoopBodyPluginChild runs a plugin action in each loop iteration.
func (s *EngineSuite) TestLoopBodyPluginChild() {
	p := new(mockPluginCaller)
	s.engine.(*defaultEngine).plugins = p
	s.workflows = []config.WorkflowDef{{
		Name: "loop-plugin",
		Nodes: []config.NodeDef{{
			ID:            "loop",
			Type:          config.NodeTypeLoop,
			MaxIterations: 2,
			Body:          []*config.NodeDef{{ID: "tick", Type: config.NodeTypePlugin, Plugin: "quiz", Action: "daily"}},
		}},
	}}

	s.expectRunPersistence()
	done := s.waitForTerminalStatus()

	var calls atomic.Int32
	p.On("RunPluginAction", mock.Anything, "quiz", "daily", "", "", mock.Anything, "").Return(nil).Run(func(_ mock.Arguments) { calls.Add(1) })

	_, err := s.engine.StartRun(context.Background(), StartRunOptions{WorkflowName: "loop-plugin"})
	require.NoError(s.T(), err)

	s.awaitStatus(done, db.WorkflowRunStatusCompleted)
	require.Equal(s.T(), int32(2), calls.Load())
}
