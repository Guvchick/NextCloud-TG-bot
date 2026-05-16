# Telegram Nextcloud Beta Bot

Бот принимает заявки на beta-тест, отправляет их администраторам Telegram, создает пользователя в Nextcloud после одобрения, выдает логин/пароль и выставляет стартовую квоту 10 GB.

## Что умеет

- `/start` для пользователя создает заявку и отправляет ее админам.
- Админ одобряет или отклоняет заявку прямо из Telegram.
- После одобрения бот создает или обновляет Nextcloud-пользователя с логином, равным Telegram ID, генерирует пароль и выставляет квоту.
- Админ-панель показывает пользователей, заявки, статусы и квоты.
- Админ видит актуально занятое место пользователя в Nextcloud.
- Админ может добавлять пользователю место: `+1GB`, `+5GB`, `+10GB` или произвольное число.
- Админ может сбросить пароль одобренному пользователю.
- Админ может удалить пользователя из Nextcloud и базы бота.
- Есть отключение и включение Nextcloud-пользователя.
- Пользователь может отправить файл, фото, видео, аудио или документ прямо в бота, а бот загрузит его в Nextcloud.
- Пользователь сразу видит текущий пароль в `/start` и может сменить его через кнопку.
- Пользователь может переключить язык интерфейса: русский/английский.
- Есть кнопка `Донат`.
- Во вложенных пользовательских разделах есть кнопки возврата назад/на главную.
- В пользовательском экране всегда видны квота, занятое место и шкала заполнения.
- Есть ссылка на саппорт в Telegram и email.
- Есть синхронизация с Nextcloud: если пользователя уже нет в Nextcloud, запись удаляется из базы бота.
- Есть сжатые SQLite/JSON-бекапы, восстановление из SQLite-бекапа, авто-бекап и чистка старых файлов.
- Есть логи в stdout и `./logs/bot.log`.
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
BOT_TOKEN=123456:telegram-bot-token
ADMIN_IDS=123456789,987654321
NEXTCLOUD_URL=https://cloud.example.com
NEXTCLOUD_INTERNAL_URL=
NEXTCLOUD_HOSTNAME=cloud.example.com
NEXTCLOUD_ADMIN_USER=admin
NEXTCLOUD_ADMIN_PASSWORD=nextcloud-app-password
DEFAULT_QUOTA_GB=10
DATABASE_PATH=data/bot.sqlite3
BACKUP_DIR=backups
LOG_DIR=logs
UPLOAD_FOLDER=Telegram uploads
SUPPORT_TELEGRAM=@support_username
SUPPORT_EMAIL=support@example.com
DONATE_URL=https://example.com/donate
BACKUP_RETENTION_DAYS=7
AUTO_BACKUP_INTERVAL_HOURS=24
NEXTCLOUD_SYNC_INTERVAL_MINUTES=60
TELEGRAM_MAX_DOWNLOAD_MB=20
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

Файлы из Telegram загружаются в корень диска пользователя. `UPLOAD_FOLDER` оставлен как legacy-настройка, но пользовательский сценарий ее не показывает.

`SUPPORT_TELEGRAM`, `SUPPORT_EMAIL` и `DONATE_URL` показываются пользователю в отдельных кнопочных разделах.

`TELEGRAM_MAX_DOWNLOAD_MB` - лимит скачивания через Bot API. Если Telegram не дает скачать большой файл, бот заранее объяснит это пользователю и предложит загрузить файл напрямую через Nextcloud.

`STICKER_*` - необязательные кастомные `file_id` стикеров. Если они не заданы или Telegram их отклонит, бот оставит базовые визуальные маркеры в тексте. Настроить можно через `/setsticker welcome`, `/setsticker approved`, `/setsticker upload_ok`, `/setsticker error`, `/setsticker support`, `/setsticker donate`, `/setsticker language`, `/setsticker password`.

## Запуск локально

```bash
python3 -m venv .venv
source .venv/bin/activate
pip install -r requirements.txt
python -m bot.main
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

## Бекапы

Бот автоматически создает сжатый SQLite-бекап каждые `AUTO_BACKUP_INTERVAL_HOURS` часов. Старые файлы старше `BACKUP_RETENTION_DAYS` дней удаляются автоматически.

В админ-панели `Бекапы` можно создать сжатый SQLite-бекап, создать сжатый JSON-экспорт, посмотреть последние SQLite-бекапы на сервере и восстановить базу из SQLite-бекапа.

Перед восстановлением бот создает safety-бекап текущей базы. После восстановления лучше перезапустить контейнер.

## Загрузка файлов

После одобрения пользователь может отправить боту файл, фото, видео, аудио, voice или animation. Бот скачает файл из Telegram и загрузит его в корень Nextcloud-аккаунта этого пользователя.

В карточке пользователя админ видит занятое место. Данные обновляются при открытии карточки.

Если Nextcloud возвращает `Permission denied to create directory` при создании папки загрузок, бот автоматически повторяет загрузку в корень диска.

## Важная деталь по паролям

Пароль генерируется при одобрении и отправляется пользователю. Бот сохраняет текущий пароль в SQLite, потому что без него он не сможет загружать файлы в пространство пользователя через WebDAV. JSON-бекап не включает пароль, но SQLite-бекап содержит рабочие учетные данные, поэтому храните его аккуратно.

Если пользователь поменяет пароль вручную в Nextcloud, загрузка через бота перестанет работать. Пользователь видит текущий пароль в `/start` и может нажать `Сменить пароль`, либо администратор может открыть карточку пользователя и нажать `Сбросить пароль`.
