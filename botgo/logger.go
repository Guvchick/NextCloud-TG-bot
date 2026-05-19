package main

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type PrettyLogWriter struct {
	mu       sync.Mutex
	out      io.Writer
	minLevel int
}

const (
	logDebug = iota
	logInfo
	logWarn
	logError
)

func NewPrettyLogWriter(out io.Writer, level string) *PrettyLogWriter {
	return &PrettyLogWriter{out: out, minLevel: parseLogLevel(level)}
}

func (w *PrettyLogWriter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if line == "" {
		return len(p), nil
	}
	level, label, mark := classifyLog(line)
	if level < w.minLevel {
		return len(p), nil
	}
	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	formatted := fmt.Sprintf("%s %-5s %s %s\n", timestamp, label, mark, line)
	w.mu.Lock()
	_, err := w.out.Write([]byte(formatted))
	w.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func classifyLog(line string) (int, string, string) {
	lower := strings.ToLower(line)
	switch {
	case strings.Contains(lower, "panic") || strings.Contains(lower, "fatal") || strings.Contains(lower, "failed") || strings.Contains(lower, "error"):
		return logError, "ERROR", "🚨"
	case strings.Contains(lower, "warning") || strings.Contains(lower, "недоступ") || strings.Contains(lower, "не удалось"):
		return logWarn, "WARN", "⚠️"
	case strings.Contains(lower, "debug"):
		return logDebug, "DEBUG", "🔎"
	case strings.Contains(lower, "started") || strings.Contains(lower, "completed") || strings.Contains(lower, "created") || strings.Contains(lower, "approved"):
		return logInfo, "INFO", "✅"
	case strings.Contains(lower, "upload") || strings.Contains(lower, "webhook") || strings.Contains(lower, "payment"):
		return logInfo, "INFO", "📡"
	default:
		return logInfo, "INFO", "ℹ️"
	}
}

func parseLogLevel(level string) int {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return logDebug
	case "warn", "warning":
		return logWarn
	case "error":
		return logError
	default:
		return logInfo
	}
}

func logFilePath(dir string) string {
	if strings.TrimSpace(dir) == "" {
		dir = "logs"
	}
	return filepath.Join(dir, "bot-go.log")
}
