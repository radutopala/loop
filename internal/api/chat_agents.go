package api

import (
	"context"
	"strings"

	"github.com/radutopala/loop/internal/agent"
	"github.com/radutopala/loop/internal/agentregistry"
	"github.com/radutopala/loop/internal/db"
)

// sideAgentPrefix starts the agent ID of a side chat, which is its channel
// ID: side chats' IDs start with it.
const sideAgentPrefix = "side-"

// agentListItem is an agent as GET /api/agents lists it. Self marks the
// chat the request came from, which a chat's agent can't tell from the
// list by ID: every chat runs as agent "chat".
type agentListItem struct {
	*agentregistry.AgentInfo
	Self bool `json:"self,omitempty"`
}

// agentGroup returns the channel whose agents channelID's agents work
// with: a side chat's parent, or channelID itself. chat is channelID's own
// chat agent ID: "chat", or a side chat's.
func (s *Server) agentGroup(ctx context.Context, channelID string) (group, chat string, err error) {
	if s.store == nil {
		return channelID, agent.ChatAgentID, nil
	}
	ch, err := s.store.GetChannel(ctx, channelID)
	if err != nil {
		return "", "", err
	}
	if ch != nil && ch.Kind == db.ChannelKindSide && ch.ParentID != "" {
		return ch.ParentID, ch.ChannelID, nil
	}
	return channelID, agent.ChatAgentID, nil
}

// chatAgents lists the chats of group as agents: its own chat and its side
// chats. A chat is an agent whether or not its live session runs: a
// message starts one.
func (s *Server) chatAgents(ctx context.Context, group string) ([]*agentregistry.AgentInfo, error) {
	if s.store == nil {
		return nil, nil
	}
	ch, err := s.store.GetChannel(ctx, group)
	if err != nil || ch == nil {
		return nil, err
	}
	chats := []*agentregistry.AgentInfo{s.chatAgent(ctx, agent.ChatAgentID, ch, "Chat")}
	sides, err := s.store.ListSideChats(ctx, group)
	if err != nil {
		return nil, err
	}
	for _, side := range sides {
		chats = append(chats, s.chatAgent(ctx, side.ChannelID, side, side.Name))
	}
	return chats, nil
}

// chatAgent describes the chat of ch as an agent, running while its live
// session takes a turn. Its times are the channel's.
func (s *Server) chatAgent(ctx context.Context, agentID string, ch *db.Channel, name string) *agentregistry.AgentInfo {
	status := "idle"
	if s.live != nil && s.live.LiveSession(ctx, ch.ChannelID).Busy {
		status = "running"
	}
	return &agentregistry.AgentInfo{AgentID: agentID, ChannelID: ch.ChannelID, Name: name, Status: status, CreatedAt: ch.CreatedAt, UpdatedAt: ch.UpdatedAt}
}

// chatTarget returns the channel of the chat agentID names in group, or ""
// when it names none.
func (s *Server) chatTarget(ctx context.Context, group, agentID string) (string, error) {
	if agentID == agent.ChatAgentID {
		return group, nil
	}
	if !strings.HasPrefix(agentID, sideAgentPrefix) || s.store == nil {
		return "", nil
	}
	side, err := s.store.GetChannel(ctx, agentID)
	if err != nil || side == nil || side.Kind != db.ChannelKindSide || side.ParentID != group {
		return "", err
	}
	return side.ChannelID, nil
}
