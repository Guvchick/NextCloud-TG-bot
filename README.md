# Telegram Nextcloud Beta Bot

Бот принимает заявки на beta-тест, отправляет их администраторам Telegram, создает пользователя в Nextcloud после одобрения, выдает логин/пароль и выставляет стартовую квоту 10 GB.

## Что умеет

- `/start` для пользователя создает заявку и отправляет ее админам.
- Админ одобряет или отклоняет заявку прямо из Telegram.
- После одобрения бот создает или обновляет Nextcloud-пользователя с логином, равным Telegram ID, генерирует пароль и выставляет квоту.
- Админ-панель показывает пользователей, заявки, статусы и квоты.
- В админ-панели есть поиск по Telegram ID и Telegram-тегу.
- Админ видит актуально занятое место пользователя в Nextcloud.
- Админ может добавлять пользователю место: `+1GB`, `+5GB`, `+10GB` или произвольное число.
- Админ может сбросить пароль одобренному пользователю.
- Админ может удалить пользователя из Nextcloud и базы бота.
- Есть отключение и включение Nextcloud-пользователя.
- Пользователь может отправить файл, фото, видео, аудио или документ прямо в бота, а бот поставит его в очередь и загрузит в облако.
- Пользователь сразу видит текущий пароль в `/start` и может сменить его через кнопку.
- Пользователь может переключить язык интерфейса: русский/английский.
- Есть отключаемая кнопка `Донат` с Telegram Stars и Platega.
- После оплаты Telegram Stars пользователь получает премиум-иконку и приоритет в очереди загрузок.
- Во вложенных пользовательских разделах есть кнопки возврата назад.
- В пользовательском экране всегда видны квота, занятое место и шкала заполнения.
- Есть ссылка на саппорт в Telegram и email.
- Есть синхронизация с Nextcloud: если пользователя уже нет в Nextcloud, запись удаляется из базы бота.
- Есть сжатые SQLite/JSON-бекапы, восстановление из SQLite-бекапа, авто-бекап и чистка старых файлов.
- Есть логи в stdout и `./logs/bot-go.log`.
- Есть панель бекапов: SQLite-база или JSON-экспорт отправляются в Telegram.
- Есть рассылка по всем активным одобренным пользователям.

## Настройка

1. Создайте бота через BotFather и получите токен.
2. В Nextcloud создайте app password для администратора.
3. Скопируйте пример окружения:

```bash
cp .env.example .env
```

4. Заполните `.env`:

```env
# Telegram bot
BOT_TOKEN=123456:telegram-bot-token
ADMIN_IDS=123456789,987654321

# Nextcloud
NEXTCLOUD_URL=https://cloud.example.com
NEXTCLOUD_INTERNAL_URL=
NEXTCLOUD_HOSTNAME=cloud.example.com
NEXTCLOUD_ADMIN_USER=admin
NEXTCLOUD_ADMIN_PASSWORD=nextcloud-admin-app-password
DEFAULT_QUOTA_GB=10

# Local storage
DATABASE_PATH=data/bot.sqlite3
DATABASE_URL=http://bot-db:8080
DATABASE_API_TOKEN=
DATABASE_SECRET_KEY=
BACKUP_DIR=backups
LOG_DIR=logs

# User interface blocks
ENABLE_SUPPORT_BLOCK=true
SUPPORT_TELEGRAM=@support_username
SUPPORT_EMAIL=support@example.com
ENABLE_DONATE_BLOCK=true

# Donations: Telegram Stars
TELEGRAM_STARS_ENABLED=true
TELEGRAM_STARS_AMOUNTS=50,100,250

# Donations: Platega
PLATEGA_ENABLED=true
PLATEGA_URL=
PLATEGA_MERCHANT_ID=
PLATEGA_SECRET=
PLATEGA_BASE_URL=https://app.platega.io
PLATEGA_AMOUNTS_RUB=100,300,500
PLATEGA_RETURN_URL=
PLATEGA_FAILED_URL=
DONATE_URL=

# Background jobs and limits
BACKUP_RETENTION_DAYS=7
AUTO_BACKUP_INTERVAL_HOURS=24
NEXTCLOUD_SYNC_INTERVAL_MINUTES=60
TELEGRAM_MAX_DOWNLOAD_MB=20
PREMIUM_DAYS=30

# Optional custom stickers
STICKER_WELCOME=
STICKER_APPROVED=
STICKER_UPLOAD_OK=
STICKER_ERROR=
```

`ADMIN_IDS` - это Telegram ID администраторов через запятую.

`NEXTCLOUD_URL` - публичный адрес, который бот показывает пользователям.

`NEXTCLOUD_INTERNAL_URL` - внутренний адрес, по которому сам бот ходит в Nextcloud API/WebDAV. Если бот и Nextcloud на одном сервере, лучше задать его отдельно:

- Nextcloud в том же Docker network: `NEXTCLOUD_INTERNAL_URL=http://nextcloud`
- Nextcloud установлен прямо на хосте, а бот в Docker: `NEXTCLOUD_INTERNAL_URL=http://host.docker.internal`
- Nextcloud доступен только через публичный домен: оставьте пустым, бот возьмет `NEXTCLOUD_URL`

Если `http://host.docker.internal` редиректит на HTTPS и ломается TLS, используйте публичный домен как внутренний URL, но направьте домен на Docker host:

```env
NEXTCLOUD_URL=https://claud.kys-paw.life
NEXTCLOUD_INTERNAL_URL=https://claud.kys-paw.life
NEXTCLOUD_HOSTNAME=claud.kys-paw.life
```

