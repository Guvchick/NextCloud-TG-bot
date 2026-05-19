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
	DatabaseURL                  string
	DatabaseAPIToken             string
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
	TelegramMaxDownloadMB        int
	PremiumDays                  int
	BackupRetentionDays          int
	AutoBackupIntervalHours      int
	NextcloudSyncIntervalMinutes int
	UploadWorkers                int
	QuotaCacheSeconds            int
	StickerStoreFile             string
	CustomEmojiPackURL           string
}

type App struct {
	cfg       Config
	tg        *Telegram
	db        *DB
	nc        *Nextcloud
	platega   *Platega
	states    *StateStore
	uploads   *UploadQueue
	batches   *UploadBatchManager
	quota     *QuotaCache
	stickers  *StickerStore
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
	TransactionID string `json:"transaction_id"`
	TelegramID    int64  `json:"telegram_id"`
	Provider      string `json:"provider"`
	Amount        int    `json:"amount"`
	Currency      string `json:"currency"`
	Status        string `json:"status"`
}

type StateKind string

const (
	StateNone           StateKind = ""
	StateChangePassword StateKind = "change_password"
	StateQuotaCustom    StateKind = "quota_custom"
	StateAdminSearch    StateKind = "admin_search"
	StateBroadcast      StateKind = "broadcast"
	StateSticker        StateKind = "sticker"
)

type State struct {
	Kind     StateKind
	TargetID int64
	Event    string
}
