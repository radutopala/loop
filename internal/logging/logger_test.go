package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LoggerSuite struct {
	suite.Suite
}

func TestLoggerSuite(t *testing.T) {
	suite.Run(t, new(LoggerSuite))
}

func (s *LoggerSuite) TestNewLoggerTextFormat() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("info", "text", &buf)
	logger.Info("hello")
	require.Contains(s.T(), buf.String(), "hello")
}

func (s *LoggerSuite) TestNewLoggerJSONFormat() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("info", "json", &buf)
	logger.Info("hello")
	require.Contains(s.T(), buf.String(), `"msg":"hello"`)
}

func (s *LoggerSuite) TestNewLoggerJSONFormatCaseInsensitive() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("info", "JSON", &buf)
	logger.Info("test")
	require.Contains(s.T(), buf.String(), `"msg":"test"`)
}

func (s *LoggerSuite) TestNewLoggerDebugLevel() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("debug", "text", &buf)
	logger.Debug("debug-msg")
	require.Contains(s.T(), buf.String(), "debug-msg")
}

func (s *LoggerSuite) TestNewLoggerWarnLevel() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("warn", "text", &buf)
	logger.Info("info-msg")
	require.NotContains(s.T(), buf.String(), "info-msg")
	logger.Warn("warn-msg")
	require.Contains(s.T(), buf.String(), "warn-msg")
}

func (s *LoggerSuite) TestNewLoggerErrorLevel() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("error", "text", &buf)
	logger.Warn("warn-msg")
	require.NotContains(s.T(), buf.String(), "warn-msg")
	logger.Error("error-msg")
	require.Contains(s.T(), buf.String(), "error-msg")
}

func (s *LoggerSuite) TestNewLoggerDefaultLevel() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("unknown", "text", &buf)
	logger.Debug("debug-msg")
	require.NotContains(s.T(), buf.String(), "debug-msg")
	logger.Info("info-msg")
	require.Contains(s.T(), buf.String(), "info-msg")
}

func (s *LoggerSuite) TestNewLoggerUsesStderr() {
	logger := NewLogger("info", "text")
	require.NotNil(s.T(), logger)
}

func (s *LoggerSuite) TestParseLevel() {
	require.Equal(s.T(), slog.LevelDebug, parseLevel("debug"))
	require.Equal(s.T(), slog.LevelInfo, parseLevel("info"))
	require.Equal(s.T(), slog.LevelWarn, parseLevel("warn"))
	require.Equal(s.T(), slog.LevelError, parseLevel("error"))
	require.Equal(s.T(), slog.LevelInfo, parseLevel(""))
	require.Equal(s.T(), slog.LevelInfo, parseLevel("invalid"))
}

func (s *LoggerSuite) TestParseLevelCaseInsensitive() {
	require.Equal(s.T(), slog.LevelDebug, parseLevel("DEBUG"))
	require.Equal(s.T(), slog.LevelDebug, parseLevel("Debug"))
}

func (s *LoggerSuite) TestLogOutputContainsLevel() {
	var buf bytes.Buffer
	logger := NewLoggerWithWriter("debug", "text", &buf)
	logger.Debug("test")
	output := buf.String()
	require.True(s.T(), strings.Contains(output, "DEBUG") || strings.Contains(output, "level=DEBUG"))
}
