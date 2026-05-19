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
- Есть PostgreSQL + Redis: PostgreSQL хранит пользователей/платежи/настройки, Redis хранит временные Telegram-состояния.
- Есть сжатые PostgreSQL/JSON-бекапы, восстановление из PostgreSQL-бекапа, авто-бекап и чистка старых файлов.
- Есть логи в stdout и `./logs/bot-go.log`.
- Есть панель бекапов: PostgreSQL-дамп или JSON-экспорт отправляются в Telegram.
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
COMPOSE_PROFILES=
TELEGRAM_API_BASE_URL=https://api.telegram.org
TELEGRAM_FILE_BASE_URL=https://api.telegram.org/file
TELEGRAM_LOCAL_MODE=false
TELEGRAM_API_ID=
TELEGRAM_API_HASH=
TELEGRAM_VERBOSITY=2
TELEGRAM_LOCAL_PATH_PREFIX=
TELEGRAM_BOT_PATH_PREFIX=

# Nextcloud
NEXTCLOUD_URL=https://cloud.example.com
NEXTCLOUD_INTERNAL_URL=
NEXTCLOUD_HOSTNAME=cloud.example.com
NEXTCLOUD_ADMIN_USER=admin
NEXTCLOUD_ADMIN_PASSWORD=nextcloud-admin-app-password
DEFAULT_QUOTA_GB=10

# Database and cache
POSTGRES_DB=bot
POSTGRES_USER=bot
POSTGRES_PASSWORD=change-me-please
POSTGRES_SSLMODE=disable
REDIS_URL=redis://redis:6379/0
DATABASE_SECRET_KEY=
BACKUP_DIR=backups
LOG_DIR=logs
LOG_LEVEL=info

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
UPLOAD_WORKERS=3
QUOTA_CACHE_SECONDS=45
TELEGRAM_MAX_DOWNLOAD_MB=20
PREMIUM_DAYS=30

# Stickers
STICKER_STORE_FILE=data/stickers.json
CONTENT_STORE_FILE=data/content.json
STICKER_PACK_URL=https://t.me/addemoji/CPT_Emoji
```

`ADMIN_IDS` - это Telegram ID администраторов через запятую.

`TELEGRAM_API_BASE_URL` и `TELEGRAM_FILE_BASE_URL` по умолчанию используют публичный Bot API. Для локального `tdlib/telegram-bot-api` или `tdlight-team/tdlight-telegram-bot-api` обычно ставят:

```env
COMPOSE_PROFILES=telegram-local
TELEGRAM_API_BASE_URL=http://telegram-bot-api:8081
TELEGRAM_FILE_BASE_URL=http://telegram-bot-api:8081/file
TELEGRAM_LOCAL_MODE=true
TELEGRAM_API_ID=123456
TELEGRAM_API_HASH=your_api_hash
TELEGRAM_VERBOSITY=2
TELEGRAM_MAX_DOWNLOAD_MB=2000
TELEGRAM_LOCAL_PATH_PREFIX=/var/lib/telegram-bot-api
TELEGRAM_BOT_PATH_PREFIX=/telegram-bot-api-data
```

`TELEGRAM_API_ID` и `TELEGRAM_API_HASH` нужны только для локального Bot API. Их берут в https://my.telegram.org/apps. Если указать `TELEGRAM_API_BASE_URL=http://telegram-bot-api:8081`, но не включить `COMPOSE_PROFILES=telegram-local`, бот не найдет контейнер `telegram-bot-api` и будет писать ошибку DNS.

В `--local` режиме `getFile` может вернуть абсолютный путь к файлу на локальном Bot API сервере. Если бот видит тот же volume по другому пути, задайте маппинг: `TELEGRAM_LOCAL_PATH_PREFIX=/var/lib/telegram-bot-api` и `TELEGRAM_BOT_PATH_PREFIX=/telegram-bot-api-data`.

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

Telegram-бот запускается как Go-бинарник из `botgo` и подключается напрямую к единственному PostgreSQL-контейнеру. Redis используется для временных Telegram-состояний: поиск, рассылка, смена пароля, кастомная квота и установка стикеров. `DATABASE_SECRET_KEY` включает шифрование сохраненных Nextcloud-паролей перед записью в PostgreSQL. Укажите длинную случайную строку и не меняйте ее после запуска: старые зашифрованные пароли без нее не расшифровать.

`ENABLE_SUPPORT_BLOCK=false` полностью убирает кнопку поддержки. `SUPPORT_TELEGRAM` и `SUPPORT_EMAIL` показываются пользователю в разделе поддержки.

`ENABLE_DONATE_BLOCK=false` полностью убирает кнопку доната. Внутри доната есть отдельные ветки `Telegram Stars` и `Platega`; их можно отдельно выключить через `TELEGRAM_STARS_ENABLED=false` и `PLATEGA_ENABLED=false`. `TELEGRAM_STARS_AMOUNTS` - суммы кнопок Stars через запятую.

