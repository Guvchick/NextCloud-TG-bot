package main

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func loadConfig() Config {
	nextcloudURL := requiredEnv("NEXTCLOUD_URL")
	nextcloudInternal := strings.TrimRight(env("NEXTCLOUD_INTERNAL_URL", ""), "/")
	if nextcloudInternal == "" {
		nextcloudInternal = nextcloudURL
	}
	return Config{
		BotToken:                     requiredEnv("BOT_TOKEN"),
		AdminIDs:                     parseAdminIDs(requiredEnv("ADMIN_IDS")),
		NextcloudURL:                 strings.TrimRight(nextcloudURL, "/"),
		NextcloudInternalURL:         nextcloudInternal,
		NextcloudAdminUser:           requiredEnv("NEXTCLOUD_ADMIN_USER"),
		NextcloudAdminPassword:       requiredEnv("NEXTCLOUD_ADMIN_PASSWORD"),
		DefaultQuotaGB:               envInt("DEFAULT_QUOTA_GB", 10),
		DatabaseURL:                  strings.TrimRight(env("DATABASE_URL", "http://bot-db:8080"), "/"),
		DatabaseAPIToken:             env("DATABASE_API_TOKEN", ""),
		RedisURL:                     env("REDIS_URL", "redis://redis:6379/0"),
		BackupDir:                    env("BACKUP_DIR", "backups"),
		LogDir:                       env("LOG_DIR", "logs"),
		EnableSupportBlock:           envBool("ENABLE_SUPPORT_BLOCK", true),
		SupportTelegram:              env("SUPPORT_TELEGRAM", ""),
		SupportEmail:                 env("SUPPORT_EMAIL", ""),
		EnableDonateBlock:            envBool("ENABLE_DONATE_BLOCK", true),
		DonateURL:                    env("DONATE_URL", ""),
		TelegramStarsEnabled:         envBool("TELEGRAM_STARS_ENABLED", true),
		TelegramStarsAmounts:         envIntList("TELEGRAM_STARS_AMOUNTS", []int{50, 100, 250}),
		PlategaEnabled:               envBool("PLATEGA_ENABLED", true),
		PlategaURL:                   env("PLATEGA_URL", ""),
		PlategaMerchantID:            env("PLATEGA_MERCHANT_ID", ""),
		PlategaSecret:                env("PLATEGA_SECRET", ""),
		PlategaBaseURL:               strings.TrimRight(env("PLATEGA_BASE_URL", "https://app.platega.io"), "/"),
		PlategaAmountsRUB:            envIntList("PLATEGA_AMOUNTS_RUB", []int{100, 300, 500}),
		PlategaReturnURL:             env("PLATEGA_RETURN_URL", ""),
		PlategaFailedURL:             env("PLATEGA_FAILED_URL", ""),
		TelegramMaxDownloadMB:        envInt("TELEGRAM_MAX_DOWNLOAD_MB", 20),
		PremiumDays:                  envInt("PREMIUM_DAYS", 30),
		BackupRetentionDays:          envInt("BACKUP_RETENTION_DAYS", 7),
		AutoBackupIntervalHours:      envInt("AUTO_BACKUP_INTERVAL_HOURS", 24),
		NextcloudSyncIntervalMinutes: envInt("NEXTCLOUD_SYNC_INTERVAL_MINUTES", 60),
		UploadWorkers:                envInt("UPLOAD_WORKERS", 3),
		QuotaCacheSeconds:            envInt("QUOTA_CACHE_SECONDS", 45),
		StickerStoreFile:             env("STICKER_STORE_FILE", "data/stickers.json"),
		CustomEmojiPackURL:           env("CUSTOM_EMOJI_PACK_URL", "https://t.me/addemoji/CPT_Emoji"),
	}
}

func configureLogging(cfg Config) {
	_ = os.MkdirAll(cfg.LogDir, 0o755)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	file, err := os.OpenFile(filepath.Join(cfg.LogDir, "bot-go.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err == nil {
		log.SetOutput(io.MultiWriter(os.Stdout, file))
	}
}

func loadDotEnv(path string) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
		if os.Getenv(key) == "" {
			_ = os.Setenv(key, value)
		}
	}
}

func requiredEnv(name string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		log.Fatalf("missing required environment variable: %s", name)
	}
	return value
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	value, err := strconv.Atoi(strings.TrimSpace(os.Getenv(name)))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func envBool(name string, fallback bool) bool {
	raw := strings.ToLower(strings.TrimSpace(os.Getenv(name)))
	if raw == "" {
		return fallback
	}
	return raw == "1" || raw == "true" || raw == "yes" || raw == "y" || raw == "on" || raw == "да"
}

func envIntList(name string, fallback []int) []int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	var values []int
	for _, part := range strings.Split(raw, ",") {
		value, err := strconv.Atoi(strings.TrimSpace(part))
		if err == nil && value > 0 {
			values = append(values, value)
		}
	}
	if len(values) == 0 {
		return fallback
	}
	return values
}