В `docker-compose.yml` уже есть `extra_hosts`, который направит `NEXTCLOUD_HOSTNAME` на `host-gateway`.

Файлы из Telegram загружаются в корень диска пользователя. Пользовательский интерфейс говорит просто про облако, без технических деталей Nextcloud/WebDAV.

Telegram-бот теперь запускается как Go-бинарник из `botgo`. База данных обслуживается отдельным Go-сервисом `bot-db`; бот ходит к нему по `DATABASE_URL`. `DATABASE_API_TOKEN` включает bearer-токен между ботом и Go-БД. `DATABASE_SECRET_KEY` включает шифрование сохраненных Nextcloud-паролей внутри SQLite. Укажите длинную случайную строку и не меняйте ее после запуска: старые зашифрованные пароли без нее не расшифровать. Файл БД дополнительно создается с правами `0600`, папка данных - `0700`; SQLite работает с `foreign_keys`, `WAL`, `busy_timeout` и `secure_delete`.

`ENABLE_SUPPORT_BLOCK=false` полностью убирает кнопку поддержки. `SUPPORT_TELEGRAM` и `SUPPORT_EMAIL` показываются пользователю в разделе поддержки.

`ENABLE_DONATE_BLOCK=false` полностью убирает кнопку доната. Внутри доната есть отдельные ветки `Telegram Stars` и `Platega`; их можно отдельно выключить через `TELEGRAM_STARS_ENABLED=false` и `PLATEGA_ENABLED=false`. `TELEGRAM_STARS_AMOUNTS` - суммы кнопок Stars через запятую.

Для Platega можно задать статическую ссылку `PLATEGA_URL`, либо включить API-создание ссылок через `PLATEGA_MERCHANT_ID` и `PLATEGA_SECRET`. По документации Platega запросы идут на `https://app.platega.io/`, платежная ссылка создается через `POST /v2/transaction/process`, а статус проверяется через `GET /transaction/{id}`. `PLATEGA_AMOUNTS_RUB` задает суммы в рублях, `PLATEGA_RETURN_URL` и `PLATEGA_FAILED_URL` передаются в платеж при создании.

`TELEGRAM_MAX_DOWNLOAD_MB` - лимит скачивания через Bot API. Если Telegram не дает скачать большой файл, бот заранее объяснит это пользователю и предложит загрузить файл напрямую через Nextcloud.

`PREMIUM_DAYS` - срок действия премиум-иконки после Telegram Stars, Platega или ручной выдачи админом. По умолчанию `30`, то есть примерно один месяц.

`STICKER_*` - необязательные кастомные `file_id` стикеров. Если они не заданы или Telegram их отклонит, бот оставит базовые визуальные маркеры в тексте. Настроить можно через `/setsticker welcome`, `/setsticker approved`, `/setsticker upload_ok`, `/setsticker error`, `/setsticker support`, `/setsticker donate`, `/setsticker language`, `/setsticker password`.

## Запуск локально

```bash
go run ./botgo
```

## Запуск через Docker

```bash
docker compose up -d --build
```

Данные базы будут храниться в `./data`, бекапы - в `./backups`, логи - в `./logs`.

## Команды

- `/start` - для пользователя отправляет заявку, для админа открывает панель.
- `/admin` - открывает админ-панель.
- `/health` - проверяет, может ли бот достучаться до Nextcloud API.
- `/sync` - вручную сверяет базу бота с Nextcloud и удаляет из бота отсутствующих Nextcloud-пользователей.
- `/search 123456789` или `/search @username` - поиск пользователя в админке.
- `/broadcast текст` - отправляет текст всем активным одобренным пользователям.
- `/setsticker welcome` и другие события - сохраняет кастомный стикер через Go-БД.
- `/stickers` - показывает статус стикеров.

## Бекапы

Бот автоматически создает сжатый SQLite-бекап каждые `AUTO_BACKUP_INTERVAL_HOURS` часов. Старые файлы старше `BACKUP_RETENTION_DAYS` дней удаляются автоматически.

В админ-панели `Бекапы` можно создать сжатый SQLite-бекап, создать сжатый JSON-экспорт, посмотреть последние SQLite-бекапы на сервере и восстановить базу из SQLite-бекапа.

Перед восстановлением бот создает safety-бекап текущей базы. После восстановления лучше перезапустить контейнер.

## Загрузка файлов

После одобрения пользователь может отправить боту файл, фото, видео, аудио, voice или animation. Бот скачает файл из Telegram и загрузит его в корень Nextcloud-аккаунта этого пользователя.

Загрузки идут через очередь, чтобы одновременные файлы от разных пользователей не ломали Telegram API и WebDAV-загрузку. Пользователи с премиум-иконкой `⭐` получают приоритет и обрабатываются раньше обычной очереди.

В карточке пользователя админ видит занятое место. Данные обновляются при открытии карточки.

Файлы через бота кладутся в корень облака пользователя.

## Важная деталь по паролям

Пароль генерируется при одобрении и отправляется пользователю. Бот сохраняет текущий пароль в SQLite, потому что без него он не сможет загружать файлы в пространство пользователя через WebDAV. JSON-бекап не включает пароль, но SQLite-бекап содержит рабочие учетные данные, поэтому храните его аккуратно.

Если пользователь поменяет пароль вручную в Nextcloud, загрузка через бота перестанет работать. Пользователь видит текущий пароль в `/start` и может нажать `Сменить пароль`, либо администратор может открыть карточку пользователя и нажать `Сбросить пароль`.
