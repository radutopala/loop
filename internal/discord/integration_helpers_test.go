//go:build integration

package discord

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/tailscale/hujson"
)

const (
	defaultTimeout = 30 * time.Second
	apiDelay       = 200 * time.Millisecond
)

// discordIntegrationConfig holds tokens loaded from env vars or config file.
type discordIntegrationConfig struct {
	Token   string
	AppID   string
	GuildID string
}

// discordIntegrationFileConfig is the JSON structure of ~/.loop/config.integration.json.
type discordIntegrationFileConfig struct {
	DiscordToken   string `json:"discord_token"`
	DiscordAppID   string `json:"discord_app_id"`
	DiscordGuildID string `json:"discord_guild_id"`
}

// loadDiscordIntegrationConfig loads tokens with env vars taking precedence
// over ~/.loop/config.integration.json.
func loadDiscordIntegrationConfig() (*discordIntegrationConfig, error) {
	cfg := &discordIntegrationConfig{
		Token:   os.Getenv("DISCORD_BOT_TOKEN"),
		AppID:   os.Getenv("DISCORD_APP_ID"),
		GuildID: os.Getenv("DISCORD_GUILD_ID"),
	}

	// Fall back to config.integration.json for missing values.
	if cfg.Token == "" || cfg.AppID == "" || cfg.GuildID == "" {
		fileCfg, err := loadDiscordIntegrationFileConfig()
		if err == nil {
			if cfg.Token == "" {
				cfg.Token = fileCfg.DiscordToken
			}
			if cfg.AppID == "" {
				cfg.AppID = fileCfg.DiscordAppID
			}
			if cfg.GuildID == "" {
				cfg.GuildID = fileCfg.DiscordGuildID
			}
		}
	}

	if cfg.Token == "" {
		return nil, fmt.Errorf("DISCORD_BOT_TOKEN not set and not found in ~/.loop/config.integration.json")
	}
	if cfg.AppID == "" {
		return nil, fmt.Errorf("DISCORD_APP_ID not set and not found in ~/.loop/config.integration.json")
	}
	if cfg.GuildID == "" {
		return nil, fmt.Errorf("DISCORD_GUILD_ID not set and not found in ~/.loop/config.integration.json")
	}

	return cfg, nil
}

// loadDiscordIntegrationFileConfig reads ~/.loop/config.integration.json.
func loadDiscordIntegrationFileConfig() (*discordIntegrationFileConfig, error) {
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
	var cfg discordIntegrationFileConfig
	if err := json.Unmarshal(standardJSON, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// waitForDiscordMessage polls channel history until a message matching the
// predicate appears. Uses exponential backoff.
func waitForDiscordMessage(session *discordgo.Session, channelID string,
	predicate func(*discordgo.Message) bool, timeout time.Duration) (*discordgo.Message, error) {

	deadline := time.Now().Add(timeout)
	wait := 500 * time.Millisecond

	for time.Now().Before(deadline) {
		msgs, err := session.ChannelMessages(channelID, 20, "", "", "")
		if err != nil {
			// Rate limited — wait and retry.
			if restErr, ok := err.(*discordgo.RESTError); ok && restErr.Response != nil && restErr.Response.StatusCode == 429 {
				time.Sleep(2 * time.Second)
				continue
			}
			return nil, fmt.Errorf("get channel messages: %w", err)
		}

		for _, msg := range msgs {
			if predicate(msg) {
				return msg, nil
			}
		}

		time.Sleep(wait)
		if wait < 2*time.Second {
			wait = wait * 3 / 2
		}
	}

	return nil, fmt.Errorf("timeout waiting for message in channel %s", channelID)
}

// randomSuffix returns a short random string for unique naming.
func randomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano()%1000000)
}

// rateSleep adds a small delay between API calls to avoid rate limits.
func rateSleep() {
	time.Sleep(apiDelay)
}

// messageContains returns a predicate that matches messages containing the text.
func messageContains(text string) func(*discordgo.Message) bool {
	return func(msg *discordgo.Message) bool {
		return strings.Contains(msg.Content, text)
	}
}
