package main

import (
	"fmt"
	"log"
	"net/http"
	"net/url"
	"runtime/debug"
	"strings"
	"time"
)

func main() {
	loadDotEnv(".env")
	cfg := loadConfig()
	configureLogging(cfg)
	redis, err := NewRedisClient(cfg.RedisURL)
	if err != nil {
		log.Fatalf("redis config: %v", err)
	}
	db, err := NewDB(cfg)
	if err != nil {
		log.Fatalf("postgres config: %v", err)
	}

	app := &App{
		cfg: cfg,
		tg: &Telegram{
			token:           cfg.BotToken,
			apiURL:          cfg.TelegramAPIBaseURL + "/bot" + cfg.BotToken + "/",
			fileURL:         cfg.TelegramFileBaseURL + "/bot" + cfg.BotToken + "/",
			localMode:       cfg.TelegramLocalMode,
			localPathPrefix: cfg.TelegramLocalPathPrefix,
			botPathPrefix:   cfg.TelegramBotPathPrefix,
			client:          &http.Client{Timeout: 90 * time.Second},
		},
		db: db,
		nc: &Nextcloud{
			baseURL:  strings.TrimRight(cfg.NextcloudInternalURL, "/"),
			username: cfg.NextcloudAdminUser,
			password: cfg.NextcloudAdminPassword,
			client:   &http.Client{Timeout: 90 * time.Second},
		},
		states:   NewStateStore(redis),
		uploads:  NewUploadQueue(),
		batches:  NewUploadBatchManager(),
		quota:    NewQuotaCache(time.Duration(cfg.QuotaCacheSeconds) * time.Second),
		stickers: NewStickerStore(cfg.StickerStoreFile),
		content:  NewContentStore(cfg.ContentStoreFile),
	}
	if err := app.stickers.Load(); err != nil {
		log.Printf("sticker store load failed: %v", err)
	}
	if err := app.content.Load(); err != nil {
		log.Printf("content store load failed: %v", err)
	}
	if cfg.PlategaEnabled && cfg.PlategaMerchantID != "" && cfg.PlategaSecret != "" {
		app.platega = &Platega{
			merchantID: cfg.PlategaMerchantID,
			secret:     cfg.PlategaSecret,
			baseURL:    cfg.PlategaBaseURL,
			client:     &http.Client{Timeout: 30 * time.Second},
		}
	}
	if cfg.PallyEnabled && cfg.PallyToken != "" && cfg.PallyShopID != "" {
		app.pally = &Pally{
			token:   cfg.PallyToken,
			shopID:  cfg.PallyShopID,
			baseURL: cfg.PallyBaseURL,
			client:  &http.Client{Timeout: 30 * time.Second},
		}
	}
	if cfg.CryptoBotEnabled && cfg.CryptoBotToken != "" {
		app.cryptoBot = &CryptoBotPay{
			token:   cfg.CryptoBotToken,
			baseURL: cfg.CryptoBotBaseURL,
			client:  &http.Client{Timeout: 30 * time.Second},
		}
	}
	if cfg.HeleketEnabled && cfg.HeleketMerchantID != "" && cfg.HeleketAPIKey != "" {
		app.heleket = &Heleket{
			merchantID: cfg.HeleketMerchantID,
			apiKey:     cfg.HeleketAPIKey,
			baseURL:    cfg.HeleketBaseURL,
			client:     &http.Client{Timeout: 30 * time.Second},
		}
	}

	if err := app.db.Init(); err != nil {
		log.Fatalf("init db: %v", err)
	}
	log.Printf("bot started | nextcloud_public=%s nextcloud_internal=%s postgres=%s/%s telegram_api=%s telegram_local_mode=%v upload_workers=%d quota_cache_seconds=%d", cfg.NextcloudURL, cfg.NextcloudInternalURL, env("POSTGRES_HOST", "postgres"), env("POSTGRES_DB", "bot"), cfg.TelegramAPIBaseURL, cfg.TelegramLocalMode, cfg.UploadWorkers, cfg.QuotaCacheSeconds)
	app.logRuntimeHints()
	app.notifyStartup()

	for i := 0; i < cfg.UploadWorkers; i++ {
		workerID := i + 1
		app.safeGo(fmt.Sprintf("upload-worker-%d", workerID), func() { app.uploadWorker(workerID) })
	}
	app.safeGo("auto-backup", app.autoBackupLoop)
	app.safeGo("nextcloud-sync", app.nextcloudSyncLoop)
	app.safeGo("premium-expiration", app.premiumExpirationLoop)
	app.safeGo("webhook-server", app.startWebhookServer)
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Printf("panic in poll loop: %v\n%s", r, string(stack))
			app.notifyCrash("poll-loop", r, stack)
		}
	}()
	app.poll()
}

