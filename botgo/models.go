package main

const pageSize = 8

type Config struct {
	BotToken                     string
	AdminIDs                     map[int64]bool
	NextcloudURL                 string
	NextcloudInternalURL         string
	NextcloudAdminUser           string
	NextcloudAdminPassword       string
	DefaultQuotaGB               int
	RedisURL                     string
	BackupDir                    string
	LogDir                       string
	EnableSupportBlock           bool
	SupportTelegram              string
	SupportEmail                 string
	EnableDonateBlock            bool
	DonateURL                    string
	TelegramStarsEnabled         bool
	TelegramStarsAmounts         []int
	PlategaEnabled               bool
	PlategaURL                   string
	PlategaMerchantID            string
	PlategaSecret                string
	PlategaBaseURL               string
	PlategaAmountsRUB            []int
	PlategaReturnURL             string
	PlategaFailedURL             string
	PlategaCallbackURL           string
	PublicWebhookBaseURL         string
	PallyEnabled                 bool
	PallyToken                   string
	PallyShopID                  string
	PallyBaseURL                 string
	PallyCallbackPath            string
	CryptoBotEnabled             bool
	CryptoBotToken               string
	CryptoBotBaseURL             string
	CryptoBotWebhookPath         string
	HeleketEnabled               bool
	HeleketMerchantID            string
	HeleketAPIKey                string
	HeleketBaseURL               string
	HeleketCurrency              string
	HeleketToCurrency            string
	HeleketWebhookPath           string
	WebhookListenAddr            string
	PlategaWebhookPath           string
	TelegramMaxDownloadMB        int
	PremiumDays                  int
	BackupRetentionDays          int
	AutoBackupIntervalHours      int
	NextcloudSyncIntervalMinutes int
	UploadWorkers                int
	QuotaCacheSeconds            int
	StickerStoreFile             string
	StickerPackURL               string
	TelegramAPIBaseURL           string
	TelegramFileBaseURL          string
	TelegramLocalMode            bool
	TelegramLocalPathPrefix      string
	TelegramBotPathPrefix        string
	ContentStoreFile             string
	LogLevel                     string
}

type App struct {
	cfg       Config
	tg        *Telegram
	db        *DB
	nc        *Nextcloud
	platega   *Platega
	pally     *Pally
	cryptoBot *CryptoBotPay
	heleket   *Heleket
	states    *StateStore
	uploads   *UploadQueue
	batches   *UploadBatchManager
	quota     *QuotaCache
	stickers  *StickerStore
	content   *ContentStore
	uploadSeq int64
}

type User struct {
	TelegramID     int64   `json:"telegram_id"`
	Username       *string `json:"username"`
	FirstName      *string `json:"first_name"`
	LastName       *string `json:"last_name"`
	Status         string  `json:"status"`
	Language       string  `json:"language"`
	NCUserID       *string `json:"nc_user_id"`
	NCPassword     *string `json:"nc_password"`
	QuotaGB        int     `json:"quota_gb"`
	IsSupporter    int     `json:"is_supporter"`
	SupporterUntil *string `json:"supporter_until"`
	IsDisabled     int     `json:"is_disabled"`
	CreatedAt      string  `json:"created_at"`
	UpdatedAt      string  `json:"updated_at"`
	ApprovedAt     *string `json:"approved_at"`
}

type Payment struct {
	TransactionID string  `json:"transaction_id"`
	TelegramID    int64   `json:"telegram_id"`
	Provider      string  `json:"provider"`
	Amount        int     `json:"amount"`
	Currency      string  `json:"currency"`
	Status        string  `json:"status"`
	PaymentURL    *string `json:"payment_url"`
	Payload       *string `json:"payload"`
	CreatedAt     string  `json:"created_at"`
	UpdatedAt     string  `json:"updated_at"`
}

type PromoCode struct {
	Code        string  `json:"code"`
	QuotaGB     int     `json:"quota_gb"`
	PremiumDays int     `json:"premium_days"`
	MaxUses     int     `json:"max_uses"`
	UsedCount   int     `json:"used_count"`
	IsActive    int     `json:"is_active"`
	CreatedAt   string  `json:"created_at"`
	ExpiresAt   *string `json:"expires_at"`
}

type BotStats struct {
	UsersTotal        int
	UsersRequested    int
	UsersApproved     int
	UsersRejected     int
	UsersDisabled     int
	SupportersActive  int
	QuotaTotalGB      int
	PaymentsTotal     int
	PaymentsConfirmed int
	PaymentsRub       int
	PromoCodesTotal   int
	PromoUsesTotal    int
}

type StateKind string

const (
	StateNone           StateKind = ""
	StateChangePassword StateKind = "change_password"
	StateQuotaCustom    StateKind = "quota_custom"
	StateAdminSearch    StateKind = "admin_search"
	StateBroadcast      StateKind = "broadcast"
	StateSticker        StateKind = "sticker"
	StateContentMessage StateKind = "content_message"
	StateContentButton  StateKind = "content_button"
	StateContentPhoto   StateKind = "content_photo"
	StatePromoApply     StateKind = "promo_apply"
	StatePromoCreate    StateKind = "promo_create"
	StateSetting        StateKind = "setting"
)

type State struct {
	Kind     StateKind
	TargetID int64
	Event    string
}