Для Platega можно задать статическую ссылку `PLATEGA_URL`, либо включить API-создание ссылок через `PLATEGA_MERCHANT_ID` и `PLATEGA_SECRET`. По документации Platega запросы идут на `https://app.platega.io/`, платежная ссылка создается через `POST /v2/transaction/process`, а статус проверяется через `GET /transaction/{id}`. `PLATEGA_AMOUNTS_RUB` задает суммы в рублях, `PLATEGA_RETURN_URL` и `PLATEGA_FAILED_URL` передаются в платеж при создании.

`TELEGRAM_MAX_DOWNLOAD_MB` - лимит скачивания через Bot API. Для публичного `api.telegram.org` оставляйте около `20`; для локального Bot API в `--local` режиме можно ставить до `2000`.

`UPLOAD_WORKERS` - количество параллельных обработчиков очереди загрузок. Премиум-пользователи все равно получают более высокий приоритет. `QUOTA_CACHE_SECONDS` - кеш статистики места по каждому отдельному Nextcloud-пользователю, чтобы админка и `/start` не подвисали на каждом WebDAV-запросе.

`PREMIUM_DAYS` - срок действия премиум-иконки после Telegram Stars, Platega или ручной выдачи админом. По умолчанию `30`, то есть примерно один месяц.

`STICKER_STORE_FILE` - JSON-файл, где бот хранит кастомные Telegram-стикеры и custom emoji, чтобы они не сбрасывались после перезапуска. Настройка доступна в админке через `✨ Стикеры`: выбрать событие, отправить стикер или custom emoji, посмотреть предпросмотр или очистить. `CONTENT_STORE_FILE` хранит редактируемые тексты и названия кнопок из меню `✏️ Тексты и кнопки`; туда можно вставлять эмодзи и HTML. `STICKER_PACK_URL` показывает админам тестовый пакет, по умолчанию `https://t.me/addemoji/CPT_Emoji`. Bot API не позволяет импортировать все `file_id` пака по ссылке, поэтому нужный стикер надо отправить боту один раз.

## Запуск локально

```bash
go run ./botgo
```

## Запуск через Docker

```bash
docker compose up -d --build
```

Данные PostgreSQL хранятся в Docker volume `postgres_data`, Redis - в `redis_data`, локальный Telegram Bot API - в `telegram_bot_api_data`, бекапы - в `./backups`, логи - в `./logs`, стикеры/custom emoji и тексты - в `./data`.

## Команды

- `/start` - для пользователя отправляет заявку, для админа открывает панель.
- `/admin` - открывает админ-панель.
- `/health` - проверяет, может ли бот достучаться до Nextcloud API.
- `/sync` - вручную сверяет базу бота с Nextcloud и удаляет из бота отсутствующих Nextcloud-пользователей.
- `/search 123456789` или `/search @username` - поиск пользователя в админке.
- `/broadcast текст` - отправляет текст всем активным одобренным пользователям.
- `/setsticker welcome` и другие события - запускает сохранение стикера/custom emoji в JSON-файл.
- `/stickers` - открывает панель стикеров с кнопками и предпросмотром.

## Бекапы

Бот автоматически создает сжатый PostgreSQL-бекап каждые `AUTO_BACKUP_INTERVAL_HOURS` часов. Старые файлы старше `BACKUP_RETENTION_DAYS` дней удаляются автоматически.

В админ-панели `Синхр./восстановление` можно синхронизировать пользователей с Nextcloud, проверить админский cloud из `.env`, создать сжатый PostgreSQL-бекап, создать публичный JSON-экспорт без паролей, посмотреть последние PostgreSQL-бекапы на сервере и восстановить пользователей из PostgreSQL-бекапа.

Перед восстановлением бот создает safety-бекап текущей базы.

## Загрузка файлов

После одобрения пользователь может отправить боту файл, фото, видео, аудио, voice или animation. Бот скачает файл из Telegram и загрузит его в корень Nextcloud-аккаунта этого пользователя.

Загрузки идут через очередь, чтобы одновременные файлы от разных пользователей не ломали Telegram API и WebDAV-загрузку. Несколько файлов от одного пользователя собираются в одно статус-сообщение: там видно очередь, доставку и итог. Пользователи с премиум-иконкой `⭐` получают приоритет и обрабатываются раньше обычной очереди.

В карточке пользователя админ видит занятое место. Статистика кешируется отдельно по каждому Nextcloud-аккаунту и обновляется после успешной загрузки файлов.

Файлы через бота кладутся в корень облака пользователя.

## Важная деталь по паролям

Пароль генерируется при одобрении и отправляется пользователю. Бот сохраняет текущий пароль в PostgreSQL, потому что без него он не сможет загружать файлы в пространство пользователя через WebDAV. Публичный JSON-бекап не включает пароль, но PostgreSQL-бекап содержит рабочие учетные данные, поэтому храните его аккуратно.

Если пользователь поменяет пароль вручную в Nextcloud, загрузка через бота перестанет работать. Пользователь видит текущий пароль в `/start` и может нажать `Сменить пароль`, либо администратор может открыть карточку пользователя и нажать `Сбросить пароль`.