func (a *App) logRuntimeHints() {
	if a.cfg.TelegramLocalMode {
		log.Printf("telegram local mode enabled: api=%s file_api=%s local_path_prefix=%s bot_path_prefix=%s max_download_mb=%d", a.cfg.TelegramAPIBaseURL, a.cfg.TelegramFileBaseURL, a.cfg.TelegramLocalPathPrefix, a.cfg.TelegramBotPathPrefix, a.cfg.TelegramMaxDownloadMB)
		if strings.Contains(a.cfg.TelegramAPIBaseURL, "telegram-bot-api") {
			log.Printf("telegram local mode hint: docker compose must run profile telegram-local; set COMPOSE_PROFILES=telegram-local if service name is telegram-bot-api")
		}
	} else if a.cfg.TelegramMaxDownloadMB > 20 {
		log.Printf("telegram public api warning: TELEGRAM_MAX_DOWNLOAD_MB=%d but TELEGRAM_LOCAL_MODE=false; public Bot API may still reject large files", a.cfg.TelegramMaxDownloadMB)
	}
	if a.cfg.PlategaEnabled && a.cfg.PlategaCallbackURL == "" {
		log.Printf("platega warning: PLATEGA_CALLBACK_URL is empty; payments can still be checked manually, but automatic webhook fulfillment will not work")
	}
	if a.cfg.CryptoBotEnabled && strings.Trim(a.cfg.CryptoBotWebhookPath, "/") == "" {
		log.Printf("cryptobot warning: CRYPTOBOT_WEBHOOK_PATH must be a secret path for webhook verification")
	}
	if a.cfg.HeleketEnabled && a.publicWebhookURL(a.cfg.HeleketWebhookPath) == "" {
		log.Printf("heleket warning: set PUBLIC_WEBHOOK_BASE_URL or PLATEGA_CALLBACK_URL so bot can pass Heleket url_callback")
	}
}

func (a *App) publicWebhookURL(path string) string {
	base := strings.TrimSpace(a.cfg.PublicWebhookBaseURL)
	if base == "" {
		base = strings.TrimSpace(a.cfg.PlategaCallbackURL)
	}
	if base == "" {
		return ""
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	if path == "" {
		return base
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func (a *App) poll() {
	offset := 0
	for {
		updates, err := a.tg.GetUpdates(offset)
		if err != nil {
			log.Printf("getUpdates failed: %v", err)
			time.Sleep(3 * time.Second)
			continue
		}
		for _, update := range updates {
			if update.UpdateID >= offset {
				offset = update.UpdateID + 1
			}
			a.handleUpdate(update)
		}
	}
}

func (a *App) handleUpdate(update Update) {
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			log.Printf("panic while handling update %d: %v\n%s", update.UpdateID, r, string(stack))
			a.notifyCrash(fmt.Sprintf("update:%d", update.UpdateID), r, stack)
		}
	}()
	if update.PreCheckoutQuery != nil {
		a.tg.AnswerPreCheckout(update.PreCheckoutQuery.ID, strings.HasPrefix(update.PreCheckoutQuery.InvoicePayload, "stars_donate:"))
		return
	}
	if update.CallbackQuery != nil {
		a.handleCallback(update.CallbackQuery)
		return
	}
	if update.Message != nil {
		a.handleMessage(update.Message)
	}
}

func (a *App) handleMessage(msg *Message) {
	if msg.From == nil {
		return
	}
	userID := msg.From.ID
	if msg.SuccessfulPayment != nil {
		a.handleSuccessfulPayment(msg)
		return
	}
	if !a.isAdmin(userID) && a.maintenanceEnabled() {
		_, _ = a.sendContent(msg.Chat.ID, "maintenance", nil, nil)
		return
	}
	if st, ok := a.states.Get(userID); ok {
		a.handleStateMessage(msg, st)
		return
	}
	if strings.HasPrefix(msg.Text, "/") {
		a.handleCommand(msg)
		return
	}
	if a.hasUpload(msg) {
		a.handleUpload(msg)
	}
}

func (a *App) handleCommand(msg *Message) {
	parts := strings.Fields(msg.Text)
	command := strings.Split(strings.TrimPrefix(parts[0], "/"), "@")[0]
	switch command {
	case "start":
		a.start(msg)
	case "admin":
		if a.isAdmin(msg.From.ID) {
			_, _ = a.tg.SendMessage(msg.Chat.ID, a.adminSummary(), adminKeyboard())
		}
	case "health":
		if a.isAdmin(msg.From.ID) {
			a.health(msg.Chat.ID)
		}
	case "sync":
		if a.isAdmin(msg.From.ID) {
			checked, removed, err := a.syncNextcloudUsers()
			if err != nil {
				_, _ = a.tg.SendMessage(msg.Chat.ID, "⚠️ Синхронизация не удалась: <code>"+esc(err.Error())+"</code>", nil)
				return
			}
			_, _ = a.tg.SendMessage(msg.Chat.ID, fmt.Sprintf("🔄 <b>Синхронизация завершена</b>\n\nПроверено: <b>%d</b>\nУдалено: <b>%d</b>", checked, removed), adminKeyboard())
		}
	case "search":
		if a.isAdmin(msg.From.ID) {
			query := strings.TrimSpace(strings.TrimPrefix(msg.Text, parts[0]))
			a.renderSearch(msg.Chat.ID, query)
		}
	case "broadcast":
		if a.isAdmin(msg.From.ID) {
			text := strings.TrimSpace(strings.TrimPrefix(msg.Text, parts[0]))
			a.broadcastText(msg.Chat.ID, text)
		}
	case "setsticker":
		if a.isAdmin(msg.From.ID) {
			a.setStickerStart(msg, parts)
		}
	case "stickers":
		if a.isAdmin(msg.From.ID) {
			_, _ = a.tg.SendMessage(msg.Chat.ID, a.stickersText(), stickersKeyboard(a.stickers, a.cfg.StickerPackURL))
		}
	}
}
