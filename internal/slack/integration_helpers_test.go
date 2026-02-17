//go:build integration

package slack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	goslack "github.com/slack-go/slack"
	"github.com/tailscale/hujson"
)

// integrationConfig holds tokens loaded from env vars or config file.
type integrationConfig struct {
	BotToken  string
	AppToken  string
	UserToken string // optional
}

// integrationFileConfig is the JSON structure of ~/.loop/config.integration.json.
type integrationFileConfig struct {
	SlackBotToken  string `json:"slack_bot_token"`
	SlackAppToken  string `json:"slack_app_token"`
	SlackUserToken string `json:"slack_user_token"`
}

// loadIntegrationConfig loads tokens with env vars taking precedence
// over ~/.loop/config.integration.json.
func loadIntegrationConfig() (*integrationConfig, error) {
	cfg := &integrationConfig{
		BotToken:  os.Getenv("SLACK_BOT_TOKEN"),
		AppToken:  os.Getenv("SLACK_APP_TOKEN"),
		UserToken: os.Getenv("SLACK_USER_TOKEN"),
	}

	// Fall back to config.integration.json for missing tokens.
	if cfg.BotToken == "" || cfg.AppToken == "" || cfg.UserToken == "" {
		fileCfg, err := loadIntegrationFileConfig()
		if err == nil {
			if cfg.BotToken == "" {
				cfg.BotToken = fileCfg.SlackBotToken
			}
			if cfg.AppToken == "" {
				cfg.AppToken = fileCfg.SlackAppToken
			}
			if cfg.UserToken == "" {
				cfg.UserToken = fileCfg.SlackUserToken
			}
		}
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("SLACK_BOT_TOKEN not set and not found in ~/.loop/config.integration.json")
	}
	if cfg.AppToken == "" {
		return nil, fmt.Errorf("SLACK_APP_TOKEN not set and not found in ~/.loop/config.integration.json")
	}

	return cfg, nil
}

// loadIntegrationFileConfig reads ~/.loop/config.integration.json.
func loadIntegrationFileConfig() (*integrationFileConfig, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(home, ".loop", "config.integration.json"))
	if err != nil {
		return nil, err
	}
	standardJSON, err := hujson.Standardize(data)
	if err != nil {
		return nil, err
	}
	var cfg integrationFileConfig
	if err := json.Unmarshal(standardJSON, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// waitForMessage polls conversation history until a message matching the predicate
// appears after the given timestamp. Uses exponential backoff.
func waitForMessage(client *goslack.Client, channelID, afterTS string,
	predicate func(goslack.Message) bool, timeout time.Duration) (*goslack.Message, error) {

	deadline := time.Now().Add(timeout)
	wait := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		params := &goslack.GetConversationHistoryParameters{
			ChannelID: channelID,
			Oldest:    afterTS,
			Limit:     20,
		}
		resp, err := client.GetConversationHistory(params)
		if err != nil {
			// Rate limited — wait and retry.
			if rlErr, ok := err.(*goslack.RateLimitedError); ok {
				time.Sleep(rlErr.RetryAfter)
				continue
			}
			return nil, fmt.Errorf("get conversation history: %w", err)
		}

		for i := range resp.Messages {
			if predicate(resp.Messages[i]) {
				return &resp.Messages[i], nil
			}
		}

		time.Sleep(wait)
		if wait < 2*time.Second {
			wait = wait * 3 / 2 // 500ms → 750ms → 1125ms → 1687ms → 2s cap
		}
	}

	return nil, fmt.Errorf("timeout waiting for message in channel %s", channelID)
}

// waitForThreadReply polls for a reply in a thread matching the predicate.
func waitForThreadReply(client *goslack.Client, channelID, threadTS string,
	predicate func(goslack.Message) bool, timeout time.Duration) (*goslack.Message, error) {

	deadline := time.Now().Add(timeout)
	wait := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		msgs, _, _, err := client.GetConversationReplies(&goslack.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTS,
			Limit:     20,
		})
		if err != nil {
			if rlErr, ok := err.(*goslack.RateLimitedError); ok {
				time.Sleep(rlErr.RetryAfter)
				continue
			}
			return nil, fmt.Errorf("get conversation replies: %w", err)
		}

		// Skip the first message (parent) and check replies.
		for i := 1; i < len(msgs); i++ {
			if predicate(msgs[i]) {
				return &msgs[i], nil
			}
		}

		time.Sleep(wait)
		if wait < 2*time.Second {
			wait = wait * 3 / 2
		}
	}

	return nil, fmt.Errorf("timeout waiting for thread reply in %s:%s", channelID, threadTS)
}

// waitForReaction polls for a specific reaction on a message.
func waitForReaction(client *goslack.Client, channelID, timestamp, emoji string,
	timeout time.Duration) (bool, error) {

	deadline := time.Now().Add(timeout)
	wait := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		reactions, err := client.GetReactions(goslack.NewRefToMessage(channelID, timestamp),
			goslack.NewGetReactionsParameters())
		if err != nil {
			if rlErr, ok := err.(*goslack.RateLimitedError); ok {
				time.Sleep(rlErr.RetryAfter)
				continue
			}
			return false, fmt.Errorf("get reactions: %w", err)
		}

		for _, r := range reactions {
			if r.Name == emoji {
				return true, nil
			}
		}

		time.Sleep(wait)
		if wait < 2*time.Second {
			wait = wait * 3 / 2
		}
	}

	return false, nil
}

// sendUserMessage sends a message as the user (using a user token client).
func sendUserMessage(userClient *goslack.Client, channelID, text string) (string, error) {
	_, ts, err := userClient.PostMessage(channelID, goslack.MsgOptionText(text, false))
	if err != nil {
		return "", fmt.Errorf("send user message: %w", err)
	}
	return ts, nil
}

// sendUserMention sends a message mentioning the bot as the user.
func sendUserMention(userClient *goslack.Client, channelID, botUserID, text string) (string, error) {
	mention := fmt.Sprintf("<@%s> %s", botUserID, text)
	return sendUserMessage(userClient, channelID, mention)
}

// sendUserThreadReply sends a thread reply as the user.
func sendUserThreadReply(userClient *goslack.Client, channelID, threadTS, text string) (string, error) {
	_, ts, err := userClient.PostMessage(channelID,
		goslack.MsgOptionText(text, false),
		goslack.MsgOptionTS(threadTS),
	)
	if err != nil {
		return "", fmt.Errorf("send user thread reply: %w", err)
	}
	return ts, nil
}

// archiveChannel archives a channel (best-effort cleanup).
func archiveChannel(client *goslack.Client, channelID string) {
	_ = client.ArchiveConversation(channelID)
}

// randomSuffix returns a short random string for unique naming.
func randomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
}

// messageContains returns a predicate that matches messages containing the text.
func messageContains(text string) func(goslack.Message) bool {
	return func(msg goslack.Message) bool {
		return strings.Contains(msg.Text, text)
	}
}
