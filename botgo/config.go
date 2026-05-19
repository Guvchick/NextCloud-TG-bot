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
	telegramLocalMode := envBool("TELEGRAM_LOCAL_MODE", false)
	telegramMaxDownloadDefault := 20
	if telegramLocalMode {
		telegramMaxDownloadDefault = 2000
	}
	return Config{
		BotToken:                     requiredEnv("BOT_TOKEN"),
		AdminIDs:                     parseAdminIDs(requiredEnv("ADMIN_IDS")),
		NextcloudURL:                 strings.TrimRight(nextcloudURL, "/"),
		NextcloudInternalURL:         nextcloudInternal,
		NextcloudAdminUser:           requiredEnv("NEXTCLOUD_ADMIN_USER"),
		NextcloudAdminPassword:       requiredEnv("NEXTCLOUD_ADMIN_PASSWORD"),
		DefaultQuotaGB:               envInt("DEFAULT_QUOTA_GB", 10),
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
		PlategaCallbackURL:           env("PLATEGA_CALLBACK_URL", ""),
		PublicWebhookBaseURL:         strings.TrimRight(env("PUBLIC_WEBHOOK_BASE_URL", ""), "/"),
		PallyEnabled:                 envBool("PALLY_ENABLED", false),
		PallyToken:                   env("PALLY_TOKEN", ""),
		PallyShopID:                  env("PALLY_SHOP_ID", ""),
		PallyBaseURL:                 strings.TrimRight(env("PALLY_BASE_URL", "https://pal24.pro"), "/"),
		PallyCallbackPath:            env("PALLY_CALLBACK_PATH", "/webhooks/pally"),
		CryptoBotEnabled:             envBool("CRYPTOBOT_ENABLED", false),
		CryptoBotToken:               env("CRYPTOBOT_TOKEN", ""),
		CryptoBotBaseURL:             strings.TrimRight(env("CRYPTOBOT_BASE_URL", "https://pay.crypt.bot"), "/"),
		CryptoBotWebhookPath:         env("CRYPTOBOT_WEBHOOK_PATH", "/webhooks/cryptobot"),
		HeleketEnabled:               envBool("HELEKET_ENABLED", false),
		HeleketMerchantID:            env("HELEKET_MERCHANT_ID", ""),
		HeleketAPIKey:                env("HELEKET_API_KEY", ""),
		HeleketBaseURL:               strings.TrimRight(env("HELEKET_BASE_URL", "https://api.heleket.com"), "/"),
		HeleketCurrency:              env("HELEKET_CURRENCY", "RUB"),
		HeleketToCurrency:            env("HELEKET_TO_CURRENCY", "USDT"),
		HeleketWebhookPath:           env("HELEKET_WEBHOOK_PATH", "/webhooks/heleket"),
		WebhookListenAddr:            env("WEBHOOK_LISTEN_ADDR", ":8088"),
		PlategaWebhookPath:           env("PLATEGA_WEBHOOK_PATH", "/webhooks/platega"),
		TelegramMaxDownloadMB:        envInt("TELEGRAM_MAX_DOWNLOAD_MB", telegramMaxDownloadDefault),
		PremiumDays:                  envInt("PREMIUM_DAYS", 30),
		BackupRetentionDays:          envInt("BACKUP_RETENTION_DAYS", 7),
		AutoBackupIntervalHours:      envInt("AUTO_BACKUP_INTERVAL_HOURS", 24),
		NextcloudSyncIntervalMinutes: envInt("NEXTCLOUD_SYNC_INTERVAL_MINUTES", 60),
		UploadWorkers:                envInt("UPLOAD_WORKERS", 3),
		QuotaCacheSeconds:            envInt("QUOTA_CACHE_SECONDS", 45),
		StickerStoreFile:             env("STICKER_STORE_FILE", "data/stickers.json"),
		StickerPackURL:               env("STICKER_PACK_URL", env("CUSTOM_EMOJI_PACK_URL", "https://t.me/addemoji/CPT_Emoji")),
		TelegramAPIBaseURL:           strings.TrimRight(env("TELEGRAM_API_BASE_URL", "https://api.telegram.org"), "/"),
		TelegramFileBaseURL:          strings.TrimRight(env("TELEGRAM_FILE_BASE_URL", "https://api.telegram.org/file"), "/"),
		TelegramLocalMode:            telegramLocalMode,
		TelegramLocalPathPrefix:      strings.TrimRight(env("TELEGRAM_LOCAL_PATH_PREFIX", ""), "/"),
		TelegramBotPathPrefix:        strings.TrimRight(env("TELEGRAM_BOT_PATH_PREFIX", ""), "/"),
		ContentStoreFile:             env("CONTENT_STORE_FILE", "data/content.json"),
		LogLevel:                     strings.ToLower(env("LOG_LEVEL", "info")),
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
